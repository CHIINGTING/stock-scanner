package report

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// ── fixtures ─────────────────────────────────────────────────────────────────────────

const epSynthVersion = "EP-SYNTH-v9"

func epF(v float64) *float64 { return &v }

// epSynthPlan is a fully published plan with distinctive numbers. RuleVersion is synthetic so a
// renderer that printed entryplan.RuleVersion instead of Plan.RuleVersion would be visible.
func epSynthPlan(status entryplan.EntryStatus, current float64) *entryplan.Plan {
	return &entryplan.Plan{
		Symbol: "1111", AsOf: "2026-06-05", Status: status, RuleVersion: epSynthVersion,
		IdealEntry: &entryplan.PriceZone{Low: 94.70, High: 97.30, PriceBasis: entryplan.PriceBasisRaw,
			Basis: []string{"MA20"}},
		MaxChasePrice: epF(99.80),
		Invalidation:  &entryplan.Invalidation{Price: 92.00, PriceBasis: entryplan.PriceBasisRaw},
		Target1:       &entryplan.PriceLevel{Price: 104.00, PriceBasis: entryplan.PriceBasisRaw, RMultiple: epF(1.2641)},
		Target2:       &entryplan.PriceLevel{Price: 115.50, PriceBasis: entryplan.PriceBasisRaw, RMultiple: epF(3.4339)},
		// Deliberately inconsistent: Reward/Risk = 1.51, Ratio = 1.2641. The section must print the
		// published Ratio, so a renderer that recomputed it would print 1.51.
		RiskReward: &entryplan.RiskReward{RiskPerShare: 5.3, RewardPerShare: 8.0, Ratio: 1.2641, Target: "TARGET_1"},
		EntryTrace: &entryplan.EntryTrace{RuleVersion: epSynthVersion, Policy: entryplan.PolicyTrace{Name: entryplan.PolicyNormal}},
		Confidence: entryplan.ConfidenceMedium,
		Reasons:    []entryplan.Reason{entryplan.ReasonStatusAboveMaxChase},
		Evidence: []entryplan.Evidence{
			{Key: entryplan.EvidencePriceBasis, Status: entryplan.Available, Text: "RAW"},
			{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Available, Value: epF(current), PriceBasis: entryplan.PriceBasisRaw},
			// A buy-side entry semantic: the section renders only for these (epBuySideSemantic).
			{Key: entryplan.EvidenceEntrySemantic, Status: entryplan.Available,
				Text: string(entryplan.EntrySemanticPullback)},
		},
		Caveats: []string{"合成測試注意事項"},
	}
}

// epLegacyEntry carries distinctive LEGACY price fields, so a ⑲ that printed them is visible.
func epLegacyEntry(p *entryplan.Plan) scanner.WatchlistEntry {
	e := sampleEntry(false)
	e.EntryZone = "88.81 ~ 89.91"
	e.StopLossPrice = 87.65
	e.TakeProfitZone = "121.11 ~ 131.31"
	e.BreakoutPrice = 111.11
	e.SupportPrice = 86.66
	e.A.EntryPrice = 88.44
	e.EntryPlan = p
	return e
}

var epSectionRe = regexp.MustCompile(`(?s)<section class="wl-sec wl-ep">.*?</section>`)
var epStylesRe = regexp.MustCompile(`(?s)<style id="ep19-styles">.*?</style>`)

func epSections(html string) []string { return epSectionRe.FindAllString(html, -1) }

// epOneSection renders one entry with ShowEntryPlan on and returns its single ⑲ block.
func epOneSection(t *testing.T, e scanner.WatchlistEntry) string {
	t.Helper()
	secs := epSections(genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{ShowEntryPlan: true}))
	if len(secs) != 1 {
		t.Fatalf("want exactly one ⑲ section, got %d", len(secs))
	}
	return secs[0]
}

func epRow(label, value string) string {
	return "<div><span>" + label + "</span> " + value + "</div>"
}

func epAssertContains(t *testing.T, sec string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(sec, w) {
			t.Errorf("⑲ is missing %q\nsection:\n%s", w, sec)
		}
	}
}

func epAssertAbsent(t *testing.T, sec string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(sec, b) {
			t.Errorf("⑲ must not contain %q\nsection:\n%s", b, sec)
		}
	}
}

// epConstNames parses one internal/entryplan source file and returns the names and values of
// every constant declared with the given type, so a new constant breaks the tests that use it.
func epConstNames(t *testing.T, file, typeName string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "entryplan", file), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != typeName {
				continue
			}
			for i, n := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s: constant %s is not a string literal", file, n.Name)
				}
				out[n.Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}
	return out
}

// ── bridge fixtures (the real scanner.AttachEntryPlan) ──────────────────────────────────

var epBridgeDate = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func epBridgeSeries() []fetcher.Candle {
	out := make([]fetcher.Candle, 90)
	for i := range out {
		c := 80 + 0.25*float64(i)
		out[i] = fetcher.Candle{Date: epBridgeDate.AddDate(0, 0, i-89), Open: c, High: c + 1, Low: c - 1,
			Close: c, AdjClose: c, Volume: 1_000_000}
	}
	return out
}

// epBridgeEntry is a PULLBACK_BUY entry in a BULL market whose plan the bridge computes. With a
// valuation BASE target of 105.4321 the published Target2 is capped to 105.00; the trace's raw
// structural Target2 is 115.85 (published as 115.50 when no valuation is supplied).
func epBridgeEntry(action scanner.Action, baseTarget *float64) scanner.WatchlistEntry {
	return epBridgeEntryWith(action, scanner.ActPullbackBuy, baseTarget)
}

