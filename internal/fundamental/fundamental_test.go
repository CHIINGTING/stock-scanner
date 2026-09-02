package fundamental

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ── parsing ───────────────────────────────────────────────────────────────────────────

// ROC years are the single most dangerous parse in this package: reading 115 as 115 AD would
// place every publication date nineteen centuries in the past, and the point-in-time filter
// would then admit every record at every date. The look-ahead would be total and silent.
func TestParseROCDates(t *testing.T) {
	got, err := parseROCDate("1150831")
	if err != nil {
		t.Fatalf("1150831: %v", err)
	}
	if y, m, d := got.Date(); y != 2026 || m != time.August || d != 31 {
		t.Errorf("1150831 → %v, want 2026-08-31", got)
	}
	if got.Location().String() == "UTC" {
		t.Error("a Taiwanese disclosure date was parsed as UTC; the day boundary would be 8h early")
	}

	y, m, err := parseROCYearMonth("11507")
	if err != nil || y != 2026 || m != 7 {
		t.Errorf("11507 → (%d, %d), %v; want 2026-07", y, m, err)
	}
	if gotY, err := parseROCYear("115"); err != nil || gotY != 2026 {
		t.Errorf("115 → %d, %v; want 2026", gotY, err)
	}
	// An already-Gregorian year must not gain another 1911.
	if gotY, _ := parseROCYear("2026"); gotY != 2026 {
		t.Errorf("2026 → %d; a Gregorian year was shifted again", gotY)
	}

	for _, bad := range []string{"", "115083", "11508311", "abcdefg", "1151331", "1150832"} {
		if _, err := parseROCDate(bad); err == nil {
			t.Errorf("%q was accepted as a date", bad)
		}
	}
}

// THE missing-vs-zero guard at the parser. These datasets leave a cell blank whenever it does
// not apply to the industry — a bank has no cost of sales — and answering "0" would report
// that every bank ran a 100% gross margin.
func TestParseNumberMissingIsNotZero(t *testing.T) {
	for _, blank := range []string{"", "  ", "-", "--", "N/A"} {
		v, err := parseNumber(blank)
		if err != nil {
			t.Errorf("%q: %v", blank, err)
		}
		if v != nil {
			t.Errorf("%q parsed as %v; a blank cell must stay missing", blank, *v)
		}
	}
	// A real zero is a reading and must come back as one.
	v, err := parseNumber("0")
	if err != nil || v == nil || *v != 0 {
		t.Errorf(`"0" → %v, %v; want a present zero`, v, err)
	}
	// Thousands separators and parenthesised negatives both appear in MOPS exports.
	for in, want := range map[string]float64{
		"1,234,567": 1234567, "(1.25)": -1.25, "-3.5": -3.5, "49.33": 49.33,
	} {
		v, err := parseNumber(in)
		if err != nil || v == nil || *v != want {
			t.Errorf("%q → %v, %v; want %v", in, v, err, want)
		}
	}
	// A value that is PRESENT but unreadable is an error — silently dropping it would hide a
	// format change until someone noticed the numbers were wrong.
	if _, err := parseNumber("about 12"); err == nil {
		t.Error(`"about 12" was accepted`)
	}
}

// ── provider ──────────────────────────────────────────────────────────────────────────

// Fixtures captured 2026-09-01 from the live endpoints, trimmed to the rows under test.
//
//	TWSE  openapi.twse.com.tw/v1/opendata/t187ap05_L      and  .../t187ap06_L_ci
//	TPEx  www.tpex.org.tw/openapi/v1/mopsfin_t187ap05_OA  and  .../mopsfin_t187ap06_O_ci
const twseRevenueFixture = `[
 {"出表日期":"1150817","資料年月":"11507","公司代號":"2330","公司名稱":"台積電","產業別":"半導體業",
  "營業收入-當月營收":"467580548","營業收入-上月營收":"442679969","營業收入-去年當月營收":"323165707",
  "累計營業收入-當月累計營收":"2872064238","累計營業收入-去年累計營收":"2096211240","備註":"-"}
]`

const twseIncomeFixture = `[
 {"出表日期":"1150831","年度":"115","季別":"2","公司代號":"2330","公司名稱":"台積電",
  "營業收入":"2404483690.00","營業成本":"792877574.00","營業毛利（毛損）淨額":"1611606116.00",
  "營業利益（損失）":"1425568793.00","本期淨利（淨損）":"1279582227.00","基本每股盈餘（元）":"49.33"}
]`

