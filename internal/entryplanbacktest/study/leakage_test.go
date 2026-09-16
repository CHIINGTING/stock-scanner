package study

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────────────────
// EP-9 §33 — ANTI-LEAKAGE, ten behavioural items.
//
// Every one drives the real function and fails on a real behaviour change. None of them
// asserts a comment, and none passes because nothing happened.
// ──────────────────────────────────────────────────────────────────────────────────────────

type fakeSess struct{ dates []string }

func (f fakeSess) IndexOf(d string) (int, bool) {
	for i, x := range f.dates {
		if x == d {
			return i, true
		}
	}
	return -1, false
}
func (f fakeSess) BarCount() int { return len(f.dates) }

// series builds bars from explicit OHLC rows on consecutive weekday-ish dates taken verbatim.
type row struct {
	date                   string
	open, high, low, close float64
}

func series(rows ...row) ([]fetcher.Candle, fakeSess) {
	out := make([]fetcher.Candle, 0, len(rows))
	s := fakeSess{}
	for _, r := range rows {
		t, err := time.Parse(entryplanbacktest.DateLayout, r.date)
		if err != nil {
			panic(err)
		}
		out = append(out, fetcher.Candle{Date: t, Open: r.open, High: r.high, Low: r.low,
			Close: r.close, AdjClose: r.close, Volume: 1_000_000})
		s.dates = append(s.dates, r.date)
	}
	return out, s
}

// L1 — the signal bar can never fill, even when its Low is deep inside the zone.
func TestL1SignalBarNeverFillsEvenWhenItsLowIsInsideTheZone(t *testing.T) {
	bars, s := series(
		row{"2026-09-09", 100, 101, 99, 100},
		row{"2026-09-10", 100, 101, 90, 100}, // signal bar: Low 90, far below the limit
		row{"2026-09-11", 100, 101, 99.5, 100},
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 3)
	if f.Filled() && f.BarIndex == 1 {
		t.Fatalf("filled on the signal bar at %v — T.Low <= limit must never fill", f.Price)
	}
	if f.Outcome != FillMissed {
		t.Fatalf("outcome %q, want MISSED: only T+1 (Low 99.5) was eligible and it did not "+
			"reach 95", f.Outcome)
	}
}

// L2 — T+1 is the first eligible session, and it IS eligible.
func TestL2TPlusOneIsTheFirstEligibleSession(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 94, 96}, // T+1 reaches the limit
		row{"2026-09-14", 100, 101, 90, 100},
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 3)
	if !f.Filled() {
		t.Fatalf("no fill; T+1 Low 94 <= limit 95")
	}
	if f.BarIndex != 1 || f.SessionsWaited != 1 {
		t.Fatalf("filled on bar %d after %d sessions, want bar 1 after 1", f.BarIndex, f.SessionsWaited)
	}
}

// L3 — appending future bars cannot change a fill already decided at T.
func TestL3FutureBarsCannotChangeADecidedFill(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 94, 96},
		row{"2026-09-14", 100, 101, 98, 100},
	)
	before := SimulateLimit(bars, s, "2026-09-10", 95, 3)
	future := append(append([]fetcher.Candle(nil), bars...), fetcher.Candle{
		Date: mustDate("2026-09-15"), Open: 50, High: 51, Low: 40, Close: 45})
	s2 := fakeSess{dates: append(append([]string(nil), s.dates...), "2026-09-15")}
	after := SimulateLimit(future, s2, "2026-09-10", 95, 3)
	if before != after {
		t.Fatalf("a bar appended after the window changed the fill: %+v -> %+v", before, after)
	}
}

