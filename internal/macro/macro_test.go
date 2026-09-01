package macro

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── providers ─────────────────────────────────────────────────────────────────────────

// Fixtures captured 2026-09-01 from the live endpoints, trimmed to the rows under test.
const nyFedFixture = `{"refRates":[
 {"effectiveDate":"2026-08-28","percentRate":3.63,"targetRateFrom":3.5,"targetRateTo":3.75},
 {"effectiveDate":"2026-08-27","percentRate":3.63,"targetRateFrom":3.5,"targetRateTo":3.75},
 {"effectiveDate":"2026-05-01","percentRate":3.88,"targetRateFrom":3.75,"targetRateTo":4.0}
]}`

const treasuryFixture = `Date,"1 Mo","2 Yr","10 Yr","30 Yr"
08/31/2026,3.85,4.34,4.75,5.25
08/28/2026,3.84,4.30,4.70,5.20
`

const vixFixture = `DATE,OPEN,HIGH,LOW,CLOSE
08/28/2026,14.570000,14.840000,14.130000,14.430000
08/31/2026,15.240000,15.480000,14.860000,14.920000
`

func blsFixture() string {
	mk := func(id string, vals map[string]string) map[string]any {
		var data []map[string]string
		for p, v := range vals {
			data = append(data, map[string]string{"year": p[:4], "period": p[4:], "value": v})
		}
		return map[string]any{"seriesID": id, "data": data}
	}
	b, _ := json.Marshal(map[string]any{
		"status": "REQUEST_SUCCEEDED",
		"Results": map[string]any{"series": []any{
			mk(seriesCPI, map[string]string{"2025M07": "322.9", "2026M06": "332.5", "2026M07": "333.918"}),
			mk(seriesCoreCPI, map[string]string{"2025M07": "328.9", "2026M06": "336.2", "2026M07": "337.133"}),
			mk(seriesUnemployment, map[string]string{"2026M06": "4.0", "2026M07": "4.1"}),
			mk(seriesPayrolls, map[string]string{"2026M06": "158881", "2026M07": "158858"}),
		}},
	})
	return string(b)
}

func serve(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := endpointOverride
	endpointOverride = map[string]string{
		nyFedEFFR: srv.URL + "/fed", treasuryCurve: srv.URL + "/treasury",
		blsAPI: srv.URL + "/bls", cboeVIX: srv.URL + "/vix",
	}
	t.Cleanup(func() { endpointOverride = old })
}

func allFixtures(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fed":
			_, _ = w.Write([]byte(nyFedFixture))
		case "/treasury":
			_, _ = w.Write([]byte(treasuryFixture))
		case "/bls":
			_, _ = w.Write([]byte(blsFixture()))
		default:
			_, _ = w.Write([]byte(vixFixture))
		}
	})
}

// The Fed's two numbers must stay apart. EFFR is where money traded; the range is what the
// Committee decided. They move for different reasons, and one number could not express both.
func TestFedKeepsEFFRAndTargetRangeSeparate(t *testing.T) {
	allFixtures(t)
	got, err := NewProvider(nil).FetchFed(context.Background(), 90)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Available {
		t.Fatalf("status = %s", got.Status)
	}
	if got.EFFR == nil || *got.EFFR != 3.63 {
		t.Errorf("EFFR = %v", got.EFFR)
	}
	if got.TargetLower == nil || *got.TargetLower != 3.5 {
		t.Errorf("lower = %v", got.TargetLower)
	}
	if got.TargetUpper == nil || *got.TargetUpper != 3.75 {
		t.Errorf("upper = %v", got.TargetUpper)
	}
	if got.Date != "2026-08-28" {
		t.Errorf("date = %q, want the newest observation", got.Date)
	}
	// History must be oldest-first, so "the previous range" is the element before.
	if len(got.History) != 3 || got.History[0].Date != "2026-05-01" {
		t.Errorf("history not oldest-first: %+v", got.History)
	}
}

