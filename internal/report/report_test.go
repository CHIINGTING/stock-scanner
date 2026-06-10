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
			MultiTimeframe: &scanner.MultiTimeframe{
				SignalStrength: "STRONG", AlignmentLabel: "FULL_BULL", LongTermFilter: "BULLISH",
				Daily:  scanner.TimeframeView{Valid: true, TrendState: "UPTREND"},
				Weekly: scanner.TimeframeView{Valid: true, TrendState: "UPTREND"},
			},
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

// ── R4-4: Multi-Timeframe display in Guardrail Signals ──────────────────────

// MTF section renders (at the end of the guardrail block) when show=true.
func TestR44MTFShown(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	for _, m := range []string{"Multi-Timeframe", "SignalStrength STRONG", "FULL_BULL", "200日線上"} {
		if !strings.Contains(html, m) {
			t.Errorf("show=true should render MTF %q", m)
		}
	}
}

// show=false → no MTF content (whole guardrail block trimmed).
func TestR44MTFHiddenWhenShowFalse(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: false})
	if strings.Contains(html, "Multi-Timeframe") {
		t.Error("show=false must not render Multi-Timeframe")
	}
}

// MTFRiskNote: shown only when non-empty (no empty 多週期提示 line otherwise).
func TestR44RiskNoteEmptyVsSet(t *testing.T) {
	empty := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	if strings.Contains(empty, "多週期提示") {
		t.Error("empty MTFRiskNote must not render the hint line")
	}
	e := sampleEntry(true)
	e.MTFRiskNote = "短線反彈、週線仍弱，追高需謹慎"
	set := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	if !strings.Contains(set, "多週期提示：短線反彈、週線仍弱，追高需謹慎") {
		t.Error("non-empty MTFRiskNote should render the hint line")
	}
}

// partial weekly → 「本週未完成」label.
func TestR44PartialWeekly(t *testing.T) {
	e := sampleEntry(true)
	e.Shadow.MultiTimeframe.Weekly.Partial = true
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	if !strings.Contains(html, "本週未完成") {
		t.Error("partial weekly should render 本週未完成")
	}
}

// No MultiTimeframe shadow → no MTF line, no error (section still renders).
func TestR44NoMTFNoError(t *testing.T) {
	e := sampleEntry(true)
	e.Shadow.MultiTimeframe = nil
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	if strings.Contains(html, "Multi-Timeframe") {
		t.Error("nil MultiTimeframe must not render the MTF line")
	}
	if !strings.Contains(html, "Guardrail Signals") {
		t.Error("guardrail section should still render")
	}
}

func TestR44LTFLabel(t *testing.T) {
	cases := map[string]string{"BULLISH": "200日線上", "BEARISH": "200日線下", "UNKNOWN": "未知", "": "未知"}
	for in, want := range cases {
		if got := mtfLTFLabel(in); got != want {
			t.Errorf("mtfLTFLabel(%q)=%q want %q", in, got, want)
		}
	}
}

// ── Guardrail Summary (human-readable, display-only) ────────────────────────

// Summary renders above the raw data when show=true.
func TestGuardrailSummaryShown(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: true, RSWatchThreshold: 70})
	for _, m := range []string{"Guardrail Summary：", "優勢：", "操作解讀：", "以下為原始數據", "空手：", "持有：", "加碼："} {
		if !strings.Contains(html, m) {
			t.Errorf("show=true should render summary marker %q", m)
		}
	}
	// raw data must still be present below the summary.
	if !strings.Contains(html, "18→10→5") {
		t.Error("raw VCP depths must still render under the summary")
	}
}

// show=false → no summary (whole guardrail block trimmed).
func TestGuardrailSummaryHiddenWhenShowFalse(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{Show: false})
	if strings.Contains(html, "Guardrail Summary") {
		t.Error("show=false must not render the summary")
	}
}

// FADING momentum → risk line + 不追高 headline + 轉回 BUILDING action.
func TestGuardrailSummaryFading(t *testing.T) {
	e := sampleEntry(true)
	e.Shadow.Momentum = &scanner.MomentumState{Computed: true, Flow: scanner.MomentumFading, Score: 40}
	v := guardrailSummary(e, 70)
	if !containsStr(v.Risks, "短線動能轉弱（FADING）") {
		t.Errorf("FADING should add risk, got %v", v.Risks)
	}
	if !strings.Contains(v.Headline, "暫不追高") {
		t.Errorf("strong base + fading headline should advise 暫不追高, got %q", v.Headline)
	}
	if !containsStr(v.Actions, "加碼：等 MomentumFlow 由 FADING 轉回 BUILDING / CONTINUATION") {
		t.Errorf("fading action missing, got %v", v.Actions)
	}
}

// CONFLICT → divergence-of-timeframes headline + conflict action.
func TestGuardrailSummaryConflict(t *testing.T) {
	e := sampleEntry(true)
	e.Shadow.MultiTimeframe = &scanner.MultiTimeframe{SignalStrength: "CONFLICTED", AlignmentLabel: "CONFLICT",
		LongTermFilter: "BULLISH", Daily: scanner.TimeframeView{TrendState: "UPTREND"}, Weekly: scanner.TimeframeView{TrendState: "DOWNTREND"}}
	v := guardrailSummary(e, 70)
	if !strings.Contains(v.Headline, "方向分歧") {
		t.Errorf("conflict headline expected, got %q", v.Headline)
	}
	if !containsStr(v.Risks, "日週多空不一致") {
		t.Errorf("conflict risk expected, got %v", v.Risks)
	}
}

// Nil shadow → neutral headline, no panic.
func TestGuardrailSummaryNilShadow(t *testing.T) {
	v := guardrailSummary(sampleEntry(false), 70)
	if v.Headline == "" {
		t.Error("nil shadow should still produce a headline")
	}
	if len(v.Pros) != 0 || len(v.Risks) != 0 {
		t.Error("nil shadow should have no pros/risks")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// 回測洞察分頁：ShowBacktestInsights=false（預設）→ report 完全沒有此分頁。
func TestBacktestInsightsHiddenByDefault(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{ShowBacktestInsights: false})
	for _, marker := range []string{"回測洞察", "tab-backtest", "崩盤情境警告", "Setup D 倖存者"} {
		if strings.Contains(html, marker) {
			t.Errorf("ShowBacktestInsights=false must not render %q", marker)
		}
	}
}

// 回測洞察分頁：ShowBacktestInsights=true → 顯示分頁 + 紅字崩盤警告 + 純顯示框定。
func TestBacktestInsightsShownWhenEnabled(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(true)}, GuardrailViewOptions{ShowBacktestInsights: true})
	for _, marker := range []string{
		"回測洞察",            // 分頁標題
		"tab-backtest",     // 分頁容器
		"崩盤情境警告",         // 紅字警告標題
		"不可外推到空頭／崩盤",    // 多頭結論不適用崩盤
		"勿", "不要停損", // 不可當停損依據
		"Setup D 倖存者", "LOW confidence", // 唯一崩盤相關項標低信心
		"不改變任何停損、排名或下單", // 純顯示框定
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("ShowBacktestInsights=true must render %q", marker)
		}
	}
}
