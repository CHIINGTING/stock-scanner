package analyzer

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── synthetic fixture ─────────────────────────────────────────────────────────────────
//
// Real .cache data is exercised by regime_history_test.go, which now runs THROUGH
// ReplayRegimes. This file needs something else: a series whose FUTURE can be rewritten at
// will, because that is the only way to prove the absence of look-ahead. So the fixture is
// synthetic and deterministic — no rand, no time, no cache.

const (
	synthDates  = 260 // > MinBenchmarkBars(120) + MA120 warm-up, with room for a tail
	synthStocks = 600 // > MinBreadthUniverse(500), so breadth is actually measurable
	synthSplit  = 200 // bars[:200] is the shared prefix; everything after it is rewritten
)

func synthDate(i int) string {
	// A dense but strictly ascending calendar. Real trading days are irrelevant here; the
	// only property the code depends on is that panel.Dates is sorted ascending.
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, i).Format("2006-01-02")
}

// synthBars is a benchmark with several genuine structure changes in it (trend, correction,
// recovery) so the replay produces more than one regime.
func synthBars(n int) []model.Bar {
	out := make([]model.Bar, n)
	for i := 0; i < n; i++ {
		x := float64(i)
		c := 100 +
			14*math.Sin(x/47) + // the slow leg: uptrend → top → correction → recovery
			5*math.Sin(x/11) + // an intermediate wave, for MA20/MA60 crossings
			1.2*math.Sin(x/2.3) // session noise, so WorstDay20 is not degenerate
		out[i] = model.Bar{Date: synthDate(i), Close: round2(c)}
	}
	return out
}

// synthUniverse spreads the stocks around the benchmark's shape with per-symbol phase and
// drift, so breadth genuinely rises and falls instead of pinning at 100%.
func synthUniverse(n int) [][]float64 {
	closes := make([][]float64, synthStocks)
	for s := range closes {
		series := make([]float64, n)
		phase := float64(s % 41)
		amp := 4 + float64(s%13)
		drift := 0.004 * float64(s%9)
		for i := 0; i < n; i++ {
			x := float64(i)
			series[i] = 40 +
				amp*math.Sin((x+phase)/9) +
				6*math.Sin((x+phase*0.7)/47) +
				drift*x
		}
		closes[s] = series
	}
	return closes
}

func synthPanel(n int) *BreadthPanel {
	dates := make([]string, n)
	for i := range dates {
		dates[i] = synthDate(i)
	}
	full := synthUniverse(synthDates)
	closes := make([][]float64, len(full))
	for s := range full {
		closes[s] = full[s][:n]
	}
	return BuildBreadthPanel(dates, closes)
}

// rewriteFuture returns a copy of bars whose closes from index `at` onward are replaced by
// an absurd value. The prefix is byte-identical; the suffix is a market that never happened.
func rewriteFuture(bars []model.Bar, at int, close float64) []model.Bar {
	out := make([]model.Bar, len(bars))
	copy(out, bars)
	for i := at; i < len(out); i++ {
		out[i].Close = close
	}
	return out
}

// normalize zeroes the only field that legitimately differs between two runs: ReplayedAt is
// the wall clock of the replay, not a property of the session. Everything else — regime,
// rule, every structure metric, every reason and caveat string, the whole input extent — is
// compared as-is.
func normalize(rows []model.RegimeReplay) []model.RegimeReplay {
	out := make([]model.RegimeReplay, len(rows))
	copy(out, rows)
	for i := range out {
		out[i].ReplayedAt = time.Time{}
	}
	return out
}