func TestTreasurySpread(t *testing.T) {
	allFixtures(t)
	got, err := NewProvider(nil).FetchTreasury(context.Background(), 2026)
	if err != nil {
		t.Fatal(err)
	}
	if got.Yield2Y == nil || *got.Yield2Y != 4.34 {
		t.Errorf("2Y = %v", got.Yield2Y)
	}
	if got.Yield10Y == nil || *got.Yield10Y != 4.75 {
		t.Errorf("10Y = %v", got.Yield10Y)
	}
	// Spread is 10Y − 2Y, not the other way round: a positive value is an upward-sloping
	// curve, and getting the sign backwards would invert every reading of it.
	if got.Spread2Y10Y == nil || math.Abs(*got.Spread2Y10Y-0.41) > 1e-9 {
		t.Errorf("spread = %v, want +0.41", got.Spread2Y10Y)
	}
	if got.Date != "2026-08-31" {
		t.Errorf("date = %q; MM/DD/YYYY must be normalised", got.Date)
	}

	// A row missing a tenor is skipped, not zero-filled.
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Date,\"2 Yr\",\"10 Yr\"\n08/31/2026,,\n08/28/2026,4.30,4.70\n"))
	})
	got2, _ := NewProvider(nil).FetchTreasury(context.Background(), 2026)
	if got2.Date != "2026-08-28" {
		t.Errorf("a row with blank tenors was used: %+v", got2)
	}
}

