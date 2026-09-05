package healthcheck

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The model-suitability block: is trailing P/E the right primary anchor for this company?
//
// The classification is a pure function in internal/valuation. This file does two things it
// cannot: it reads the filed earnings sign out of the fundamental archive, and it hands over
// the base case's implied upside, which is computed one layer up. Neither is a judgement —
// both are evidence lookups — and the verdict itself is decided before any wording is chosen.
//
// # Why this is assembled after the target price
//
// One of the caution reasons is about the target's own output. That does NOT make the target
// an input to the verdict: cautions are appended after the class is decided, and the domain
// keeps them structurally incapable of changing it. Assembling here is what lets the block
// state "target AVAILABLE, suitability WEAK" — the pairing the whole layer exists to express.

// SuitabilityBlock is the verdict plus the evidence and wording a page needs.
type SuitabilityBlock struct {
	// Suitability is SUITABLE / CONDITIONAL / WEAK / UNSUITABLE / INSUFFICIENT_DATA. It is a
	// statement about the MODEL, never about the company's prospects: UNSUITABLE does not
	// mean bad, overpriced, risky or a sell.
	Suitability string `json:"suitability"`
	RuleVersion string `json:"rule_version"`
	// Reasons are canonical codes. The wording in Notes is presentation; these are what get
	// persisted and compared.
	Reasons []string `json:"reasons,omitempty"`
	Notes   []string `json:"notes,omitempty"`

	// ── the evidence, carried so a verdict can be checked rather than trusted ──
	EarningsSign   string `json:"earnings_sign"`
	EarningsPeriod string `json:"earnings_period,omitempty"`
	EarningsSource string `json:"earnings_source,omitempty"`
	PEPersistence  string `json:"pe_persistence"`
	WindowSessions int    `json:"window_sessions"`

	Summary string `json:"summary"`
}

// buildSuitability assembles the verdict from evidence that already exists.
func buildSuitability(ev *Evidence, h *Health) SuitabilityBlock {
	in := valuation.SuitabilityInput{
		// UNKNOWN, never NEVER: with no valuation evidence at all there is no pattern to
		// read, and NEVER would assert that the exchange published nothing.
		//
		// Defence in depth, not the guard. ev.Valuation == nil always implies
		// WindowSessions == 0, so ClassifySuitability's rule 0 fires first and normalises the
		// shape anyway — that rule is the tested one. This line exists so the input is never
		// momentarily wrong on its way there.
		Persistence:    valuation.PEUnknown,
		WindowSessions: h.Valuation.Historical.Quality.WindowSessions,
	}
	if ev.Valuation != nil && ev.Valuation.HistoricalPE.Persistence != "" {
		in.Persistence = ev.Valuation.HistoricalPE.Persistence
	}
	in.Earnings, in.EarningsPeriod, in.EarningsSource = filedEarningsSign(ev.Fundamental)

	// Caution inputs. Both are read-only, and the domain appends their notes after the class
	// is already decided.
	if v, ok := h.Valuation.Historical.Quality.RelativeIQR.Float(); ok {
		r := v / 100 // the block carries it as a percentage; the domain wants the ratio
		in.RelativeIQR = &r
	}
	if v, ok := h.Target.MarginOfSafety.Float(); ok {
		in.BaseUpsidePct = &v
	}
	return projectSuitability(valuation.ClassifySuitability(in))
}

// filedEarningsSign reads the sign of the most recent FILED period.
//
// Never inferred from the absence of a published P/E. That inference is the specific mistake
// this function exists to make impossible: a company with no P/E is not thereby known to be
// loss-making, and stating otherwise would be inventing a financial fact.
//
// The EPS and the net income must AGREE. They come from the same filing and describe the same
// window, so a disagreement means one of them was misread — and a sign taken from a
// misparsed field is worse than no sign at all.
func filedEarningsSign(v *fundamental.View) (valuation.EarningsSign, string, string) {
	if v == nil || v.Financials == nil {
		return valuation.EarningsUnknown, "", ""
	}
	f := v.Financials
	period := periodLabel(f.Period)
	if f.CumulativeEPS == nil {
		// A filing with no EPS line. The net income alone could carry the sign, but EPS is
		// the figure the exchange's P/E is built on, and substituting a different one here
		// would make the evidence and the ratio describe different things.
		return valuation.EarningsUnknown, period, fundSource
	}
	eps, ni := *f.CumulativeEPS, float64(f.NetIncome)
	switch {
	case eps > 0 && ni > 0:
		return valuation.EarningsPositive, period, fundSource
	case eps <= 0 && ni <= 0:
		return valuation.EarningsNonPositive, period, fundSource
	default:
		return valuation.EarningsUnknown, period, fundSource
	}
}

