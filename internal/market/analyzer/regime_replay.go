package analyzer

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Point-in-time regime replay, promoted from a test helper to a production API.
//
// The whole M2+M3 chain (AnalyzePrice → AnalyzeBreadth → BuildStructureView → DecideRegime)
// is a pure function of the bars and the breadth axis, so it can be re-run for any past
// session without re-fetching anything. That is what makes regime-conditional entry
// planning and regime-conditional backtesting possible at all: data/market/ holds only a
// couple of live snapshots, which is not a history.
//
// ReplayRegimes is a strict promotion of the helper that used to live in
// regime_history_test.go — that file's invariant tests now call this function, so they are
// the regression suite proving the promotion changed no behaviour.
//
// WHAT IT CANNOT DO, part 1 — POSTURE: it cannot reconstruct institutional posture, because
// nothing produces posture retroactively. Every replayed day passes model.PostureUnknown, so
// rules R4 and R5 (which read Posture) can never fire in a replay.
//
// WHAT IT CANNOT DO, part 2 — UNIVERSE: the breadth panel it is handed was built from the
// symbols available NOW, not the symbols listed on the replayed day. That is survivorship
// bias, and it moves the numbers a rule actually reads: on 2026-08-31 the replayed
// breadth_above_ma20 was 50.56 against the live snapshot's 46.65 while every price metric
// matched exactly. ReplayRegimes cannot fix this — the membership is decided by whoever built
// the panel — so it records panel.Symbols in InputExtent.BreadthSymbolsLoaded and attaches
// model.CaveatReplayUniverseAsCached to every row.
//
// A replayed regime is therefore NOT interchangeable with the regime a live run recorded for
// the same session. The output type carries that fact in three ways — no
// Score/Confidence/Evidence fields at all, two mandatory caveats, and separate JSON
// keys/filenames — see model.RegimeReplay.

// ReplayRegimes replays the regime decision for every session in bars that the breadth axis
// also covers, oldest first.
//
// Point-in-time discipline: iteration i analyses bars[:i+1] and NOTHING ELSE. The slice
// bound is the entire guarantee — pass the full series and every day still only sees its own
// past. Sessions before th.MinBenchmarkBars are skipped rather than emitted as UNKNOWN,
// matching what the caller of a warm-up window expects. Sessions missing from panel.Dates
// are skipped: a date the universe never aligned to has no measurable breadth, and guessing
// one would be worse than having no row.
//
// benchmark names the series bars came from. It is a parameter rather than a constant because
// the recorded InputExtent.BenchmarkSymbol is an AUDIT TRAIL: hard-coding Benchmark0050 while
// accepting arbitrary bars meant a TAIEX replay would persist "0050", and nothing downstream
// could catch it (RegimeReplay.Validate only checks the benchmark is one of the defined
// values, which "0050" always is). An empty benchmark defaults to model.Benchmark0050, the
// only series the CLI replays today.
func ReplayRegimes(benchmark model.Benchmark, bars []model.Bar, panel *BreadthPanel,
	th model.StructureThresholds) []model.RegimeReplay {
	th = th.Defaulted()
	if benchmark == "" {
		benchmark = model.Benchmark0050
	}
	if panel == nil || len(panel.Dates) == 0 || len(bars) == 0 {
		return nil
	}
	// One timestamp for the whole run: these rows describe one replay, not len(bars) of them.
	replayedAt := time.Now().UTC()

	var out []model.RegimeReplay
	for i := th.MinBenchmarkBars; i < len(bars); i++ {
		asOf := bars[:i+1] // <- the point-in-time guarantee; never widen this
		_, pview := AnalyzePrice(benchmark, asOf, th)

		bi := indexOfPanelDate(panel.Dates, bars[i].Date)
		if bi < 0 {
			continue
		}
		nearHigh := pview.DDHigh <= th.NearHighDDPct
		_, bview := AnalyzeBreadth(panel, bi, nearHigh, th)

		// Posture is UNKNOWN by construction, not by omission. There is no parameter to pass
		// a real posture through, so a caller cannot accidentally make a replay look live.
		v := BuildStructureView(pview, bview, model.PostureUnknown, th)
		d := DecideRegime(v, th)

		out = append(out, model.RegimeReplay{
			Kind:      model.ReplayKind,
			Date:      bars[i].Date,
			Regime:    d.Regime,
			RuleID:    d.RuleID,
			Structure: d.View,
			Reasons:   copyStrings(d.Reasons),
			// Both mandatory caveats, in the order model.NewRegimeReplay uses. Validate
			// rejects a row missing either, so a caller cannot strip one on the way to disk.
			Caveats: append(copyStrings(d.Caveats),
				model.CaveatReplayPostureUnknown, model.CaveatReplayUniverseAsCached),
			ReplayedAt: replayedAt,
			SchemaVer:  model.ReplaySchemaVersion,
			InputExtent: model.ReplayExtent{
				BenchmarkSymbol: string(benchmark),
				BenchmarkBars:   len(asOf),
				BenchmarkFirst:  asOf[0].Date,
				BenchmarkLast:   asOf[len(asOf)-1].Date,
				BreadthIndex:    bi,
				BreadthFirst:    panel.Dates[0],
				BreadthLast:     panel.Dates[bi],
				BreadthUniverse: panelValidCount(panel, bi),
				// The cache membership this run used, so a re-run's different breadth has a
				// visible cause rather than looking like nondeterminism.
				BreadthSymbolsLoaded: panel.Symbols,
			},
		})
	}
	return out
}

// copyStrings detaches a slice from whatever produced it, so two rows can never end up
// sharing (and later aliasing) the same backing array.
func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func panelValidCount(p *BreadthPanel, at int) int {
	if at < 0 || at >= len(p.ValidCount) {
		return 0
	}
	return p.ValidCount[at]
}

// indexOfPanelDate binary-searches the ascending breadth axis, returning -1 when the date is
// not on it.
//
// (Named differently from the identical helper in history_test.go only because that file is
// frozen: the two cannot share a name in one package, and the test file is the regression
// evidence for this promotion, so it is the one that must not move.)
func indexOfPanelDate(dates []string, d string) int {
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
