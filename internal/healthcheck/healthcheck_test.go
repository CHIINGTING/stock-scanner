package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// ── fixtures ──────────────────────────────────────────────────────────────────────────

// stubPrices is a PriceSource that returns a series a test wrote by hand.
//
// It replaces the NETWORK, and nothing else. Truncation, the scanner's analyzers, the
// archives and every projection below still run for real — a test that stubbed those would
// only be testing itself.
type stubPrices struct {
	data map[string]fetcher.StockData
	err  error
}

func (s stubPrices) Fetch(info fetcher.StockInfo) (fetcher.StockData, error) {
	if s.err != nil {
		return fetcher.StockData{}, s.err
	}
	d, ok := s.data[info.Symbol]
	if !ok {
		return fetcher.StockData{}, errors.New("no price data for " + info.Symbol)
	}
	return d, nil
}

// series builds n daily bars ending on end, rising by 1 TWD a day from start.
func series(end time.Time, n int, start float64) []fetcher.Candle {
	out := make([]fetcher.Candle, 0, n)
	for i := n - 1; i >= 0; i-- {
		d := end.AddDate(0, 0, -i)
		p := start + float64(n-1-i)
		out = append(out, fetcher.Candle{
			Date: d, Open: p, High: p + 1, Low: p - 1, Close: p, AdjClose: p, Volume: 1_000_000,
		})
	}
	return out
}

func day(s string) time.Time {
	t, err := time.ParseInLocation(dateLayout, s, Taipei)
	if err != nil {
		panic(err)
	}
	return t
}

const testOverlay = `
stocks:
  - symbol: "2330.TW"
    name: "台積電"
    industry: "半導體"
    aliases: [TSMC]
  - symbol: "9999"
    name: "無市場股"
`

func testCatalog(t *testing.T) *stockcatalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(p, []byte(testOverlay), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := stockcatalog.Load(stockcatalog.Sources{
		NamesFile: "-", SectorsFile: "-", AliasesFile: "-", OverlayFile: p,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

// newService builds a service whose archives all live under one temp root, so a test can
// place exactly the evidence it wants to reason about and nothing else.
func newService(t *testing.T, prices PriceSource, today string) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		Scanner: scanner.Config{
			// Pointed at the temp root so a test never reads the repo's real
			// data/institution — the default the scanner config would otherwise supply.
			Institution: scanner.InstitutionConfig{
				SnapshotDir: filepath.Join(root, "institution"),
			},
		},
		FundamentalDir:    filepath.Join(root, "fundamental"),
		ValuationDir:      filepath.Join(root, "valuation"),
		MacroDir:          filepath.Join(root, "macro"),
		MarketSnapshotDir: filepath.Join(root, "market"),
		NewsSnapshotDir:   filepath.Join(root, "reports"),
	}
	s := &Service{
		Catalog: testCatalog(t),
		Prices:  prices,
		Scanner: scanner.New(cfg.Scanner),
		Cfg:     cfg.Defaulted(),
		Now:     func() time.Time { return day(today).Add(15 * time.Hour) },
	}
	return s, root
}

func check(t *testing.T, s *Service, req Request) *Health {
	t.Helper()
	h, err := s.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check(%+v): %v", req, err)
	}
	return h
}

// ── identity ──────────────────────────────────────────────────────────────────────────

func TestUnknownStockIsAnError(t *testing.T) {
	s, _ := newService(t, stubPrices{}, "2026-09-04")
	for _, sym := range []string{"0000", "not-a-stock", ""} {
		_, err := s.Check(context.Background(), Request{Symbol: sym})
		if !errors.Is(err, ErrUnknownStock) {
			t.Fatalf("Check(%q) err = %v, want ErrUnknownStock", sym, err)
		}
	}
}

// The identity on the answer must be the catalog's, never assembled from the request.
func TestIdentityComesFromTheCatalog(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Name: "whatever the feed said", Market: "TW", Candles: series(end, 120, 900)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "TSMC"})
	if h.Identity.Symbol != "2330" || h.Identity.Name != "台積電" {
		t.Fatalf("identity = %+v — the price feed's name must not become the identity", h.Identity)
	}
	if h.Identity.CanonicalSymbol != "2330.TW" || h.Identity.MarketSource != "CATALOG" {
		t.Fatalf("identity = %+v", h.Identity)
	}
}