// epBridgeEntryWith is epBridgeEntry with the WatchAction (and so the projected entry semantic)
// chosen by the caller.
func epBridgeEntryWith(action scanner.Action, watch scanner.WatchAction, baseTarget *float64) scanner.WatchlistEntry {
	series := epBridgeSeries()
	e := scanner.WatchlistEntry{
		A: scanner.StockAnalysis{Symbol: "2330", Name: "測試", Date: epBridgeDate, Close: series[89].Close,
			ATR: 2.5, MA20: 96, AvgVolume20: 12_000, Action: action},
		Consol:      scanner.Consolidation{Bucket: scanner.SwingBase, Days: 14, BaseLow: 92, PivotHigh: 104},
		WatchAction: watch,
		EntryZone:   "88.81 ~ 89.91",
	}
	var vals map[string]scanner.EntryPlanValuation
	if baseTarget != nil {
		v := *baseTarget
		vals = map[string]scanner.EntryPlanValuation{"2330": {Status: string(valuation.Available),
			Suitability: string(valuation.SuitabilitySuitable), BaseTarget: &v, AsOf: "2026-09-10"}}
	}
	es := []scanner.WatchlistEntry{e}
	scanner.AttachEntryPlan(es, map[string][]fetcher.Candle{"2330": series},
		scanner.EntryPlanMarket{Available: true, Regime: "BULL"}, true, vals)
	return es[0]
}

// ── 1. show gate ─────────────────────────────────────────────────────────────────────

func TestEntryPlanSectionAbsentWhenShowFalse(t *testing.T) {
	e := epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50))
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{})
	for _, marker := range []string{"⑲", "進場計畫", "wl-ep", "ep19-styles", "ep-status", epSynthVersion} {
		if strings.Contains(html, marker) {
			t.Errorf("%q rendered with ShowEntryPlan off", marker)
		}
	}
	// Anti-vacuity: the same entry DOES render the section when the flag is on.
	if on := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{ShowEntryPlan: true}); len(epSections(on)) != 1 {
		t.Fatal("fixture: the section does not render even with the flag on")
	}
}

// ── 2. show=true renders the published plan ─────────────────────────────────────────

func TestEntryPlanSectionRendersPublishedPlan(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusTooExtended, 101.25)))
	epAssertContains(t, sec,
		"<h4>⑲ 進場計畫（Shadow Only，不改變 BUY／WATCH／SELL）</h4>",
		"狀態 <b>TOO_EXTENDED</b>　已超過追價上限，不追",
		epRow("理想進場區", `94.70 – 97.30 <span class="ep-code">MA20・RAW</span>`),
		epRow("計畫所見現價", "101.25"),
		epRow("現價位置", `高於追價上限 <span class="ep-code">ABOVE_CHASE_CEILING</span>`),
		epRow("追價上限", "99.80"),
		epRow("想法失效價", "92.00"),
		epRow("目標一", "104.00（1.26R）"),
		epRow("目標二", "115.50（3.43R）"),
		epRow("風險報酬比", "1.26（量至 TARGET_1）"),
		epRow("Policy", "NORMAL"),
		epRow("RuleVersion", epSynthVersion),
		epRow("Confidence", "MEDIUM（證據部分佐證）"),
		"<li>現價高於追價上限（STATUS_ABOVE_MAX_CHASE）</li>",
		"<li>合成測試注意事項</li>",
	)
	epAssertAbsent(t, sec, "1.51")
}

// ── 3. missing ≠ zero ────────────────────────────────────────────────────────────────

func TestEntryPlanSectionMissingValuesRenderDashNeverZero(t *testing.T) {
	p := &entryplan.Plan{Symbol: "1111", AsOf: "2026-06-05", Status: entryplan.StatusInsufficientData,
		RuleVersion: epSynthVersion, Confidence: entryplan.ConfidenceInsufficientData,
		Evidence: []entryplan.Evidence{{Key: entryplan.EvidenceEntrySemantic,
			Status: entryplan.Available, Text: string(entryplan.EntrySemanticBreakout)}}}
	sec := epOneSection(t, epLegacyEntry(p))
	epAssertContains(t, sec,
		epRow("理想進場區", "—"),
		epRow("計畫所見現價", "—"),
		epRow("現價位置", `無法判斷（計畫未發布可用的現價或進場區） <span class="ep-code">UNAVAILABLE</span>`),
		epRow("追價上限", "—"),
		epRow("想法失效價", "—"),
		epRow("目標一", "—"),
		epRow("目標二", "—"),
		epRow("風險報酬比", "—"),
		epRow("Policy", "—"),
	)
	epAssertAbsent(t, sec, "0.00", "$0", "NT$0", "> 0<", " 0</div>", "（0", "0R")

	// A pointer to a non-price (0, negative, NaN) is not a published price either.
	p2 := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p2.MaxChasePrice = epF(0)
	p2.Invalidation.Price = -1
	p2.Target1.Price = math.NaN()
	sec2 := epOneSection(t, epLegacyEntry(p2))
	epAssertContains(t, sec2, epRow("追價上限", "—"), epRow("想法失效價", "—"), epRow("目標一", "—"))
	epAssertAbsent(t, sec2, "0.00", "-1.00", "NaN")
}

// ── 4–9. every canonical status, enumerated from internal/entryplan/types.go ────────────

