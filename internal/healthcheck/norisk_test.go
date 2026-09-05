package healthcheck

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The no-risk sentinel must not leak out of the presentation layer.
//
// internal/scanner writes "—" when a stock has NO risk label. Every layer that tested
// `!= ""` treated that as a risk, and the same wrong test was made independently in three
// places: the assessment verdict, the persistent evidence table, and the model's prompt.
// These tests hold each boundary separately, because one shared helper is only a guarantee
// while every caller actually uses it.

// ── the contract with internal/scanner ────────────────────────────────────────────────

// RiskNone must be what the scanner ACTUALLY emits for an unremarkable stock.
//
// The producer (rocketRisk) is unexported, so this pins the value through the public path
// the dashboard really uses: build a calm series, run the scanner's own enrichment, and read
// the label off the entry. If rocket.go ever changes its default marker this fails here
// rather than silently downgrading 跌破支撐 to a caution.
func TestRiskNoneMatchesWhatTheScannerEmits(t *testing.T) {
	end := day("2026-09-04")
	// A gently rising series with steady volume: no breakout, no limit-up, no failure.
	bars := series(end, 120, 100)
	sc := scanner.New(scanner.Config{})
	entries := sc.EnrichWatchlist([]fetcher.StockData{{
		Symbol: "2330", Name: "台積電", Market: "TW", Candles: bars,
	}}, nil, nil, nil, nil)
	if len(entries) == 0 {
		t.Fatal("the scanner produced no entry; the fixture is wrong, not the contract")
	}
	got := entries[0].RiskLabel
	if got == "" {
		t.Skip("this series produced an empty label; the sentinel cannot be pinned from it")
	}
	if got != RiskNone {
		t.Fatalf("the scanner's no-risk marker is %q, but RiskNone is %q.\n"+
			"Update RiskNone — hasRisk() is now wrong, and every unflagged stock is being "+
			"treated as carrying a risk.", got, RiskNone)
	}
	if hasRisk(got) {
		t.Fatalf("hasRisk(%q) = true for a stock the scanner did not flag", got)
	}
}

// The two limit-status constants come from the scanner, so a rename is a compile error.
func TestLimitStatusConstantsComeFromTheScanner(t *testing.T) {
	if !hazardLimitStatuses[scanner.LimitUpFailed] ||
		!hazardLimitStatuses[scanner.LimitDistribution] {
		t.Fatal("the hazard set does not match the scanner's own distribution statuses")
	}
	if hazardLimitStatuses[scanner.LimitLockedLowVol] {
		t.Fatal("scanner.LimitLockedLowVol is 中性偏多 and must not be a hazard")
	}
}

// ── persistence ───────────────────────────────────────────────────────────────────────

// An unflagged stock must persist NO risk_label row at all.
//
// A row holding "—" would put a full-width dash in the risk category of every unflagged
// stock, contradicting the file's own contract that an absent key is absent — and M10
// aggregates that table.
func TestNoRiskStockPersistsNoRiskLabelRow(t *testing.T) {
	ctx := context.Background()
	h := openTestHistory(t)

	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	hc := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	// The production shape: the scanner's no-risk marker, not an empty string.
	hc.Technical.RiskLabel = RiskNone

	id, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err := h.store.Evidence().ByCategory(ctx, id, "risk")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Key == "risk_label" {
			v := ""
			if r.TextValue != nil {
				v = *r.TextValue
			}
			t.Fatalf("a risk_label row was persisted for an unflagged stock: %q", v)
		}
	}

	// And a genuinely flagged stock DOES persist one, so the guard is not simply dropping
	// the field.
	hc.Technical.RiskLabel = "跌破支撐"
	id2, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatalf("save flagged: %v", err)
	}
	rows2, err := h.store.Evidence().ByCategory(ctx, id2, "risk")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows2 {
		if r.Key == "risk_label" {
			found = true
			if r.TextValue == nil || *r.TextValue != "跌破支撐" {
				t.Fatalf("risk_label = %v, want 跌破支撐", r.TextValue)
			}
		}
	}
	if !found {
		t.Fatal("a real risk label was not persisted")
	}
}