func fieldDiff(a, b model.RegimeReplay) string {
	switch {
	case a.Date != b.Date:
		return fmt.Sprintf("date %s vs %s", a.Date, b.Date)
	case a.Regime != b.Regime:
		return fmt.Sprintf("regime %s vs %s", a.Regime, b.Regime)
	case a.RuleID != b.RuleID:
		return fmt.Sprintf("rule %s vs %s", a.RuleID, b.RuleID)
	case !reflect.DeepEqual(a.Structure, b.Structure):
		return fmt.Sprintf("structure\n  %+v\n  %+v", a.Structure, b.Structure)
	case !reflect.DeepEqual(a.Reasons, b.Reasons):
		return fmt.Sprintf("reasons %q vs %q", a.Reasons, b.Reasons)
	case !reflect.DeepEqual(a.Caveats, b.Caveats):
		return fmt.Sprintf("caveats %q vs %q", a.Caveats, b.Caveats)
	case a.InputExtent != b.InputExtent:
		return fmt.Sprintf("extent\n  %+v\n  %+v", a.InputExtent, b.InputExtent)
	}
	return ""
}

// ── the point-in-time test ────────────────────────────────────────────────────────────

// This is the test the whole work item rests on, and it is shaped like
// internal/r6backtest/engine_test.go's TestEntryPriceMustNotDependOnFutureBars: build two
// series that are IDENTICAL up to day k and wildly different afterwards, replay both, and
// require the first k rows to match field for field.
//
// Why this catches look-ahead when an "output looks sane" test cannot: the guarantee lives
// entirely in the slice bound `bars[:i+1]`. Widening it to `bars` compiles, keeps every
// invariant in regime_history_test.go satisfied (the verdicts are still well-formed, still
// carry rules and reasons, still respect the R1 ordering), and still produces a plausible
// regime distribution — because it is computing a real regime, just the WRONG DAY'S. Nothing
// about a single run's output reveals that. Two runs whose only difference is in the future
// do: under `bars`, run A reports day 199's tape for every row while run B reports day 249's
// fabricated 999 tape for every row, so row 0 disagrees immediately.
//
// The tail of the longer run is deliberately NOT compared. It legitimately differs — that is
// where the rewritten future actually is, and a replay that ignored it would have a
// different bug.
func TestReplayedRegimeMustNotDependOnFutureBars(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	bars := synthBars(synthDates)
	panel := synthPanel(synthDates)

	prefix := ReplayRegimes(model.Benchmark0050, bars[:synthSplit], panel, th)
	if len(prefix) == 0 {
		t.Fatal("fixture produced no replayed session")
	}

	// Three different futures, so a passing run cannot be an accident of one particular
	// rewrite: a melt-up, a collapse, and a flatline all leave the prefix untouched.
	for _, future := range []struct {
		name  string
		close float64
	}{
		{"melt-up to 999", 999},
		{"collapse to 3", 3},
		{"flatline at the split close", bars[synthSplit-1].Close},
	} {
		t.Run(future.name, func(t *testing.T) {
			mutated := rewriteFuture(bars, synthSplit, future.close)
			// Guard the fixture itself: the prefix must be identical, or this test proves
			// nothing at all.
			for i := 0; i < synthSplit; i++ {
				if mutated[i] != bars[i] {
					t.Fatalf("fixture broken: bar %d differs in the shared prefix", i)
				}
			}

			long := ReplayRegimes(model.Benchmark0050, mutated, panel, th)
			if len(long) <= len(prefix) {
				t.Fatalf("the longer run produced %d rows, want more than %d",
					len(long), len(prefix))
			}

			a, b := normalize(prefix), normalize(long[:len(prefix)])
			for i := range a {
				if d := fieldDiff(a[i], b[i]); d != "" {
					t.Fatalf("row %d (%s) changed when only the FUTURE changed: %s",
						i, a[i].Date, d)
				}
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("rows 0..%d differ under a future-only rewrite (field-by-field "+
					"comparison missed it — widen fieldDiff)", len(a)-1)
			}
		})
	}
}

// The same property from the other direction: shortening the breadth axis to end on the last
// replayed date must not change any row either. Together with the test above this covers both
// inputs — bars and panel — so neither can smuggle in a later session.
func TestReplayedRegimeMustNotDependOnALongerBreadthAxis(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	bars := synthBars(synthDates)

	full := ReplayRegimes(model.Benchmark0050, bars[:synthSplit], synthPanel(synthDates), th)
	short := ReplayRegimes(model.Benchmark0050, bars[:synthSplit], synthPanel(synthSplit), th)

	if len(full) == 0 || len(full) != len(short) {
		t.Fatalf("row counts differ: %d with the full axis, %d with the truncated one",
			len(full), len(short))
	}
	for i := range full {
		if d := fieldDiff(normalize(full)[i], normalize(short)[i]); d != "" {
			t.Fatalf("row %d (%s) changed when the breadth axis was truncated to the "+
				"replayed date: %s", i, full[i].Date, d)
		}
	}
}