// epExpectedStatus is this test's own copy of the expected presentation, keyed by the constant
// NAME in types.go. A status added to entryplan without a row here fails the test.
var epExpectedStatus = map[string]struct{ label, css string }{
	"StatusBuyNow":           {"現價符合計畫進場條件", "ep-s-now"},
	"StatusWaitPullback":     {"等待拉回至進場區", "ep-s-wait"},
	"StatusWaitBreakout":     {"等待價格突破進場區", "ep-s-wait"},
	"StatusTooExtended":      {"已超過追價上限，不追", "ep-s-no"},
	"StatusNoValidEntry":     {"無有效進場價位", "ep-s-no"},
	"StatusInsufficientData": {"資料不足，無法判定", "ep-s-data"},
}

func TestEntryPlanSectionRendersEveryCanonicalStatus(t *testing.T) {
	declared := epConstNames(t, "types.go", "EntryStatus")
	if len(declared) < 6 {
		t.Fatalf("parsed %d EntryStatus constants from types.go, want at least 6", len(declared))
	}
	seen := 0
	for name, code := range declared {
		want, ok := epExpectedStatus[name]
		if !ok {
			t.Errorf("entryplan declares %s = %q with no expected ⑲ presentation; add it to the "+
				"report's epStatusLabels and to this table", name, code)
			continue
		}
		t.Run(code, func(t *testing.T) {
			st := entryplan.EntryStatus(code)
			if !st.Valid() {
				t.Fatalf("%s is not Valid() in entryplan", code)
			}
			sec := epOneSection(t, epLegacyEntry(epSynthPlan(st, 98.50)))
			line := `<div class="ep-status ` + want.css + `">狀態 <b>` + code + `</b>　` + want.label + `</div>`
			if !strings.Contains(sec, line) {
				t.Errorf("status line missing, want %q\nsection:\n%s", line, sec)
			}
			// Never collapsed into the scanner's BUY / WATCH / SELL vocabulary.
			for _, bad := range []string{"買", "賣", "觀察", "WATCH", "SELL", "BUY", "HOLD"} {
				if strings.Contains(epStatusLabels[st], bad) {
					t.Errorf("%s label %q contains decision vocabulary %q", code, epStatusLabels[st], bad)
				}
			}
		})
		seen++
	}
	if seen != len(epExpectedStatus) {
		t.Errorf("rendered %d statuses, expected table has %d", seen, len(epExpectedStatus))
	}
}

func TestEntryPlanSectionUnknownStatusIsVisiblyUnknown(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epSynthPlan("BUY", 98.50)))
	epAssertContains(t, sec,
		`<div class="ep-status ep-s-unknown">狀態 <b>BUY</b>　未知狀態（非 entryplan 定義的狀態碼）</div>`)

	empty := epOneSection(t, epLegacyEntry(epSynthPlan("", 98.50)))
	epAssertContains(t, empty,
		`<div class="ep-status ep-s-unknown">狀態 <b>—</b>　未知狀態（非 entryplan 定義的狀態碼）</div>`)
}

// ── 10–11. current-price relationship ───────────────────────────────────────────────

func epRelationRow(rel epRelation) string {
	return epRow("現價位置", epRelationLabels[rel]+` <span class="ep-code">`+string(rel)+`</span>`)
}

func TestEntryPlanRelationZoneBoundaries(t *testing.T) {
	cases := []struct {
		name string
		cur  float64
		want epRelation
	}{
		{"current == Zone.Low is inside", 94.70, epRelInsideZone},
		{"current == Zone.High is inside", 97.30, epRelInsideZone},
		{"one tick below Zone.Low is below", 94.65, epRelBelowZone},
		{"between High and MaxChase", 98.00, epRelAboveZoneWithinChase},
	}
	ran := 0
	for _, c := range cases {
		sec := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, c.cur)))
		if !strings.Contains(sec, epRelationRow(c.want)) {
			t.Errorf("%s: want %s\nsection:\n%s", c.name, c.want, sec)
		}
		ran++
	}
	if ran != 4 {
		t.Fatalf("ran %d of 4 cases", ran)
	}
}

func TestEntryPlanRelationCurrentEqualsMaxChase(t *testing.T) {
	at := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 99.80)))
	if !strings.Contains(at, epRelationRow(epRelAboveZoneWithinChase)) {
		t.Errorf("current == MaxChase must not be above the chase ceiling\nsection:\n%s", at)
	}
	over := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusTooExtended, 99.85)))
	if !strings.Contains(over, epRelationRow(epRelAboveChaseCeiling)) {
		t.Errorf("current strictly above MaxChase must be above the chase ceiling\nsection:\n%s", over)
	}
	// Above the zone with no published ceiling gets its own label, not an invented ceiling.
	p := epSynthPlan(entryplan.StatusWaitPullback, 120.00)
	p.MaxChasePrice = nil
	none := epOneSection(t, epLegacyEntry(p))
	if !strings.Contains(none, epRelationRow(epRelAboveZoneNoCeiling)) {
		t.Errorf("above zone with no ceiling must say so\nsection:\n%s", none)
	}
}

