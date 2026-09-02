package scanner

import (
	"context"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/ai"

	"github.com/deep-huang/stock-scanner/internal/institution"
)

// marketEntry is a minimal watchlist entry for evidence projection.
func marketEntry() *WatchlistEntry {
	e := &WatchlistEntry{RocketScore: 71, RocketStage: StagePreBreakout, WatchAction: ActPrepare}
	e.A = StockAnalysis{Symbol: "2330", Name: "台積電", Close: 1000, MA20: 950, Score: 80}
	return e
}

// When a snapshot exists, the model must actually receive the regime. Before M3 this struct
// was constructed empty at the call site, so the fields existed and were always blank.
func TestMarketContextReachesTheEvidence(t *testing.T) {
	ev := buildAIEvidence(marketEntry(), AIMarketContext{
		Available: true, Regime: "BULL_PULLBACK", Score: 61.4,
	})
	if ev.MarketDataStatus != MarketDataAvailable {
		t.Errorf("status = %q, want AVAILABLE", ev.MarketDataStatus)
	}
	if ev.MarketRegime != "BULL_PULLBACK" {
		t.Errorf("regime = %q", ev.MarketRegime)
	}
	if ev.MarketScore != 61.4 {
		t.Errorf("score = %v", ev.MarketScore)
	}
}

// THE missing-vs-neutral guard. A scan with no market snapshot must say "unavailable" — not
// omit the field, and above all not send a regime it does not have.
func TestMissingMarketIsUnavailableNotNeutral(t *testing.T) {
	ev := buildAIEvidence(marketEntry(), AIMarketContext{}) // the zero value: nothing loaded

	if ev.MarketDataStatus != MarketDataUnavailable {
		t.Errorf("status = %q, want UNAVAILABLE", ev.MarketDataStatus)
	}
	if ev.MarketRegime != "" {
		t.Errorf("a regime was invented with no snapshot: %q", ev.MarketRegime)
	}
	if ev.MarketScore != 0 {
		t.Errorf("a score was invented with no snapshot: %v", ev.MarketScore)
	}
	// The status must be non-empty so `omitempty` cannot drop it — an omitted field is
	// exactly the ambiguity this exists to remove.
	if ev.MarketDataStatus == "" {
		t.Fatal("the status field would be omitted from the JSON")
	}

	// A regime carried on an unavailable context must NOT leak through: Available is the
	// authority, not the presence of a string.
	stale := buildAIEvidence(marketEntry(), AIMarketContext{
		Available: false, Regime: "BULL", Score: 90,
	})
	if stale.MarketRegime != "" || stale.MarketScore != 0 {
		t.Errorf("an unavailable context leaked regime=%q score=%v",
			stale.MarketRegime, stale.MarketScore)
	}
	if stale.MarketDataStatus != MarketDataUnavailable {
		t.Errorf("status = %q", stale.MarketDataStatus)
	}
}

// The same discipline for chips: "no snapshot" and "the institutions were flat" must not
// arrive as the same evidence.
func TestInstitutionUnavailableIsNotZeroFlow(t *testing.T) {
	// No institution layer at all.
	none := buildAIEvidence(marketEntry(), AIMarketContext{})
	if none.InstitutionalStatus != InstitutionDataUnavailable {
		t.Errorf("status = %q, want UNAVAILABLE", none.InstitutionalStatus)
	}
	if len(none.Institutional) != 0 {
		t.Errorf("lines produced with no data: %v", none.Institutional)
	}

	// Data present, but every leg netted exactly zero. That is a MEASUREMENT, and it must
	// not look like the case above.
	e := marketEntry()
	e.Institution = &institution.StockChipView{Code: "2330", AsOfDate: "2026-08-31"}
	flat := buildAIEvidence(e, AIMarketContext{})
	if flat.InstitutionalStatus != InstitutionDataAvailable {
		t.Errorf("a present-but-flat snapshot reported %q", flat.InstitutionalStatus)
	}
	if flat.InstitutionalStatus == none.InstitutionalStatus {
		t.Fatal("flat flow and absent data produced the same evidence")
	}

	// Data present and non-flat: the lines appear.
	e2 := marketEntry()
	e2.Institution = &institution.StockChipView{
		Code: "2330", AsOfDate: "2026-08-31",
		Foreign: institution.LegStats{TodayNet: 1500, Sum5d: 4200},
	}
	live := buildAIEvidence(e2, AIMarketContext{})
	if live.InstitutionalStatus != InstitutionDataAvailable {
		t.Errorf("status = %q", live.InstitutionalStatus)
	}
	if len(live.Institutional) == 0 || !strings.Contains(live.Institutional[0], "外資") {
		t.Errorf("institutional lines = %v", live.Institutional)
	}
}

// THE shadow guarantee, asserted on the projection rather than on a comment: building the
// evidence must not mutate the entry it reads.
func TestBuildAIEvidenceDoesNotMutateTheEntry(t *testing.T) {
	e := marketEntry()
	e.ExplosionProb = "44%"
	before := *e

	_ = buildAIEvidence(e, AIMarketContext{Available: true, Regime: "BULL", Score: 70})

	if e.RocketScore != before.RocketScore {
		t.Errorf("RocketScore changed: %v → %v", before.RocketScore, e.RocketScore)
	}
	if e.ExplosionProb != before.ExplosionProb {
		t.Errorf("ExplosionProb changed: %v → %v", before.ExplosionProb, e.ExplosionProb)
	}
	if e.WatchAction != before.WatchAction {
		t.Errorf("WatchAction changed: %v → %v", before.WatchAction, e.WatchAction)
	}
	if e.RocketStage != before.RocketStage {
		t.Errorf("RocketStage changed: %v → %v", before.RocketStage, e.RocketStage)
	}
	if e.A.Score != before.A.Score || e.A.Action != before.A.Action {
		t.Errorf("the underlying analysis changed: %+v → %+v", before.A, e.A)
	}
}

// AttachAI with the feature disabled must not touch a single entry, whatever the market
// context says.
func TestAttachAIDisabledChangesNothing(t *testing.T) {
	entries := []WatchlistEntry{*marketEntry(), *marketEntry()}
	entries[1].A.Symbol = "2454"
	before := append([]WatchlistEntry(nil), entries...)

	AttachAI(context.Background(), entries, ai.Config{BaseURL: "http://127.0.0.1:1"}, false,
		AIMarketContext{Available: true, Regime: "BULL", Score: 70}, nil)

	for i := range entries {
		if entries[i].AI != nil {
			t.Errorf("%s gained an AI field with the feature disabled", entries[i].A.Symbol)
		}
		if entries[i].RocketScore != before[i].RocketScore ||
			entries[i].WatchAction != before[i].WatchAction ||
			entries[i].ExplosionProb != before[i].ExplosionProb {
			t.Errorf("%s was modified: %+v → %+v", entries[i].A.Symbol, before[i], entries[i])
		}
	}
}