const tpexRevenueFixture = `[
 {"出表日期":"1150817","資料年月":"11507","公司代號":"6488","公司名稱":"環球晶","產業別":"半導體業",
  "營業收入-當月營收":"4977758","營業收入-上月營收":"5624376","營業收入-去年當月營收":"4162281",
  "累計營業收入-當月累計營收":"34176866","累計營業收入-去年累計營收":"35764712","備註":"-"}
]`

const tpexIncomeFixture = `[
 {"Date":"1150831","Year":"115","Season":"2","SecuritiesCompanyCode":"6488","CompanyName":"環球晶",
  "營業收入":"29199108.00","營業成本":"23145169.00","營業毛利（毛損）淨額":"6053939.00",
  "營業利益（損失）":"2898184.00","本期淨利（淨損）":"2318912.00","基本每股盈餘（元）":"11.87"}
]`

// serveFixtures points the provider at a local server. Tests never touch the live endpoints.
func serveFixtures(t *testing.T, body map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := body[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(b))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	old := endpointOverride
	endpointOverride = map[string]string{
		twseRevenue: srv.URL + "/twse-rev",
		twseIncome:  srv.URL + "/twse-inc",
		tpexRevenue: srv.URL + "/tpex-rev",
		tpexIncome:  srv.URL + "/tpex-inc",
	}
	t.Cleanup(func() { endpointOverride = old })
}

func defaultFixtures() map[string]string {
	return map[string]string{
		"/twse-rev": twseRevenueFixture, "/twse-inc": twseIncomeFixture,
		"/tpex-rev": tpexRevenueFixture, "/tpex-inc": tpexIncomeFixture,
	}
}

// Both markets must route to their own datasets. TWSE's endpoints do not contain OTC
// companies at all — 6488 is absent from every _L dataset — so a provider that sent an OTC
// symbol to the TWSE endpoint would report a real company as having no financials.
func TestProviderRoutesBothMarkets(t *testing.T) {
	serveFixtures(t, defaultFixtures())
	p := NewProvider(nil)
	ctx := context.Background()

	tw, err := p.FetchMarket(ctx, fetcher.MarketTWSE)
	if err != nil {
		t.Fatalf("TWSE: %v", err)
	}
	if _, ok := tw.Revenue["2330"]; !ok {
		t.Error("2330 missing from the TWSE revenue dataset")
	}
	if _, ok := tw.Revenue["6488"]; ok {
		t.Error("an OTC company appeared in the TWSE dataset; the fixture is wrong")
	}

	two, err := p.FetchMarket(ctx, fetcher.MarketTPEX)
	if err != nil {
		t.Fatalf("TPEx: %v", err)
	}
	if _, ok := two.Financials["6488"]; !ok {
		t.Error("6488 missing from the TPEx financial dataset")
	}

	// An unsupported market must be refused, not silently routed to TWSE.
	if _, err := p.FetchMarket(ctx, "US"); err == nil {
		t.Error("market US was accepted")
	}
}

// THE most expensive semantic in this package. The income statement's 季別=2 row is the FIRST
// HALF, not Q2 alone — verified against the monthly series, exact to the last digit. A
// consumer reading it as a standalone quarter would double every per-quarter figure.
func TestQuarterlyStatementIsCumulative(t *testing.T) {
	serveFixtures(t, defaultFixtures())
	p := NewProvider(nil)

	for _, tc := range []struct{ market, code string }{
		{fetcher.MarketTWSE, "2330"}, {fetcher.MarketTPEX, "6488"},
	} {
		ds, err := p.FetchMarket(context.Background(), tc.market)
		if err != nil {
			t.Fatalf("%s: %v", tc.market, err)
		}
		fin, ok := ds.Financials[tc.code]
		if !ok {
			t.Fatalf("%s missing", tc.code)
		}
		if !fin.Period.Cumulative {
			t.Fatalf("%s Q%d was not marked cumulative", tc.code, fin.Period.Quarter)
		}

		// The proof: year-to-date revenue through July, minus July, equals the Q2 statement.
		rev := ds.Revenue[tc.code]
		h1 := float64(rev.YTD) - float64(rev.Revenue)
		if math.Abs(h1-float64(fin.Revenue)) > 1 {
			t.Errorf("%s: Jan–Jul %.0f − Jul %.0f = %.0f, but the Q2 statement says %.0f — "+
				"the cumulative assumption no longer holds and every derived figure is suspect",
				tc.code, float64(rev.YTD), float64(rev.Revenue), h1, float64(fin.Revenue))
		}
	}
}