// The label's boundaries agree with the domain's own decision at the same inputs. This calls
// entryplan.DecideStatus from a TEST file only; production report code may not (see the guard).
func TestEntryPlanRelationAgreesWithDecideStatusAtBoundaries(t *testing.T) {
	base := epSynthPlan(entryplan.StatusWaitBreakout, 0)
	policy := entryplan.PolicyFor(model.RegimeBull, entryplan.EntrySemanticBreakout)
	checked := 0
	for _, cur := range []float64{94.65, 94.70, 96.00, 97.30, 98.00, 99.80, 99.85} {
		c := cur
		d := entryplan.DecideStatus(entryplan.DecisionInput{
			Semantic: entryplan.EntrySemanticBreakout, Policy: policy, Thesis: entryplan.ThesisNotContradicted,
			CurrentPrice: &c, Zone: base.IdealEntry, MaxChasePrice: base.MaxChasePrice, Invalidation: base.Invalidation,
		})
		rel := epZoneRelation(base, c, true)
		inside := d.Rule == entryplan.RuleInsideZone || d.Rule == entryplan.RuleInsideZoneUndecided ||
			d.Rule == entryplan.RuleInsideZoneRefused
		if inside != (rel == epRelInsideZone) {
			t.Errorf("cur=%.2f: DecideStatus rule %s, report relation %s disagree on inside", c, d.Rule, rel)
		}
		if (d.Rule == entryplan.RuleAboveMaxChase) != (rel == epRelAboveChaseCeiling) {
			t.Errorf("cur=%.2f: DecideStatus rule %s, report relation %s disagree on the ceiling", c, d.Rule, rel)
		}
		checked++
	}
	if checked != 7 {
		t.Fatalf("checked %d of 7 prices", checked)
	}
}

// The current price is the plan's OWN CURRENT_PRICE evidence, never StockAnalysis.Close.
func TestEntryPlanCurrentPriceComesFromPlanEvidenceOnly(t *testing.T) {
	e := epLegacyEntry(epSynthPlan(entryplan.StatusTooExtended, 101.25))
	e.A.Close = 96.00 // inside the zone: a fallback to A.Close would print INSIDE_ZONE
	sec := epOneSection(t, e)
	epAssertContains(t, sec, epRow("計畫所見現價", "101.25"), epRelationRow(epRelAboveChaseCeiling))
	epAssertAbsent(t, sec, "96.00")

	p := epSynthPlan(entryplan.StatusInsufficientData, 101.25)
	p.Evidence[1] = entryplan.Evidence{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Unavailable}
	e2 := epLegacyEntry(p)
	e2.A.Close = 96.00
	sec2 := epOneSection(t, e2)
	epAssertContains(t, sec2, epRow("計畫所見現價", "—"), epRelationRow(epRelUnavailable))
	epAssertAbsent(t, sec2, "96.00", "INSIDE_ZONE")
}

// ── 12–13. Target2 ───────────────────────────────────────────────────────────────────

func TestEntryPlanSectionValuationCappedTarget2ThroughBridge(t *testing.T) {
	e := epBridgeEntry(scanner.ActionBuy, epF(105.4321))
	p := e.EntryPlan
	if p == nil || p.Target2 == nil || p.Target2.Price != 105 || p.EntryTrace == nil ||
		p.EntryTrace.Targets.Target2Raw == nil || *p.EntryTrace.Targets.Target2Raw != 115.85 {
		t.Fatalf("fixture: want capped Target2 105 with raw 115.85, got %+v", p)
	}
	sec := epOneSection(t, e)
	epAssertContains(t, sec, `<div><span>目標二</span> 105.00（`)
	epAssertAbsent(t, sec, "115.85", "115.50", "105.43", "105.4321")
}

func TestEntryPlanSectionWithdrawnTarget2RendersDash(t *testing.T) {
	p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p.Target2 = nil
	p.Reasons = append(p.Reasons, entryplan.ReasonValuationCeilingBelowEntry)
	p.EntryTrace.Targets = entryplan.TargetTrace{Target2Raw: epF(133.33), Target2Clamped: epF(131.11),
		ValuationTarget: epF(128.88), Target2: epF(127.77)}
	sec := epOneSection(t, epLegacyEntry(p))
	epAssertContains(t, sec, epRow("目標二", "—"))
	epAssertAbsent(t, sec, "133.33", "131.11", "128.88", "127.77", "133.3", "131.1", "128.9", "127.8")
}

// ── 14. sell-side ────────────────────────────────────────────────────────────────────

func TestEntryPlanSectionSellSideActionsThroughBridge(t *testing.T) {
	planted := 105.4321
	gv := GuardrailViewOptions{ShowEntryPlan: true, Valuation: map[string]*valuation.Valuation{
		"2330": {Status: valuation.Available, Symbol: "2330", TrailingPE: epF(23.4567)},
	}}

	// Anti-vacuity control: a BUY with the same valuation shows its capped Target2 in ⑲.
	control := epSections(genHTML(t, []scanner.WatchlistEntry{epBridgeEntry(scanner.ActionBuy, &planted)}, gv))
	if len(control) != 1 || !strings.Contains(control[0], `<div><span>目標二</span> 105.00（`) {
		t.Fatalf("control: BUY plan does not show its capped Target2 in ⑲: %v", control)
	}

	actions := []scanner.Action{scanner.ActionSell, scanner.ActionReduce, scanner.ActionTakeProfit, scanner.ActionStopLoss}
	ran := 0
	for _, a := range actions {
		e := epBridgeEntry(a, &planted)
		if e.EntryPlan == nil || e.EntryPlan.Status != entryplan.StatusNoValidEntry || e.EntryPlan.HasExecutablePrices() {
			t.Fatalf("%s: fixture is not a price-less NO_VALID_ENTRY plan: %+v", a, e.EntryPlan)
		}
		html := genHTML(t, []scanner.WatchlistEntry{e}, gv)
		secs := epSections(html)
		if len(secs) != 1 {
			t.Fatalf("%s: want one ⑲ section, got %d", a, len(secs))
		}
		sec := secs[0]
		epAssertContains(t, sec,
			"狀態 <b>NO_VALID_ENTRY</b>　無有效進場價位",
			epRow("理想進場區", "—"), epRow("追價上限", "—"), epRow("想法失效價", "—"),
			epRow("目標一", "—"), epRow("目標二", "—"), epRow("風險報酬比", "—"),
			"<li>掃描器主要動作與進場想法矛盾（STATUS_THESIS_CONTRADICTED）</li>",
		)
		epAssertAbsent(t, sec, "105.00", "105.43", "105.4321", "115.50", "115.85", "104.00", "94.70", "97.30", "23.46")
		// ⑰ may still show its research valuation, outside ⑲.
		if !strings.Contains(html, "⑰ 基本面／估值") || !strings.Contains(html, "23.46") {
			t.Errorf("%s: ⑰ valuation should still render outside ⑲", a)
		}
		ran++
	}
	if ran != 4 {
		t.Fatalf("ran %d of 4 sell-side Actions", ran)
	}
}

