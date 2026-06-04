package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func sampleEntry(withShadow bool) scanner.WatchlistEntry {
	e := scanner.WatchlistEntry{
		A:             scanner.StockAnalysis{Symbol: "1111", Name: "Test", Close: 100},
		RocketScore:   60,
		RocketStage:   scanner.StagePreBreakout,
		WatchAction:   scanner.ActPrepare,
		ExplosionProb: "MEDIUM",
		DaysToWatch:   "1~3 天",
	}
	if withShadow {
		e.Shadow = &scanner.ShadowSignals{
			RS:      &scanner.RSResult{Computed: true, RSRankPercentile: 60, RSScore: 60},
			NewHigh: &scanner.NewHighResult{Computed: true, NewHighScore: 70, DistanceFrom52wHighPct: -7, Near52wHigh: true, H20: true, H60: true},
			VCP:     &scanner.VCPResult{Computed: true, Valid: true, Grade: scanner.VCPGradeStandard, QualityScore: 80, Depths: []float64{18, 10, 5}},
			Momentum: &scanner.MomentumState{Computed: true, Flow: scanner.MomentumBuilding, Score: 70, StructureTrend: "HH_HL"},
		}
	}
	return e
}

func genHTML(t *testing.T, entries []scanner.WatchlistEntry, gv GuardrailViewOptions) string {
	t.Helper()
	dir := t.TempDir()
	r := New(Config{OutputDir: dir})
	date := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	if err := r.Generate(nil, nil, entries, nil, "-", date, gv); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report_20260605.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(b)
}

// 1. show=false → default output has no Guardrail Signals section.
func TestC7HiddenByDefault(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: false})
	for _, marker := range []string{"Guardrail Signals", "實驗性", "RS Rank", "NewHighScore"} {
		if strings.Contains(html, marker) {
			t.Errorf("show=false must not render %q", marker)
		}
	}
}

// 2. show=true + shadow present → all four signal blocks + experimental notice.
func TestC7ShowsAllSignals(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{
		Show: true, RSWatchThreshold: 70, MFScoreModifierBuilding: 5,
	})
	for _, marker := range []string{
		"Guardrail Signals", "實驗性", "RS Rank", "NewHighScore",
		"VCP Valid", "18→10→5", "Flow", "MOMENTUM_BUILDING", "+5",
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("show=true should render %q", marker)
		}
	}
}

// 3. show=true + shadow nil → no panic, explicit "not computed" message.
func TestC7ShowWithoutShadow(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{Show: true})
	if !strings.Contains(html, "Guardrail Signals") {
		t.Error("section header should still render")
	}
	if !strings.Contains(html, "未啟用訊號計算") {
		t.Error("nil shadow should show the not-computed message")
	}
}

// 4. scoring disabled → shadow-only wording.
func TestC7ScoringDisabledWording(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{
		Show: true, GuardrailScoringEnabled: false, RSWatchThreshold: 70,
	})
	if !strings.Contains(html, "scoring 未啟用") {
		t.Error("scoring-disabled wording missing")
	}
	if strings.Contains(html, "scoring 已啟用") {
		t.Error("must not claim scoring enabled when it is off")
	}
}

// 5. scoring enabled → "符合觸發條件"-style wording, never "已實際套用".
func TestC7ScoringEnabledWording(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{
		Show: true, GuardrailScoringEnabled: true, RSWatchThreshold: 70,
	})
	if !strings.Contains(html, "scoring 已啟用") {
		t.Error("scoring-enabled wording missing")
	}
	// RS rank 60 < 70 → meets gate condition (wording is conditional, not "applied").
	if !strings.Contains(html, "符合 RS gate 觸發條件") {
		t.Error("expected RS gate condition wording for rank below threshold")
	}
	if strings.Contains(html, "已實際套用") {
		t.Error("must not claim definitive application (C7 does not re-derive from rocket.go)")
	}
}

// 5b. show=false leaves no whitespace artifact: section ⑥ flows straight into the
// wl-grid close with no blank line where the gated ⑦ block sits (trim markers).
func TestC7HiddenNoWhitespaceArtifact(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: false})
	if strings.Contains(html, "wl-guardrail") {
		t.Error("show=false must not emit the guardrail block")
	}
	// clean junction: ⑥ wl-note close → ⑥ wl-sec close → wl-grid close, no blank line.
	clean := "<div class=\"wl-note\"></div>\n          </div>\n        </div>"
	if !strings.Contains(html, clean) {
		t.Error("show=false should keep a clean ⑥→grid junction (no inserted blank line)")
	}
}

// 5c. With show=false, shadow presence must make ZERO difference (byte-identical).
func TestC7HiddenShadowIndependent(t *testing.T) {
	withShadow := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: false})
	noShadow := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{Show: false})
	if withShadow != noShadow {
		t.Error("show=false output must be byte-identical regardless of shadow presence")
	}
}

// 6. format helpers.
func TestC7Helpers(t *testing.T) {
	if got := vcpDepths([]float64{18, 10, 5}); got != "18→10→5" {
		t.Errorf("vcpDepths = %q want 18→10→5", got)
	}
	if got := vcpDepths(nil); got != "—" {
		t.Errorf("vcpDepths(nil) = %q want —", got)
	}
	gv := GuardrailViewOptions{MFScoreModifierBuilding: 5, MFScoreModifierShiftDown: -12}
	if got := mfModifier(scanner.MomentumBuilding, gv); got != "+5" {
		t.Errorf("mfModifier(BUILDING) = %q want +5", got)
	}
	if got := mfModifier(scanner.StructuralShiftDown, gv); got != "-12" {
		t.Errorf("mfModifier(SHIFT_DOWN) = %q want -12", got)
	}
	if got := mfModifier(scanner.MomentumNeutral, gv); got != "0" {
		t.Errorf("mfModifier(NEUTRAL) = %q want 0", got)
	}
}

// 7. Generate is read-only on the entries (display never mutates scoring).
func TestC7GenerateReadOnly(t *testing.T) {
	entries := []scanner.WatchlistEntry{sampleEntry(true)}
	before := entries[0].RocketScore
	beforeAct := entries[0].WatchAction
	beforeProb := entries[0].ExplosionProb
	_ = genHTML(t, entries, GuardrailViewOptions{Show: true, GuardrailScoringEnabled: true, RSWatchThreshold: 70})
	if entries[0].RocketScore != before || entries[0].WatchAction != beforeAct || entries[0].ExplosionProb != beforeProb {
		t.Error("Generate must not mutate entry scoring fields")
	}
}
