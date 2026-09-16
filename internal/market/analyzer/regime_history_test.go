package analyzer

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// replayRegimes runs the whole M2+M3 chain across the cached history. Posture is UNKNOWN
// throughout because no producer exists until M6 — which is exactly the state M3 ships in,
// so this is the honest thing to validate against.
//
// The loop this helper used to inline is now ReplayRegimes (regime_replay.go), a production
// API. The helper delegates to it and maps the rows back onto model.RegimeDecision, so the
// assertions below — written against the inlined loop — now serve as the regression suite
// for that promotion: they pass only if the production function behaves identically.
func replayRegimes(t *testing.T) []model.RegimeDecision {
	t.Helper()
	bars, panel := loadHistory(t)
	rows := ReplayRegimes(model.Benchmark0050, bars, panel, th)
	var out []model.RegimeDecision
	for _, r := range rows {
		out = append(out, model.RegimeDecision{
			Regime:  r.Regime,
			RuleID:  r.RuleID,
			View:    r.Structure,
			Reasons: r.Reasons,
			Caveats: r.Caveats,
		})
	}
	return out
}

// Invariants that must hold on any real data, not a snapshot of today's distribution.
func TestRealHistoryRegimeInvariants(t *testing.T) {
	decisions := replayRegimes(t)
	if len(decisions) == 0 {
		t.Skip("no history")
	}

	byRegime := map[model.Regime]int{}
	byRule := map[string]int{}
	for _, d := range decisions {
		byRegime[d.Regime]++
		byRule[d.RuleID]++

		if !d.Regime.Valid() {
			t.Fatalf("undefined regime %q", d.Regime)
		}
		if d.RuleID == "" {
			t.Fatalf("%s produced no rule id", d.Regime)
		}
		if len(d.Reasons) == 0 {
			t.Fatalf("%s/%s carries no reasons — an unexplainable verdict", d.Regime, d.RuleID)
		}
		// The R1 invariant, verified against real inputs rather than only synthetic ones.
		if d.View.Structure == model.StructureDowntrend && d.Regime != model.RegimeBear {
			t.Fatalf("real DOWNTREND day classified %s (rule %s)", d.Regime, d.RuleID)
		}
		// Both pullback rules gate on the primary trend holding.
		if d.Regime == model.RegimeBullPullback && !d.View.TrendIntact {
			t.Fatalf("BULL_PULLBACK without trendIntact (rule %s)", d.RuleID)
		}
		// Distribution always has deteriorating internals behind it.
		if d.Regime == model.RegimeDistribution &&
			!d.View.Breadth.Deteriorating() && d.View.Posture != model.PostureDeteriorating {
			t.Fatalf("DISTRIBUTION from a clean tape (rule %s)", d.RuleID)
		}
	}
	t.Logf("regimes: %v", byRegime)
	t.Logf("rules:   %v", byRule)

	// A classifier that only ever emits one or two states is not classifying.
	if len(byRegime) < 4 {
		t.Errorf("degenerate regime distribution: %v", byRegime)
	}
	// No single regime may swallow the entire tape.
	for r, n := range byRegime {
		if float64(n)/float64(len(decisions)) > 0.60 {
			t.Errorf("%s covers %.0f%% of history — the table has collapsed to one answer",
				r, float64(n)/float64(len(decisions))*100)
		}
	}
}

// One-day regime flapping would make the dashboard unusable as context. This measures it and
// holds it to a loose ceiling — loose because a genuine turn SHOULD flip the regime.
func TestRealHistoryRegimeStability(t *testing.T) {
	decisions := replayRegimes(t)
	if len(decisions) < 3 {
		t.Skip("not enough history")
	}
	flips, oneDay := 0, 0
	for i := 1; i < len(decisions); i++ {
		if decisions[i].Regime != decisions[i-1].Regime {
			flips++
			if i+1 < len(decisions) && decisions[i+1].Regime == decisions[i-1].Regime {
				oneDay++ // A → B → A
			}
		}
	}
	t.Logf("transitions %d (%.1f%% of days), one-day round trips %d",
		flips, float64(flips)/float64(len(decisions))*100, oneDay)

	if float64(flips)/float64(len(decisions)) > 0.35 {
		t.Errorf("regime changes on %.0f%% of days — too unstable to be context",
			float64(flips)/float64(len(decisions))*100)
	}
}