// Monetary units are normalised once, at the boundary. The sources report 千元; nothing
// downstream should have to know that.
func TestUnitNormalisation(t *testing.T) {
	serveFixtures(t, defaultFixtures())
	ds, err := NewProvider(nil).FetchMarket(context.Background(), fetcher.MarketTWSE)
	if err != nil {
		t.Fatal(err)
	}
	rev := ds.Revenue["2330"]
	// 467,580,548 千元 = 467,580,548,000 TWD.
	if float64(rev.Revenue) != 467580548*1000 {
		t.Errorf("revenue = %v, want thousands converted to whole TWD", float64(rev.Revenue))
	}
	fin := ds.Financials["2330"]
	if float64(fin.Revenue) != 2404483690*1000 {
		t.Errorf("statement revenue = %v", float64(fin.Revenue))
	}
	// EPS is per share and keeps its own scale — multiplying it by 1000 would be catastrophic
	// and completely plausible-looking.
	if fin.CumulativeEPS == nil || *fin.CumulativeEPS != 49.33 {
		t.Errorf("EPS = %v, want 49.33 (per share, not thousands)", fin.CumulativeEPS)
	}
}

func TestProviderFailures(t *testing.T) {
	// Non-2xx must never become an empty dataset: "rate limited" and "no company reported"
	// are different facts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	old := endpointOverride
	endpointOverride = map[string]string{
		twseRevenue: srv.URL, twseIncome: srv.URL,
	}
	defer func() { endpointOverride = old }()

	if _, err := NewProvider(nil).FetchMarket(context.Background(), fetcher.MarketTWSE); err == nil {
		t.Error("a 429 on both datasets was accepted")
	}

	// Malformed JSON is an error too.
	fx := defaultFixtures()
	fx["/twse-rev"] = "{not json"
	serveFixtures(t, fx)
	ds, err := NewProvider(nil).FetchMarket(context.Background(), fetcher.MarketTWSE)
	if err != nil {
		t.Fatalf("one bad dataset should not fail the other: %v", err)
	}
	if ds.RevenueErr == nil {
		t.Error("malformed revenue JSON was accepted")
	}
	// …and the healthy half survived.
	if _, ok := ds.Financials["2330"]; !ok {
		t.Error("a broken revenue dataset took the financial dataset with it")
	}

	// HTTP 200 with an empty array is a SUCCESSFUL fetch of an empty dataset.
	fx = defaultFixtures()
	fx["/twse-rev"] = "[]"
	serveFixtures(t, fx)
	ds, err = NewProvider(nil).FetchMarket(context.Background(), fetcher.MarketTWSE)
	if err != nil || ds.RevenueErr != nil {
		t.Errorf("an empty dataset was treated as an error: %v / %v", err, ds.RevenueErr)
	}
	if len(ds.Revenue) != 0 {
		t.Errorf("%d rows from an empty dataset", len(ds.Revenue))
	}
}

// ── derived metrics ───────────────────────────────────────────────────────────────────

func TestRevenueGrowth(t *testing.T) {
	r := &MonthlyRevenue{
		Revenue: 120, PriorMonth: 100, PriorYearMonth: 80, YTD: 700, PriorYearYTD: 500,
	}
	m := DeriveRevenue(r)
	if m.YoY.Status != Available || math.Abs(m.YoY.Value-0.5) > 1e-9 {
		t.Errorf("YoY = %+v, want 0.5 (120 vs 80)", m.YoY)
	}
	if m.MoM.Status != Available || math.Abs(m.MoM.Value-0.2) > 1e-9 {
		t.Errorf("MoM = %+v, want 0.2 (120 vs 100)", m.MoM)
	}
	if m.YTDYoY.Status != Available || math.Abs(m.YTDYoY.Value-0.4) > 1e-9 {
		t.Errorf("YTD YoY = %+v, want 0.4", m.YTDYoY)
	}

	// A DECLINE is a valid reading, not a missing one.
	down := DeriveRevenue(&MonthlyRevenue{Revenue: 50, PriorYearMonth: 100})
	if down.YoY.Status != Available || down.YoY.Value != -0.5 {
		t.Errorf("a 50%% decline = %+v; negative growth is data, not absence", down.YoY)
	}

	// Zero growth is likewise a reading.
	flat := DeriveRevenue(&MonthlyRevenue{Revenue: 100, PriorYearMonth: 100})
	if flat.YoY.Status != Available || flat.YoY.Value != 0 {
		t.Errorf("flat growth = %+v, want AVAILABLE 0", flat.YoY)
	}
}