// ── posture ───────────────────────────────────────────────────────────────────────────

// The honesty invariant. ReplayRegimes has no posture parameter, but that only stops the
// CALLER from lying; this checks the function does not invent one either.
func TestReplayedPostureIsAlwaysUnknown(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	rows := ReplayRegimes(model.Benchmark0050, synthBars(synthDates), synthPanel(synthDates), th)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Structure.Posture != model.PostureUnknown {
			t.Fatalf("%s replayed with posture %s — nothing produces institutional "+
				"evidence retroactively, so this is a fabricated read",
				r.Date, r.Structure.Posture)
		}
		// The two rules that read Posture must therefore be unreachable in a replay.
		if r.RuleID == model.RuleDistCoolingInst || r.RuleID == model.RuleDistFailedHighs {
			t.Fatalf("%s fired %s, a rule that requires PostureDeteriorating", r.Date, r.RuleID)
		}
	}
}

// Every row must announce what it is, and must survive the archive's own validation. If a
// replay could not be persisted there would be no point producing it.
func TestReplayedRowsAreSelfDescribingAndStorable(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	rows := ReplayRegimes(model.Benchmark0050, synthBars(synthDates), synthPanel(synthDates), th)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for i := range rows {
		r := rows[i]
		if err := r.Validate(); err != nil {
			t.Fatalf("%s: a replayed row must be storable: %v", r.Date, err)
		}
		found := false
		for _, c := range r.Caveats {
			if c == model.CaveatReplayPostureUnknown {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s carries no %s caveat: %v", r.Date, model.CaveatReplayCode, r.Caveats)
		}
		if len(r.Reasons) == 0 {
			t.Fatalf("%s carries no reasons — an unexplainable verdict", r.Date)
		}
		if r.ReplayedAt.IsZero() {
			t.Fatalf("%s has no replayed_at", r.Date)
		}
		if r.Kind != model.ReplayKind {
			t.Fatalf("%s has kind %q", r.Date, r.Kind)
		}
	}
}

// The input extent is the audit trail behind the point-in-time claim, so it has to be true
// rather than decorative.
func TestReplayInputExtentDescribesExactlyWhatWasSeen(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	bars := synthBars(synthDates)
	panel := synthPanel(synthDates)
	rows := ReplayRegimes(model.Benchmark0050, bars, panel, th)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.InputExtent.BenchmarkLast != r.Date || r.InputExtent.BreadthLast != r.Date {
			t.Fatalf("%s: extent ends at %s/%s", r.Date,
				r.InputExtent.BenchmarkLast, r.InputExtent.BreadthLast)
		}
		if r.InputExtent.BenchmarkFirst != bars[0].Date {
			t.Fatalf("%s: extent starts at %s, want %s", r.Date,
				r.InputExtent.BenchmarkFirst, bars[0].Date)
		}
		// The bar count must be the as-of window, never the whole series.
		want := indexOfPanelDate(panel.Dates, r.Date) + 1
		if r.InputExtent.BenchmarkBars != want {
			t.Fatalf("%s: extent claims %d bars, the as-of window is %d",
				r.Date, r.InputExtent.BenchmarkBars, want)
		}
		if r.InputExtent.BenchmarkBars > len(bars) {
			t.Fatalf("%s: extent claims more bars than exist", r.Date)
		}
		if r.InputExtent.BenchmarkSymbol != string(model.Benchmark0050) {
			t.Fatalf("%s: benchmark recorded as %q", r.Date, r.InputExtent.BenchmarkSymbol)
		}
	}
	// The first row must be the warm-up boundary, not day 0.
	if rows[0].Date != bars[th.MinBenchmarkBars].Date {
		t.Errorf("first replayed row is %s, want %s (MinBenchmarkBars=%d)",
			rows[0].Date, bars[th.MinBenchmarkBars].Date, th.MinBenchmarkBars)
	}
}