// When the catalog does not know the market, the fetcher's resolution is recorded as
// RESOLVED — a discovered fact, distinct from a configured one and from a guess.
func TestUnknownMarketIsResolvedByTheFetcherAndLabelled(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"9999": {Symbol: "9999", Market: "TWO", Candles: series(end, 60, 50)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "9999"})
	if h.Identity.MarketCode != "TWO" || h.Identity.Market != "TPEX" {
		t.Fatalf("identity = %+v", h.Identity)
	}
	if h.Identity.MarketSource != MarketSourceResolved {
		t.Fatalf("MarketSource = %q, want RESOLVED", h.Identity.MarketSource)
	}
	if h.Identity.CanonicalSymbol != "9999.TWO" {
		t.Fatalf("CanonicalSymbol = %q", h.Identity.CanonicalSymbol)
	}
}

// ── point in time ─────────────────────────────────────────────────────────────────────

// The hard requirement. The cache holds bars up to today; a check dated in the past must see
// none of them.
func TestPriceSeriesIsTruncatedAtAsOf(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 200, 700)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-07-01"})
	if h.Price.BarDate > "2026-07-01" {
		t.Fatalf("BarDate = %s — the check read a bar after its own as-of date", h.Price.BarDate)
	}
	if h.Price.BarDate != "2026-07-01" {
		t.Fatalf("BarDate = %s, want the 2026-07-01 bar", h.Price.BarDate)
	}
	full := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if full.Price.BarCount <= h.Price.BarCount {
		t.Fatalf("a later as-of must see more bars: %d vs %d", full.Price.BarCount, h.Price.BarCount)
	}
	// The close must be the historical one, not today's.
	got, ok := h.Price.Close.Float()
	if !ok {
		t.Fatal("no close")
	}
	todayClose, _ := full.Price.Close.Float()
	if got == todayClose {
		t.Fatalf("historical close %v equals today's %v", got, todayClose)
	}
}

func TestTruncateCandlesBoundaries(t *testing.T) {
	end := day("2026-09-04")
	cs := series(end, 10, 100)
	if got := truncateCandles(cs, end); len(got) != 10 {
		t.Fatalf("as-of == last bar: %d", len(got))
	}
	if got := truncateCandles(cs, end.AddDate(0, 0, 5)); len(got) != 10 {
		t.Fatalf("as-of after the series: %d", len(got))
	}
	if got := truncateCandles(cs, day("2026-08-30")); len(got) != 5 {
		t.Fatalf("mid-series: %d, want 5", len(got))
	}
	if got := truncateCandles(cs, day("2020-01-01")); len(got) != 0 {
		t.Fatalf("before the series: %d", len(got))
	}
	// The returned slice must not alias the cache: an append by a caller must not scribble
	// into the shared series.
	cut := truncateCandles(cs, day("2026-08-30"))
	cut = append(cut, fetcher.Candle{Close: -1})
	if cs[5].Close == -1 {
		t.Fatal("truncateCandles returned a slice aliasing the source array")
	}
}

// The archives must be read AS OF, not "the newest file". This is the test §30 asks for:
// a newer snapshot exists, and the historical check must not see it.
func TestArchivesAreReadAsOfNotLatest(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 200, 700)},
	}}, "2026-09-04")

	fdir := filepath.Join(root, "fundamental")
	writeFundamental(t, fdir, "2026-06-01", 11.11)
	writeFundamental(t, fdir, "2026-09-01", 99.99) // the NEWER one, which must stay invisible

	vdir := filepath.Join(root, "valuation")
	writeValuation(t, vdir, "2026-06-01", 12.0)
	writeValuation(t, vdir, "2026-09-01", 34.0)

	ev := mustEvidence(t, s, "2330", day("2026-07-01"))
	if ev.Fundamental == nil || ev.Fundamental.Financials == nil {
		t.Fatalf("no fundamental view: %+v", ev.FundamentalReason)
	}
	if got := *ev.Fundamental.Financials.CumulativeEPS; got != 11.11 {
		t.Fatalf("EPS = %v, want the 2026-06-01 archive's 11.11 — the check read the future", got)
	}
	if ev.Valuation == nil || ev.Valuation.TrailingPE == nil {
		t.Fatalf("no valuation: %+v", ev.ValuationReason)
	}
	if got := *ev.Valuation.TrailingPE; got != 12.0 {
		t.Fatalf("P/E = %v, want the 2026-06-01 archive's 12.0", got)
	}

	// And a check dated after both must see the later one — otherwise the test above would
	// pass for a build that simply never reads the archive at all.
	later := mustEvidence(t, s, "2330", day("2026-09-04"))
	if got := *later.Fundamental.Financials.CumulativeEPS; got != 99.99 {
		t.Fatalf("later EPS = %v, want 99.99", got)
	}
}