// Division by zero must yield UNAVAILABLE — never NaN, never Inf, never 0.
func TestGrowthGuards(t *testing.T) {
	zero := DeriveRevenue(&MonthlyRevenue{Revenue: 100, PriorYearMonth: 0})
	if zero.YoY.Status != Unavailable {
		t.Errorf("a zero base gave %+v", zero.YoY)
	}
	if zero.YoY.Value != 0 || math.IsNaN(zero.YoY.Value) || math.IsInf(zero.YoY.Value, 0) {
		t.Errorf("a zero base leaked a value: %v", zero.YoY.Value)
	}

	// A NEGATIVE base is the subtler trap: the arithmetic works and produces something like
	// +200% for a swing from −1 to +1, which reads as spectacular growth when what happened
	// was a return to profit.
	neg := growth(1, -1, "prior period")
	if neg.Status == Available {
		t.Errorf("a negative base produced %v — that number would mislead", neg.Value)
	}
	if !contains(neg.Reason, "NOT_MEANINGFUL") {
		t.Errorf("the reason does not mark it as not meaningful: %q", neg.Reason)
	}

	// No record at all.
	none := DeriveRevenue(nil)
	for name, m := range map[string]Metric{"YoY": none.YoY, "MoM": none.MoM} {
		if m.Status != Unavailable || m.Value != 0 {
			t.Errorf("%s with no record = %+v", name, m)
		}
	}
}

func TestMargins(t *testing.T) {
	f := &Financials{Revenue: 1000, GrossProfit: 400, OperatingIncome: 250, NetIncome: 200}
	m := DeriveFinancials(f)
	if m.GrossMargin.Status != Available || math.Abs(m.GrossMargin.Value-0.4) > 1e-9 {
		t.Errorf("gross margin = %+v, want 0.4 as a RATIO (not 40)", m.GrossMargin)
	}
	if math.Abs(m.OperatingMargin.Value-0.25) > 1e-9 || math.Abs(m.NetMargin.Value-0.2) > 1e-9 {
		t.Errorf("margins = %+v", m)
	}

	// A NEGATIVE margin is a real, important reading — a loss-making quarter.
	loss := DeriveFinancials(&Financials{Revenue: 1000, OperatingIncome: -300, NetIncome: -350})
	if loss.OperatingMargin.Status != Available || loss.OperatingMargin.Value != -0.3 {
		t.Errorf("a loss-making margin = %+v; that is data, not an error", loss.OperatingMargin)
	}

	// Revenue of zero makes every margin meaningless, not zero.
	for _, rev := range []Money{0, -100} {
		z := DeriveFinancials(&Financials{Revenue: rev, GrossProfit: 50})
		if z.GrossMargin.Status != Unavailable {
			t.Errorf("revenue %v gave a margin of %+v", rev, z.GrossMargin)
		}
	}
}