func projectSuitability(v valuation.SuitabilityVerdict) SuitabilityBlock {
	b := SuitabilityBlock{
		Suitability:    string(v.Suitability),
		RuleVersion:    v.RuleVersion,
		EarningsSign:   string(v.Earnings),
		EarningsPeriod: v.EarningsPeriod,
		EarningsSource: v.EarningsSource,
		PEPersistence:  string(v.Persistence),
		WindowSessions: v.WindowSessions,
	}
	for _, r := range v.Reasons {
		b.Reasons = append(b.Reasons, string(r))
		if n := reasonNote(r); n != "" {
			b.Notes = append(b.Notes, n)
		}
	}
	b.Summary = suitabilitySummary(v)
	return b
}

// reasonNote is the natural-language rendering of a canonical code.
//
// Written as observations about the MODEL, not about the stock. "P/E 不適合作為主要估值錨" is a
// statement about which lens to use; "這檔股票不好" is one this layer has no standing to make,
// and a reader who confuses them will act on it.
func reasonNote(r valuation.SuitabilityReason) string {
	switch r {
	case valuation.ReasonPositiveEligibilityPersistent:
		// Worded as what it is: an eligibility history. "盈餘穩定" would be the overclaim this
		// reason code was renamed to avoid.
		return "涵蓋期間內每個已封存 session 交易所都算得出本益比，代表盈餘基準持續維持在正值"
	case valuation.ReasonEarningsMagnitudeUnknown:
		return "本判定只證明盈餘持續為正，不代表盈餘金額穩定；本 repo 每檔只有一期財報，" +
			"無法判斷盈餘規模的變化"
	case valuation.ReasonInsufficientObservation:
		return "已封存的 session 太少，還不足以認定「交易所長期未公告本益比」"
	case valuation.ReasonPositiveEarningsBasis:
		return "最近一期財報的每股盈餘為正"
	case valuation.ReasonNoPositiveTrailing:
		return "最近一期財報顯示盈餘基準不為正"
	case valuation.ReasonPENeverPublished:
		return "涵蓋期間內交易所從未公告本益比"
	case valuation.ReasonPEAvailableOnlyRecently:
		return "本益比只在近期出現：較早的 session 全部沒有，之後才全部有"
	case valuation.ReasonPEAvailabilityIntermit:
		return "本益比時有時無，代表盈餘基準在期間內多次跨越零"
	case valuation.ReasonEarningsSignChange:
		return "盈餘基準在涵蓋期間內變號，歷史分布混合了不同的獲利階段"
	case valuation.ReasonEarningsBasisUnknown:
		return "沒有可對照的財報，無法確認目前的盈餘基準"
	case valuation.ReasonInsufficientEarningsHist:
		return "觀察期間不足一個財報週期，還看不出盈餘基準是否穩定"
	case valuation.ReasonNoArchivedSessions:
		return "這檔股票還沒有任何已封存的本益比 session，沒有可判讀的資料"
	case valuation.ReasonValuationRegimeWide:
		return "歷史本益比分布很寬，代表市場曾大幅重新評價（僅供參考，不影響適用性判定）"
	case valuation.ReasonTargetSensitivityCheck:
		return "目標價相對現價的差距很大，對本益比倍數的假設相當敏感（僅供參考，不影響適用性判定）"
	}
	return ""
}

func suitabilitySummary(v valuation.SuitabilityVerdict) string {
	head := map[valuation.Suitability]string{
		valuation.SuitabilitySuitable:         "本益比適合作為目前的主要估值錨",
		valuation.SuitabilityConditional:      "本益比可作為估值參考，但有明確的但書",
		valuation.SuitabilityWeak:             "本益比算得出來，但作為長期主要估值錨的經濟意義偏弱",
		valuation.SuitabilityUnsuitable:       "目前證據顯示本益比不適合作為主要估值模型",
		valuation.SuitabilityInsufficientData: "證據不足以判斷本益比是否適用",
	}[v.Suitability]
	return fmt.Sprintf("%s：%s（盈餘基準 %s／本益比出現形態 %s／觀察 %d 個 session；規則 %s）",
		v.Suitability, head, v.Earnings, v.Persistence, v.WindowSessions, v.RuleVersion)
}