// A future date has no evidence behind it and would silently read as today's.
func TestFutureAsOfIsRefused(t *testing.T) {
	s, _ := newService(t, stubPrices{}, "2026-09-04")
	_, err := s.Check(context.Background(), Request{Symbol: "2330", AsOf: "2026-09-05"})
	if !errors.Is(err, ErrBadDate) {
		t.Fatalf("err = %v, want ErrBadDate", err)
	}
	_, err = s.Check(context.Background(), Request{Symbol: "2330", AsOf: "not-a-date"})
	if !errors.Is(err, ErrBadDate) {
		t.Fatalf("err = %v, want ErrBadDate", err)
	}
}

// Every source's point-in-time behaviour must be ON the response, including the ones that
// cannot honour an as-of date.
func TestPointInTimeIsReported(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 120, 900)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330"})
	modes := map[string]PITMode{}
	for _, p := range h.DataQuality.PointInTime {
		modes[p.Source] = p.Mode
	}
	for src, want := range map[string]PITMode{
		"price / technical": PITTruncated,
		"fundamental":       PITAsOf,
		"valuation":         PITAsOf,
		"institution":       PITAsOf,
		"macro":             PITAsOf,
		"news":              PITExactDate,
		"market regime":     PITExactDate,
	} {
		if got, ok := modes[src]; !ok || got != want {
			t.Fatalf("PIT[%s] = %q (present=%v), want %s", src, got, ok, want)
		}
	}
}

// ── missing data is never zero ────────────────────────────────────────────────────────

func TestMissingBlocksReportStatusNotZero(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 120, 900)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330"})

	// Nothing was archived under the temp root, so every archive-backed block must say so.
	if h.Institution.Status != StatusUnavailable {
		t.Fatalf("institution = %+v", h.Institution)
	}
	if h.News.Status != StatusUnavailable {
		t.Fatalf("news = %+v", h.News)
	}
	if h.Market.Status != StatusUnavailable {
		t.Fatalf("market = %+v", h.Market)
	}
	if got := h.Market.Score.Display; got == "0" || got == "0.0" || got == "-" {
		t.Fatalf("an absent market score rendered as %q", got)
	}
	if h.Market.Score.Display != string(StatusUnavailable) {
		t.Fatalf("Score.Display = %q", h.Market.Score.Display)
	}
	if h.DataQuality.Level != QualityPartial {
		t.Fatalf("quality = %s, want PARTIAL", h.DataQuality.Level)
	}
}

// A stock with no usable price series still gets an answer — with INSUFFICIENT quality.
func TestNoPriceSeriesStillAnswers(t *testing.T) {
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{}}, "2026-09-04")
	h := check(t, s, Request{Symbol: "2330"})
	if h.Price.Status != StatusUnavailable {
		t.Fatalf("price = %+v", h.Price)
	}
	if h.Price.Close.Display != string(StatusUnavailable) {
		t.Fatalf("Close.Display = %q — an absent price must never render as a number", h.Price.Close.Display)
	}
	if h.Technical.Status != StatusUnavailable {
		t.Fatalf("technical = %+v", h.Technical)
	}
	if h.DataQuality.Level != QualityInsufficient {
		t.Fatalf("quality = %s, want INSUFFICIENT", h.DataQuality.Level)
	}
	if h.Identity.Symbol != "2330" {
		t.Fatal("identity must survive a total data failure")
	}
}