// Nothing in this package may emit NaN or Inf — they survive as errors in JSON, or worse,
// render as plausible numbers.
func TestNoNaNOrInfReachesJSON(t *testing.T) {
	for _, r := range []*MonthlyRevenue{
		{Revenue: 1, PriorYearMonth: 0}, {Revenue: 0, PriorMonth: 0},
		{Revenue: math.MaxFloat64, PriorYearMonth: 1e-300},
	} {
		b, err := json.Marshal(DeriveRevenue(r))
		if err != nil {
			t.Errorf("%+v produced unmarshalable metrics: %v", r, err)
			continue
		}
		for _, bad := range []string{"NaN", "Inf", "inf"} {
			if contains(string(b), bad) {
				t.Errorf("%+v produced %q in %s", r, bad, b)
			}
		}
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

// ── snapshot & point-in-time ──────────────────────────────────────────────────────────

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	eps := 49.33
	pub := time.Date(2026, 8, 31, 0, 0, 0, 0, taipei())
	want := &Snapshot{
		SchemaVersion: SchemaVersion, Symbol: "2330.TW", Source: SourceName,
		ObservedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, taipei()),
		Status:     Available, RevenueStatus: Available, FinancialStatus: Available,
		Revenue: &MonthlyRevenue{Symbol: "2330.TW", Period: Period{Year: 2026, Month: 7},
			Revenue: 1000, PriorYearMonth: 800, PublishedAt: pub},
		Financials: &Financials{Symbol: "2330.TW",
			Period:  Period{Year: 2026, Quarter: 2, Cumulative: true},
			Revenue: 5000, GrossProfit: 2000, CumulativeEPS: &eps, PublishedAt: pub},
	}
	path, err := SaveSnapshot(dir, want)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// Dated directory, canonical symbol filename — never a company name.
	if filepath.Base(path) != "2330_TW.json" {
		t.Errorf("filename = %s", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "2026-09-01" {
		t.Errorf("directory = %s; the archive must be dated or history cannot accrue",
			filepath.Dir(path))
	}

	got, err := LoadSnapshot(dir, "2026-09-01", "2330.TW")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Compared field by field with the timestamp via Equal(): a time.Time carries a location
	// POINTER, and one rebuilt by the JSON decoder never shares it with the original even
	// when both name the same instant and offset. reflect.DeepEqual would fail on identity.
	if !got.Revenue.PublishedAt.Equal(want.Revenue.PublishedAt) {
		t.Errorf("published at %v, want %v", got.Revenue.PublishedAt, want.Revenue.PublishedAt)
	}
	gotRev, wantRev := *got.Revenue, *want.Revenue
	gotRev.PublishedAt, wantRev.PublishedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(gotRev, wantRev) {
		t.Errorf("revenue round trip:\n got %+v\nwant %+v", gotRev, wantRev)
	}
	if got.Financials.CumulativeEPS == nil || *got.Financials.CumulativeEPS != eps {
		t.Errorf("EPS lost: %v", got.Financials.CumulativeEPS)
	}
	if !got.Financials.Period.Cumulative {
		t.Error("the cumulative flag was lost in serialisation — the most dangerous field to drop")
	}

	// A corrupt file is an ERROR, never zeroed fundamentals.
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(dir, "2026-09-01", "2330.TW"); err == nil {
		t.Error("a corrupt snapshot loaded successfully")
	}
}

// THE look-ahead guard. A run dated in the past must see what was knowable then.
func TestPointInTimeSelectsThePastSnapshot(t *testing.T) {
	dir := t.TempDir()
	mk := func(observed string, rev Money, pub string) {
		t.Helper()
		p, _ := time.ParseInLocation("2006-01-02", pub, taipei())
		o, _ := time.ParseInLocation("2006-01-02", observed, taipei())
		if _, err := SaveSnapshot(dir, &Snapshot{
			Symbol: "2330.TW", ObservedAt: o, Status: Available, RevenueStatus: Available,
			Revenue: &MonthlyRevenue{Symbol: "2330.TW", Revenue: rev, PriorYearMonth: 100,
				PublishedAt: p},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("2026-06-10", 100, "2026-06-10")
	mk("2026-07-10", 200, "2026-07-10")
	mk("2026-08-10", 300, "2026-08-10")

	svc := NewService(nil, dir, nil)
	for _, tc := range []struct {
		asOf string
		want Money
	}{
		{"2026-06-15", 100}, {"2026-07-20", 200}, {"2026-08-10", 300}, {"2026-12-31", 300},
	} {
		v, err := svc.LoadView(tc.asOf, "2330.TW")
		if err != nil {
			t.Fatalf("%s: %v", tc.asOf, err)
		}
		if v.Revenue == nil {
			t.Fatalf("%s: no revenue", tc.asOf)
		}
		if v.Revenue.Revenue != tc.want {
			t.Errorf("as of %s got revenue %v, want %v — a later snapshot leaked backwards",
				tc.asOf, v.Revenue.Revenue, tc.want)
		}
	}

	// Before ANY snapshot existed, there is nothing — and crucially, no live fetch.
	v, err := svc.LoadView("2026-01-01", "2330.TW")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if v.Status != Unavailable || v.Revenue != nil {
		t.Errorf("a date before the archive began returned %+v", v)
	}
}

// The second half of the contract: a snapshot old enough is not sufficient — the RECORD
// inside it must also have been published by the cutoff.
func TestRecordPublishedAfterCutoffIsInvisible(t *testing.T) {
	dir := t.TempDir()
	obs := time.Date(2026, 8, 31, 18, 0, 0, 0, taipei())
	pub := time.Date(2026, 8, 31, 0, 0, 0, 0, taipei())
	if _, err := SaveSnapshot(dir, &Snapshot{
		Symbol: "2330.TW", ObservedAt: obs, Status: Available, FinancialStatus: Available,
		Financials: &Financials{Symbol: "2330.TW",
			Period:  Period{Year: 2026, Quarter: 2, Cumulative: true},
			Revenue: 1000, GrossProfit: 400, PublishedAt: pub},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, dir, nil)

	// The day of publication: visible.
	if v, _ := svc.LoadView("2026-08-31", "2330.TW"); v.Financials == nil {
		t.Error("a statement published on the cutoff date was hidden")
	}
	// A run dated BEFORE publication must not see it, even though the snapshot exists.
	if v, _ := svc.LoadView("2026-08-20", "2330.TW"); v.Financials != nil {
		t.Error("a statement published on 08-31 was visible to a run dated 08-20 — look-ahead")
	}

	// A record with no usable publication date is not safe for point-in-time use at all.
	if VisibleAsOf(time.Time{}, "2026-12-31") {
		t.Error("a record with no publication date was treated as visible")
	}
}

// A stock present in one dataset and absent from the other is PARTIAL, not unavailable —
// revenue and statements are published on different schedules by different pipelines.
func TestPartialAvailability(t *testing.T) {
	ds := &Dataset{
		ObservedAt: time.Now(),
		Revenue:    map[string]MonthlyRevenue{"2330": {Symbol: "2330", Revenue: 100}},
		Financials: map[string]Financials{},
	}
	snap := buildSnapshot("2330.TW", "2330", ds)
	if snap.Status != Partial {
		t.Errorf("status = %s, want PARTIAL", snap.Status)
	}
	if snap.RevenueStatus != Available || snap.FinancialStatus != Unavailable {
		t.Errorf("halves = %s / %s", snap.RevenueStatus, snap.FinancialStatus)
	}

	// Absent from both.
	empty := buildSnapshot("9999.TW", "9999", &Dataset{
		Revenue: map[string]MonthlyRevenue{}, Financials: map[string]Financials{}})
	if empty.Status != Unavailable || empty.Reason == "" {
		t.Errorf("a stock in neither dataset = %+v", empty)
	}
	// The three states must be distinct values.
	if Available == Partial || Partial == Unavailable || Unavailable == Disabled {
		t.Fatal("availability states collide")
	}
}

// Bulk, not N+1: a hundred candidates must cost four requests, not two hundred.
func TestFetchIsBulkNotPerSymbol(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/twse-rev":
			_, _ = w.Write([]byte(twseRevenueFixture))
		case "/twse-inc":
			_, _ = w.Write([]byte(twseIncomeFixture))
		case "/tpex-rev":
			_, _ = w.Write([]byte(tpexRevenueFixture))
		default:
			_, _ = w.Write([]byte(tpexIncomeFixture))
		}
	}))
	defer srv.Close()
	old := endpointOverride
	endpointOverride = map[string]string{
		twseRevenue: srv.URL + "/twse-rev", twseIncome: srv.URL + "/twse-inc",
		tpexRevenue: srv.URL + "/tpex-rev", tpexIncome: srv.URL + "/tpex-inc",
	}
	defer func() { endpointOverride = old }()

	// Twenty symbols across both markets.
	var syms []string
	for i := 0; i < 10; i++ {
		syms = append(syms, "2330.TW", "6488.TWO")
	}
	svc := NewService(nil, t.TempDir(), nil)
	got, err := svc.FetchAndStore(context.Background(), syms)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(got) != len(syms) {
		t.Errorf("%d snapshots for %d symbols", len(got), len(syms))
	}
	// Two datasets × two markets. Anything more is an N+1.
	if hits != 4 {
		t.Errorf("%d HTTP requests for %d symbols; want 4 (two datasets per market)",
			hits, len(syms))
	}
}