// A sell-side-shaped plan whose evidence and trace DO carry valuation and raw target numbers
// (the bridge withholds them; this plan is synthetic on purpose) still renders no price.
func TestEntryPlanSectionSellSideRendersNoRecoveredPrice(t *testing.T) {
	p := &entryplan.Plan{Symbol: "1111", AsOf: "2026-06-05", Status: entryplan.StatusNoValidEntry,
		RuleVersion: epSynthVersion, Confidence: entryplan.ConfidenceHigh,
		Reasons: []entryplan.Reason{entryplan.ReasonStatusThesisContradicted},
		Evidence: []entryplan.Evidence{
			{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Available, Value: epF(102.25)},
			{Key: entryplan.EvidenceEntrySemantic, Status: entryplan.Available,
				Text: string(entryplan.EntrySemanticPullback)},
			{Key: entryplan.EvidenceValuationBaseTarget, Status: entryplan.Available, Value: epF(187.6543)},
		},
		EntryTrace: &entryplan.EntryTrace{Targets: entryplan.TargetTrace{ValuationTarget: epF(187.6543),
			Target2Raw: epF(176.5432), Target1: epF(165.4321)}},
	}
	sec := epOneSection(t, epLegacyEntry(p))
	epAssertContains(t, sec, epRow("目標一", "—"), epRow("目標二", "—"), epRow("理想進場區", "—"))
	epAssertAbsent(t, sec, "187.65", "176.54", "165.43", "187.6543")
}

// ── 15–16. RuleVersion / Confidence ─────────────────────────────────────────────────

func TestEntryPlanSectionRuleVersionComesFromPlan(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50)))
	epAssertContains(t, sec, epRow("RuleVersion", epSynthVersion))
	if entryplan.RuleVersion == epSynthVersion {
		t.Fatal("fixture: synthetic version equals the production constant")
	}
	epAssertAbsent(t, sec, entryplan.RuleVersion)
}

var epExpectedConfidence = map[string]string{
	"ConfidenceHigh":             "證據完整",
	"ConfidenceMedium":           "證據部分佐證",
	"ConfidenceLow":              "僅最低必要證據",
	"ConfidenceInsufficientData": "必要證據不足",
}

func TestEntryPlanSectionConfidence(t *testing.T) {
	declared := epConstNames(t, "confidence.go", "Confidence")
	if len(declared) < 4 {
		t.Fatalf("parsed %d Confidence constants, want at least 4", len(declared))
	}
	seen := 0
	for name, code := range declared {
		label, ok := epExpectedConfidence[name]
		if !ok {
			t.Errorf("entryplan declares %s = %q with no expected ⑲ label", name, code)
			continue
		}
		p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
		p.Confidence = entryplan.Confidence(code)
		sec := epOneSection(t, epLegacyEntry(p))
		epAssertContains(t, sec, epRow("Confidence", code+"（"+label+"）"))
		seen++
	}
	if seen != len(epExpectedConfidence) {
		t.Errorf("checked %d confidence values, table has %d", seen, len(epExpectedConfidence))
	}
	p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p.Confidence = "VERY_HIGH"
	epAssertContains(t, epOneSection(t, epLegacyEntry(p)), epRow("Confidence", "VERY_HIGH（未知信心值）"))
}

// ── 17. reasons and caveats ─────────────────────────────────────────────────────────

func TestEntryPlanSectionReasonsAndCaveats(t *testing.T) {
	p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p.Reasons = []entryplan.Reason{
		entryplan.ReasonStatusPriceAboveEntryZone,
		"EP8_TEST_UNMAPPED_CODE",
		entryplan.ReasonRecentPriceAdjustment,
	}
	p.Caveats = []string{"第二則<b>注意</b>", "第一則"}
	sec := epOneSection(t, epLegacyEntry(p))
	epAssertContains(t, sec,
		`<ul class="wl-gs-list ep-reasons"><li>現價高於進場區（STATUS_PRICE_ABOVE_ENTRY_ZONE）</li>`+
			`<li>EP8_TEST_UNMAPPED_CODE</li><li>RECENT_PRICE_ADJUSTMENT</li></ul>`,
		`<ul class="wl-gs-list ep-caveats"><li>第二則&lt;b&gt;注意&lt;/b&gt;</li><li>第一則</li></ul>`,
	)
	if _, mapped := epReasonLabels[entryplan.ReasonRecentPriceAdjustment]; mapped {
		t.Fatal("fixture: RECENT_PRICE_ADJUSTMENT is expected to be unmapped")
	}
	// No dead mapping: every mapped code is a code entryplan can publish.
	for r := range epReasonLabels {
		if !entryplan.KnownReason(r) {
			t.Errorf("epReasonLabels maps %q, which entryplan does not register", r)
		}
	}
	// No reasons at all → "—", not an empty list that reads as "nothing to say".
	p.Reasons = nil
	epAssertContains(t, epOneSection(t, epLegacyEntry(p)), "理由：</div>\n            <div>—</div>")
}

