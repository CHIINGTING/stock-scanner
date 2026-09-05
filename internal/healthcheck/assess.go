package healthcheck

import (
	"fmt"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The entry assessment: the deterministic answer to "would I enter now?".
//
// # It is a READING of evidence that already exists, not a new score
//
// Nothing here computes an indicator, and nothing here weighs one factor against another
// with a coefficient. It is a short, ORDERED list of named rules over figures the rest of
// this package already produced, and the first rule whose condition holds decides the answer.
// That is deliberate: a scoring system with weights would be a second opinion competing with
// the scanner's, tunable in exactly the way this repo has decided not to tune things, and
// impossible to explain to the person acting on it.
//
// Every rule states its own condition and its own threshold on the row it produces, so the
// answer is always traceable to a rule ID and the numbers that satisfied it.
//
// # Its vocabulary is separate on purpose
//
// ATTRACTIVE / ACCEPTABLE / WAIT / OVERVALUED / HIGH_RISK / INSUFFICIENT_DATA is NOT
// BUY / WATCH / SELL. The scanner answers "what does this chart score out to"; this answers
// "is this a place to put money today, given price, value and risk together". Sharing a
// vocabulary would invite a reader to treat one as the other, and would make it look as
// though this could override a scan. It cannot: this package is a sidecar and nothing here
// is ever read back by a scan.
//
// # The AI does not participate
//
// The assessment is computed before the judge is called, and the judge is told the verdict
// as evidence. It may explain the verdict; it cannot produce or change one.

// Assessment vocabulary.
const (
	// AssessAttractive — value and setup both support entering now.
	AssessAttractive = "ATTRACTIVE"
	// AssessAcceptable — entering is defensible, without a margin worth calling attractive.
	AssessAcceptable = "ACCEPTABLE"
	// AssessWait — nothing is wrong; the entry condition has not arrived.
	AssessWait = "WAIT"
	// AssessOvervalued — the price already exceeds what the evidence supports.
	AssessOvervalued = "OVERVALUED"
	// AssessHighRisk — a specific hazard makes this unsuitable regardless of value.
	AssessHighRisk = "HIGH_RISK"
	// AssessInsufficientData — too little evidence to answer. NOT a mild "no".
	AssessInsufficientData = "INSUFFICIENT_DATA"
)

// RiskNone is what internal/scanner emits when a stock has NO risk label.
//
// It is a full-width dash, not an empty string — rocketRisk's default branch returns "—"
// with the warning 「依計畫設好停損即可」. Treating "not empty" as "has a hazard" therefore
// fires on every stock that has a watchlist entry at all, which is every stock this page can
// check. That is not a hypothetical: it shipped, it made HIGH_RISK the only reachable
// verdict, and it turned rules 3 through 8 into dead code.
const RiskNone = "—"

// hazardRiskLabels are the labels severe enough to override a value judgement outright.
//
// The set is deliberately SHORT and named rather than inferred, because the scanner already
// distinguishes severity and flattening its labels into a boolean throws that away:
//
//	跌破支撐  the pattern has failed — the setup this judgement rests on no longer exists
//	追高      extended far above the 5-day line; "enter now" is precisely the wrong question
//
// The remaining labels (族群轉弱 / 假突破 / 量不足 / 上影/收弱) are CAUTIONS. They are real,
// and they are reported, but "the volume has not confirmed yet" is not the same claim as
// "do not touch this regardless of price", and only the second belongs in HIGH_RISK.
var hazardRiskLabels = map[string]bool{
	"跌破支撐": true,
	"追高":   true,
}

// hazardLimitStatuses mirror internal/scanner's OWN negative set, using its OWN constants.
//
// scanner/rocket.go treats exactly these two as distribution. scanner.LimitLockedLowVol is
// documented there as 中性偏多 — 「不應直接視為轉弱」 — and is excluded on purpose. Reading it
// as a hazard here would not be a reading of the scanner's evidence; it would be a
// reinterpretation that contradicts it.
//
// The constants are IMPORTED rather than copied so a rename over there becomes a compile
// error here rather than a silently empty set.
var hazardLimitStatuses = map[string]bool{
	scanner.LimitUpFailed:     true,
	scanner.LimitDistribution: true,
}

// hasRisk is the ONE place this package decides whether a risk label says anything.
//
// It exists because the answer is not "the string is non-empty": the scanner writes "—" for
// no risk. That sentinel leaked into three layers at once — the assessment's verdict, a row
// in the persistent evidence table, and the model's prompt — because each of them made the
// same wrong test independently. There is one test now, and callers use it.
func hasRisk(label string) bool {
	return label != "" && label != RiskNone
}

// Thresholds. Every one is named, stated here rather than inline, and reported on the block
// so a reader can see the bar that was applied rather than inferring it.
const (
	// MarginAttractive is the BASE-case upside at or above which value is "attractive".
	MarginAttractive = 15.0
	// MarginAcceptable is the BASE-case upside at or above which entering is defensible.
	MarginAcceptable = 0.0
	// PercentileExpensive is the historical P/E percentile at or above which a stock is
	// expensive against its OWN history.
	PercentileExpensive = 80.0
)

// AssessmentRule is one named rule, reported whether or not it fired.
type AssessmentRule struct {
	ID string `json:"id"`
	// Description is the rule in words, including its threshold.
	Description string `json:"description"`
	// Fired is whether this rule's condition held.
	Fired bool `json:"fired"`
	// Detail is the evidence that decided it, in words.
	Detail string `json:"detail,omitempty"`
}

// AssessmentBlock is the verdict plus every rule that was considered.
type AssessmentBlock struct {
	Verdict string `json:"verdict"`
	// Reason is the one-line summary of the rule that decided the verdict.
	Reason string `json:"reason"`
	// DecidedBy is the ID of the rule that decided the verdict, DEFAULT included. It is
	// empty only when there was no Health to assess at all.
	DecidedBy string `json:"decided_by,omitempty"`
	// Rules is the ordered EVALUATION TRACE, up to and including the rule that decided the
	// verdict. Rules that were evaluated and did not fire are present with Fired=false;
	// rules after the terminal one were never reached and are absent. So the list answers
	// "what was checked, in what order, and where did it stop" rather than only "what
	// triggered".
	Rules []AssessmentRule `json:"rules"`
	// Inputs names the figures the rules read, with their rendered values.
	Inputs map[string]string `json:"inputs,omitempty"`
	// Note states the boundary between this and the scanner's own action.
	Note string `json:"note"`
}

const assessNote = "此判定使用獨立詞彙，與掃描器的 BUY / WATCH / SELL 不同，也不會回饋到掃描器。" +
	"它是對既有 evidence 的規則判讀，不是新的評分系統。"

// assess evaluates the rules in order and returns the first verdict that holds.
//
// The order is the priority: a hazard outranks a valuation, and a valuation outranks a setup.
// Rules after the deciding one are still reported, with Fired computed, so the block shows
// what was considered rather than stopping mid-list.
func assess(h *Health) AssessmentBlock {
	b := AssessmentBlock{Note: assessNote, Inputs: map[string]string{}}
	if h == nil {
		b.Verdict = AssessInsufficientData
		b.Reason = "沒有可判讀的 evidence"
		return b
	}

	// ── the inputs, all already computed ──
	pct, hasPct := h.Valuation.Historical.CurrentPercentile.Float()
	margin, hasMargin := h.Target.MarginOfSafety.Float()
	risk := h.Technical.RiskLabel
	limit := h.Technical.LimitStatus
	action := h.Technical.ScannerAction

	b.Inputs["price"] = h.Price.Close.Display
	b.Inputs["historical_pe_percentile"] = h.Valuation.Historical.CurrentPercentile.Display
	b.Inputs["margin_of_safety"] = h.Target.MarginOfSafety.Display
	b.Inputs["scanner_action"] = orDash(action)
	b.Inputs["risk_label"] = orDash(risk)
	b.Inputs["limit_status"] = orDash(limit)
	b.Inputs["data_quality"] = string(h.DataQuality.Level)

	add := func(id, desc string, fired bool, detail string) bool {
		b.Rules = append(b.Rules, AssessmentRule{
			ID: id, Description: desc, Fired: fired, Detail: detail})
		return fired
	}

	// 1. No usable price means no answer at all. This is first because every rule below
	//    reads a figure derived from one.
	noPrice := h.Price.Status != StatusAvailable
	if add("NO_PRICE", "沒有可用的收盤價 → INSUFFICIENT_DATA", noPrice, h.Price.Reason) {
		return decide(b, AssessInsufficientData, "NO_PRICE", "沒有可用的收盤價，無法判讀")
	}

	// 2. A NAMED hazard outranks everything. Membership of the two sets above is the test,
	//    never "the field is non-empty": the scanner writes "—" for no risk, and it marks
	//    LOCKED_LIMIT_UP_LOW_VOLUME as neutral-to-bullish.
	hazard := hazardRiskLabels[risk] || hazardLimitStatuses[limit]
	detail := ""
	if hazard {
		var parts []string
		if hazardRiskLabels[risk] {
			parts = append(parts, "風險標籤 "+risk)
		}
		if hazardLimitStatuses[limit] {
			parts = append(parts, "漲停籌碼 "+limit)
		}
		detail = strings.Join(parts, "；")
	}
	if add("HAZARD", "掃描器標記了嚴重風險（跌破支撐／追高）或漲停出貨 → HIGH_RISK",
		hazard, detail) {
		return decide(b, AssessHighRisk, "HAZARD",
			"掃描器已對此標的標記嚴重風險，價值判斷不足以抵銷")
	}

	// A caution is real but is not a hazard. It blocks ATTRACTIVE — "the volume has not
	// confirmed" is a reason not to call something the best available entry — without
	// pretending the stock is untouchable.
	caution := hasRisk(risk) && !hazardRiskLabels[risk]
	if caution {
		b.Inputs["caution"] = risk
	}

	// 3. Expensive against its own history AND no upside to the base case. BOTH are
	//    required: a high percentile alone describes a stock that has re-rated, which is not
	//    by itself a reason to stay out.
	expensive := hasPct && hasMargin && pct >= PercentileExpensive && margin < MarginAcceptable
	if expensive {
		detail = fmt.Sprintf("本益比位於自身歷史 %.0f 百分位（門檻 %.0f），且 BASE 情境上檔 %.1f%%",
			pct, PercentileExpensive, margin)
	} else {
		detail = ""
	}
	if add("EXPENSIVE", fmt.Sprintf(
		"歷史本益比百分位 ≥ %.0f 且 BASE 情境上檔 < %.0f%% → OVERVALUED",
		PercentileExpensive, MarginAcceptable), expensive, detail) {
		return decide(b, AssessOvervalued, "EXPENSIVE",
			"以自身歷史評價來看已偏貴，且 BASE 情境沒有上檔空間")
	}

	// 4. Without a margin of safety there is no value judgement to make. The setup may be
	//    fine, but "enter now" is a claim about price, and there is no price target.
	if add("NO_MARGIN", "沒有可用的安全邊際（目標價 INSUFFICIENT_DATA）→ WAIT",
		!hasMargin, h.Target.Reason) {
		return decide(b, AssessWait, "NO_MARGIN",
			"沒有足夠資料算出目標價，無法判斷現價是否值得進場")
	}

	// 5. A real margin, and the scan already likes the setup.
	buyable := action == "BUY"
	attractive := margin >= MarginAttractive && buyable && !caution
	if attractive {
		detail = fmt.Sprintf("BASE 情境上檔 %.1f%%（門檻 %.0f%%），掃描器判定 BUY，且無提醒標籤",
			margin, MarginAttractive)
	} else if caution && margin >= MarginAttractive && buyable {
		detail = "上檔與型態都符合，但掃描器標記了「" + risk + "」，不列為最佳進場"
	} else {
		detail = ""
	}
	if add("VALUE_AND_SETUP", fmt.Sprintf(
		"BASE 情境上檔 ≥ %.0f%%、掃描器判定 BUY、且無提醒標籤 → ATTRACTIVE", MarginAttractive),
		attractive, detail) {
		return decide(b, AssessAttractive, "VALUE_AND_SETUP",
			"價值與型態同時支持現在進場")
	}

	// 6. A margin, but the setup has not arrived.
	waiting := margin >= MarginAcceptable && !buyable
	if waiting {
		detail = fmt.Sprintf("BASE 情境上檔 %.1f%%，但掃描器判定為 %s", margin, orDash(action))
	} else {
		detail = ""
	}
	if add("VALUE_NO_SETUP", "有上檔空間但掃描器尚未判定 BUY → WAIT", waiting, detail) {
		return decide(b, AssessWait, "VALUE_NO_SETUP",
			"價值面可以接受，但進場型態尚未形成")
	}

	// 7. Negative margin with no hazard and no extreme percentile.
	negative := margin < MarginAcceptable
	if negative {
		detail = fmt.Sprintf("BASE 情境上檔 %.1f%%", margin)
	} else {
		detail = ""
	}
	if add("NO_UPSIDE", fmt.Sprintf("BASE 情境上檔 < %.0f%% → WAIT", MarginAcceptable),
		negative, detail) {
		return decide(b, AssessWait, "NO_UPSIDE", "現價已高於 BASE 情境目標價")
	}

	// 8. Default. Everything above declined to fire, so: a margin at or above the acceptable
	//    bar, no hazard, and the scanner DOES say BUY — a non-BUY with a margin is caught by
	//    rule 6, and a negative margin by rule 7. What is left is a BUY whose margin is real
	//    but under the attractive bar, or one carrying a caution label.
	b.Rules = append(b.Rules, AssessmentRule{
		ID: "DEFAULT", Description: "以上皆未觸發 → ACCEPTABLE", Fired: true,
		Detail: fmt.Sprintf("BASE 情境上檔 %.1f%%，掃描器判定 %s", margin, orDash(action)),
	})
	return decide(b, AssessAcceptable, "DEFAULT", "沒有明顯阻礙，但也沒有突出的安全邊際")
}

func decide(b AssessmentBlock, verdict, ruleID, reason string) AssessmentBlock {
	b.Verdict, b.DecidedBy, b.Reason = verdict, ruleID, reason
	return b
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
