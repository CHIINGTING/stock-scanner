package valuation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func pe(v float64, date string) Ratios { return Ratios{Date: date, PERatio: &v} }

// series builds n daily observations ending at the given date.
func series(end string, n int, values ...float64) []Ratios {
	out := make([]Ratios, 0, n)
	t, _ := time.Parse("2006-01-02", end)
	for i := n - 1; i >= 0; i-- {
		v := values[i%len(values)]
		out = append(out, pe(v, t.AddDate(0, 0, -i).Format("2006-01-02")))
	}
	return out
}

// ── the scope contract ────────────────────────────────────────────────────────────────

// The metrics with no source must report NOT_IMPLEMENTED, not silently vanish.
//
// This is the guard against the failure R13-M6 was written to avoid: a struct field with no
// data behind it. Stating "there is no source" is a different claim from "it came out empty",
// and a reader must be able to tell them apart.
func TestUnsourcedMetricsAreExplicit(t *testing.T) {
	v := Calculate(Input{Symbol: "2330.TW", Current: &Ratios{Date: "2026-08-28", PERatio: ptr(28.05)}})

	for name, got := range map[string]Availability{
		"forward PE": v.ForwardPEStatus, "PEG": v.PEGStatus, "fair value": v.FairValueStatus,
	} {
		if got != NotImplemented {
			t.Errorf("%s = %q, want NOT_IMPLEMENTED — there is no authorized forecast source",
				name, got)
		}
	}
	// And no field exists that could hold a fabricated forward number.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"forward_eps", "peg\"", "fair_value\"", "target_price",
		"consensus", "upside"} {
		if contains(string(b), banned) {
			t.Errorf("the valuation carries %q, but nothing can populate it", banned)
		}
	}
}

// ── trailing P/E ──────────────────────────────────────────────────────────────────────

// The exchange's own figure is carried through unchanged. This layer never divides a price by
// an earnings number of its own: R13-M5 established the statements are CUMULATIVE, so the
// only way to manufacture a TTM EPS here would be to annualise a half-year — the exact guess
// a valuation layer must not make.
func TestTrailingPEComesFromTheExchange(t *testing.T) {
	v := Calculate(Input{Symbol: "2330.TW",
		Current: &Ratios{Date: "2026-08-28", PERatio: ptr(28.05), PBRatio: ptr(9.76),
			DividendYield: ptr(0.91)}})
	if v.TrailingStatus != Available {
		t.Fatalf("status = %q", v.TrailingStatus)
	}
	if v.TrailingPE == nil || *v.TrailingPE != 28.05 {
		t.Errorf("trailing PE = %v, want the published 28.05 unchanged", v.TrailingPE)
	}
	if v.TrailingDate != "2026-08-28" {
		t.Errorf("date = %q; the session the figure belongs to must survive", v.TrailingDate)
	}
	if v.PBRatio == nil || *v.PBRatio != 9.76 {
		t.Errorf("PB = %v", v.PBRatio)
	}
}

// A company with non-positive earnings has NO published P/E. That absence is a fact about the
// company, and turning it into 0 would place a loss-making stock at the cheap end of every
// percentile.
func TestNoPublishedPEIsUnavailableNotZero(t *testing.T) {
	for name, r := range map[string]*Ratios{
		"omitted":  {Date: "2026-08-28"},
		"zero":     {Date: "2026-08-28", PERatio: ptr(0)},
		"negative": {Date: "2026-08-28", PERatio: ptr(-12)},
		"no data":  nil,
	} {
		v := Calculate(Input{Symbol: "X.TW", Current: r})
		if v.TrailingStatus != Unavailable {
			t.Errorf("%s: status = %q, want UNAVAILABLE", name, v.TrailingStatus)
		}
		if v.TrailingPE != nil {
			t.Errorf("%s: a P/E of %v was published from nothing", name, *v.TrailingPE)
		}
	}
}

// ── historical distribution ───────────────────────────────────────────────────────────