// ── 18. shadow-only disclaimer and legacy distinction ─────────────────────────────────

func TestEntryPlanSectionShadowOnlyDisclaimer(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusBuyNow, 96.00)))
	epAssertContains(t, sec,
		"<h4>⑲ 進場計畫（Shadow Only，不改變 BUY／WATCH／SELL）</h4>",
		"規劃／研究證據，不是下單指令。規則為啟發式，未經回測驗證。",
		"舊版「④ 價位計畫」的進場區／停損價／停利區維持原樣，EP-10 之前不收斂；本區不取代舊欄位。",
	)
	epAssertAbsent(t, sec, "回測驗證通過", "已回測", "已驗證", "買進", "賣出")
}

func TestEntryPlanSectionNeverShowsLegacyPriceFields(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50)))
	epAssertContains(t, sec, epRow("理想進場區", `94.70 – 97.30 <span class="ep-code">MA20・RAW</span>`))
	epAssertAbsent(t, sec, "88.81", "89.91", "87.65", "121.11", "131.31", "111.1", "86.66", "88.44")
}

// ── display plumbing ─────────────────────────────────────────────────────────────────

func TestEntryPlanStylesFollowTheirOwnFlagOnly(t *testing.T) {
	entries := []scanner.WatchlistEntry{epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50))}
	if on := genHTML(t, entries, GuardrailViewOptions{ShowEntryPlan: true}); len(epStylesRe.FindAllString(on, -1)) != 1 {
		t.Error("the ⑲ styles are missing (or duplicated) when its own flag is on")
	}
	for name, gv := range map[string]GuardrailViewOptions{
		"technical":       {ShowTechnicalIndicators: true},
		"trend extension": {ShowTrendExtension: true},
		"ai":              {ShowAI: true},
		"guardrail":       {Show: true},
	} {
		if html := genHTML(t, entries, gv); strings.Contains(html, "ep19-styles") || strings.Contains(html, ".wl-ep") {
			t.Errorf("⑲ styles were emitted by the unrelated %s flag", name)
		}
	}
}

func TestEntryPlanHTMLCarriesNoNaNOrInf(t *testing.T) {
	p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p.Target1.RMultiple = epF(math.NaN())
	p.Target2.RMultiple = epF(math.Inf(1))
	p.RiskReward.Ratio = math.Inf(-1)
	p.MaxChasePrice = epF(math.NaN())
	p.IdealEntry.High = math.Inf(1)
	html := genHTML(t, []scanner.WatchlistEntry{epLegacyEntry(p)}, GuardrailViewOptions{ShowEntryPlan: true})
	if len(epSections(html)) != 1 {
		t.Fatal("fixture: section not rendered")
	}
	for _, bad := range []string{"NaN", "+Inf", "-Inf", "Inf"} {
		if strings.Contains(html, bad) {
			t.Errorf("the report contains %q", bad)
		}
	}
}

// A nil plan renders nothing for that entry, the report's established presentation for a
// feature result that does not exist (see TestTechnicalNilResultRendersNothing). No plan is
// fabricated.
func TestEntryPlanNilPlanWithShowTrueRendersNothing(t *testing.T) {
	withPlan := epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50))
	nilPlan := epLegacyEntry(nil)
	nilPlan.A.Symbol = "2222"

	mixed := genHTML(t, []scanner.WatchlistEntry{nilPlan, withPlan}, GuardrailViewOptions{ShowEntryPlan: true})
	if n := len(epSections(mixed)); n != 1 {
		t.Errorf("one nil and one published plan rendered %d sections, want 1", n)
	}
	allNil := genHTML(t, []scanner.WatchlistEntry{nilPlan, nilPlan}, GuardrailViewOptions{ShowEntryPlan: true})
	for _, marker := range []string{"⑲", `class="ep-status`, "INSUFFICIENT_DATA", "NO_VALID_ENTRY"} {
		if strings.Contains(allNil, marker) {
			t.Errorf("nil plans rendered %q", marker)
		}
	}
}

// ── 17 (isolation). OFF vs ON, and rendering is side-effect-free ───────────────────────

func epIsolationEntries() []scanner.WatchlistEntry {
	buy := epBridgeEntry(scanner.ActionBuy, epF(105.4321))
	sell := epBridgeEntry(scanner.ActionSell, epF(105.4321))
	sell.A.Symbol = "2454"
	synth := epLegacyEntry(epSynthPlan(entryplan.StatusTooExtended, 101.25))
	none := epLegacyEntry(nil)
	none.A.Symbol = "3333"
	return []scanner.WatchlistEntry{buy, sell, synth, none}
}

type epPtrs struct {
	plan, zone, chase, inv, t1, t2, rr, trace uintptr
}

func epPointers(p *entryplan.Plan) epPtrs {
	if p == nil {
		return epPtrs{}
	}
	addr := func(v any) uintptr {
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			return 0
		}
		return rv.Pointer()
	}
	return epPtrs{addr(p), addr(p.IdealEntry), addr(p.MaxChasePrice), addr(p.Invalidation),
		addr(p.Target1), addr(p.Target2), addr(p.RiskReward), addr(p.EntryTrace)}
}

