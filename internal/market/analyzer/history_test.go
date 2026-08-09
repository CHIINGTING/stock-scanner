package analyzer

import (
	"os"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// M2's acceptance check: replay the two structural layers across the entire cached history
// and assert the classifiers behave sanely on real Taiwan-market data rather than only on
// hand-built fixtures.
//
// This is the reason M2 was sequenced before the network milestones — price and breadth are
// the only dimensions with two years of history available on day one, so they are the only
// ones whose thresholds can be validated before anything is wired to an exchange.
//
// Skips when .cache is absent (fresh clone / CI), so it can never be the reason the suite
// goes red. It asserts INVARIANTS, not specific verdicts: the thresholds are uncalibrated
// hypotheses, and pinning today's exact classification would just freeze a guess.

const cacheDir = "../../../.cache"

func loadHistory(t *testing.T) ([]model.Bar, *BreadthPanel) {
	t.Helper()
	if _, err := os.Stat(cacheDir); err != nil {
		t.Skipf("no .cache at %s — skipping real-history validation", cacheDir)
	}
	feed := provider.CacheFeed{Dir: cacheDir}

	bars, err := feed.Benchmark(string(model.Benchmark0050), "")
	if err != nil {
		t.Skipf("benchmark 0050 unavailable: %v", err)
	}
	u, err := feed.Universe("")
	if err != nil {
		t.Skipf("universe unavailable: %v", err)
	}
	t.Logf("benchmark %d bars (%s → %s); universe %d symbols × %d dates (dropped %d low-coverage dates)",
		len(bars), bars[0].Date, bars[len(bars)-1].Date, u.Symbols, len(u.Dates), len(u.DroppedDates))

	return bars, BuildBreadthPanel(u.Dates, u.Closes)
}

// Replay every date and assert the invariants that must hold on any data.
func TestRealHistoryFullReplay(t *testing.T) {
	bars, panel := loadHistory(t)

	structures := map[model.PrimaryStructure]int{}
	breadths := map[model.BreadthQuality]int{}
	unknownAfterWarmup := 0

	for i := range bars {
		asOf := bars[:i+1]
		ev, pview := AnalyzePrice(model.Benchmark0050, asOf, th)

		// Contract 1: below the bar floor it is MISSING, above it, it is measured.
		if len(asOf) < th.MinBenchmarkBars {
			if ev.Available {
				t.Fatalf("%s: %d bars is under the floor but evidence is available", bars[i].Date, len(asOf))
			}
			if ev.Direction != model.DirUnknown {
				t.Fatalf("%s: insufficient history must be UNKNOWN, got %s", bars[i].Date, ev.Direction)
			}
			continue
		}
		if !ev.Available {
			t.Fatalf("%s: %d bars is above the floor but evidence is missing (%s)", bars[i].Date, len(asOf), ev.Reason)
		}

		s := ClassifyStructure(pview, th)
		structures[s]++
		if s == model.StructureUnknown {
			unknownAfterWarmup++
		}

		// Contract 2: a measured structure always carries a scoreable direction.
		if !ev.Usable() {
			t.Fatalf("%s: available price evidence must be usable, got %s", bars[i].Date, ev.Direction)
		}

		// Contract 3: metrics that gate rules must be present and sane.
		if dd, ok := ev.Metric(model.MetricDDFromHigh60); !ok || dd < 0 {
			t.Fatalf("%s: drawdown metric missing or negative: %v", bars[i].Date, dd)
		}

		// Breadth for the same date, when the axis reaches it.
		bi := indexOfDate(panel.Dates, bars[i].Date)
		if bi < 0 {
			continue
		}
		nearHigh := pview.DDHigh <= th.NearHighDDPct
		bev, bview := AnalyzeBreadth(panel, bi, nearHigh, th)
		q := ClassifyBreadth(bview, nearHigh, th)
		breadths[q]++

		if bev.Available != bview.Sufficient {
			t.Fatalf("%s: breadth evidence availability disagrees with view sufficiency", bars[i].Date)
		}
		if bview.Sufficient {
			if bview.AboveMA20 < 0 || bview.AboveMA20 > 100 {
				t.Fatalf("%s: breadth %v out of range", bars[i].Date, bview.AboveMA20)
			}
			if bview.Drop20 < 0 {
				t.Fatalf("%s: Drop20 must never be negative, got %v", bars[i].Date, bview.Drop20)
			}
			if q == model.BreadthUnknown {
				t.Fatalf("%s: a sufficient breadth view classified as UNKNOWN", bars[i].Date)
			}
		}
	}

	t.Logf("structures: %v", structures)
	t.Logf("breadth:    %v", breadths)

	// Once past the warm-up, every date must produce a real structure.
	if unknownAfterWarmup != 0 {
		t.Errorf("%d dates were UNKNOWN despite having enough history", unknownAfterWarmup)
	}
	// A classifier that only ever emits one state is not classifying anything. Two years of
	// Taiwan tape genuinely contains more than one structural regime.
	if len(structures) < 2 {
		t.Errorf("structure classifier is degenerate — only produced %v", structures)
	}
	if len(breadths) < 2 {
		t.Errorf("breadth classifier is degenerate — only produced %v", breadths)
	}
}

// The layer-1 ordering must hold on real data too: DOWNTREND and WEAKENING are separated
// solely by MA120, and no real session may satisfy both readings.
func TestRealHistoryStructureOrderingHolds(t *testing.T) {
	bars, _ := loadHistory(t)

	for i := th.MinBenchmarkBars; i < len(bars); i++ {
		_, v := AnalyzePrice(model.Benchmark0050, bars[:i+1], th)
		switch ClassifyStructure(v, th) {
		case model.StructureDowntrend:
			if v.Close >= v.MA120 {
				t.Fatalf("%s: DOWNTREND while close %.2f >= MA120 %.2f", bars[i].Date, v.Close, v.MA120)
			}
		case model.StructureWeakening:
			if v.Close <= v.MA120 {
				t.Fatalf("%s: WEAKENING requires the long-term floor to hold, close %.2f <= MA120 %.2f",
					bars[i].Date, v.Close, v.MA120)
			}
		case model.StructureStrongUptrend:
			if v.DDHigh > th.NearHighDDPct {
				t.Fatalf("%s: STRONG_UPTREND while %.2f%% off the high", bars[i].Date, v.DDHigh)
			}
		}
	}
}

// BuildStructureView must never panic or emit an undefined state across the whole history,
// and an unknown posture must never invalidate a known price/breadth read.
func TestRealHistoryStructureViewAlwaysValid(t *testing.T) {
	bars, panel := loadHistory(t)

	known := 0
	for i := th.MinBenchmarkBars; i < len(bars); i++ {
		_, pview := AnalyzePrice(model.Benchmark0050, bars[:i+1], th)
		bi := indexOfDate(panel.Dates, bars[i].Date)
		if bi < 0 {
			continue
		}
		nearHigh := pview.DDHigh <= th.NearHighDDPct
		_, bview := AnalyzeBreadth(panel, bi, nearHigh, th)

		v := BuildStructureView(pview, bview, model.PostureUnknown, th)
		if !v.Structure.Valid() || !v.Breadth.Valid() || !v.Posture.Valid() {
			t.Fatalf("%s: undefined structural state %+v", bars[i].Date, v)
		}
		if v.Benchmark != model.Benchmark0050 {
			t.Fatalf("%s: benchmark must be recorded as the proxy it is, got %q", bars[i].Date, v.Benchmark)
		}
		if v.Known() {
			known++
		}
		if v.OrderlyCorrection && (v.Metrics[model.MetricDDFromHigh60] < th.CorrectionMinPct ||
			v.Metrics[model.MetricDDFromHigh60] > th.CorrectionMaxPct) {
			t.Fatalf("%s: orderly correction outside the band: %v", bars[i].Date, v.Metrics[model.MetricDDFromHigh60])
		}
	}
	if known == 0 {
		t.Error("no date in the whole history produced a Known() structure view")
	}
	t.Logf("Known() structure views: %d", known)
}

func indexOfDate(dates []string, d string) int {
	lo, hi := 0, len(dates)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case dates[mid] == d:
			return mid
		case dates[mid] < d:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return -1
}
