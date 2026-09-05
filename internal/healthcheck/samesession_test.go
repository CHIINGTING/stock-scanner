package healthcheck

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// candleOn builds one bar on the given date.
func candleOn(date string, close float64) fetcher.Candle {
	d, err := time.ParseInLocation(dateLayout, date, Taipei)
	if err != nil {
		panic(err)
	}
	return fetcher.Candle{Date: d, Open: close, High: close, Low: close,
		Close: close, AdjClose: close, Volume: 1000}
}

// The same-session invariant, held against the arrival of historical data.
//
//	ImpliedTTMEPS = Close ÷ PE is permitted ONLY when Close.Date == PE.Date.
//
// This was a real defect once: the engine divided the analysis-date close by an older
// session's published P/E, recovering an "EPS" that never existed — the true figure scaled by
// whatever the price did in between. Backfill makes it easy to reintroduce, because the
// archive now holds hundreds of P/E rows whose sessions have no matching bar in a truncated
// price series. These tests pin all four cases so it cannot come back quietly.

// sameSessionFixture builds a check whose valuation names `peDate` and whose price series
// covers `barDates`.
func sameSessionFixture(t *testing.T, peDate string, barDates []string, pe float64) TargetPriceBlock {
	t.Helper()
	ev := &Evidence{
		Identity: Identity{Symbol: "2330", CanonicalSymbol: "2330.TW"},
		AsOf:     "2026-09-04",
	}
	for i, d := range barDates {
		ev.Bars = append(ev.Bars, candleOn(d, 700+float64(i)))
	}
	p := pe
	ev.Valuation = &valuation.Valuation{
		Symbol: "2330.TW", Status: valuation.Available,
		TrailingPE: &p, TrailingDate: peDate, TrailingStatus: valuation.Available,
		// A full distribution, so nothing else can be the reason for a refusal.
		HistoricalPE: valuation.HistoricalPEStats{
			Status: valuation.Available, SampleCount: 250,
			WindowDays: valuation.DefaultWindowDays,
			P25:        fptr(16), Median: fptr(22), P75: fptr(28),
		},
	}
	price := PriceBlock{Status: StatusUnavailable, Close: Missing(StatusUnavailable, "no bars")}
	if n := len(ev.Bars); n > 0 {
		price = PriceBlock{
			Status:  StatusAvailable,
			BarDate: barDates[n-1],
			Close:   Num(ev.Bars[n-1].Close, "TWD", 2, "test"),
		}
	}
	return buildTargetPrice(ev, price)
}

func fptr(v float64) *float64 { return &v }

// 1. Same session → allowed.
func TestSameSessionPriceAndPEProduceATarget(t *testing.T) {
	got := sameSessionFixture(t, "2026-09-04", []string{"2026-09-02", "2026-09-03", "2026-09-04"}, 20)
	if got.Status != StatusAvailable {
		t.Fatalf("status = %s (%s), want AVAILABLE for a same-session pair", got.Status, got.Reason)
	}
	if got.EPSBaseDate != "2026-09-04" {
		t.Fatalf("EPSBaseDate = %q, want the P/E's own session", got.EPSBaseDate)
	}
	base, ok := got.EPSBasePrice.Float()
	if !ok {
		t.Fatalf("no EPS base price: %+v", got.EPSBasePrice)
	}
	eps, ok := got.EPS.Float()
	if !ok {
		t.Fatalf("no EPS: %+v", got.EPS)
	}
	if want := base / 20; eps != want {
		t.Fatalf("EPS = %v, want %v (= %v ÷ 20)", eps, want, base)
	}
}

//  2. The price series is NEWER than the P/E — the original defect. The close for the P/E's
//     session is simply not there, and no target may be produced from a different day's price.
func TestPriceNewerThanPEProducesNoTarget(t *testing.T) {
	got := sameSessionFixture(t, "2026-08-20",
		[]string{"2026-09-02", "2026-09-03", "2026-09-04"}, 20)
	assertRefused(t, got, "price newer than the P/E")
}