func TestEntryPlanDisplayChangesOnlyTheEntryPlanBlockAndMutatesNothing(t *testing.T) {
	entries := epIsolationEntries()

	// Deep copies taken BEFORE rendering: the plans through JSON (every pointer target
	// followed), the whole watchlist as JSON bytes, and each plan's pointer identities.
	planCopies := make([]*entryplan.Plan, len(entries))
	ptrs := make([]epPtrs, len(entries))
	withPlans := 0
	for i, e := range entries {
		ptrs[i] = epPointers(e.EntryPlan)
		if e.EntryPlan == nil {
			continue
		}
		withPlans++
		b, err := json.Marshal(e.EntryPlan)
		if err != nil {
			t.Fatal(err)
		}
		var cp entryplan.Plan
		if err := json.Unmarshal(b, &cp); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(&cp, e.EntryPlan) {
			t.Fatalf("fixture: JSON deep copy of plan %d is not DeepEqual to the original", i)
		}
		planCopies[i] = &cp
	}
	if withPlans != 3 {
		t.Fatalf("fixture: %d entries carry a plan, want 3", withPlans)
	}
	before, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}

	off := genHTML(t, entries, GuardrailViewOptions{})
	on := genHTML(t, entries, GuardrailViewOptions{ShowEntryPlan: true})

	if n := len(epSections(on)); n != 3 {
		t.Fatalf("ON rendered %d ⑲ sections, want 3", n)
	}
	if len(epSections(off)) != 0 || strings.Contains(off, "ep19-styles") {
		t.Fatal("OFF rendered ⑲ content")
	}
	stripped := epStylesRe.ReplaceAllString(epSectionRe.ReplaceAllString(on, ""), "")
	if stripped != off {
		t.Errorf("enabling ⑲ changed HTML outside the ⑲ block and its styles (len off=%d, stripped on=%d)\n%s",
			len(off), len(stripped), epFirstDiff(off, stripped))
	}

	after, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("rendering changed the watchlist's JSON")
	}
	for i, e := range entries {
		if got := epPointers(e.EntryPlan); got != ptrs[i] {
			t.Errorf("entry %d: rendering replaced a plan pointer: before %+v after %+v", i, ptrs[i], got)
		}
		if planCopies[i] != nil && !reflect.DeepEqual(planCopies[i], e.EntryPlan) {
			t.Errorf("entry %d: rendering mutated the plan:\nbefore %+v\nafter  %+v", i, planCopies[i], e.EntryPlan)
		}
	}
}

func epFirstDiff(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 80
			if lo < 0 {
				lo = 0
			}
			hiA, hiB := i+80, i+80
			if hiA > len(a) {
				hiA = len(a)
			}
			if hiB > len(b) {
				hiB = len(b)
			}
			return "off: …" + a[lo:hiA] + "…\non:  …" + b[lo:hiB] + "…"
		}
	}
	return "one output is a prefix of the other"
}

// ── EP-8 review: only a BUY-SIDE entry semantic gets a ⑲ section ───────────────────────
//
// The user's decision, resolving the TODO at internal/scanner/entryplan_attach.go:613. It is a
// PRESENTATION rule: the bridge still projects every entry, the plan is still attached, and EP-7
// still persists it (internal/research/entryplan_evidence_test.go,
// TestEntryPlanExitActionEntryStillPersistsItsRows).
func TestEntryPlanSectionRendersOnlyBuySideSemantics(t *testing.T) {
	cases := []struct {
		name  string
		watch scanner.WatchAction
		want  bool
	}{
		{"PULLBACK_BUY renders", scanner.ActPullbackBuy, true},
		{"BREAKOUT_BUY renders", scanner.ActBreakoutBuy, true},
		{"TAKE_PROFIT is an exit action", scanner.ActTakeProfit, false},
		{"REMOVE_FROM_WATCHLIST is an exit action", scanner.ActRemove, false},
		// Consequence of the rule as stated ("only a buy-side semantic renders"): the scanner
		// answered and named no shape, so there is no buy-side semantic to render.
		{"PREPARE_ENTRY names no entry shape", scanner.ActPrepare, false},
		{"WATCH_CLOSELY names no entry shape", scanner.ActWatchClose, false},
		{"WAIT names no entry shape", scanner.ActWait, false},
	}
	ran, rendered := 0, 0
	for _, c := range cases {
		e := epBridgeEntryWith(scanner.ActionBuy, c.watch, nil)
		if e.EntryPlan == nil {
			t.Fatalf("%s: the bridge attached no plan — the fixture cannot test the display rule", c.name)
		}
		got := len(epSections(genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{ShowEntryPlan: true})))
		want := 0
		if c.want {
			want = 1
			rendered++
		}
		if got != want {
			t.Errorf("%s (WatchAction %s): rendered %d sections, want %d", c.name, c.watch, got, want)
		}
		ran++
	}
	if ran != 7 || rendered != 2 {
		t.Fatalf("ran %d cases with %d rendering, want 7 and 2", ran, rendered)
	}
}

// Both halves in ONE report: the exit-action entry shows nothing while the buy-side entry beside
// it shows its section, so a report that simply rendered nothing could not pass.
func TestEntryPlanExitActionEntryRendersNothingBesideABuySideEntry(t *testing.T) {
	buy := epBridgeEntryWith(scanner.ActionBuy, scanner.ActPullbackBuy, nil)
	exit := epBridgeEntryWith(scanner.ActionBuy, scanner.ActTakeProfit, nil)
	exit.A.Symbol, exit.A.Name = "2454", "出場標的"
	if exit.EntryPlan == nil {
		t.Fatal("the bridge left the exit-action entry with no plan — EP-8 must not make it nil")
	}

	html := genHTML(t, []scanner.WatchlistEntry{exit, buy}, GuardrailViewOptions{ShowEntryPlan: true})
	secs := epSections(html)
	if len(secs) != 1 {
		t.Fatalf("want exactly one ⑲ section (the buy-side entry), got %d", len(secs))
	}
	if n := strings.Count(html, "⑲ 進場計畫"); n != 1 {
		t.Errorf("the ⑲ heading appears %d times, want 1", n)
	}
	if n := strings.Count(html, `class="ep-status`); n != 1 {
		t.Errorf("⑲ status markup appears %d times, want 1", n)
	}
	// The exit entry is still IN the report (its card is rendered) and still carries its plan.
	if !strings.Contains(html, "2454") || !strings.Contains(html, "出場標的") {
		t.Error("the exit-action entry's own card is missing from the report")
	}
	if exit.EntryPlan == nil || exit.EntryPlan.Status == "" {
		t.Error("rendering removed the exit-action entry's plan")
	}
	// And the section that did render belongs to the buy-side entry.
	if !strings.Contains(secs[0], "WAIT_PULLBACK") && !strings.Contains(secs[0], "BUY_NOW") &&
		!strings.Contains(secs[0], "TOO_EXTENDED") {
		t.Errorf("the rendered section does not look like the buy-side entry's plan:\n%s", secs[0])
	}
}