// A distribution over too few points describes those points, not a distribution. Reporting it
// anyway would give a brand-new archive the same apparent authority as a five-year one.
func TestInsufficientSamplesRefusesStatistics(t *testing.T) {
	v := Calculate(Input{
		Symbol: "2330.TW", Current: &Ratios{Date: "2026-08-28", PERatio: ptr(20)},
		History: series("2026-08-28", 5, 10, 15, 20, 25, 30), AsOfDate: "2026-08-28",
	})
	h := v.HistoricalPE
	if h.Status != InsufficientData {
		t.Errorf("status = %q with 5 samples (minimum %d)", h.Status, MinHistoricalPESamples)
	}
	if h.Median != nil || h.CurrentPercentile != nil {
		t.Errorf("statistics were produced from 5 samples: median=%v pct=%v",
			h.Median, h.CurrentPercentile)
	}
	// The count is still reported: "5 of the 20 needed" is more useful than a bare refusal.
	if h.SampleCount != 5 {
		t.Errorf("sample count = %d, want 5", h.SampleCount)
	}
	// Trailing P/E is unaffected — one missing metric must not take the other with it.
	if v.TrailingStatus != Available || v.Status != Partial {
		t.Errorf("trailing=%q overall=%q; want the available half preserved as PARTIAL",
			v.TrailingStatus, v.Status)
	}
}

// The statistics themselves, on a distribution with a known shape.
func TestHistoricalStatistics(t *testing.T) {
	// 1..40, so median = 20.5, P25 = 10.75, P75 = 30.25 by linear interpolation.
	var vals []float64
	for i := 1; i <= 40; i++ {
		vals = append(vals, float64(i))
	}
	hist := make([]Ratios, 0, 40)
	base, _ := time.Parse("2006-01-02", "2026-08-28")
	for i, v := range vals {
		hist = append(hist, pe(v, base.AddDate(0, 0, -(39-i)).Format("2006-01-02")))
	}

	v := Calculate(Input{Symbol: "X.TW", Current: &Ratios{Date: "2026-08-28", PERatio: ptr(20)},
		History: hist, AsOfDate: "2026-08-28"})
	h := v.HistoricalPE
	if h.Status != Available {
		t.Fatalf("status = %q with 40 samples", h.Status)
	}
	if h.SampleCount != 40 {
		t.Errorf("count = %d", h.SampleCount)
	}
	check := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-9 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("min", h.Min, 1)
	check("max", h.Max, 40)
	check("median", h.Median, 20.5)
	check("p25", h.P25, 10.75)
	check("p75", h.P75, 30.25)

	// Percentile counts ties as at-or-below, matching indicator.PercentileRank. With "<"
	// instead, a stock at exactly its median would report below 50 and two places in the
	// repo would disagree about the same stock.
	check("percentile at 20", h.CurrentPercentile, 20.0/40.0)

	// An odd count too, where the median is an actual sample rather than an interpolation.
	odd := make([]Ratios, 0, 21)
	for i := 1; i <= 21; i++ {
		odd = append(odd, pe(float64(i), base.AddDate(0, 0, -(21-i)).Format("2006-01-02")))
	}
	v2 := Calculate(Input{Symbol: "X.TW", History: odd, AsOfDate: "2026-08-28"})
	check("odd median", v2.HistoricalPE.Median, 11)
}

// Non-positive and missing observations are DROPPED, never zeroed — including them would drag
// every percentile toward the cheap end and make a loss-making year look like a bargain.
func TestInvalidSamplesAreExcluded(t *testing.T) {
	base, _ := time.Parse("2006-01-02", "2026-08-28")
	var hist []Ratios
	for i := 0; i < 30; i++ {
		hist = append(hist, pe(20, base.AddDate(0, 0, -i).Format("2006-01-02")))
	}
	// Ten unusable observations mixed in.
	for i := 30; i < 40; i++ {
		d := base.AddDate(0, 0, -i).Format("2006-01-02")
		hist = append(hist, Ratios{Date: d})                  // no P/E published
		hist = append(hist, Ratios{Date: d, PERatio: ptr(0)}) // non-positive
	}
	v := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: "2026-08-28"})
	if v.HistoricalPE.SampleCount != 30 {
		t.Errorf("sample count = %d, want 30 — unusable observations were counted",
			v.HistoricalPE.SampleCount)
	}
	if v.HistoricalPE.Min == nil || *v.HistoricalPE.Min != 20 {
		t.Errorf("min = %v; a zero leaked into the distribution", v.HistoricalPE.Min)
	}
}