// L4 — the outcome never reads a bar at or before the fill bar for its RETURNS.
func TestL4ReturnsNeverReadTheFillBarOrEarlier(t *testing.T) {
	bars, _ := flatSeries(30, 100)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100, SessionsWaited: 1}
	out1 := Measure(bars, f, nil, nil, nil)
	if len(out1.Returns) != len(Horizons) {
		t.Fatalf("fixture produced %d of %d horizons; the test would be vacuous",
			len(out1.Returns), len(Horizons))
	}
	// Rewrite every bar at or before the fill bar to an absurd value. Nothing may move.
	bars[0] = fetcher.Candle{Date: bars[0].Date, Open: 1, High: 1, Low: 1, Close: 1}
	bars[1] = fetcher.Candle{Date: bars[1].Date, Open: 1, High: 1, Low: 1, Close: 1}
	out2 := Measure(bars, f, nil, nil, nil)
	for h, v := range out1.Returns {
		if out2.Returns[h] != v {
			t.Fatalf("Return@%d moved from %v to %v after rewriting bars at/below the fill bar",
				h, v, out2.Returns[h])
		}
	}
	if (out1.MFE20 == nil) != (out2.MFE20 == nil) {
		t.Fatal("MFE availability moved")
	}
	if out1.MFE20 != nil && *out1.MFE20 != *out2.MFE20 {
		t.Fatalf("MFE moved from %v to %v", *out1.MFE20, *out2.MFE20)
	}
}

// L5 — the excursion window excludes the fill bar's own High/Low.
func TestL5TheExcursionWindowExcludesTheFillBar(t *testing.T) {
	bars, _ := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 500, 10, 100}, // fill bar with an absurd range
		row{"2026-09-14", 100, 102, 98, 100},
	)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100}
	out := Measure(bars, f, nil, nil, nil)
	if out.MFE20 == nil || out.MAE20 == nil {
		t.Fatal("no excursion measured")
	}
	if *out.MFE20 > 100 {
		t.Fatalf("MFE %v picked up the fill bar's High of 500 — daily OHLC cannot say whether "+
			"it happened before or after the fill", *out.MFE20)
	}
	if *out.MAE20 < -50 {
		t.Fatalf("MAE %v picked up the fill bar's Low of 10", *out.MAE20)
	}
}

// L6 — a plan's published levels come from the plan, and no level is invented.
func TestL6NoLevelTheePlanDidNotPublishIsEverMeasured(t *testing.T) {
	bars, _ := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 99, 100},
		row{"2026-09-14", 100, 200, 1, 100}, // would hit any invented level
	)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100}
	out := Measure(bars, f, nil, nil, nil)
	if out.InvalidationHit != nil || out.Target1Hit != nil || out.Target2Hit != nil {
		t.Fatalf("a hit was reported for a level the plan never published: %+v", out)
	}
	if out.FirstTouch != "" {
		t.Fatalf("FirstTouch %q on a plan with no levels — ordering is not a question here",
			out.FirstTouch)
	}
}

// L7 — MISSED fabricates no fill and therefore no return.
func TestL7MissedFabricatesNoFillAndNoReturn(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 99, 100},
		row{"2026-09-14", 100, 101, 99, 100},
		row{"2026-09-15", 100, 101, 99, 100},
	)
	o := obsFor(bars, s, "2026-09-10", ptr(50.0), nil, nil, nil, nil)
	r := Evaluate(o, ArmZoneLimit, 3)
	if r.Fill.Outcome != FillMissed {
		t.Fatalf("outcome %q, want MISSED", r.Fill.Outcome)
	}
	if r.Fill.Price != 0 {
		t.Fatalf("a missed order carries price %v", r.Fill.Price)
	}
	if len(r.Out.Returns) != 0 || r.Out.MFE20 != nil || r.Out.MAE20 != nil {
		t.Fatalf("a missed order produced outcomes: %+v", r.Out)
	}
	m := Aggregate(ArmMissed, 3, "", []Row{r})
	if m.Returns[5].N != 0 {
		t.Fatalf("the MISSED cell has %d Return@5 samples, want 0", m.Returns[5].N)
	}
}