// Too little history is PARTIAL, not UNAVAILABLE: the last close is a real fact even when no
// analyzer could run.
func TestShortSeriesKeepsTheRawFactsAndSaysWhy(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 10, 100)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330"})
	if h.Price.Status != StatusPartial {
		t.Fatalf("price status = %s", h.Price.Status)
	}
	if _, ok := h.Price.Close.Float(); !ok {
		t.Fatal("the last close is a fact and must survive a short series")
	}
	if !strings.Contains(h.Price.Reason, "30") {
		t.Fatalf("reason = %q, should name the bar requirement", h.Price.Reason)
	}
	if h.Technical.RSI.Status == StatusAvailable {
		t.Fatal("RSI must not be available on 10 bars")
	}
	if h.Technical.RSI.Display == "0" || h.Technical.RSI.Display == "0.0" {
		t.Fatalf("RSI rendered as %q", h.Technical.RSI.Display)
	}
}

// The scanner's own numbers must be carried through unchanged, not recomputed.
func TestTechnicalValuesComeFromTheScanner(t *testing.T) {
	end := day("2026-09-04")
	bars := series(end, 200, 700)
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: bars},
	}}, "2026-09-04")

	ev := mustEvidence(t, s, "2330", end)
	if ev.Entry == nil {
		t.Fatalf("no entry: %s", ev.PriceReason)
	}
	h := check(t, s, Request{Symbol: "2330"})

	if got, _ := h.Technical.MA20.Float(); got != ev.Entry.A.MA20 {
		t.Fatalf("MA20 = %v, scanner says %v", got, ev.Entry.A.MA20)
	}
	if got, _ := h.Technical.RSI.Float(); got != ev.Entry.A.RSI {
		t.Fatalf("RSI = %v, scanner says %v", got, ev.Entry.A.RSI)
	}
	if h.Technical.Stage != string(ev.Entry.RocketStage) || h.Technical.RocketScore != ev.Entry.RocketScore {
		t.Fatalf("stage/score drifted from the scanner")
	}
}

// A number must never render with a bare zero when it is absent, anywhere on the page.
func TestValueDisplayNeverLooksLikeAFigureWhenMissing(t *testing.T) {
	for _, st := range []Status{StatusUnavailable, StatusInsufficientData, StatusDisabled,
		StatusNotApplicable, StatusNotImplemented, StatusPartial} {
		v := Missing(st, "because")
		if v.Display != string(st) {
			t.Fatalf("Missing(%s).Display = %q", st, v.Display)
		}
		if _, ok := v.Float(); ok {
			t.Fatalf("Missing(%s) reported a number", st)
		}
	}
	// A real zero is a reading and must survive as one.
	z := Num(0, "%", 2, "test")
	if got, ok := z.Float(); !ok || got != 0 {
		t.Fatal("a genuine 0 must stay a value")
	}
	if z.Display != "0.00%" {
		t.Fatalf("Display = %q", z.Display)
	}
	// Non-finite input must not reach a page as "NaN" or "+Inf".
	if got := Num(math.NaN(), "", 2, "test"); got.Status == StatusAvailable {
		t.Fatalf("NaN became %+v", got)
	}
}