func TestVIXTakesTheLatestClose(t *testing.T) {
	allFixtures(t)
	got, err := NewProvider(nil).FetchVIX(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.VIX.Value == nil || *got.VIX.Value != 14.92 {
		t.Errorf("VIX = %v, want the 08-31 close", got.VIX.Value)
	}
	if got.VIX.Date != "2026-08-31" {
		t.Errorf("date = %q", got.VIX.Date)
	}
}

// A refused request must be an error, never an empty dataset. "No company reported inflation
// this month" is not a thing that happens.
func TestProviderErrorsAreNotEmptyData(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	p := NewProvider(nil)
	ctx := context.Background()

	if _, err := p.FetchFed(ctx, 10); err == nil {
		t.Error("a 429 from NY Fed was accepted")
	}
	if _, err := p.FetchTreasury(ctx, 2026); err == nil {
		t.Error("a 429 from Treasury was accepted")
	}
	if _, err := p.FetchBLS(ctx, 2025, 2026); err == nil {
		t.Error("a 429 from BLS was accepted")
	}
	if _, err := p.FetchVIX(ctx); err == nil {
		t.Error("a 429 from CBOE was accepted")
	}

	// BLS answers 200 even when it refuses, so the BODY is the authority.
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"REQUEST_NOT_PROCESSED","Results":{}}`))
	})
	if _, err := p.FetchBLS(ctx, 2025, 2026); err == nil {
		t.Error("a 200 carrying REQUEST_NOT_PROCESSED was accepted as success")
	}
}

// One request for four series. Four requests would be four chances to half-fetch an economy.
func TestBLSFetchesEverySeriesInOneRequest(t *testing.T) {
	var hits int
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(blsFixture()))
	})
	got, err := NewProvider(nil).FetchBLS(context.Background(), 2025, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("%d requests for four series; want 1", hits)
	}
	for _, id := range []string{seriesCPI, seriesCoreCPI, seriesUnemployment, seriesPayrolls} {
		if len(got[id]) == 0 {
			t.Errorf("%s missing", id)
		}
	}
	// Observations must come back oldest-first, or "the previous month" is the wrong one.
	cpi := got[seriesCPI]
	for i := 1; i < len(cpi); i++ {
		if cpi[i-1].Year > cpi[i].Year ||
			(cpi[i-1].Year == cpi[i].Year && cpi[i-1].Month > cpi[i].Month) {
			t.Fatalf("series is not chronological: %+v", cpi)
		}
	}
}

// ── derived ───────────────────────────────────────────────────────────────────────────

// The index and the rate are different quantities. CPI is 333.918 — a level, not 33391.8%.
func TestInflationIndexAndRateStaySeparate(t *testing.T) {
	cpi := []blsPoint{
		{Year: 2025, Month: 7, Value: 322.9},
		{Year: 2026, Month: 6, Value: 332.5},
		{Year: 2026, Month: 7, Value: 333.918},
	}
	got := BuildInflation(cpi, nil)
	if got.CPIIndex.Value == nil || *got.CPIIndex.Value != 333.918 {
		t.Errorf("index = %v, want the raw level", got.CPIIndex.Value)
	}
	if got.CPIIndex.Date != "2026-07" {
		t.Errorf("index date = %q", got.CPIIndex.Date)
	}
	// 333.918 / 322.9 − 1 = 3.41%, stored as a RATIO.
	if got.CPIYoY == nil || math.Abs(*got.CPIYoY-0.034128) > 1e-5 {
		t.Errorf("YoY = %v, want ~0.0341 as a ratio", got.CPIYoY)
	}
	if got.CPIYoY != nil && *got.CPIYoY > 1 {
		t.Errorf("YoY = %v looks like a percentage, not a ratio", *got.CPIYoY)
	}
	if got.CPIMoM == nil || math.Abs(*got.CPIMoM-0.004265) > 1e-5 {
		t.Errorf("MoM = %v", got.CPIMoM)
	}
}

// Year-on-year must find the SAME MONTH a year back, not twelve array positions back — a gap
// would otherwise produce an eleven-month change wearing a year-on-year label.
func TestYearOverYearMatchesTheMonthNotThePosition(t *testing.T) {
	// A gap: no 2025-07, so the year-on-year for 2026-07 is not computable.
	pts := []blsPoint{
		{Year: 2025, Month: 6, Value: 100},
		{Year: 2026, Month: 6, Value: 110},
		{Year: 2026, Month: 7, Value: 111},
	}
	if got := yearOverYear(pts, 2); got != nil {
		t.Errorf("a missing base month produced %v; want nil", *got)
	}
	// The one that IS present works.
	if got := yearOverYear(pts, 1); got == nil || math.Abs(*got-0.10) > 1e-9 {
		t.Errorf("2026-06 vs 2025-06 = %v, want 0.10", got)
	}

	// Month-on-month likewise refuses a non-adjacent predecessor.
	gap := []blsPoint{{Year: 2026, Month: 5, Value: 100}, {Year: 2026, Month: 7, Value: 110}}
	if got := monthOverMonth(gap, 1); got != nil {
		t.Errorf("a two-month gap produced a MoM of %v", *got)
	}
}

// The payroll LEVEL and the payroll CHANGE are three orders of magnitude apart.
func TestPayrollLevelIsNotPayrollChange(t *testing.T) {
	payrolls := []blsPoint{
		{Year: 2026, Month: 6, Value: 158881},
		{Year: 2026, Month: 7, Value: 158858},
	}
	got := BuildLabor(nil, payrolls)
	if got.PayrollLevel.Value == nil || *got.PayrollLevel.Value != 158858 {
		t.Errorf("level = %v, want the total in thousands", got.PayrollLevel.Value)
	}
	// 158,858 − 158,881 = −23 thousand jobs. Markets would call this "−23K".
	if got.PayrollChange == nil || *got.PayrollChange != -23 {
		t.Errorf("change = %v, want -23", got.PayrollChange)
	}
	// A single observation has a level but no change — never a change of zero.
	one := BuildLabor(nil, payrolls[:1])
	if one.PayrollChange != nil {
		t.Errorf("one observation produced a change of %v", *one.PayrollChange)
	}
	if one.PayrollLevel.Value == nil {
		t.Error("the level was dropped along with the change")
	}
}

// Missing is never zero.
func TestMissingIsNotZero(t *testing.T) {
	empty := BuildInflation(nil, nil)
	if empty.Status != Unavailable || empty.CPIIndex.Value != nil || empty.CPIYoY != nil {
		t.Errorf("no observations produced %+v", empty)
	}
	lab := BuildLabor(nil, nil)
	if lab.Status != Unavailable || lab.UnemploymentRate.Value != nil {
		t.Errorf("no observations produced %+v", lab)
	}
	// A blank cell stays missing.
	if v := parseFloat(""); v != nil {
		t.Errorf(`"" parsed as %v`, *v)
	}
	if v := parseFloat("-"); v != nil {
		t.Errorf(`"-" parsed as %v`, *v)
	}
	// A real zero is a reading.
	if v := parseFloat("0"); v == nil || *v != 0 {
		t.Errorf(`"0" → %v`, v)
	}
}

// ── interpretation ────────────────────────────────────────────────────────────────────

func fedWith(lower, upper float64, history ...[2]float64) FedSnapshot {
	f := FedSnapshot{Status: Available, Date: "2026-08-28",
		EFFR: ptr(3.63), TargetLower: ptr(lower), TargetUpper: ptr(upper)}
	for i, h := range history {
		f.History = append(f.History, FedObservation{
			Date:        time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			TargetLower: ptr(h[0]), TargetUpper: ptr(h[1]),
		})
	}
	f.History = append(f.History, FedObservation{
		Date: "2026-08-28", TargetLower: ptr(lower), TargetUpper: ptr(upper)})
	return f
}

// Direction comes from the TARGET RANGE. EFFR drifts a basis point or two on funding
// conditions with no decision behind it, and reading that as policy would report a move most
// weeks.
func TestFedDirectionUsesTargetRangeNotEFFR(t *testing.T) {
	asOf := "2026-08-29"
	for name, tc := range map[string]struct {
		snap FedSnapshot
		want FedDirection
		bps  *float64
	}{
		"cut":       {fedWith(3.50, 3.75, [2]float64{3.75, 4.00}), FedEasing, ptr(-25)},
		"hike":      {fedWith(4.00, 4.25, [2]float64{3.75, 4.00}), FedTightening, ptr(25)},
		"no change": {fedWith(3.50, 3.75), FedUnknown, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := interpretFed(tc.snap, asOf)
			if got.Direction != tc.want {
				t.Errorf("direction = %s, want %s", got.Direction, tc.want)
			}
			if (tc.bps == nil) != (got.LastChangeBps == nil) {
				t.Fatalf("bps presence differs: %v vs %v", got.LastChangeBps, tc.bps)
			}
			if tc.bps != nil && math.Abs(*got.LastChangeBps-*tc.bps) > 1e-9 {
				t.Errorf("bps = %v, want %v", *got.LastChangeBps, *tc.bps)
			}
		})
	}

	// EFFR moving while the range holds is NOT a policy change.
	drift := fedWith(3.50, 3.75)
	drift.History[0].EFFR = ptr(3.60)
	drift.EFFR = ptr(3.66)
	if got := interpretFed(drift, asOf); got.Direction == FedEasing || got.Direction == FedTightening {
		t.Errorf("EFFR drift was read as a policy move: %s", got.Direction)
	}
}

func TestYieldCurveStates(t *testing.T) {
	mk := func(y2, y10 float64) TreasurySnapshot {
		s := y10 - y2
		return TreasurySnapshot{Status: Available, Date: "2026-08-31",
			Yield2Y: ptr(y2), Yield10Y: ptr(y10), Spread2Y10Y: &s}
	}
	for name, tc := range map[string]struct {
		t    TreasurySnapshot
		want CurveState
	}{
		"inverted": {mk(4.50, 4.20), CurveInverted},
		"flat":     {mk(4.50, 4.55), CurveFlat},
		"normal":   {mk(4.34, 4.75), CurveNormal},
	} {
		if got := interpretTreasury(tc.t, "2026-09-01").Curve; got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	// A missing tenor leaves the curve unknown, never flat.
	if got := interpretTreasury(TreasurySnapshot{Status: Available, Yield2Y: ptr(4.3)}, "2026-09-01"); got.Curve != CurveUnknown {
		t.Errorf("a missing 10Y gave %s", got.Curve)
	}
}

// The trend compares the RATE, not the index — the index rises almost monotonically and would
// report acceleration forever.
func TestInflationTrendComparesTheRate(t *testing.T) {
	mk := func(cur, prior float64) InflationSnapshot {
		return InflationSnapshot{Status: Available,
			CoreCPIIndex: Observation{Date: "2026-07", Value: ptr(337.1)},
			CoreCPIYoY:   ptr(cur), PriorCoreCPIYoY: ptr(prior)}
	}
	for name, tc := range map[string]struct {
		in   InflationSnapshot
		want InflationTrend
	}{
		"accelerating": {mk(0.031, 0.025), InflationAccelerating},
		"decelerating": {mk(0.025, 0.031), InflationDecelerating},
		"stable":       {mk(0.0250, 0.0251), InflationStable},
	} {
		if got := interpretInflation(tc.in, "2026-08-20").Trend; got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	// Only one observation — the trend is unknown, not stable.
	one := InflationSnapshot{Status: Available, CoreCPIYoY: ptr(0.03),
		CoreCPIIndex: Observation{Date: "2026-07"}}
	if got := interpretInflation(one, "2026-08-20").Trend; got != InflationUnknown {
		t.Errorf("a single observation gave %s", got)
	}
}

func TestLaborStates(t *testing.T) {
	mk := func(unemp, prior, change float64) LaborSnapshot {
		return LaborSnapshot{Status: Available,
			UnemploymentRate:  Observation{Date: "2026-07", Value: ptr(unemp)},
			PriorUnemployment: ptr(prior), PayrollChange: ptr(change)}
	}
	for name, tc := range map[string]struct {
		in   LaborSnapshot
		want LaborState
	}{
		"strengthening": {mk(3.9, 4.1, 150), LaborStrengthening},
		"weakening":     {mk(4.3, 4.1, -20), LaborWeakening},
		"mixed":         {mk(4.3, 4.1, 150), LaborMixed},
	} {
		if got := interpretLabor(tc.in, "2026-08-20").State; got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	// A missing leg leaves the state unknown; the other leg still shows.
	partial := LaborSnapshot{Status: Partial,
		UnemploymentRate: Observation{Date: "2026-07", Value: ptr(4.1)}}
	got := interpretLabor(partial, "2026-08-20")
	if got.State != LaborUnknown {
		t.Errorf("a missing payroll leg gave %s", got.State)
	}
	if got.Unemployment == nil {
		t.Error("the available leg was discarded")
	}
}

// Bands are half-open on the upper side, so a boundary value belongs to exactly one state.
func TestVIXBoundaries(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want VIXState
	}{
		{14.99, VIXLow}, {15.00, VIXNormal}, {19.99, VIXNormal}, {20.00, VIXElevated},
		{29.99, VIXElevated}, {30.00, VIXHigh}, {39.99, VIXHigh}, {40.00, VIXExtreme},
	} {
		in := VolatilitySnapshot{Status: Available, VIX: Observation{Date: "2026-08-31", Value: ptr(tc.v)}}
		if got := interpretVIX(in, "2026-09-01").State; got != tc.want {
			t.Errorf("VIX %.2f → %s, want %s", tc.v, got, tc.want)
		}
	}
}

// Freshness is judged against each series' own frequency. One threshold would call a
// month-old CPI print stale while accepting a month-old Treasury yield.
func TestFreshnessIsFrequencyAware(t *testing.T) {
	// A daily series two days old is fresh; twenty days old is not.
	tre := TreasurySnapshot{Status: Available, Date: "2026-08-30",
		Yield2Y: ptr(4.3), Yield10Y: ptr(4.7), Spread2Y10Y: ptr(0.4)}
	if got := interpretTreasury(tre, "2026-09-01").Freshness; got != Fresh {
		t.Errorf("a two-day-old yield = %s", got)
	}
	tre.Date = "2026-08-01"
	if got := interpretTreasury(tre, "2026-09-01").Freshness; got != Stale {
		t.Errorf("a month-old yield = %s", got)
	}

	// July CPI is published in mid-August and is the current figure on 1 September. Measuring
	// from the period START would make every monthly series permanently stale.
	inf := InflationSnapshot{Status: Available,
		CoreCPIIndex: Observation{Date: "2026-07", Value: ptr(337.1)}}
	if got := interpretInflation(inf, "2026-09-01").Freshness; got != Fresh {
		t.Errorf("the latest available CPI = %s; it is the current print", got)
	}
	// But if the next release is long overdue, it is stale.
	if got := interpretInflation(inf, "2026-11-01").Freshness; got != Stale {
		t.Errorf("a three-month-old CPI = %s", got)
	}
}

// Stale is not unavailable: the value is still shown, with its age.
func TestStaleIsNotUnavailable(t *testing.T) {
	tre := TreasurySnapshot{Status: Available, Date: "2026-01-01",
		Yield2Y: ptr(4.3), Yield10Y: ptr(4.7), Spread2Y10Y: ptr(0.4)}
	got := interpretTreasury(tre, "2026-09-01")
	if got.Freshness != Stale {
		t.Fatalf("freshness = %s", got.Freshness)
	}
	if got.Status != Available || got.Yield10Y == nil {
		t.Error("a stale reading was discarded; it should be shown with its age")
	}
}

// Risk flags are named conditions, each traceable to exactly one state. Nothing forecasts.
func TestRiskFlagsTraceToStates(t *testing.T) {
	r := Research{
		Treasury:   TreasuryView{Curve: CurveInverted},
		Inflation:  InflationView{Trend: InflationAccelerating},
		Labor:      LaborView{State: LaborWeakening},
		Volatility: VolatilityView{State: VIXHigh},
		Fed:        FedView{Direction: FedTightening},
	}
	got := riskFlags(r)
	want := map[RiskFlag]bool{
		FlagYieldCurveInverted: true, FlagInflationAccelerating: true,
		FlagLaborWeakening: true, FlagVIXElevated: true, FlagFedTightening: true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("unexpected flag %s", f)
		}
	}
	// A benign picture raises nothing.
	calm := Research{
		Treasury: TreasuryView{Curve: CurveNormal}, Inflation: InflationView{Trend: InflationDecelerating},
		Labor: LaborView{State: LaborStrengthening}, Volatility: VolatilityView{State: VIXLow},
		Fed: FedView{Direction: FedEasing},
	}
	if f := riskFlags(calm); len(f) != 0 {
		t.Errorf("a calm picture raised %v", f)
	}
}

// No score, and no forecast vocabulary anywhere in the interpreted output.
func TestNoScoreAndNoForecast(t *testing.T) {
	allFixtures(t)
	snap, err := NewService(nil, t.TempDir(), nil).FetchAndStore(context.Background(),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(Interpret(snap, "2026-09-01"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"score", "Score", "probability", "forecast", "expected",
		"bullish", "bearish", "recession", "buy", "sell"} {
		if containsFold(string(b), banned) {
			t.Errorf("the interpreted output contains %q: %s", banned, b)
		}
	}
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Interpretation is pure: no clock, no network, no filesystem.
func TestInterpretIsPure(t *testing.T) {
	s := &Snapshot{
		ArchiveDate: "2026-09-01",
		Fed:         fedWith(3.50, 3.75, [2]float64{3.75, 4.00}),
		Treasury:    TreasurySnapshot{Status: Available, Date: "2026-08-31", Yield2Y: ptr(4.34), Yield10Y: ptr(4.75), Spread2Y10Y: ptr(0.41)},
	}
	a, _ := json.Marshal(Interpret(s, "2026-09-01"))
	time.Sleep(2 * time.Millisecond)
	b, _ := json.Marshal(Interpret(s, "2026-09-01"))
	if string(a) != string(b) {
		t.Errorf("two calls differed:\n%s\n%s", a, b)
	}
	// A different asOf legitimately changes freshness — proving the date is the input, not
	// the wall clock.
	c, _ := json.Marshal(Interpret(s, "2027-01-01"))
	if string(a) == string(c) {
		t.Error("asOf did not affect the result; freshness may be reading the clock")
	}
}

// ── archive ───────────────────────────────────────────────────────────────────────────

// A historical run reads what was archived by its date, never a later capture.
func TestArchivePointInTime(t *testing.T) {
	dir := t.TempDir()
	mk := func(date string, effr float64) {
		t.Helper()
		if _, err := SaveSnapshot(dir, &Snapshot{
			ArchiveDate: date, ObservedAt: time.Now(), Status: Available,
			Fed: FedSnapshot{Status: Available, Date: date, EFFR: ptr(effr)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("2026-09-01", 3.63)
	mk("2026-09-03", 3.38)

	for _, tc := range []struct {
		asOf string
		want float64
	}{{"2026-09-01", 3.63}, {"2026-09-02", 3.63}, {"2026-09-03", 3.38}, {"2026-12-01", 3.38}} {
		got, err := LoadAsOf(dir, tc.asOf)
		if err != nil || got == nil {
			t.Fatalf("%s: %v", tc.asOf, err)
		}
		if got.Fed.EFFR == nil || *got.Fed.EFFR != tc.want {
			t.Errorf("as of %s got EFFR %v, want %v — a later archive leaked backwards",
				tc.asOf, got.Fed.EFFR, tc.want)
		}
	}
	// Before anything was archived: nothing, and no fetch.
	got, err := LoadAsOf(dir, "2026-01-01")
	if err != nil || got != nil {
		t.Errorf("a date before the archive returned %v / %v", got, err)
	}
}

// Each component keeps its OWN observation date; a single snapshot-level date would assert
// that a July CPI figure was a September one.
func TestObservationDatesAreSeparateFromArchiveDate(t *testing.T) {
	allFixtures(t)
	snap, err := NewService(nil, t.TempDir(), nil).FetchAndStore(context.Background(),
		time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snap.ArchiveDate != "2026-09-01" {
		t.Errorf("archive date = %q", snap.ArchiveDate)
	}
	if snap.Treasury.Date != "2026-08-31" {
		t.Errorf("treasury observed %q, want its own date", snap.Treasury.Date)
	}
	if snap.Inflation.CPIIndex.Date != "2026-07" {
		t.Errorf("CPI observed %q — a monthly figure must keep its month", snap.Inflation.CPIIndex.Date)
	}
	if snap.Inflation.CPIIndex.Date == snap.ArchiveDate {
		t.Error("the CPI observation date collapsed into the archive date")
	}
}

// One dead source must not erase four live ones.
func TestPartialSnapshot(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vix" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/fed":
			_, _ = w.Write([]byte(nyFedFixture))
		case "/treasury":
			_, _ = w.Write([]byte(treasuryFixture))
		default:
			_, _ = w.Write([]byte(blsFixture()))
		}
	})
	snap, err := NewService(nil, t.TempDir(), nil).FetchAndStore(context.Background(),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != Partial {
		t.Errorf("status = %s, want PARTIAL", snap.Status)
	}
	if snap.Fed.Status != Available || snap.Treasury.Status != Available {
		t.Error("a dead VIX feed took the live sources with it")
	}
	if snap.Volatility.Status != Unavailable {
		t.Errorf("VIX = %s", snap.Volatility.Status)
	}
	// A 500 must not become VIX = 0.
	if snap.Volatility.VIX.Value != nil {
		t.Errorf("a failed fetch produced a VIX of %v", *snap.Volatility.VIX.Value)
	}
	// The unusable sources are declared, not omitted.
	if snap.PCEStatus != NotImplemented || snap.FOMCCalendarStatus != NotImplemented {
		t.Errorf("PCE=%s FOMC=%s; both were investigated and found unusable",
			snap.PCEStatus, snap.FOMCCalendarStatus)
	}
}