// THE look-ahead guard. A historical run must not see observations made after its own date.
func TestHistoryIsBoundedByAsOf(t *testing.T) {
	base, _ := time.Parse("2006-01-02", "2026-08-28")
	var hist []Ratios
	for i := 0; i < 40; i++ {
		// Older observations are cheap (10), the most recent twenty are expensive (50).
		v := 10.0
		if i < 20 {
			v = 50
		}
		hist = append(hist, pe(v, base.AddDate(0, 0, -i).Format("2006-01-02")))
	}

	// A run dated 40 days ago must see ONLY the cheap history.
	early := base.AddDate(0, 0, -25).Format("2006-01-02")
	got := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: early,
		MinSamples: 5})
	if got.HistoricalPE.Max == nil {
		t.Fatal("no statistics")
	}
	if *got.HistoricalPE.Max > 10 {
		t.Errorf("a run dated %s saw a P/E of %v from a later observation — look-ahead",
			early, *got.HistoricalPE.Max)
	}

	// The full run sees both.
	full := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: "2026-08-28", MinSamples: 5})
	if full.HistoricalPE.Max == nil || *full.HistoricalPE.Max != 50 {
		t.Errorf("the current run should see the recent observations: %v", full.HistoricalPE.Max)
	}
}

// The window bounds the distribution, so a five-year statistic never quietly becomes a
// ten-year one as the archive grows.
func TestWindowBoundsTheDistribution(t *testing.T) {
	base, _ := time.Parse("2006-01-02", "2026-08-28")
	var hist []Ratios
	for i := 0; i < 60; i++ {
		v := 20.0
		if i >= 30 {
			v = 99 // beyond a 30-day window
		}
		hist = append(hist, pe(v, base.AddDate(0, 0, -i).Format("2006-01-02")))
	}
	got := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: "2026-08-28",
		WindowDays: 29, MinSamples: 5})
	if got.HistoricalPE.Max == nil || *got.HistoricalPE.Max != 20 {
		t.Errorf("max = %v; observations outside the window leaked in", got.HistoricalPE.Max)
	}
	if got.HistoricalPE.WindowDays != 29 {
		t.Errorf("window = %d", got.HistoricalPE.WindowDays)
	}
}

// Nothing may emit NaN or Inf: they survive JSON as errors, or render as plausible numbers.
func TestNoNaNOrInf(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	hist := []Ratios{{Date: "2026-08-01", PERatio: &nan}, {Date: "2026-08-02", PERatio: &inf}}
	v := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: "2026-08-28"})
	if v.HistoricalPE.SampleCount != 0 {
		t.Errorf("NaN/Inf were counted as samples: %d", v.HistoricalPE.SampleCount)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("unmarshalable: %v", err)
	}
	for _, bad := range []string{"NaN", "Inf"} {
		if contains(string(b), bad) {
			t.Errorf("%q reached the JSON: %s", bad, b)
		}
	}
}

// Calculate must be pure: same input, same output, no clock and no I/O.
func TestCalculateIsPure(t *testing.T) {
	in := Input{Symbol: "X.TW", Current: &Ratios{Date: "2026-08-28", PERatio: ptr(20)},
		History: series("2026-08-28", 30, 10, 20, 30), AsOfDate: "2026-08-28"}
	a, _ := json.Marshal(Calculate(in))
	time.Sleep(2 * time.Millisecond)
	b, _ := json.Marshal(Calculate(in))
	if string(a) != string(b) {
		t.Errorf("two calls differed:\n%s\n%s", a, b)
	}
}

