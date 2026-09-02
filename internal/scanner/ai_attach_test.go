package scanner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/institution"
)

// These tests never contact OpenAI. Every request goes to an httptest server, so the suite is
// deterministic whether or not a key exists in the environment.

const aiReply = `{"summary":"沿 20 日均線整理，量能溫和放大。","bull_case":["站上 MA20"],` +
	`"bear_case":["族群偏弱"],"risk_flags":["整理天數偏短"],"confidence":0.6}`

func aiServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "unit-test-key-not-a-secret")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func aiOKHandler(calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		b, _ := json.Marshal(map[string]any{
			"status": "completed",
			"output": []any{
				map[string]any{"type": "reasoning", "id": "rs_test"},
				map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": aiReply}},
				},
			},
			"usage": map[string]int{"total_tokens": 120},
		})
		w.Write(b)
	}
}

// scoredFields is everything AI must never touch. Comparing a rendered string rather than
// individual asserts means a NEW scored field added later is covered automatically.
func scoredFields(entries []WatchlistEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(strings.Join([]string{
			e.A.Symbol, e.A.Name,
			aiItoa(e.A.Score), aiItoa(e.RocketScore), string(e.RocketStage), e.ExplosionProb,
			string(e.WatchAction), e.DaysToWatch, e.RiskLabel, e.RiskWarning,
			aiFtoa(e.BreakoutPrice), aiFtoa(e.SupportPrice), aiFtoa(e.StopLossPrice),
			e.EntryZone, e.TakeProfitZone,
			e.Sector, e.SectorFlowDir, string(e.SectorStage),
			strings.Join(e.Reasons, "|"),
		}, "\x1f"))
		sb.WriteString("\n")
	}
	return sb.String()
}

func aiItoa(i int) string     { b, _ := json.Marshal(i); return string(b) }
func aiFtoa(f float64) string { b, _ := json.Marshal(f); return string(b) }

func aiEntries(t *testing.T) []WatchlistEntry {
	t.Helper()
	out := enrich(Config{})
	if len(out) == 0 {
		t.Fatal("fixture produced no watchlist entries")
	}
	return out
}

// The core guarantee: whatever the AI layer does — succeed, fail, or never run — every scored
// field is byte-identical.
func TestAttachAIDoesNotAffectScoring(t *testing.T) {
	baseline := scoredFields(aiEntries(t))

	cases := []struct {
		name    string
		enabled bool
		handler http.HandlerFunc
	}{
		{"disabled", false, aiOKHandler(nil)},
		{"success", true, aiOKHandler(nil)},
		{"429", true, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }},
		{"500", true, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"garbage", true, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{{{")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := aiServer(t, tc.handler)
			entries := aiEntries(t)
			AttachAI(context.Background(), entries, ai.Config{BaseURL: url}, tc.enabled,
				AIMarketContext{Regime: "NEUTRAL", Score: 50}, nil)

			if got := scoredFields(entries); got != baseline {
				t.Errorf("AI changed a scored field.\n baseline: %s\n after AI: %s", baseline, got)
			}
		})
	}
}

// Ordering is part of the output. A post-pass must not reshuffle it.
func TestAttachAIPreservesOrder(t *testing.T) {
	url := aiServer(t, aiOKHandler(nil))
	entries := aiEntries(t)
	before := make([]string, len(entries))
	for i, e := range entries {
		before[i] = e.A.Symbol
	}
	AttachAI(context.Background(), entries, ai.Config{BaseURL: url}, true, AIMarketContext{}, nil)
	for i, e := range entries {
		if e.A.Symbol != before[i] {
			t.Fatalf("order changed at %d: %s → %s", i, before[i], e.A.Symbol)
		}
	}
}

// Disabled must mean "nothing attached at all", not "attached with an empty reading" — the
// report then renders exactly as it did before the feature existed.
func TestAttachAINilWhenDisabled(t *testing.T) {
	entries := aiEntries(t)
	AttachAI(context.Background(), entries, ai.Config{BaseURL: "http://127.0.0.1:1"}, false,
		AIMarketContext{}, nil)
	for _, e := range entries {
		if e.AI != nil {
			t.Errorf("%s: AI must be nil when disabled, got %+v", e.A.Symbol, e.AI)
		}
	}
}

// A missing key is a normal, silent outcome — not an error, and not a stalled scan.
func TestAttachAIWithoutAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	entries := aiEntries(t)
	// An unroutable base URL proves no request was attempted: if one were, this would hang
	// until the timeout rather than returning immediately.
	AttachAI(context.Background(), entries, ai.Config{BaseURL: "http://127.0.0.1:1"}, true,
		AIMarketContext{}, nil)
	for _, e := range entries {
		if e.AI == nil {
			continue
		}
		if e.AI.Status != ai.StatusNoKey || e.AI.OK() {
			t.Errorf("%s: expected NO_API_KEY, got %+v", e.A.Symbol, e.AI)
		}
	}
}