// The EP-6D/6G sell-side CONTRADICTION is a different case and is NOT hidden: the scanner named a
// buy shape and its primary Action contradicts it, so ⑲ says NO_VALID_ENTRY with no prices.
func TestEntryPlanSellSideContradictionStillRenders(t *testing.T) {
	e := epBridgeEntryWith(scanner.ActionSell, scanner.ActPullbackBuy, nil)
	sec := epOneSection(t, e)
	epAssertContains(t, sec, "狀態 <b>NO_VALID_ENTRY</b>　無有效進場價位")
}

// DEFENSIVE (the domain cannot publish these today — targets.go:399): a zero ratio and a negative
// R multiple are not measurements and must not be printed as if they were.
func TestEntryPlanNonPositiveRatioAndRMultipleRenderDash(t *testing.T) {
	p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
	p.RiskReward.Ratio = 0
	p.Target1.RMultiple = epF(-2)
	p.Target2.RMultiple = epF(0)
	sec := epOneSection(t, epLegacyEntry(p))
	epAssertContains(t, sec,
		epRow("風險報酬比", "—"),
		epRow("目標一", "104.00"),
		epRow("目標二", "115.50"),
	)
	epAssertAbsent(t, sec, "0.00（量至", "（-2.00R）", "（0.00R）", "0.00R")
}

// Case 8: a SELL-SIDE Action on an entry that has NO ENTRY THESIS renders nothing.
//
// THE REASON IT IS HIDDEN IS THE MISSING THESIS, NOT THE SELL ACTION. The distinction is what
// this test exists for, and its control lives next door:
// TestEntryPlanSellSideContradictionStillRenders hands the SAME sell-side Action to an entry whose
// WatchAction IS a buy shape, and that one RENDERS (NO_VALID_ENTRY, no prices — the EP-6D/6G
// contradiction, case 9). So an Action alone hides nothing; only the absent entry semantic does.
//
// Every fixture goes through the real bridge, and the plan's own ENTRY_SEMANTIC evidence row is
// asserted UNAVAILABLE first, so a fixture that happened to carry a thesis could not make this
// test pass for the wrong reason.
func TestEntryPlanSellSideActionWithoutAnEntryThesisRendersNothing(t *testing.T) {
	cases := []struct {
		action scanner.Action
		watch  scanner.WatchAction
	}{
		{scanner.ActionSell, scanner.ActTakeProfit},
		{scanner.ActionReduce, scanner.ActRemove},
		{scanner.ActionStopLoss, scanner.ActTakeProfit},
		{scanner.ActionTakeProfit, scanner.ActRemove},
	}
	ran := 0
	for _, c := range cases {
		e := epBridgeEntryWith(c.action, c.watch, nil)
		e.A.Symbol, e.A.Name = "2454", "無進場想法"
		if e.EntryPlan == nil {
			t.Fatalf("%s/%s: the bridge attached no plan — EP-8 must not make EntryPlan nil", c.action, c.watch)
		}
		// The fixture really is "no entry thesis": the plan publishes an UNAVAILABLE semantic.
		sem, found := entryplan.Evidence{}, false
		for _, ev := range e.EntryPlan.Evidence {
			if ev.Key == entryplan.EvidenceEntrySemantic {
				sem, found = ev, true
			}
		}
		if !found || sem.Status != entryplan.Unavailable || sem.Text != "" {
			t.Fatalf("%s/%s: fixture ENTRY_SEMANTIC row = %+v (found=%v), want UNAVAILABLE with no text",
				c.action, c.watch, sem, found)
		}

		html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{ShowEntryPlan: true})
		if n := len(epSections(html)); n != 0 {
			t.Errorf("%s/%s: rendered %d ⑲ sections, want 0", c.action, c.watch, n)
		}
		for _, marker := range []string{"⑲ 進場計畫", `class="ep-status`} {
			if strings.Contains(html, marker) {
				t.Errorf("%s/%s: %q rendered for an entry with no entry thesis", c.action, c.watch, marker)
			}
		}
		// The entry is still in the report and still carries its plan.
		if !strings.Contains(html, "2454") || !strings.Contains(html, "無進場想法") {
			t.Errorf("%s/%s: the entry's own card is missing from the report", c.action, c.watch)
		}
		if e.EntryPlan == nil || e.EntryPlan.Status != entryplan.StatusInsufficientData {
			t.Errorf("%s/%s: plan after rendering = %+v, want an attached INSUFFICIENT_DATA plan",
				c.action, c.watch, e.EntryPlan)
		}
		ran++
	}
	if ran != 4 {
		t.Fatalf("ran %d of 4 sell-side-without-thesis cases", ran)
	}
}