func TestNumberFormatting(t *testing.T) {
	for _, tc := range []struct {
		v        float64
		unit     string
		decimals int
		want     string
	}{
		{1234567.891, "TWD", 2, "1,234,567.89 TWD"},
		{-1234.5, "%", 1, "-1,234.5%"},
		{12.3456, "x", 2, "12.35x"},
		{0, "", 0, "0"},
		{999, "", 0, "999"},
		{1000, "", 0, "1,000"},
	} {
		if got := formatNumber(tc.v, tc.unit, tc.decimals); got != tc.want {
			t.Fatalf("formatNumber(%v,%q,%d) = %q, want %q", tc.v, tc.unit, tc.decimals, got, tc.want)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────────────

// mustEvidence runs the real evidence builder and fails on a context error, so every test
// below exercises the same ctx-checked path the HTTP handler uses.
func mustEvidence(t *testing.T, s *Service, sym string, asOf time.Time) *Evidence {
	t.Helper()
	ev, err := s.buildEvidence(context.Background(), mustStock(t, s, sym), asOf)
	if err != nil {
		t.Fatalf("buildEvidence(%s, %s): %v", sym, asOf.Format(dateLayout), err)
	}
	return ev
}

func mustStock(t *testing.T, s *Service, sym string) stockcatalog.Stock {
	t.Helper()
	st, ok := s.Catalog.Resolve(sym)
	if !ok {
		t.Fatalf("catalog cannot resolve %q", sym)
	}
	return st
}

func writeFundamental(t *testing.T, dir, observed string, eps float64) {
	t.Helper()
	obs := day(observed)
	snap := &fundamental.Snapshot{
		SchemaVersion:   fundamental.SchemaVersion,
		Symbol:          "2330.TW",
		Source:          "test",
		ObservedAt:      obs,
		Status:          fundamental.Available,
		RevenueStatus:   fundamental.Unavailable,
		FinancialStatus: fundamental.Available,
		Financials: &fundamental.Financials{
			Symbol:          "2330.TW",
			Period:          fundamental.Period{Year: 2026, Quarter: 2, Cumulative: true},
			Revenue:         1000,
			GrossProfit:     500,
			OperatingIncome: 400,
			NetIncome:       300,
			CumulativeEPS:   &eps,
			PublishedAt:     obs.AddDate(0, 0, -1),
		},
	}
	if _, err := fundamental.SaveSnapshot(dir, snap); err != nil {
		t.Fatalf("save fundamental %s: %v", observed, err)
	}
}

func writeValuation(t *testing.T, dir, observed string, pe float64) {
	t.Helper()
	obs := day(observed)
	snap := &valuation.Snapshot{
		SchemaVersion: valuation.SchemaVersion,
		Symbol:        "2330.TW",
		Source:        "test",
		ObservedAt:    obs,
		Status:        valuation.Available,
		Ratios: &valuation.Ratios{
			Symbol: "2330.TW", Date: observed, PERatio: &pe,
		},
	}
	if _, err := valuation.SaveSnapshot(dir, snap); err != nil {
		t.Fatalf("save valuation %s: %v", observed, err)
	}
}

// ── institution / news fixtures ───────────────────────────────────────────────────────

// writeInstitutionDay writes one TWSE snapshot holding one stock's three legs.
//
// The nets are the exchange's own 股 figures; only the four fields the four-way collapse
// reads are filled, because that is all institution.Compute consumes.
func writeInstitutionDay(t *testing.T, dir, date, code string, foreign, trust, dealer int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := institution.Snapshot{
		SchemaVersion: institution.SchemaVersion,
		Market:        institution.MarketTWSE,
		AsOfDate:      date,
		Unit:          institution.UnitShares,
		Source:        institution.SourceTWSE,
		FetchedAt:     day(date).Add(18 * time.Hour),
		Stocks: []institution.StockChip{{
			Code:         code,
			Name:         "台積電",
			ForeignTotal: institution.Leg{Net: foreign},
			Trust:        institution.Leg{Net: trust},
			DealerTotal:  institution.Leg{Net: dealer},
			Reconciled:   true,
		}},
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "twse_"+date+".json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeNewsSnapshot writes one news_YYYYMMDD_HHMMSS.json run.
func writeNewsSnapshot(t *testing.T, dir, date, clock string, signals []news.NewsSignal) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	observed := day(date).Add(10 * time.Hour)
	env := map[string]any{"date": date, "observed_at": observed, "signals": signals}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	name := "news_" + strings.ReplaceAll(date, "-", "") + "_" + clock + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newsSignal(id string, sig news.SignalType, code, title string, sectors []string, published time.Time) news.NewsSignal {
	s := news.NewsSignal{
		EventID:     id,
		PublishedAt: published,
		ObservedAt:  published.Add(time.Hour),
		Signal:      sig,
		Strength:    7,
		Confidence:  8,
		Sectors:     sectors,
		Summary:     title,
		RawTitle:    title,
	}
	if code != "" {
		s.Stocks = []news.StockReference{{Code: code, Name: "台積電", Resolved: true}}
	}
	return s
}

func legByName(t *testing.T, b InstitutionBlock, name string) InstitutionLeg {
	t.Helper()
	for _, l := range b.Legs {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("no %q leg in %+v", name, b.Legs)
	return InstitutionLeg{}
}

// ── institution ───────────────────────────────────────────────────────────────────────

// A session with no record for this stock must NOT print 0 股.
//
// This is the most dangerous zero on the page: "the foreigners were flat today" and "the
// exchange file for today never arrived" are opposite pieces of news, and institution.legStats
// leaves TodayNet at its int64 zero in both cases.
func TestMissingTodayChipIsNotZero(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")
	idir := filepath.Join(root, "institution")

	// Records for the three sessions BEFORE the as-of date, and none for the day itself.
	writeInstitutionDay(t, idir, "2026-09-01", "2330", 1_000_000, 200_000, -50_000)
	writeInstitutionDay(t, idir, "2026-09-02", "2330", 1_500_000, 100_000, -20_000)
	writeInstitutionDay(t, idir, "2026-09-03", "2330", 2_000_000, 300_000, 10_000)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.Institution.Status != StatusAvailable {
		t.Fatalf("institution status = %s (%s), want AVAILABLE — the history did load",
			h.Institution.Status, h.Institution.Reason)
	}
	if h.Institution.TodayComplete {
		t.Fatalf("TodayComplete = true, but no record was written for 2026-09-04")
	}
	foreign := legByName(t, h.Institution, "外資")
	if foreign.TodayNet.Status == StatusAvailable {
		t.Fatalf("TodayNet = %+v — an absent record rendered as an available figure", foreign.TodayNet)
	}
	if foreign.TodayNet.Value != nil {
		t.Fatalf("TodayNet carries a number (%v) with no record behind it", *foreign.TodayNet.Value)
	}
	if strings.TrimSpace(foreign.TodayNet.Display) == "0" || foreign.TodayNet.Display == "0 股" {
		t.Fatalf("TodayNet.Display = %q — reads as 'no institutional activity today'", foreign.TodayNet.Display)
	}
	if foreign.NetBuyRatio.Status == StatusAvailable {
		t.Fatalf("NetBuyRatio = %+v with no same-day record", foreign.NetBuyRatio)
	}
	for _, leg := range h.Institution.Legs {
		if leg.TodayNet.Status == StatusAvailable {
			t.Errorf("%s: TodayNet available with no record for the day", leg.Name)
		}
		if leg.StreakComplete {
			t.Errorf("%s: StreakComplete with today missing", leg.Name)
		}
	}
}

// A cumulative that spans a gap is a real sum of what was observed, but it is not the clean
// n-day figure its label claims, so it must be marked.
func TestCumulativeAcrossAGapIsMarkedPartial(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")
	idir := filepath.Join(root, "institution")

	// 2026-09-02 is deliberately absent: the 5-day window spans it, so every cumulative is
	// tainted, while today's own record is present so TodayNet stays a real figure.
	for _, d := range []string{"2026-08-31", "2026-09-01", "2026-09-03", "2026-09-04"} {
		writeInstitutionDay(t, idir, d, "2330", 1_000_000, 0, 0)
	}

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if !h.Institution.TodayComplete {
		t.Fatalf("TodayComplete = false, but 2026-09-04 was written (%s)", h.Institution.Reason)
	}
	if len(h.Institution.MissingDates) == 0 {
		t.Fatalf("MissingDates is empty, but 2026-09-02 has no record")
	}
	var sawGap bool
	for _, d := range h.Institution.MissingDates {
		if d == "2026-09-02" {
			sawGap = true
		}
	}
	if !sawGap {
		t.Fatalf("MissingDates = %v, want 2026-09-02 among them", h.Institution.MissingDates)
	}

	foreign := legByName(t, h.Institution, "外資")
	if _, ok := foreign.TodayNet.Float(); !ok {
		t.Fatalf("TodayNet = %+v, want the real figure — today's record exists", foreign.TodayNet)
	}
	if foreign.Sum5D.Status != StatusPartial {
		t.Fatalf("Sum5D status = %s, want PARTIAL — the 5-day window spans 2026-09-02",
			foreign.Sum5D.Status)
	}
	if foreign.Sum5D.Display == string(StatusPartial) || foreign.Sum5D.Display == "" {
		t.Fatalf("Sum5D.Display = %q — a tainted cumulative is still a real sum and should print",
			foreign.Sum5D.Display)
	}
	if foreign.Sum5D.Reason == "" {
		t.Fatalf("Sum5D is PARTIAL with no reason saying why")
	}
	if _, ok := foreign.Sum5D.Float(); ok {
		t.Fatalf("Float() returned a tainted cumulative as a clean number")
	}
}

// The whole institution block must stay UNAVAILABLE — never a page of zeros — when the
// snapshot directory holds nothing at all.
func TestNoInstitutionSnapshotsIsUnavailableNotZero(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.Institution.Status != StatusUnavailable {
		t.Fatalf("status = %s, want UNAVAILABLE", h.Institution.Status)
	}
	if len(h.Institution.Legs) != 0 {
		t.Fatalf("legs were projected with no data loaded: %+v", h.Institution.Legs)
	}
	if h.Institution.Reason == "" {
		t.Fatalf("UNAVAILABLE with no reason")
	}
}

// ── news ──────────────────────────────────────────────────────────────────────────────

// A stored snapshot must reach both the evidence and the rendered block, at the right level.
func TestNewsSnapshotReachesTheBlock(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")
	rdir := filepath.Join(root, "reports")

	published := day("2026-09-04").Add(8 * time.Hour)
	writeNewsSnapshot(t, rdir, "2026-09-04", "093000", []news.NewsSignal{
		newsSignal("e1", news.SignalBullish, "2330", "先進製程訂單滿載", nil, published),
		newsSignal("e2", news.SignalRiskWarning, "", "半導體庫存調整", []string{"半導體"}, published),
	})

	ev := mustEvidence(t, s, "2330", end)
	if ev.News == nil {
		t.Fatalf("no news view: %s", ev.NewsReason)
	}
	if len(ev.News.StockItems) != 1 {
		t.Fatalf("StockItems = %d, want the one signal naming 2330", len(ev.News.StockItems))
	}
	if ev.Entry == nil || ev.Entry.News == nil {
		t.Fatalf("the view never reached the WatchlistEntry — AttachNews wrote into a discarded copy")
	}
	if ev.Entry.News != ev.News {
		t.Fatalf("the entry carries a different view than the evidence does")
	}

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.News.Status != StatusAvailable {
		t.Fatalf("news status = %s (%s)", h.News.Status, h.News.Reason)
	}
	if h.News.StockCount != 1 {
		t.Fatalf("StockCount = %d, want 1", h.News.StockCount)
	}
	if len(h.News.Items) != 1 {
		t.Fatalf("Items = %d, want 1 — the sector signal must not reach a stock with no sectors",
			len(h.News.Items))
	}
	it := h.News.Items[0]
	if it.Level != "STOCK" || it.Signal != string(news.SignalBullish) || it.Title != "先進製程訂單滿載" {
		t.Fatalf("item = %+v", it)
	}
	if !it.PublishedAt.Equal(published) {
		t.Fatalf("PublishedAt = %s, want %s — publish time must survive the projection",
			it.PublishedAt, published)
	}
	if it.ObservedAt.Equal(it.PublishedAt) {
		t.Fatalf("ObservedAt collapsed onto PublishedAt; the two are different facts")
	}
}

// A sector signal reaches a stock only through the catalog's sector membership.
func TestSectorNewsReachesTheStockThroughItsSectors(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")

	// Give the catalog entry a sector by hand — the overlay has none, and this is the only
	// route a sector-level headline has to this stock.
	stock := mustStock(t, s, "2330")
	stock.Sectors = []string{"半導體"}

	writeNewsSnapshot(t, filepath.Join(root, "reports"), "2026-09-04", "093000", []news.NewsSignal{
		newsSignal("e1", news.SignalBullish, "2330", "個股利多", nil, end.Add(8*time.Hour)),
		newsSignal("e2", news.SignalBearish, "", "類股利空", []string{"半導體"}, end.Add(8*time.Hour)),
	})

	ev, err := s.buildEvidence(context.Background(), stock, end)
	if err != nil {
		t.Fatal(err)
	}
	if ev.News == nil {
		t.Fatalf("no news view: %s", ev.NewsReason)
	}
	if len(ev.News.SectorItems) != 1 {
		t.Fatalf("SectorItems = %d, want the 半導體 signal", len(ev.News.SectorItems))
	}
	b := newsBlock(ev)
	if b.SectorCount != 1 || b.StockCount != 1 {
		t.Fatalf("counts = stock %d / sector %d, want 1 / 1", b.StockCount, b.SectorCount)
	}
	var levels []string
	for _, it := range b.Items {
		levels = append(levels, it.Level)
	}
	if len(levels) != 2 || levels[0] != "STOCK" || levels[1] != "SECTOR" {
		t.Fatalf("levels = %v, want [STOCK SECTOR] — a sector headline is weaker evidence and must stay labelled", levels)
	}
	if !b.Divergence {
		t.Fatalf("a BULLISH stock signal against a BEARISH sector signal is a divergence")
	}
}

// No snapshot for the date is UNAVAILABLE with a reason, not an empty AVAILABLE block. The
// news loader is EXACT_DATE: it must not fall back to an earlier day's headlines.
func TestNewsDoesNotFallBackToAnEarlierDay(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")

	writeNewsSnapshot(t, filepath.Join(root, "reports"), "2026-09-03", "093000", []news.NewsSignal{
		newsSignal("old", news.SignalBullish, "2330", "昨天的新聞", nil, day("2026-09-03").Add(8*time.Hour)),
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.News.Status != StatusUnavailable {
		t.Fatalf("status = %s, want UNAVAILABLE — yesterday's headlines are not today's", h.News.Status)
	}
	if len(h.News.Items) != 0 {
		t.Fatalf("items leaked from an earlier day: %+v", h.News.Items)
	}
	if h.News.Reason == "" {
		t.Fatalf("UNAVAILABLE with no reason")
	}
}

// Only the LATEST run of the day is read: each run re-reads the same feeds, so merging them
// would count every signal twice.
func TestOnlyTheLatestNewsRunOfTheDayIsUsed(t *testing.T) {
	end := day("2026-09-04")
	s, root := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")
	rdir := filepath.Join(root, "reports")

	writeNewsSnapshot(t, rdir, "2026-09-04", "093000", []news.NewsSignal{
		newsSignal("e1", news.SignalBullish, "2330", "早盤", nil, end.Add(8*time.Hour)),
	})
	writeNewsSnapshot(t, rdir, "2026-09-04", "143000", []news.NewsSignal{
		newsSignal("e1", news.SignalBullish, "2330", "早盤", nil, end.Add(8*time.Hour)),
		newsSignal("e2", news.SignalBearish, "2330", "盤後", nil, end.Add(13*time.Hour)),
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.News.StockCount != 2 {
		t.Fatalf("StockCount = %d, want 2 (the later run's two signals, not four)", h.News.StockCount)
	}
}

// ── cancellation ──────────────────────────────────────────────────────────────────────

// A cancelled context stops the real evidence builder rather than silently producing a
// half-filled answer in which every unreached source reads UNAVAILABLE.
func TestCancelledContextAbortsTheRealBuild(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, "2026-09-04")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Check(ctx, Request{Symbol: "2330", AsOf: "2026-09-04"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check on a cancelled context = %v, want context.Canceled", err)
	}
}

// Cancellation mid-flight is caught at the next stage boundary: a slow price source that has
// already blown the deadline must not be followed by six more stages of archive reads.
func TestDeadlineDuringPriceStageStopsTheRest(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, slowPrices{
		inner: stubPrices{data: map[string]fetcher.StockData{
			"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
		}},
		delay: 30 * time.Millisecond,
	}, "2026-09-04")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := s.Check(ctx, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Check = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "institution") {
		t.Fatalf("error = %v, want it to name the stage it stopped before", err)
	}
}

// slowPrices makes the one stage that can block on the network actually take time.
type slowPrices struct {
	inner stubPrices
	delay time.Duration
}

func (p slowPrices) Fetch(info fetcher.StockInfo) (fetcher.StockData, error) {
	time.Sleep(p.delay)
	return p.inner.Fetch(info)
}