// The fixture has to actually exercise the decision table; a replay that answered UNKNOWN
// everywhere would satisfy every assertion above while testing nothing.
func TestSyntheticReplayIsNotDegenerate(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	rows := ReplayRegimes(model.Benchmark0050, synthBars(synthDates), synthPanel(synthDates), th)

	byRegime := map[model.Regime]int{}
	byRule := map[string]int{}
	for _, r := range rows {
		byRegime[r.Regime]++
		byRule[r.RuleID]++
	}
	t.Logf("rows %d; regimes %v; rules %v", len(rows), byRegime, byRule)
	if byRegime[model.RegimeUnknown] == len(rows) {
		t.Fatal("the synthetic fixture replays as UNKNOWN throughout — breadth or price is " +
			"never measurable, so the point-in-time tests are comparing empty verdicts")
	}
	if len(byRegime) < 2 {
		t.Errorf("the fixture only ever produces %v — not enough variety to be a "+
			"meaningful comparison", byRegime)
	}
}

// ── edges ─────────────────────────────────────────────────────────────────────────────

func TestReplayRegimesEdgeCases(t *testing.T) {
	th := model.StructureThresholds{}.Defaulted()
	bars := synthBars(synthDates)
	panel := synthPanel(synthDates)

	if got := ReplayRegimes(model.Benchmark0050, nil, panel, th); got != nil {
		t.Errorf("no bars must yield no rows, got %d", len(got))
	}
	if got := ReplayRegimes(model.Benchmark0050, bars, nil, th); got != nil {
		t.Errorf("a nil panel must yield no rows, got %d", len(got))
	}
	if got := ReplayRegimes(model.Benchmark0050, bars, &BreadthPanel{}, th); got != nil {
		t.Errorf("an empty panel must yield no rows, got %d", len(got))
	}
	// Below the warm-up floor there is nothing to say. Not one UNKNOWN row per day: the
	// caller asked for regimes, and "we had no history" is the absence of a row here.
	if got := ReplayRegimes(model.Benchmark0050, bars[:th.MinBenchmarkBars], panel, th); got != nil {
		t.Errorf("exactly MinBenchmarkBars bars must yield no rows, got %d", len(got))
	}
	if got := ReplayRegimes(model.Benchmark0050, bars[:th.MinBenchmarkBars+1], panel, th); len(got) != 1 {
		t.Errorf("one bar past the floor must yield exactly 1 row, got %d", len(got))
	}

	// A bar the breadth axis does not cover is SKIPPED, not guessed at: without breadth
	// there is no layer 2, and inventing one is exactly the failure this package exists to
	// avoid. Moving one bar's date off the axis must drop exactly that row.
	rows := ReplayRegimes(model.Benchmark0050, bars, panel, th)
	shifted := make([]model.Bar, len(bars))
	copy(shifted, bars)
	shifted[150].Date = "2099-12-31"
	rows2 := ReplayRegimes(model.Benchmark0050, shifted, panel, th)
	for _, r := range rows2 {
		if r.Date == "2099-12-31" {
			t.Fatal("a bar outside the breadth axis was replayed anyway — its breadth would " +
				"have to be invented")
		}
	}
	if len(rows2) != len(rows)-1 {
		t.Errorf("an off-axis bar must drop exactly one row: %d vs %d", len(rows2), len(rows))
	}

	// Rows must not share backing arrays: a caller appending to one row's caveats must not
	// mutate another's.
	all := ReplayRegimes(model.Benchmark0050, bars, panel, th)
	if len(all) < 2 {
		t.Fatal("need at least two rows")
	}
	all[0].Caveats = append(all[0].Caveats, "局部註記")
	for _, c := range all[1].Caveats {
		if c == "局部註記" {
			t.Error("caveat slices are aliased between rows")
		}
	}
}