// Cost control at the scanner boundary: only actionable entries may cost a request.
func TestAttachAIOnlySpendsOnActionableCandidates(t *testing.T) {
	var calls int
	url := aiServer(t, aiOKHandler(&calls))
	entries := aiEntries(t)

	var actionable int
	for _, e := range entries {
		if aiActionable(e.WatchAction) {
			actionable++
		}
	}
	// Guard the guard: with 0 (or all) actionable entries the assertions below would pass
	// vacuously, so the fixture must contain both kinds.
	if actionable == 0 || actionable == len(entries) {
		t.Fatalf("fixture has %d/%d actionable entries; need a mix for this test to mean anything",
			actionable, len(entries))
	}
	AttachAI(context.Background(), entries, ai.Config{BaseURL: url, MaxStocks: 99}, true,
		AIMarketContext{}, nil)

	if calls != actionable {
		t.Errorf("made %d requests for %d actionable entries", calls, actionable)
	}
	for _, e := range entries {
		if aiActionable(e.WatchAction) || e.AI == nil {
			continue
		}
		if e.AI.Status != ai.StatusSkipped {
			t.Errorf("%s (%s) is not actionable but got status %s",
				e.A.Symbol, e.WatchAction, e.AI.Status)
		}
	}
}

func TestAIActionable(t *testing.T) {
	for _, a := range []WatchAction{ActPrepare, ActBreakoutBuy, ActPullbackBuy, ActWatchClose} {
		if !aiActionable(a) {
			t.Errorf("%s should be actionable", a)
		}
	}
	// WAIT and the exit-side actions have nothing to interpret, so they must never be paid for.
	for _, a := range []WatchAction{ActWait, ActRemove, ActTakeProfit} {
		if aiActionable(a) {
			t.Errorf("%s must not be analysed", a)
		}
	}
}

// The evidence contract: only fields the scanner already computed, nothing fabricated.
func TestBuildAIEvidenceCopiesOnlyExistingValues(t *testing.T) {
	e := WatchlistEntry{
		A: StockAnalysis{
			Symbol: "2330", Name: "台積電", Close: 110, MA20: 100,
			MA20Trend: "UP", VolumeRatio: 1.8, PriceVolumeSignal: "價漲量增", Score: 77,
		},
		RocketScore: 82, RocketStage: RocketStage("PRE_BREAKOUT"), ExplosionProb: "高",
		WatchAction: ActPrepare, Sector: "半導體", RiskLabel: "中",
		Consol: Consolidation{Days: 12, Bucket: "理想"},
	}
	ev := buildAIEvidence(&e, // R13-M3: Available is now the authority. A context whose regime is set but
		// whose Available is false means "we could not look", and the projection
		// deliberately refuses to pass the regime through.
		AIMarketContext{Available: true, Regime: "RISK_ON", Score: 72})

	if ev.Symbol != "2330" || ev.TechnicalScore != 77 || ev.RocketScore != 82 {
		t.Errorf("copied fields wrong: %+v", ev)
	}
	if ev.MA20DistancePct < 9.99 || ev.MA20DistancePct > 10.01 {
		t.Errorf("MA20 distance = %v, want ~10", ev.MA20DistancePct)
	}
	if ev.MarketRegime != "RISK_ON" || ev.MarketScore != 72 {
		t.Errorf("market context not carried: %+v", ev)
	}
	if !strings.Contains(ev.Consolidation, "12") {
		t.Errorf("consolidation not carried: %q", ev.Consolidation)
	}
	// The institution layer did not run, so nothing institutional may appear.
	if len(ev.Institutional) != 0 {
		t.Errorf("institutional data invented from nothing: %v", ev.Institutional)
	}

	// A missing MA must read as absent, never as a fabricated 0% distance being meaningful.
	if got := pctFrom(110, 0); got != 0 {
		t.Errorf("pctFrom with no MA = %v, want 0", got)
	}
}

// Legs with no observation are omitted rather than reported as a real zero — MISSING ≠ FLAT.
func TestAIInstitutionalLinesOmitUnobservedLegs(t *testing.T) {
	if aiInstitutionalLines(nil) != nil {
		t.Error("nil view must produce no lines")
	}
	lines := aiInstitutionalLines(&institution.StockChipView{
		Foreign: institution.LegStats{TodayNet: 1200, Sum5d: 4300},
		Trust:   institution.LegStats{}, // never observed
		Dealer:  institution.LegStats{TodayNet: -50, Sum5d: -80},
	})
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %v", len(lines), lines)
	}
	joined := strings.Join(lines, " ")
	if strings.Contains(joined, "投信") {
		t.Errorf("an unobserved leg was reported: %v", lines)
	}
	if !strings.Contains(joined, "外資") || !strings.Contains(joined, "自營商") {
		t.Errorf("observed legs missing: %v", lines)
	}
}