// ── the prompt ────────────────────────────────────────────────────────────────────────

// The sentinel must never reach the model. It reads as a risk, and the model has just been
// told to explain a verdict — whatever it invents about it is shown to the user.
func TestNoRiskLabelNeverReachesThePrompt(t *testing.T) {
	h := sampleHealth()
	h.Technical.RiskLabel = RiskNone
	h.Technical.RiskWarning = "依計畫設好停損即可"

	p := buildJudgePrompt(h)
	if strings.Contains(p, "風險標籤："+RiskNone) {
		t.Fatalf("the no-risk sentinel reached the prompt:\n%s", p)
	}
	if strings.Contains(p, "風險標籤") {
		t.Fatalf("a risk-label line was emitted for an unflagged stock:\n%s", p)
	}
	// The warning text must not arrive on its own either.
	if strings.Contains(p, "依計畫設好停損即可") {
		t.Fatalf("the no-risk warning text reached the prompt without its label")
	}

	// A real label DOES reach it, with no trailing whitespace artefact.
	h.Technical.RiskLabel = "跌破支撐"
	h.Technical.RiskWarning = "已跌破整理平台下緣"
	p = buildJudgePrompt(h)
	if !strings.Contains(p, "風險標籤：跌破支撐 已跌破整理平台下緣") {
		t.Fatalf("a real risk label did not reach the prompt:\n%s", p)
	}
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(line, "風險標籤") && strings.HasSuffix(line, " ") {
			t.Fatalf("the risk line has a trailing space: %q", line)
		}
	}
}

// ── the sentinel does not reach a verdict either ──────────────────────────────────────

// Held here as well as in assess_test.go, because this is the boundary the three leaks had
// in common and a future fourth caller should fail one of these.
func TestNoRiskSentinelDoesNotDecideAVerdict(t *testing.T) {
	got := assess(hb{risk: RiskNone, margin: fp(40), action: "BUY"}.build())
	if got.Verdict == AssessHighRisk {
		t.Fatalf("the no-risk sentinel produced HIGH_RISK: %+v", got)
	}
	if got.DecidedBy == "HAZARD" {
		t.Fatalf("HAZARD fired on an unflagged stock")
	}
}

// ── the API boundary ──────────────────────────────────────────────────────────────────

// The sentinel must not leave this package at all. This is the source fix: four consumers
// had each made the same wrong test, and one of them put 「風險：—」 in front of a user.
func TestSentinelNeverReachesTheAPI(t *testing.T) {
	end := day("2026-09-04")
	s, _ := newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 120, 100)},
	}}, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.Technical.RiskLabel == RiskNone {
		t.Fatalf("the no-risk sentinel reached the API as risk_label")
	}
	if h.Technical.RiskLabel != "" {
		t.Logf("this stock genuinely carries %q; the assertion below still applies",
			h.Technical.RiskLabel)
	}

	// And it is absent from the serialised form, so a JS truthiness test is correct.
	blob, err := json.Marshal(h.Technical)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), `"risk_label":"`+RiskNone+`"`) {
		t.Fatalf("the sentinel is in the JSON: %s", blob)
	}

	// A real label still travels.
	direct := technicalBlock(&Evidence{Entry: &scanner.WatchlistEntry{
		RiskLabel: "跌破支撐", RiskWarning: "已跌破整理平台下緣",
	}})
	if direct.RiskLabel != "跌破支撐" || direct.RiskWarning == "" {
		t.Fatalf("a genuine risk label was dropped: %+v", direct)
	}

	// The sentinel is dropped along with its warning text, which would otherwise appear
	// with nothing to attach it to.
	sentinel := technicalBlock(&Evidence{Entry: &scanner.WatchlistEntry{
		RiskLabel: RiskNone, RiskWarning: "依計畫設好停損即可",
	}})
	if sentinel.RiskLabel != "" || sentinel.RiskWarning != "" {
		t.Fatalf("the sentinel or its warning survived: %+v", sentinel)
	}
}