// L8 — the signal-close benchmark is not presented as an executable fill.
func TestL8TheSignalCloseBenchmarkIsNotExecutable(t *testing.T) {
	if ArmSignalCloseBaseline.Executable() {
		t.Fatal("the signal-close benchmark claims to be executable")
	}
	if !ArmZoneLimit.Executable() || !ArmChase.Executable() {
		t.Fatal("a limit-order arm claims not to be executable")
	}
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 99, 100},
	)
	f := SimulateSignalClose(bars, s, "2026-09-10")
	if !f.Filled() || f.BarIndex != 0 {
		t.Fatalf("benchmark fill %+v, want the signal bar", f)
	}
	m := Aggregate(ArmSignalCloseBaseline, 3, "", []Row{{Obs: obsFor(bars, s, "2026-09-10", nil, nil, nil, nil, nil), Arm: ArmSignalCloseBaseline, Fill: f}})
	if m.Executable() {
		t.Fatal("a serialized benchmark metric block reports itself as executable")
	}
}

// L9 — the benchmark still requires a session after T, so it cannot grade the newest bar.
func TestL9TheBenchmarkStillRequiresASessionAfterT(t *testing.T) {
	bars, s := series(row{"2026-09-10", 100, 101, 99, 100})
	if f := SimulateSignalClose(bars, s, "2026-09-10"); f.Filled() {
		t.Fatal("the benchmark filled on the newest bar, which has no forward window")
	}
}

// L10 — a truncated outcome window is PENDING, never a zero return.
func TestL10ATruncatedWindowIsPendingNotZero(t *testing.T) {
	bars, _ := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 99, 100},
		row{"2026-09-14", 100, 101, 99, 100},
	)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100}
	out := Measure(bars, f, nil, nil, nil)
	if out.Coverage != entryplanbacktest.WindowTruncated {
		t.Fatalf("coverage %q, want TRUNCATED", out.Coverage)
	}
	if _, ok := out.Returns[5]; ok {
		t.Fatal("Return@5 exists in a 3-bar series — a horizon past the data must be ABSENT, " +
			"not 0.00%")
	}
	m := Aggregate(ArmZoneLimit, 3, "", []Row{{Obs: &Observation{}, Fill: f, Out: out, ExecutableStatus: true}})
	if m.Returns[5].N != 0 {
		t.Fatalf("Return@5 sample N=%d, want 0", m.Returns[5].N)
	}
	if m.PendingTruncated != 1 {
		t.Fatalf("PendingTruncated %d, want 1 — a pending trade must be visible, not dropped",
			m.PendingTruncated)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────

func mustDate(s string) time.Time {
	t, err := time.Parse(entryplanbacktest.DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(v float64) *float64 { return &v }

// flatSeries builds n consecutive bars at one price, on dates that exist only as strings —
// the session axis in these tests is the fake, not a calendar.
func flatSeries(n int, price float64) ([]fetcher.Candle, fakeSess) {
	rows := make([]row, 0, n)
	d := mustDate("2026-01-01")
	for i := 0; i < n; i++ {
		rows = append(rows, row{d.AddDate(0, 0, i).Format(entryplanbacktest.DateLayout),
			price, price, price, price})
	}
	return series(rows...)
}

func obsFor(bars []fetcher.Candle, s fakeSess, asOf string,
	zoneHigh, maxChase, inv, t1, t2 *float64) *Observation {
	idx, _ := s.IndexOf(asOf)
	// WAIT_PULLBACK, so the fixture belongs to the EP-9 §18 EXECUTABLE population by default.
	// A zero-valued Status would put every helper-built observation in the excluded block and
	// quietly empty every metric these tests assert on.
	o := &Observation{Symbol: "TEST", SignalAsOf: asOf, Status: entryplan.StatusWaitPullback,
		ZoneHigh: zoneHigh, MaxChase: maxChase,
		Invalidation: inv, Target1: t1, Target2: t2, bars: bars, sessions: s, sigIdx: idx}
	if idx >= 0 && idx < len(bars) {
		o.SignalClose = bars[idx].Close
	}
	if zoneHigh != nil {
		lo := *zoneHigh - 1
		o.ZoneLow = &lo
	}
	return o
}