// ── provider ──────────────────────────────────────────────────────────────────────────

// Fixtures captured 2026-09-01 from openapi.twse.com.tw/v1/exchangeReport/BWIBBU_ALL and
// www.tpex.org.tw/openapi/v1/tpex_mainboard_peratio_analysis.
const twseFixture = `[
 {"Date":"1150828","Code":"2330","Name":"台積電","PEratio":"28.05","DividendYield":"0.91","PBratio":"9.76"},
 {"Date":"1150828","Code":"9999","Name":"虧損公司","PEratio":"-","DividendYield":"0.00","PBratio":"1.20"}
]`

const tpexFixture = `[
 {"Date":"1150831","SecuritiesCompanyCode":"6488","CompanyName":"環球晶","PriceEarningRatio":"44.27","YieldRatio":"0.84","PriceBookRatio":"4.51"}
]`

func serveRatios(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/twse" {
			_, _ = w.Write([]byte(twseFixture))
			return
		}
		_, _ = w.Write([]byte(tpexFixture))
	}))
	t.Cleanup(srv.Close)
	old := endpointOverride
	endpointOverride = map[string]string{twsePE: srv.URL + "/twse", tpexPE: srv.URL + "/tpex"}
	t.Cleanup(func() { endpointOverride = old })
}

func TestProviderParsesBothExchanges(t *testing.T) {
	serveRatios(t)
	p := NewProvider(nil)

	tw, err := p.FetchMarket(context.Background(), fetcher.MarketTWSE)
	if err != nil {
		t.Fatal(err)
	}
	r := tw["2330"]
	// ROC 1150828 → 2026-08-28. Reading 115 as 115 AD would place every observation
	// nineteen centuries early and make every point-in-time window include everything.
	if r.Date != "2026-08-28" {
		t.Errorf("date = %q, want the ROC date converted", r.Date)
	}
	if r.PERatio == nil || *r.PERatio != 28.05 {
		t.Errorf("PE = %v", r.PERatio)
	}
	// A "-" cell is MISSING, never zero.
	if loss := tw["9999"]; loss.PERatio != nil {
		t.Errorf(`a "-" P/E parsed as %v`, *loss.PERatio)
	}

	two, err := p.FetchMarket(context.Background(), fetcher.MarketTPEX)
	if err != nil {
		t.Fatal(err)
	}
	if g := two["6488"]; g.PERatio == nil || *g.PERatio != 44.27 {
		t.Errorf("OTC PE = %v", g.PERatio)
	}
	if _, err := p.FetchMarket(context.Background(), "US"); err == nil {
		t.Error("market US was accepted")
	}
}

func TestProviderRejectsBadResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	old := endpointOverride
	endpointOverride = map[string]string{twsePE: srv.URL}
	defer func() { endpointOverride = old }()

	// A 429 must never become an empty dataset.
	if _, err := NewProvider(nil).FetchMarket(context.Background(), fetcher.MarketTWSE); err == nil {
		t.Error("a 429 was accepted")
	}
}

// Bulk, not N+1.
func TestFetchIsBulk(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/twse" {
			_, _ = w.Write([]byte(twseFixture))
			return
		}
		_, _ = w.Write([]byte(tpexFixture))
	}))
	defer srv.Close()
	old := endpointOverride
	endpointOverride = map[string]string{twsePE: srv.URL + "/twse", tpexPE: srv.URL + "/tpex"}
	defer func() { endpointOverride = old }()

	var syms []string
	for i := 0; i < 15; i++ {
		syms = append(syms, "2330.TW", "6488.TWO")
	}
	svc := NewService(nil, t.TempDir(), nil)
	snaps, err := svc.FetchAndStore(context.Background(), syms)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != len(syms) {
		t.Errorf("%d snapshots for %d symbols", len(snaps), len(syms))
	}
	if hits != 2 {
		t.Errorf("%d requests for %d symbols; want 2 (one per market)", hits, len(syms))
	}
}