//  3. The P/E is newer than every bar — a backfill can archive a session the truncated series
//     has not reached, or a historical check can cut the bars before it.
func TestPENewerThanPriceProducesNoTarget(t *testing.T) {
	got := sameSessionFixture(t, "2026-09-04",
		[]string{"2026-08-18", "2026-08-19", "2026-08-20"}, 20)
	assertRefused(t, got, "P/E newer than the price")
}

// 4. No overlapping session at all.
func TestNoSameSessionPairIsInsufficient(t *testing.T) {
	got := sameSessionFixture(t, "2026-07-15",
		[]string{"2026-09-02", "2026-09-03", "2026-09-04"}, 20)
	assertRefused(t, got, "no overlapping session")
	if got.Status != StatusInsufficientData {
		t.Fatalf("status = %s, want INSUFFICIENT_DATA", got.Status)
	}
}

// 5. Backfilled history populates the DISTRIBUTION without fabricating a current EPS.
//
// This is the shape the backfill actually produces: hundreds of historical sessions, and a
// trailing P/E whose own session predates the price series in hand. The percentile is real;
// the target price is not available, and neither borrows from the other.
func TestBackfilledHistoryFeedsTheDistributionButNotTheEPS(t *testing.T) {
	ev := &Evidence{
		Identity: Identity{Symbol: "2330", CanonicalSymbol: "2330.TW"},
		AsOf:     "2026-09-04",
	}
	for _, d := range []string{"2026-09-02", "2026-09-03", "2026-09-04"} {
		ev.Bars = append(ev.Bars, candleOn(d, 759))
	}
	pe := 27.9
	pctl := 0.62
	ev.Valuation = &valuation.Valuation{
		Symbol: "2330.TW", Status: valuation.Available,
		// The trailing reading belongs to a session the price series does not cover.
		TrailingPE: &pe, TrailingDate: "2026-06-30", TrailingStatus: valuation.Available,
		HistoricalPE: valuation.HistoricalPEStats{
			Status: valuation.Available, SampleCount: 243,
			WindowDays: valuation.DefaultWindowDays,
			P25:        fptr(24.1), Median: fptr(27.5), P75: fptr(31.2),
			CurrentPercentile: &pctl,
		},
	}
	price := PriceBlock{Status: StatusAvailable, BarDate: "2026-09-04",
		Close: Num(759, "TWD", 2, "test")}

	vb := valuationBlock(ev)
	if vb.Historical.Status != StatusAvailable {
		t.Fatalf("the distribution should be usable from 243 samples: %s", vb.Historical.Status)
	}
	if vb.Historical.SampleCount != 243 {
		t.Fatalf("SampleCount = %d", vb.Historical.SampleCount)
	}
	if v, ok := vb.Historical.CurrentPercentile.Float(); !ok || v != 62 {
		t.Fatalf("percentile = %+v, want 62%%", vb.Historical.CurrentPercentile)
	}

	tp := buildTargetPrice(ev, price)
	if tp.Status == StatusAvailable {
		t.Fatalf("a target was built from a P/E session the price series does not cover: %+v", tp)
	}
	if tp.EPS.Value != nil {
		t.Fatalf("an EPS was fabricated: %v", *tp.EPS.Value)
	}
	for _, sc := range tp.Scenarios {
		if sc.TargetPrice.Value != nil {
			t.Fatalf("%s produced a target price", sc.Name)
		}
	}
}

func assertRefused(t *testing.T, got TargetPriceBlock, why string) {
	t.Helper()
	if got.Status == StatusAvailable {
		t.Fatalf("%s: a target was produced: %+v", why, got)
	}
	if got.EPS.Value != nil {
		t.Fatalf("%s: an EPS was fabricated: %v", why, *got.EPS.Value)
	}
	if got.EPSBasePrice.Value != nil {
		t.Fatalf("%s: a base price was claimed: %v", why, *got.EPSBasePrice.Value)
	}
	for _, sc := range got.Scenarios {
		if sc.TargetPrice.Value != nil || sc.UpsidePct.Value != nil {
			t.Fatalf("%s: %s carries numbers: %+v", why, sc.Name, sc)
		}
	}
	if got.Reason == "" {
		t.Fatalf("%s: refused with no reason", why)
	}
}
