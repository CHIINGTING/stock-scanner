package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func aiEntry(a *ai.Analysis) scanner.WatchlistEntry {
	e := sampleEntry(false)
	e.AI = a
	return e
}

func okAnalysis() *ai.Analysis {
	return &ai.Analysis{
		Symbol: "1111", Status: ai.StatusOK,
		Summary:    "沿 20 日均線整理，量能溫和放大。",
		BullCase:   []string{"站上 MA20"},
		BearCase:   []string{"族群資金外流"},
		RiskFlags:  []string{"整理天數偏短"},
		Confidence: 0.72, Model: "gpt-4o-mini",
	}
}

func TestAISectionRendersWhenShown(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{aiEntry(okAnalysis())},
		GuardrailViewOptions{ShowAI: true})

	for _, want := range []string{
		"⑭ AI 分析", "沿 20 日均線整理", "站上 MA20", "族群資金外流", "整理天數偏短",
		"gpt-4o-mini", "72%",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered report is missing %q", want)
		}
	}
}

// The shadow-only framing must be visible in the output itself, not only in the code — a
// reader looking at the report has to be able to tell this section decides nothing.
func TestAISectionCarriesShadowOnlyLabel(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{aiEntry(okAnalysis())},
		GuardrailViewOptions{ShowAI: true})

	if !strings.Contains(html, "Shadow Only") {
		t.Error("the ⑭ heading must say Shadow Only")
	}
	if !strings.Contains(html, "不改變任何分數") {
		t.Error("the section must state that it changes no score")
	}
	// Confidence must be labelled as the model's confidence in its own reading, so it is not
	// mistaken for a probability that the trade works.
	if !strings.Contains(html, "不是</b>交易信心") {
		t.Error("confidence must be disclaimed as not a trading confidence")
	}
}

// The ⑭ styling must be gated on ShowAI and nothing else. It was originally emitted inside
// the backtest-tab conditional, which meant the section rendered unstyled unless an unrelated
// flag happened to be on.
func TestAIStylesFollowShowAIOnly(t *testing.T) {
	entries := []scanner.WatchlistEntry{aiEntry(okAnalysis())}

	on := genHTML(t, entries, GuardrailViewOptions{ShowAI: true})
	if !strings.Contains(on, ".wl-ai .ai-summary") {
		t.Error("ShowAI=true must emit the ⑭ styles")
	}
	// ...even with every other display flag off.
	if !strings.Contains(on, ".wl-ai .ai-side.ai-bull") {
		t.Error("bull/bear colour bands missing")
	}

	off := genHTML(t, entries, GuardrailViewOptions{ShowAI: false, ShowBacktestInsights: true})
	if strings.Contains(off, ".wl-ai") {
		t.Error("ShowAI=false must emit no ⑭ styles, regardless of other flags")
	}
}

// show_ai=false is the default and must produce output with no trace of the feature.
func TestAIHiddenWhenShowFalse(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{aiEntry(okAnalysis())},
		GuardrailViewOptions{ShowAI: false})

	for _, absent := range []string{"⑭ AI 分析", "沿 20 日均線整理", "gpt-4o-mini"} {
		if strings.Contains(html, absent) {
			t.Errorf("AI content leaked with ShowAI=false: %q", absent)
		}
	}
}

// Every unavailable path renders the report intact and simply omits the section. This is the
// fail-open contract as the user actually experiences it.
func TestAIUnavailableDoesNotBreakReport(t *testing.T) {
	baseline := genHTML(t, []scanner.WatchlistEntry{aiEntry(nil)}, GuardrailViewOptions{ShowAI: true})

	cases := map[string]*ai.Analysis{
		"nil":        nil,
		"disabled":   {Symbol: "1111", Status: ai.StatusDisabled, Reason: "AI 分析未啟用"},
		"no key":     {Symbol: "1111", Status: ai.StatusNoKey, Reason: "未設定 OPENAI_API_KEY"},
		"skipped":    {Symbol: "1111", Status: ai.StatusSkipped, Reason: "超出本次分析檔數上限"},
		"error":      {Symbol: "1111", Status: ai.StatusError, Reason: "HTTP 429"},
		"bad output": {Symbol: "1111", Status: ai.StatusBadOutput, Reason: "模型回應不是合法 JSON"},
		"ok but no bullets": {Symbol: "1111", Status: ai.StatusOK, Summary: "只有一段摘要。",
			Confidence: 0.4, Model: "gpt-4o-mini"},
	}
	for name, a := range cases {
		html := genHTML(t, []scanner.WatchlistEntry{aiEntry(a)}, GuardrailViewOptions{ShowAI: true})

		// The rest of the page is always there.
		if !strings.Contains(html, "1111") || !strings.Contains(html, "</html>") {
			t.Errorf("%s: report is truncated or malformed", name)
		}
		if strings.Count(html, "<div class=\"wl-sec") == 0 {
			t.Errorf("%s: watchlist sections vanished", name)
		}
		if name == "ok but no bullets" {
			// A summary with no bullets still renders — an empty side is legitimate output.
			if !strings.Contains(html, "只有一段摘要") {
				t.Errorf("%s: a bullet-free reading should still render", name)
			}
			continue
		}
		if strings.Contains(html, "⑭ AI 分析") {
			t.Errorf("%s: an unavailable analysis must not render the section", name)
		}
		// A non-OK status must not surface its internal reason string in the report either.
		if a != nil && a.Reason != "" && strings.Contains(html, a.Reason) {
			t.Errorf("%s: internal reason %q leaked into the report", name, a.Reason)
		}
		if len(html) < len(baseline)/2 {
			t.Errorf("%s: report collapsed (%d bytes vs %d baseline)", name, len(html), len(baseline))
		}
	}
}

// The default report — the one produced when nobody has opted in — must be byte-identical to
// what it was before this feature existed.
func TestAIDefaultOutputUnchanged(t *testing.T) {
	without := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{})
	with := genHTML(t, []scanner.WatchlistEntry{aiEntry(okAnalysis())}, GuardrailViewOptions{})

	if without != with {
		t.Error("attaching an AI analysis changed the default report; it must be inert until show_ai is on")
	}
}