// The archive round-trips, and a historical read never reaches later observations.
func TestArchivePointInTime(t *testing.T) {
	dir := t.TempDir()
	mk := func(date string, v float64) {
		t.Helper()
		o, _ := time.Parse("2006-01-02", date)
		if _, err := SaveSnapshot(dir, &Snapshot{
			Symbol: "2330.TW", ObservedAt: o, Status: Available,
			Ratios: &Ratios{Symbol: "2330.TW", Date: date, PERatio: &v},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("2026-06-01", 20)
	mk("2026-07-01", 30)
	mk("2026-08-01", 40)

	svc := NewService(nil, dir, nil)
	for _, tc := range []struct {
		asOf string
		want float64
		n    int
	}{
		{"2026-06-15", 20, 1}, {"2026-07-15", 30, 2}, {"2026-09-01", 40, 3},
	} {
		v, err := svc.LoadValuation(tc.asOf, "2330.TW")
		if err != nil {
			t.Fatalf("%s: %v", tc.asOf, err)
		}
		if v.TrailingPE == nil || *v.TrailingPE != tc.want {
			t.Errorf("as of %s trailing PE = %v, want %v", tc.asOf, v.TrailingPE, tc.want)
		}
		if v.HistoricalPE.SampleCount != tc.n {
			t.Errorf("as of %s saw %d observations, want %d — a later one leaked backwards",
				tc.asOf, v.HistoricalPE.SampleCount, tc.n)
		}
	}

	// Before the archive existed: nothing, and no fetch.
	v, _ := svc.LoadValuation("2026-01-01", "2330.TW")
	if v.Status != Unavailable || v.TrailingPE != nil {
		t.Errorf("a date before the archive returned %+v", v)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = fmt.Sprint

// ── R13-M6B: session identity ─────────────────────────────────────────────────────────

// arc builds one archived record: which session it describes, and which archive it came from.
func arc(session, archive string, v float64) Ratios {
	return Ratios{Date: session, ArchiveDate: archive, PERatio: &v}
}

// THE defect this work item fixes. The exchanges publish with a two-to-four-day lag — a fetch
// on 2026-09-01 returned the 2026-08-28 session — so the daily archive stores the same session
// repeatedly. Counting each file as a sample inflates the count and unlocks a percentile that
// rests on almost nothing.
func TestDuplicateSessionCountsOnce(t *testing.T) {
	hist := []Ratios{
		arc("2026-08-28", "2026-09-01", 28.05),
		arc("2026-08-28", "2026-09-02", 28.05),
		arc("2026-08-28", "2026-09-03", 28.05),
		arc("2026-09-01", "2026-09-04", 29.10),
	}
	v := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-09-05", MinSamples: 1})

	if got := v.HistoricalPE.SampleCount; got != 2 {
		t.Fatalf("SampleCount = %d, want 2 — four archives describe two sessions", got)
	}
	// And the values are the two distinct sessions, not four copies.
	if v.HistoricalPE.Min == nil || *v.HistoricalPE.Min != 28.05 {
		t.Errorf("min = %v", v.HistoricalPE.Min)
	}
	if v.HistoricalPE.Max == nil || *v.HistoricalPE.Max != 29.10 {
		t.Errorf("max = %v", v.HistoricalPE.Max)
	}
}

// A later archive of the same session is a CORRECTION, so it supersedes the earlier copy.
func TestCorrectedSessionUsesLatestArchive(t *testing.T) {
	hist := []Ratios{
		arc("2026-08-28", "2026-09-01", 28),
		arc("2026-08-28", "2026-09-03", 30), // the exchange revised it
	}
	v := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-09-04", MinSamples: 1})
	if v.HistoricalPE.SampleCount != 1 {
		t.Fatalf("SampleCount = %d, want 1", v.HistoricalPE.SampleCount)
	}
	if v.HistoricalPE.Median == nil || *v.HistoricalPE.Median != 30 {
		t.Errorf("median = %v, want the corrected 30", v.HistoricalPE.Median)
	}

	// Input order must not decide the outcome — the archive date does.
	reversed := []Ratios{hist[1], hist[0]}
	v2 := Calculate(Input{Symbol: "2330.TW", History: reversed, AsOfDate: "2026-09-04", MinSamples: 1})
	if v2.HistoricalPE.Median == nil || *v2.HistoricalPE.Median != 30 {
		t.Errorf("reversing the input changed the winner: %v", v2.HistoricalPE.Median)
	}
}

// THE look-ahead guard for corrections. "Latest wins" must mean the latest the caller is
// ALLOWED to see: a run dated 09-02 may legitimately see the 08-28 session, but not a
// correction to it that was only archived on 09-03.
func TestCorrectionIsInvisibleBeforeItWasArchived(t *testing.T) {
	hist := []Ratios{
		arc("2026-08-28", "2026-09-01", 28),
		arc("2026-08-28", "2026-09-03", 30),
	}
	early := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-09-02", MinSamples: 1})
	if early.HistoricalPE.SampleCount != 1 {
		t.Fatalf("SampleCount = %d", early.HistoricalPE.SampleCount)
	}
	if early.HistoricalPE.Median == nil || *early.HistoricalPE.Median != 28 {
		t.Errorf("a run dated 09-02 saw %v; the correction was archived on 09-03 — look-ahead",
			early.HistoricalPE.Median)
	}

	// After the correction was archived, it wins.
	later := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-09-03", MinSamples: 1})
	if later.HistoricalPE.Median == nil || *later.HistoricalPE.Median != 30 {
		t.Errorf("a run dated 09-03 did not see the correction: %v", later.HistoricalPE.Median)
	}
}

// The minimum-sample gate must count SESSIONS. Twenty files describing seven sessions is
// seven observations, and unlocking a percentile on that would be the whole defect.
func TestMinSamplesCountsSessionsNotFiles(t *testing.T) {
	var hist []Ratios
	base, _ := time.Parse("2006-01-02", "2026-06-01")
	// Seven sessions, each archived three times (21 files).
	for s := 0; s < 7; s++ {
		session := base.AddDate(0, 0, s*7).Format("2006-01-02")
		for a := 0; a < 3; a++ {
			archive := base.AddDate(0, 0, s*7+a+1).Format("2006-01-02")
			hist = append(hist, arc(session, archive, 20+float64(s)))
		}
	}
	if len(hist) != 21 {
		t.Fatalf("fixture built %d records", len(hist))
	}

	v := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-09-01"})
	if v.HistoricalPE.Status != InsufficientData {
		t.Errorf("status = %q with 7 sessions (minimum %d) — 21 files unlocked a distribution "+
			"that rests on seven observations", v.HistoricalPE.Status, MinHistoricalPESamples)
	}
	if v.HistoricalPE.SampleCount != 7 {
		t.Errorf("SampleCount = %d, want 7 unique sessions", v.HistoricalPE.SampleCount)
	}
	if v.HistoricalPE.Median != nil || v.HistoricalPE.CurrentPercentile != nil {
		t.Error("statistics were produced below the minimum")
	}
}

// The statistics themselves must be computed from deduplicated sessions.
func TestStatisticsUseDedupedSessions(t *testing.T) {
	base, _ := time.Parse("2006-01-02", "2026-01-01")
	var hist []Ratios
	// Sessions 1..25 with P/E 1..25 — median 13. Session 1 is archived ten extra times with
	// the same value, which a file-counting implementation would let drag the median down.
	for i := 1; i <= 25; i++ {
		session := base.AddDate(0, 0, i).Format("2006-01-02")
		hist = append(hist, arc(session, base.AddDate(0, 0, i+1).Format("2006-01-02"), float64(i)))
	}
	for a := 0; a < 10; a++ {
		hist = append(hist, arc(base.AddDate(0, 0, 1).Format("2006-01-02"),
			base.AddDate(0, 0, 30+a).Format("2006-01-02"), 1))
	}

	v := Calculate(Input{Symbol: "2330.TW", History: hist, AsOfDate: "2026-06-01",
		Current: &Ratios{Date: "2026-06-01", PERatio: ptr(13)}})
	h := v.HistoricalPE
	if h.Status != Available {
		t.Fatalf("status = %q", h.Status)
	}
	if h.SampleCount != 25 {
		t.Fatalf("SampleCount = %d, want 25 sessions (35 files)", h.SampleCount)
	}
	if h.Median == nil || *h.Median != 13 {
		t.Errorf("median = %v, want 13 — duplicates of the cheapest session skewed it",
			h.Median)
	}
	if h.P25 == nil || *h.P25 != 7 || h.P75 == nil || *h.P75 != 19 {
		t.Errorf("quartiles = %v / %v, want 7 / 19", h.P25, h.P75)
	}
	// 13 of 25 sessions are at or below 13.
	if h.CurrentPercentile == nil || math.Abs(*h.CurrentPercentile-13.0/25.0) > 1e-9 {
		t.Errorf("percentile = %v, want 13/25", h.CurrentPercentile)
	}
}

// Unusable records are still dropped rather than becoming a session.
func TestInvalidRecordsDoNotCreateSessions(t *testing.T) {
	nan := math.NaN()
	hist := []Ratios{
		arc("2026-08-28", "2026-09-01", 20),
		{Date: "2026-08-27", ArchiveDate: "2026-09-01"},                  // no P/E published
		{Date: "2026-08-26", ArchiveDate: "2026-09-01", PERatio: ptr(0)}, // non-positive
		{Date: "2026-08-25", ArchiveDate: "2026-09-01", PERatio: &nan},
		{Date: "", ArchiveDate: "2026-09-01", PERatio: ptr(15)}, // no session date
	}
	v := Calculate(Input{Symbol: "X.TW", History: hist, AsOfDate: "2026-09-05", MinSamples: 1})
	if v.HistoricalPE.SampleCount != 1 {
		t.Errorf("SampleCount = %d, want 1 — an unusable record became a session",
			v.HistoricalPE.SampleCount)
	}
}

// The archive reader must stamp where each record came from, or dedup has nothing to work with.
func TestLoadHistoryStampsTheArchiveDate(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		archive, session string
		pe               float64
	}{
		{"2026-09-01", "2026-08-28", 28},
		{"2026-09-02", "2026-08-28", 28},
		{"2026-09-03", "2026-09-01", 29},
	} {
		o, _ := time.Parse("2006-01-02", tc.archive)
		v := tc.pe
		if _, err := SaveSnapshot(dir, &Snapshot{
			Symbol: "2330.TW", ObservedAt: o, Status: Available,
			Ratios: &Ratios{Symbol: "2330.TW", Date: tc.session, PERatio: &v},
		}); err != nil {
			t.Fatal(err)
		}
	}

	hist, latest, err := LoadHistory(dir, "2026-09-05", "2330.TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("read %d records, want 3 files", len(hist))
	}
	for i, want := range []string{"2026-09-01", "2026-09-02", "2026-09-03"} {
		if hist[i].ArchiveDate != want {
			t.Errorf("record %d archive date = %q, want %q", i, hist[i].ArchiveDate, want)
		}
	}
	// ArchiveDate must NOT be persisted — it describes the archive, not the observation.
	b, err := os.ReadFile(SnapshotPath(dir, "2026-09-01", "2330", "TW"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(b), "archive_date") || contains(string(b), "ArchiveDate") {
		t.Errorf("the archive date was written into the snapshot: %s", b)
	}

	// End to end: three files, two sessions.
	v := Calculate(Input{Symbol: "2330.TW", Current: latest, History: hist,
		AsOfDate: "2026-09-05", MinSamples: 1})
	if v.HistoricalPE.SampleCount != 2 {
		t.Errorf("SampleCount = %d, want 2 sessions from 3 files", v.HistoricalPE.SampleCount)
	}
}
