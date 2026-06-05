package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ── C6b-final audit: verification only (no functional change) ────────────────
// These tests assert the cross-cutting guardrail invariants now that C6b-1..4 all
// touch rocket_candidate_score / watch_action / ExplosionProb together.

func auditItems() []fetcher.StockData {
	return []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.40, 2_000_000)},
		{Symbol: "2222", Name: "Mid", Source: "watchlist", Candles: makeCandles(260, 50, 0.10, 1_500_000)},
		{Symbol: "3333", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.00, 1_000_000)},
	}
}

var (
	auditSO = map[string]string{}
	auditRT = map[string]*SectorRotation{}
	auditMB = map[string][]fetcher.StockData{}
)

func assertSameScoring(t *testing.T, base, got []WatchlistEntry) {
	t.Helper()
	if len(base) != len(got) {
		t.Fatalf("length differs: base=%d got=%d", len(base), len(got))
	}
	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol {
			t.Errorf("order changed at %d: %s vs %s", i, got[i].A.Symbol, base[i].A.Symbol)
		}
		if got[i].RocketScore != base[i].RocketScore {
			t.Errorf("%s RocketScore changed: %d vs %d", base[i].A.Symbol, got[i].RocketScore, base[i].RocketScore)
		}
		if got[i].WatchAction != base[i].WatchAction {
			t.Errorf("%s WatchAction changed: %s vs %s", base[i].A.Symbol, got[i].WatchAction, base[i].WatchAction)
		}
		if got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s ExplosionProb changed: %s vs %s", base[i].A.Symbol, got[i].ExplosionProb, base[i].ExplosionProb)
		}
	}
}

// AUDIT 1: master flag OFF + all four signal flags ON → identical to all-off baseline
// (shadow computed and attached, but nothing scored).
func TestAuditMasterOffAllSignalsOn(t *testing.T) {
	items := auditItems()
	base := New(Config{}).EnrichWatchlist(items, auditSO, auditRT, auditMB, nil)

	s := New(Config{EnableRSRank: true, EnableNewHigh: true, EnableVCP: true, EnableMomentumFlow: true}) // master OFF
	got := s.EnrichWatchlist(items, auditSO, auditRT, auditMB, s.BuildRSTable(items))

	assertSameScoring(t, base, got)
	for i := range got {
		if got[i].Shadow == nil {
			t.Errorf("%s: shadow should be attached when signal flags are on", got[i].A.Symbol)
		}
	}
}

// AUDIT 2: master flag ON + all four signal flags OFF → identical to baseline,
// and no shadow container created.
func TestAuditMasterOnAllSignalsOff(t *testing.T) {
	items := auditItems()
	base := New(Config{}).EnrichWatchlist(items, auditSO, auditRT, auditMB, nil)

	s := New(Config{EnableSignalGuardrailScoring: true}) // all signals off
	got := s.EnrichWatchlist(items, auditSO, auditRT, auditMB, nil)

	assertSameScoring(t, base, got)
	for i := range got {
		if got[i].Shadow != nil {
			t.Errorf("%s: no signal flag on → shadow must be nil", got[i].A.Symbol)
		}
	}
}

// AUDIT 4: master ON + all signal flags ON → no pathological score inflation.
// (Guardrails are replacements/corrections, not additive bonuses, so the all-on
// distribution must not collapse to 90–100 nor blow the [0,100] range.)
func TestAuditAllOnNoInflation(t *testing.T) {
	items := auditItems()
	base := New(Config{}).EnrichWatchlist(items, auditSO, auditRT, auditMB, nil)

	s := New(Config{
		EnableSignalGuardrailScoring: true,
		EnableRSRank:                 true,
		EnableNewHigh:                true,
		EnableVCP:                    true,
		EnableMomentumFlow:           true,
	})
	got := s.EnrichWatchlist(items, auditSO, auditRT, auditMB, s.BuildRSTable(items))

	byCode := map[string]int{}
	for _, e := range base {
		byCode[e.A.Symbol] = e.RocketScore
	}
	allHigh := true
	for _, e := range got {
		if e.RocketScore < 0 || e.RocketScore > 100 {
			t.Errorf("%s score out of range: %d", e.A.Symbol, e.RocketScore)
		}
		delta := e.RocketScore - byCode[e.A.Symbol]
		t.Logf("AUDIT all-on %s: base=%d all-on=%d delta=%+d action=%s prob=%s",
			e.A.Symbol, byCode[e.A.Symbol], e.RocketScore, delta, e.WatchAction, e.ExplosionProb)
		if e.RocketScore < 90 {
			allHigh = false
		}
		// Per-stock change must stay bounded by the documented guardrail magnitudes
		// (no group inflation): g2≤±9, g3 swing, momentum ≤±12. A jump > 30 would
		// indicate additive double-counting.
		if delta > 30 || delta < -40 {
			t.Errorf("%s: suspicious score delta %+d (possible inflation/double-count)", e.A.Symbol, delta)
		}
	}
	if allHigh {
		t.Errorf("every stock scored >=90 with all flags on — possible inflation")
	}
}
