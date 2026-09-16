package report

import (
	"fmt"
	"math"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ── ⑲ 進場計畫 (EP-8) display helpers ─────────────────────────────────────────────────
//
// PRESENTATION ONLY. The section renders the entryplan.Plan the scanner bridge already attached
// to WatchlistEntry.EntryPlan. These helpers read Plan fields, format numbers, map enum and
// reason codes to text, and derive ONE display-only label (where the plan's own current-price
// observation sits relative to the plan's own zone and chase ceiling). They compute no zone,
// no ceiling, no invalidation, no target, no ratio and no status, and the label they derive is
// never written back to the plan or read by anything else.
//
// MISSING ≠ ZERO: every absent value renders as epUnavailable ("—"), the notation the report's
// existing formatters (instRatioLabel, biasValLabel, pctPtr) already use for an absent value.
// None of those helpers formats a plain price, which is why epPrice exists; it is the single
// price formatter for this section and it has no branch that prints a number for an absent
// or non-price value.
//
// The structural guard is report_entryplan_guard_test.go; the behaviour proof is the output
// tests in report_entryplan_test.go.

const epUnavailable = "—"

// epRelation is the display-only relationship between the plan's current-price observation and
// the plan's published zone and chase ceiling.
type epRelation string

const (
	epRelUnavailable          epRelation = "UNAVAILABLE"
	epRelBelowZone            epRelation = "BELOW_ZONE"
	epRelInsideZone           epRelation = "INSIDE_ZONE"
	epRelAboveZoneWithinChase epRelation = "ABOVE_ZONE_WITHIN_CHASE"
	epRelAboveChaseCeiling    epRelation = "ABOVE_CHASE_CEILING"
	epRelAboveZoneNoCeiling   epRelation = "ABOVE_ZONE_NO_CHASE_CEILING"
)

var epRelationLabels = map[epRelation]string{
	epRelUnavailable:          "無法判斷（計畫未發布可用的現價或進場區）",
	epRelBelowZone:            "低於進場區",
	epRelInsideZone:           "位於進場區內（含上下緣）",
	epRelAboveZoneWithinChase: "高於進場區，未超過追價上限",
	epRelAboveChaseCeiling:    "高於追價上限",
	epRelAboveZoneNoCeiling:   "高於進場區（計畫未發布追價上限）",
}

// epStatusLabels maps every canonical entryplan.EntryStatus to presentation text. The canonical
// code is always rendered beside it. A status missing from this map renders as unknown; the test
// TestEntryPlanSectionRendersEveryCanonicalStatus parses internal/entryplan/types.go so a new
// status without a label fails the suite.
//
// Deliberately no BUY / WATCH / SELL vocabulary (買進／觀察／賣出): these are entry-execution
// readings, not the scanner's decision.
var epStatusLabels = map[entryplan.EntryStatus]string{
	entryplan.StatusBuyNow:           "現價符合計畫進場條件",
	entryplan.StatusWaitPullback:     "等待拉回至進場區",
	entryplan.StatusWaitBreakout:     "等待價格突破進場區",
	entryplan.StatusTooExtended:      "已超過追價上限，不追",
	entryplan.StatusNoValidEntry:     "無有效進場價位",
	entryplan.StatusInsufficientData: "資料不足，無法判定",
}

const epUnknownStatusLabel = "未知狀態（非 entryplan 定義的狀態碼）"

func epStatusCSS(s entryplan.EntryStatus) string {
	switch s {
	case entryplan.StatusBuyNow:
		return "ep-s-now"
	case entryplan.StatusWaitPullback, entryplan.StatusWaitBreakout:
		return "ep-s-wait"
	case entryplan.StatusTooExtended, entryplan.StatusNoValidEntry:
		return "ep-s-no"
	case entryplan.StatusInsufficientData:
		return "ep-s-data"
	default:
		return "ep-s-unknown"
	}
}

// epConfidenceLabels covers every entryplan.Confidence constant (confidence.go). The canonical
// value is always rendered; an unlisted value is shown raw and marked unknown.
var epConfidenceLabels = map[entryplan.Confidence]string{
	entryplan.ConfidenceHigh:             "證據完整",
	entryplan.ConfidenceMedium:           "證據部分佐證",
	entryplan.ConfidenceLow:              "僅最低必要證據",
	entryplan.ConfidenceInsufficientData: "必要證據不足",
}

// epReasonLabels is the presentation map for entryplan's reason codes.
//
// EP-10 COMPLETED IT: it now covers entryplan.AllReasons EXACTLY — no code without a label and
// no label for a code entryplan does not register. TestEntryPlanReasonLabelsCoverTheRegistry
// enforces both directions off entryplan.AllReasons itself, so a reason added upstream fails the
// suite here rather than silently rendering as a bare SCREAMING_SNAKE code.
//
// PRESENTATION ONLY, and three properties survive the completion:
//
//	the canonical code is ALWAYS printed beside the text (epReasons), so nothing is lost;
//	an UNMAPPED code still renders as its raw code — the fallback is kept, not removed,
//	  because a decoder reading an archived plan may meet a code this build never heard of;
//	the plan's own ORDER is preserved, so the output is deterministic.
//
// No wording here adds, removes or reinterprets a condition. Where entryplan draws a line the
// label draws the same one — in particular "未評估" (not evaluated) is never written as "未通過"
// (failed) or as anything a reader could take for a pass: STATUS_REQUIREMENT_NOT_EVALUATED is a
// missing evaluator in this repo, not a judgement about the stock (reasons.go:585 and the TODO
// decide.go's evaluateRequirement carries).
var epReasonLabels = map[entryplan.Reason]string{
	// Snapshot-level evidence.
	entryplan.ReasonSnapshotMalformed:        "輸入快照缺少代號或交易日",
	entryplan.ReasonCurrentPriceUnavailable:  "現價不可用",
	entryplan.ReasonPriceBasisUnavailable:    "價格基準不可用",
	entryplan.ReasonMarketRegimeUnavailable:  "大盤狀態不可用",
	entryplan.ReasonEntrySemanticUnavailable: "掃描器未提供進場型態",

	// EP-3b: the zone step.
	entryplan.ReasonZoneSemanticNotPermitted:          "此大盤狀態的政策不允許這種進場型態",
	entryplan.ReasonZonePolicyUnresolved:              "進場區政策未定（不是禁止）",
	entryplan.ReasonPullbackLevelsUnavailable:         "沒有可用的支撐價位（MA20／MA60／平台低點皆不可用）",
	entryplan.ReasonPullbackLevelsBasisMismatch:       "支撐價位與報價的價格基準不同，拒絕換算",
	entryplan.ReasonPullbackLevelNotBelowCurrentPrice: "所有可用支撐都不低於現價，沒有可回檔承接的位置",
	entryplan.ReasonBreakoutLevelUnavailable:          "沒有可用的前高樞紐，無從定義突破",
	entryplan.ReasonBreakoutLevelBasisMismatch:        "前高樞紐與報價的價格基準不同，拒絕換算",
	entryplan.ReasonBreakoutLevelNotAQuote:            "前高樞紐不是一個實際報價（未落在檔位上）",
	entryplan.ReasonZoneATRUnavailable:                "ATR 不可用，無法建立進場區寬度",
	entryplan.ReasonZoneATRBasisMismatch:              "ATR 與進場區的價格基準不同",
	entryplan.ReasonZoneATRPeriodMismatch:             "ATR 週期不是寬度規則所定義的週期",
	entryplan.ReasonZoneTickAlignmentUnavailable:      "進場區價位無法對齊檔位",
	entryplan.ReasonZoneLowNotAPrice:                  "進場區下緣算出來不是一個價格",
	entryplan.ReasonPullbackZoneOverlapsCurrentPrice:  "回檔進場區與現價重疊（附註，不是拒絕）",
	entryplan.ReasonRecentPriceAdjustment:             "近期有除權息還原，ATR 寬度仍含部分還原殘留（附註，不是拒絕）",

	// EP-3b: the chase step.
	entryplan.ReasonChaseNotAllowed:               "此大盤狀態的政策不允許追價",
	entryplan.ReasonChasePolicyUnresolved:         "追價政策未定（不是禁止）",
	entryplan.ReasonPreviousCloseUnavailable:      "前一日收盤不可用，無法計算追價上限",
	entryplan.ReasonPreviousCloseBasisMismatch:    "前一日收盤與計畫其餘價格的基準不同",
	entryplan.ReasonChaseLimitRequiresRawBasis:    "漲跌幅上限只適用未還原價，本計畫的價格是還原價",
	entryplan.ReasonChaseLimitRuleUnavailable:     "無法判定此標的適用的漲跌幅規則",
	entryplan.ReasonChaseLimitPriceUnavailable:    "漲跌幅適用，但無法算出當日上限價",
	entryplan.ReasonChaseCeilingBelowZone:         "當日最高合法價低於進場區上緣，追價上限撤回",
	entryplan.ReasonChaseTickAlignmentUnavailable: "追價上限價無法對齊檔位",

	// EP-4: the invalidation step.
	entryplan.ReasonNoValidInvalidation:               "沒有合格的想法失效價",
	entryplan.ReasonInvalidationEvidenceUnavailable:   "沒有任何可比較的失效價證據",
	entryplan.ReasonInvalidationSelectionInconsistent: "失效價的篩選與選取互相矛盾（內部不一致，不是市場狀態）",

	// EP-4: the target step.
	entryplan.ReasonTarget1TickAlignmentUnavailable: "目標一無法對齊檔位",
	entryplan.ReasonTarget1NotAboveEntry:            "目標一不高於進場區上緣",
	entryplan.ReasonTarget2TickAlignmentUnavailable: "目標二無法對齊檔位",
	entryplan.ReasonValuationCeilingBelowEntry:      "估值上限低於進場價，第二目標撤回",
	entryplan.ReasonTarget2BelowTarget1:             "估值上限把目標二壓到目標一之下，目標二撤回",
	entryplan.ReasonRiskNotEstablished:              "沒有風險分母，無法計算目標與風險報酬比",

	// EP-5: the decision step.
	entryplan.ReasonStatusSemanticUnresolved:       "進場型態未定",
	entryplan.ReasonStatusCurrentPriceUnavailable:  "無現價可比較",
	entryplan.ReasonStatusPolicyUnresolved:         "進場政策未定",
	entryplan.ReasonStatusPolicyRefused:            "進場政策不允許此型態",
	entryplan.ReasonStatusEntryZoneUnavailable:     "進場區不可用",
	entryplan.ReasonStatusNoLegalEntryZone:         "沒有合法進場區",
	entryplan.ReasonStatusRiskBoundaryUnavailable:  "想法失效價不可用",
	entryplan.ReasonStatusNoRiskBoundary:           "沒有風險邊界",
	entryplan.ReasonStatusAboveMaxChase:            "現價高於追價上限",
	entryplan.ReasonStatusPriceInsideZone:          "現價位於進場區內",
	entryplan.ReasonStatusPriceAboveEntryZone:      "現價高於進場區",
	entryplan.ReasonStatusPriceBelowEntryZone:      "現價低於進場區",
	entryplan.ReasonStatusImmediateEntryRefused:    "政策不允許在區外立即進場",
	entryplan.ReasonStatusImmediateEntryUnresolved: "對於是否可立即進場，政策未做出決定",
	entryplan.ReasonStatusRequirementNotMet:        "權限附帶的必要條件在這裡不成立",
	// 未評估 ≠ 通過，也 ≠ 未通過：這是本 repo 沒有實作的評估器。
	entryplan.ReasonStatusRequirementNotEvaluated:   "權限附帶的必要條件本系統尚無法評估（未評估，不等於通過）",
	entryplan.ReasonStatusImmediateChasePermitted:   "政策允許在追價上限內進場",
	entryplan.ReasonStatusChaseCeilingUnresolved:    "追價上限無法計算",
	entryplan.ReasonStatusNoExecutablePriceRelation: "現價與進場區沒有可執行的關係",
	entryplan.ReasonStatusThesisContradicted:        "掃描器主要動作與進場想法矛盾",
	entryplan.ReasonStatusThesisUnclassified:        "掃描器主要動作未分類",

	// Reserved (entryplan.ReservedReasons): production can emit them, no ComputePlan input can.
	entryplan.ReasonPlanInvariantViolated: "計畫不變式被違反（本層的程式瑕疵，不是市場狀態）",
}

// entryPlanSection is the pre-formatted view of ONE published plan. Every field is a string the
// template prints verbatim (html/template escapes it).
type entryPlanSection struct {
	StatusCode      string
	StatusLabel     string
	StatusCSS       string
	Zone            string
	ZoneBasis       string
	CurrentPrice    string
	Relation        epRelation
	RelationLabel   string
	MaxChase        string
	Invalidation    string
	Target1         string
	Target2         string
	RiskReward      string
	Policy          string
	RuleVersion     string
	Confidence      string
	ConfidenceLabel string
	Reasons         []string
	Caveats         []string
}

// entryPlanView builds the ⑲ view for one watchlist entry, or nil when the section must not be
// rendered for it. Nil renders nothing, the same presentation the report uses for a nil Technical
// result or an unavailable AI analysis. TWO cases return nil:
//
//  1. the entry carries no plan at all (enable_entry_plan was off at scan time);
//  2. the plan publishes no BUY-SIDE entry semantic — see epBuySideSemantic.
//
// It reads exactly one field of the entry, EntryPlan, and never writes through it. Neither case
// changes the plan, the projection or persistence: an entry hidden here still carries its plan
// and EP-7 still writes its ep_* rows (internal/research/entryplan_evidence_test.go,
// TestEntryPlanExitActionEntryStillPersistsItsRows).
func entryPlanView(e scanner.WatchlistEntry) *entryPlanSection {
	p := e.EntryPlan
	if p == nil || !epBuySideSemantic(p) {
		return nil
	}
	cur, curOK := epPlanCurrentPrice(p)
	rel := epZoneRelation(p, cur, curOK)

	v := &entryPlanSection{
		StatusCode:    epText(string(p.Status)),
		StatusCSS:     epStatusCSS(p.Status),
		Zone:          epZone(p.IdealEntry),
		ZoneBasis:     epZoneBasis(p.IdealEntry),
		CurrentPrice:  epCurrentPrice(cur, curOK),
		Relation:      rel,
		RelationLabel: epRelationLabels[rel],
		MaxChase:      epPricePtr(p.MaxChasePrice),
		Invalidation:  epInvalidation(p.Invalidation),
		Target1:       epTarget(p.Target1),
		Target2:       epTarget(p.Target2),
		RiskReward:    epRiskReward(p.RiskReward),
		Policy:        epPolicy(p.EntryTrace),
		RuleVersion:   epText(p.RuleVersion),
		Confidence:    epText(string(p.Confidence)),
		Reasons:       epReasons(p.Reasons),
		Caveats:       epCaveats(p.Caveats),
	}
	if label, ok := epStatusLabels[p.Status]; ok {
		v.StatusLabel = label
	} else {
		v.StatusLabel = epUnknownStatusLabel
	}
	if label, ok := epConfidenceLabels[p.Confidence]; ok {
		v.ConfidenceLabel = label
	} else {
		v.ConfidenceLabel = "未知信心值"
	}
	return v
}

// epBuySideSemantic reports whether the plan publishes a BUY-SIDE entry semantic — the user's
// EP-8 rule: only an entry the scanner named a buy shape for gets a ⑲ section.
//
// KEYED ON THE PLAN, not on the scanner's WatchAction. The ENTRY_SEMANTIC evidence row
// (plan.go:188-207) is the plan's own published statement about the shape, and ComputePlan emits
// it in three distinguishable states:
//
//	AVAILABLE           + Text "PULLBACK" / "BREAKOUT"  the scanner named a buy shape
//	INSUFFICIENT_DATA   + Text "UNKNOWN"                the scanner answered and named no shape
//	                                                    (PREPARE_ENTRY / WATCH_CLOSELY / WAIT)
//	UNAVAILABLE         + no text                       no semantic was handed over at all:
//	                                                    an EXIT action (TAKE_PROFIT /
//	                                                    REMOVE_FROM_WATCHLIST), an entry the
//	                                                    rocket pass never enriched, or a
//	                                                    WatchAction the bridge does not know
//	                                                    (entryplan_attach.go:661-676: the exit
//	                                                    arm at 661, the never-enriched arm at
//	                                                    665 and the default at 670)
//
// Only the first renders. Resolved() is entryplan's OWN definition of a shape a policy can be
// consulted about (policy.go:112-114) and the same test DecideStatus's S0 applies
// (decide.go:542), so the report does not write a second list of buy-side semantics.
//
// WHAT THIS CANNOT SEPARATE, stated rather than implied: the three UNAVAILABLE causes above are
// one row in the published plan. The section is hidden for all of them, which satisfies the
// exit-action rule; it is not evidence that the entry was an exit.
//
// A plan with no ENTRY_SEMANTIC row at all — ComputePlan's malformed-snapshot early return
// publishes no evidence — is not buy-side either.
func epBuySideSemantic(p *entryplan.Plan) bool {
	for _, ev := range p.Evidence {
		if ev.Key != entryplan.EvidenceEntrySemantic {
			continue
		}
		return ev.Status == entryplan.Available && entryplan.EntrySemantic(ev.Text).Resolved()
	}
	return false
}

// epPlanCurrentPrice returns the plan's OWN current-price observation: the CURRENT_PRICE evidence
// row ComputePlan publishes (plan.go:130-148, "2. Current price"). That row and the price DecideStatus
// compared are gated by the same PriceObservation.Usable check (plan.go currentPriceFor), so the
// label reflects what the plan saw. There is no fallback to StockAnalysis.Close.
func epPlanCurrentPrice(p *entryplan.Plan) (float64, bool) {
	for _, ev := range p.Evidence {
		if ev.Key != entryplan.EvidenceCurrentPrice {
			continue
		}
		if ev.Status != entryplan.Available || ev.Value == nil || !epIsPrice(*ev.Value) {
			return 0, false
		}
		return *ev.Value, true
	}
	return 0, false
}

// epZoneRelation mirrors the comparisons of entryplan.DecideStatus exactly, in the same order, with
// no tolerance (decide.go uses none):
//
//	decide.go:626-627  ceiling, hasCeiling := usablePrice(in.MaxChasePrice); hasCeiling && cur > ceiling
//	decide.go:636      zone.Low <= cur && cur <= zone.High          (bounds inclusive)
//	decide.go:652      above := cur > zone.High
//
// usablePrice (decide.go:809) accepts a finite, strictly positive value; epIsPrice is the same
// test. It is a label for display. It decides no status and is never written back.
func epZoneRelation(p *entryplan.Plan, cur float64, curOK bool) epRelation {
	if !curOK || p.IdealEntry == nil || !epIsPrice(p.IdealEntry.Low) || !epIsPrice(p.IdealEntry.High) {
		return epRelUnavailable
	}
	zone := *p.IdealEntry
	hasCeiling := p.MaxChasePrice != nil && epIsPrice(*p.MaxChasePrice)
	if hasCeiling && cur > *p.MaxChasePrice {
		return epRelAboveChaseCeiling
	}
	if zone.Low <= cur && cur <= zone.High {
		return epRelInsideZone
	}
	if cur > zone.High {
		if hasCeiling {
			return epRelAboveZoneWithinChase
		}
		return epRelAboveZoneNoCeiling
	}
	return epRelBelowZone
}

func epIsPrice(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }

// epPrice formats one published price. A value that is not a price (non-finite, zero or negative)
// renders as "—": a plan never publishes one, and printing it would show a number the plan does
// not stand behind.
func epPrice(v float64) string {
	if !epIsPrice(v) {
		return epUnavailable
	}
	return fmt.Sprintf("%.2f", v)
}

func epPricePtr(v *float64) string {
	if v == nil {
		return epUnavailable
	}
	return epPrice(*v)
}

func epCurrentPrice(cur float64, ok bool) string {
	if !ok {
		return epUnavailable
	}
	return epPrice(cur)
}

func epText(s string) string {
	if s == "" {
		return epUnavailable
	}
	return s
}

// epZone renders "Low – High". No midpoint, and no half-zone: if either bound is not a price the
// zone is shown as absent.
func epZone(z *entryplan.PriceZone) string {
	if z == nil || !epIsPrice(z.Low) || !epIsPrice(z.High) {
		return epUnavailable
	}
	return epPrice(z.Low) + " – " + epPrice(z.High)
}

// epZoneBasis renders the zone's own basis, and ONLY when the zone itself renders.
//
// EP-10 browser finding: a plan whose bounds are not prices rendered "理想進場區 — MA20・RAW",
// a basis for a zone that was never published. A basis is a statement ABOUT a published zone,
// so it is withheld on exactly the condition epZone withholds the zone.
func epZoneBasis(z *entryplan.PriceZone) string {
	if epZone(z) == epUnavailable {
		return ""
	}
	parts := make([]string, 0, len(z.Basis)+1)
	parts = append(parts, z.Basis...)
	if z.PriceBasis != "" {
		parts = append(parts, string(z.PriceBasis))
	}
	return strings.Join(parts, "・")
}

func epInvalidation(inv *entryplan.Invalidation) string {
	if inv == nil {
		return epUnavailable
	}
	return epPrice(inv.Price)
}

// epTarget renders the FINAL published target only, with its published R multiple when present.
func epTarget(t *entryplan.PriceLevel) string {
	if t == nil {
		return epUnavailable
	}
	out := epPrice(t.Price)
	if out == epUnavailable {
		return out
	}
	// The R multiple is shown only when it is a positive multiple. DEFENSIVE: ComputeTargets
	// refuses a target at or below the top of the entry band (targets.go:371) and rMultiple
	// returns nil for a non-positive risk (targets.go:536-539), so no plan this repo can produce
	// carries a zero or negative RMultiple — but "（0.00R）" and
	// "（-2.00R）" would read as measurements rather than as the impossible values they are.
	if t.RMultiple != nil && epIsPrice(*t.RMultiple) {
		out += fmt.Sprintf("（%.2fR）", *t.RMultiple)
	}
	return out
}

// epRiskReward renders the published Ratio (display rounding only) and the target it was
// measured to. It is never recomputed from the per-share figures.
// A non-positive ratio is DEFENSIVE, exactly like the R multiple above: RiskReward is built only
// when reward/risk is finite and strictly positive (targets.go:398-399), so the domain cannot
// publish Ratio <= 0 today. Printing "0.00" for one would claim the trade offers no reward, which
// is a measurement, rather than saying the number is not one this section can show.
func epRiskReward(rr *entryplan.RiskReward) string {
	if rr == nil || !epIsPrice(rr.Ratio) {
		return epUnavailable
	}
	out := fmt.Sprintf("%.2f", rr.Ratio)
	if rr.Target != "" {
		out += "（量至 " + rr.Target + "）"
	}
	return out
}

// epPolicy is EntryTrace.Policy.Name — the one trace field this section reads, the same source
// EP-7 persists as ep_policy. No trace price is ever read.
func epPolicy(tr *entryplan.EntryTrace) string {
	if tr == nil {
		return epUnavailable
	}
	return epText(string(tr.Policy.Name))
}

// epReasons maps codes to text in the plan's own order. Mapped codes keep their code beside the
// text; an unmapped code renders as the raw code.
func epReasons(rs []entryplan.Reason) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if label, ok := epReasonLabels[r]; ok {
			out = append(out, label+"（"+string(r)+"）")
		} else {
			out = append(out, string(r))
		}
	}
	return out
}

func epCaveats(cs []string) []string {
	out := make([]string, 0, len(cs))
	return append(out, cs...)
}
