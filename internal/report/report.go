package report

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/etfflow"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// etfSignalLabel maps an ETF flow signal to a Chinese display label for report ⑨.
// It deliberately avoids the raw enum value (which contains the identifiers SELL/BUY)
// so the rendered HTML stays free of trade-instruction tokens. "賣壓"/"加碼" are
// descriptive context, NOT the forbidden "賣出"/"買進" instructions.
func etfSignalLabel(s etfflow.FlowSignal) string {
	switch s {
	case etfflow.PassiveSellPressure:
		return "高股息／規則型 ETF 賣壓"
	case etfflow.PassiveBuySupport:
		return "高股息／規則型 ETF 加碼支撐"
	case etfflow.ActiveSellPressure:
		return "主動式 ETF 賣壓"
	case etfflow.ActiveBuySupport:
		return "主動式 ETF 加碼支撐"
	case etfflow.ActiveConsensusSell:
		return "多檔主動式 ETF 一致賣壓"
	case etfflow.ActiveConsensusBuy:
		return "多檔主動式 ETF 一致加碼"
	case etfflow.AggregateSellPressure:
		return "ETF 合計賣壓"
	case etfflow.AggregateBuySupport:
		return "ETF 合計加碼支撐"
	default:
		return "中性 / 無顯著 flow"
	}
}

// ── News (⑩) display helpers (Phase 1) ──────────────────────────────────────────
// All labels are descriptive CONTEXT only. They deliberately avoid the forbidden
// trade-instruction tokens 買進/賣出/買入/賣出/BUY/SELL/下單 — news never issues an order.

// newsSignalLabel maps a news signal type to a Chinese context label.
func newsSignalLabel(s news.SignalType) string {
	switch s {
	case news.SignalBullish:
		return "偏多"
	case news.SignalBearish:
		return "偏空"
	case news.SignalRotationIn:
		return "資金輪動進場"
	case news.SignalRotationOut:
		return "資金輪動出場"
	case news.SignalRiskWarning:
		return "風險提示"
	default:
		return "中性"
	}
}

// newsDot maps a signal type to a status dot for quick scanning.
func newsDot(s news.SignalType) string {
	switch s {
	case news.SignalBullish, news.SignalRotationIn:
		return "🟢"
	case news.SignalBearish, news.SignalRotationOut, news.SignalRiskWarning:
		return "🔴"
	default:
		return "🟡"
	}
}

// newsEpisodeLabel renders a friendly episode label from an event id ("gooaye-ep-677"
// → "股癌EP677"); non-episodic events show "事件".
func newsEpisodeLabel(eventID string) string {
	if n := news.EpisodeNumber(eventID); n > 0 {
		return fmt.Sprintf("股癌EP%d", n)
	}
	return "事件"
}

// newsAge renders a whole-day age label from a publish time ("1天" / "剛更新").
func newsAge(published time.Time) string {
	d := news.AgeInDays(published, time.Now())
	if d <= 0 {
		return "剛更新"
	}
	return fmt.Sprintf("%d天", d)
}

// newsSectors joins a signal's sector labels for display.
func newsSectors(sig news.NewsSignal) string {
	if len(sig.Sectors) == 0 {
		return "族群"
	}
	return strings.Join(sig.Sectors, "、")
}

// joinDot joins labels with a Chinese enumeration comma for the market banner.
func joinDot(xs []string) string { return strings.Join(xs, "、") }

// ── Institutional flow (⑪) + BIAS (⑫) display helpers (R10-1) ────────────────────
// All display-only. Institutional nets are stored in 股 and shown in 張 (÷1000) for
// readability, matching how 三大法人買賣超 is conventionally read.

func fmtLots(shares int64) string {
	lots := int64(0)
	if shares >= 0 {
		lots = (shares + 500) / 1000
	} else {
		lots = -((-shares + 500) / 1000)
	}
	s := fmtThousands(lots)
	if lots > 0 {
		return "+" + s
	}
	return s
}

// fmtThousands adds comma separators to an integer (keeps the sign).
func fmtThousands(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	digits := fmt.Sprintf("%d", v)
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if neg {
		return "-" + out
	}
	return out
}

func instRatioLabel(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%+.2f%%", *p)
}

// instStreakLabel renders 連買/連賣 N 日, flagging incomplete streaks (§8/§10).
func instStreakLabel(s institution.LegStats) string {
	var base string
	switch {
	case s.ConsecutiveBuyDays > 0:
		base = fmt.Sprintf("連買 %d 日", s.ConsecutiveBuyDays)
	case s.ConsecutiveSellDays > 0:
		base = fmt.Sprintf("連賣 %d 日", s.ConsecutiveSellDays)
	default:
		return "—"
	}
	if !s.StreakComplete {
		base += "（⚠資料不完整）"
	}
	return base
}

func instTransitionLabel(t string) string {
	switch t {
	case institution.TransitionBuyToSell:
		return "由買轉賣"
	case institution.TransitionSellToBuy:
		return "由賣轉買"
	default:
		return "—"
	}
}

// instRow renders one institution category as a table row (⑪).
func instRow(name string, s institution.LegStats) template.HTML {
	td := func(v int64) string { return "<td class=\"c\">" + fmtLots(v) + "</td>" }
	return template.HTML(fmt.Sprintf(
		"<tr><td>%s</td>%s%s%s%s<td>%s</td><td>%s</td><td class=\"c\">%s</td></tr>",
		template.HTMLEscapeString(name),
		td(s.TodayNet), td(s.Sum5d), td(s.Sum10d), td(s.Sum20d),
		instStreakLabel(s), instTransitionLabel(s.Transition), instRatioLabel(s.NetBuyRatio),
	))
}

func biasValLabel(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%+.1f%%", *p)
}

func biasRiskLabel(risk string) string {
	switch risk {
	case scanner.BiasRiskExtreme:
		return "EXTREME（追價風險極高）"
	case scanner.BiasRiskHigh:
		return "HIGH（追價風險偏高）"
	case scanner.BiasRiskElevated:
		return "ELEVATED（乖離偏高）"
	case scanner.BiasRiskOversold:
		return "OVERSOLD（超跌區）"
	case scanner.BiasRiskDeeplyOversold:
		return "DEEPLY_OVERSOLD（深度超跌）"
	case scanner.BiasRiskNormal:
		return "NORMAL"
	default:
		return "—"
	}
}

// chipSummary builds the "important events only" summary chips for a watchlist entry
// (§16). Empty when nothing noteworthy — raw data stays in ⑪/⑫, not the summary.
func chipSummary(e scanner.WatchlistEntry) []string {
	var chips []string
	if e.Institution != nil {
		sigs := institution.Classify(*e.Institution, institution.DefaultSignalThresholds())
		for _, s := range sigs {
			name := instCategoryName(s.Category)
			switch s.Type {
			case institution.SignalContinuousBuying:
				chips = append(chips, "🟢 "+name+" "+continuousLabel(e.Institution, s.Category, true))
			case institution.SignalContinuousSelling:
				chips = append(chips, "🔴 "+name+" "+continuousLabel(e.Institution, s.Category, false))
			case institution.SignalBuyToSell:
				chips = append(chips, "🔄 "+name+"由買轉賣")
			case institution.SignalSellToBuy:
				chips = append(chips, "🔄 "+name+"由賣轉買")
			}
		}
	}
	if e.Bias != nil && (e.Bias.Risk == scanner.BiasRiskHigh || e.Bias.Risk == scanner.BiasRiskExtreme) {
		chips = append(chips, "🔴 BIAS20 "+biasValLabel(e.Bias.Bias20)+"，追價風險偏高")
	}
	if e.Bias != nil && (e.Bias.Risk == scanner.BiasRiskOversold || e.Bias.Risk == scanner.BiasRiskDeeplyOversold) {
		chips = append(chips, "🔵 BIAS20 "+biasValLabel(e.Bias.Bias20)+"，超跌區")
	}
	if e.Confluence != nil {
		for _, s := range e.Confluence.Signals {
			chips = append(chips, "🔎 "+s.Note)
		}
	}
	return chips
}

func continuousLabel(v *institution.StockChipView, cat string, buy bool) string {
	s := legFor(v, cat)
	if buy {
		return fmt.Sprintf("連買 %d 日", s.ConsecutiveBuyDays)
	}
	return fmt.Sprintf("連賣 %d 日", s.ConsecutiveSellDays)
}

func legFor(v *institution.StockChipView, cat string) institution.LegStats {
	switch cat {
	case institution.CategoryForeign:
		return v.Foreign
	case institution.CategoryTrust:
		return v.Trust
	case institution.CategoryDealer:
		return v.Dealer
	default:
		return v.Total
	}
}

func instCategoryName(cat string) string {
	switch cat {
	case institution.CategoryForeign:
		return "外資"
	case institution.CategoryTrust:
		return "投信"
	case institution.CategoryDealer:
		return "自營商"
	default:
		return "三大法人"
	}
}

// ── Candlestick (⑬) display helpers (R10-2) ──────────────────────────────────────
// Display-only. The renderer never switches on PatternType for prose — all wording comes
// from PatternSignal.Description; these helpers only format enums/numbers (§12).

func candleDirBadge(d candlestick.Direction) string {
	switch d {
	case candlestick.DirectionBullish:
		return "🟢 Bullish"
	case candlestick.DirectionBearish:
		return "🔴 Bearish"
	default:
		return "⚪ Neutral"
	}
}

func candleConfPct(c float64) string { return fmt.Sprintf("%.0f%%", c*100) }

// candleAdjLabel renders the signed adjustment. Returns template.HTML so the leading "+"
// is not escaped to "&#43;" (the value is a bare signed integer — no HTML metacharacters).
func candleAdjLabel(v int) template.HTML {
	if v > 0 {
		return template.HTML(fmt.Sprintf("+%d", v))
	}
	return template.HTML(fmt.Sprintf("%d", v))
}

func candleGeneric(t candlestick.PatternType) bool { return candlestick.IsGenericFallback(t) }

// candleEvidenceItem is one cell of the ⑬ Evidence row (Phase 8.1). It is derived PURELY from
// PatternSignal.ConfidenceReasons — the renderer maps reason CODES to a glyph and NEVER
// recomputes Confidence / Context / Pattern (§9.2, §12). Glyph is display-only; Class carries
// nothing but colour.
type candleEvidenceItem struct {
	Label string
	Glyph string // "✓" | "✗" | "—"
	Class string // ev-yes | ev-no | ev-none  (CSS colour only)
}

// candleEvidence builds the Evidence row for one signal from its ConfidenceReasons. It is a
// pure `switch code` map — it does not re-analyse context. Capability-aware by design:
//   - Trend / Near Extreme are ✓-or-—, NEVER ✗: a missing TREND_MATCH means "no trend
//     evidence", not "trend is opposite" (§6). "Near Extreme" is direction-agnostic on purpose
//     (Bullish→near low, Bearish→near high); the Description already states which side.
//   - Volume is genuinely tri-state: ✓ VOLUME_CONFIRM / ✗ WEAK_VOLUME / — no volume evidence.
//     "—" means volume was NOT used as evidence — it does NOT mean weak volume.
//   - Stage is OMITTED until STAGE_SUPPORT can actually be credited (never in P0). The item is
//     appended ONLY when the code is present, so P0 reserves no dead column and P1 needs no
//     template/layout change — it lights up the moment the code starts appearing.
func candleEvidence(sig candlestick.PatternSignal) []candleEvidenceItem {
	has := func(code string) bool {
		for _, r := range sig.ConfidenceReasons {
			if r.Code == code {
				return true
			}
		}
		return false
	}
	yesOrNone := func(label string, ok bool) candleEvidenceItem {
		if ok {
			return candleEvidenceItem{Label: label, Glyph: "✓", Class: "ev-yes"}
		}
		return candleEvidenceItem{Label: label, Glyph: "—", Class: "ev-none"}
	}

	vol := candleEvidenceItem{Label: "Volume", Glyph: "—", Class: "ev-none"}
	switch {
	case has(candlestick.ReasonVolumeConfirm):
		vol.Glyph, vol.Class = "✓", "ev-yes"
	case has(candlestick.ReasonWeakVolume):
		vol.Glyph, vol.Class = "✗", "ev-no"
	}

	items := []candleEvidenceItem{
		yesOrNone("Trend", has(candlestick.ReasonTrendMatch)),
		yesOrNone("Near Extreme", has(candlestick.ReasonNearExtreme)),
		vol,
	}
	if has(candlestick.ReasonStageSupport) { // dormant in P0; auto-appears when P1 credits it
		items = append(items, candleEvidenceItem{Label: "Stage", Glyph: "✓", Class: "ev-yes"})
	}
	return items
}

// candleContextLimited reports whether the confidence model recorded MISSING_CONTEXT (unknown
// trend history). It drives a grey "Context Limited" badge so a "—" Trend/Near reads as "data
// insufficient" rather than "evidence genuinely absent" (§6). Read-only over reason codes.
func candleContextLimited(sig candlestick.PatternSignal) bool {
	for _, r := range sig.ConfidenceReasons {
		if r.Code == candlestick.ReasonMissingContext {
			return true
		}
	}
	return false
}

// candleStatusText returns the non-OK status message (empty for OK, where cards render).
func candleStatusText(status string) string {
	switch status {
	case candlestick.StatusNoPattern:
		return "No candlestick pattern detected."
	case candlestick.StatusInvalidBar:
		return "Latest candle invalid."
	case candlestick.StatusNoData:
		return "Insufficient candle data."
	default:
		return ""
	}
}

// candleSummaryChip returns a chip only when the top (highest-confidence) signal is >= 0.80
// (§10). Signals are already analyzer-sorted, so Signals[0] is the top; renderer never re-sorts.
func candleSummaryChip(r *candlestick.Result) string {
	if r == nil || len(r.Signals) == 0 {
		return ""
	}
	top := r.Signals[0]
	if top.Confidence < 0.80 {
		return ""
	}
	return fmt.Sprintf("🕯️ %s %s %s", top.Type, candleDirBadge(top.Direction), candleConfPct(top.Confidence))
}

// actionLabel maps a market/position Action to its Chinese label. The words are chosen
// to match the validator's Chinese action vocabulary (internal/validator/signal.go) so
// historical validation keeps classifying them; the language-independent CSS class
// (action-buy / action-watch …) is what the buy-watch-candidates skill keys off.
func actionLabel(a scanner.Action) string {
	switch a {
	case scanner.ActionStrongBuy:
		return "強勢買進"
	case scanner.ActionBuy:
		return "買進"
	case scanner.ActionWatch:
		return "觀察"
	case scanner.ActionHold:
		return "持有"
	case scanner.ActionReduce:
		return "減碼"
	case scanner.ActionTakeProfit:
		return "停利"
	case scanner.ActionStopLoss:
		return "停損"
	case scanner.ActionSell:
		return "賣出"
	default:
		return string(a)
	}
}

// watchActionText maps a watchlist WatchAction to its Chinese label. Words match the
// validator's Chinese action vocabulary; the language-independent CSS class drives any
// downstream parsing.
func watchActionText(a scanner.WatchAction) string {
	switch a {
	case scanner.ActWait:
		return "等待"
	case scanner.ActWatchClose:
		return "密切觀察"
	case scanner.ActPrepare:
		return "準備進場"
	case scanner.ActBreakoutBuy:
		return "突破買進"
	case scanner.ActPullbackBuy:
		return "拉回買進"
	case scanner.ActTakeProfit:
		return "停利"
	case scanner.ActRemove:
		return "移出觀察"
	default:
		return string(a)
	}
}

type Config struct {
	OutputDir string `yaml:"output_dir"`
}

type Report struct {
	cfg Config
}

func New(cfg Config) *Report {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./reports"
	}
	return &Report{cfg: cfg}
}

type PortfolioSummary struct {
	TotalValue  float64
	TotalCost   float64
	TotalPnL    float64
	TotalPnLPct float64
}

func calcSummary(items []scanner.StockAnalysis) PortfolioSummary {
	var s PortfolioSummary
	for _, a := range items {
		s.TotalValue += a.PortfolioValue()
		s.TotalCost += a.PortfolioCost()
	}
	s.TotalPnL = s.TotalValue - s.TotalCost
	if s.TotalCost > 0 {
		s.TotalPnLPct = s.TotalPnL / s.TotalCost * 100
	}
	return s
}

// trendStateNames maps an R12 state to its display text. The action wording is part of the
// STATE's meaning as the analyzer defined it, not a fresh judgement made at render time —
// EXTENDED in particular must read as a do-not-chase warning, never as a stronger buy.
var trendStateNames = map[scanner.TrendExtState]string{
	scanner.TrendConfirmed:       "TREND_CONFIRMED — 趨勢確認，族群共振",
	scanner.PullbackInUptrend:    "PULLBACK_IN_UPTREND — 上升趨勢中的技術性回檔（僅描述位置，非較佳進場）",
	scanner.TrendExtended:        "EXTENDED — 乖離過大，不要追（Do not chase；進場風險升高，非賣出或看空訊號）",
	scanner.SectorConfirmed:      "SECTOR_CONFIRMED — 族群共振領先，中期均線尚未翻揚",
	scanner.SectorDivergence:     "SECTOR_DIVERGENCE — 個股獨強，族群未共振，信心下修",
	scanner.TrendWeakening:       "WEAKENING — 趨勢向下且價在均線下，非拉回",
	scanner.TrendExtNeutral:      "NEUTRAL — 無明確組合",
	scanner.TrendExtInsufficient: "INSUFFICIENT_DATA — 資料不足",
}

func trendStateText(s scanner.TrendExtState) string {
	if t, ok := trendStateNames[s]; ok {
		return t
	}
	return string(s)
}

// trendStateCSS picks the colour band.
//
// Colour is a description, so it is bound by the same rule as the wording. Two consequences:
//
//   - EXTENDED shares the risk colour so it never reads as a green confirmation. It is an
//     ENTRY-RISK warning, not a sell or a bearish call — the label carries that explicitly;
//   - PULLBACK_IN_UPTREND sits in the NEUTRAL band, not the constructive one. It used to
//     share green with TREND_CONFIRMED, which visually claimed the two were equally good
//     while validation puts them 15 points apart on 20d win rate (29.9% vs 45.1%). Green
//     there was a high-probability-setup claim the data does not support.
func trendStateCSS(s scanner.TrendExtState) string {
	switch s {
	case scanner.TrendConfirmed, scanner.SectorConfirmed:
		return "te-good"
	case scanner.TrendExtended, scanner.TrendWeakening, scanner.SectorDivergence:
		return "te-warn"
	default:
		return "te-flat"
	}
}

// GuardrailViewOptions carries the minimal data report.go needs to DISPLAY the
// C6a/C6b shadow signals & guardrails (plus a couple of unrelated display
// toggles, e.g. ShowBacktestInsights). It is display-only: report never scores.
type GuardrailViewOptions struct {
	Show                    bool
	GuardrailScoringEnabled bool
	RSWatchThreshold        float64

	// ShowBacktestInsights gates the static "🔬 回測洞察" tab. Default false →
	// no such tab. Display-only; never reads reports/r6_*, never touches score /
	// action / probability / sorting / stop / WatchAction.
	ShowBacktestInsights bool

	// ShowETFFlow gates the report ⑨ "ETF Flow / Active Rotation Context" section
	// (R8-4). Default false → no such section. Display-only; never touches score /
	// action / probability / sorting / stop / WatchAction.
	ShowETFFlow bool

	// ShowNews gates the report ⑩ "消息面" per-card section + the market news banner
	// (Phase 1). Default false → no such section/banner and byte-identical output.
	// SHADOW MODE, display-only; never touches score / action / probability / sorting /
	// stop / WatchAction.
	ShowNews bool

	// ShowInstitution gates the report ⑪ "三大法人籌碼" section + summary chips (R10-1).
	// ShowBias gates the report ⑫ "乖離率" section. Both display-only; NEVER touch score /
	// action / probability / sorting / stop / WatchAction.
	ShowInstitution bool
	ShowBias        bool

	// ShowTrendExtension gates the report ⑮ "趨勢／乖離／族群熱度" section (R12).
	// Display-only; the R12 layer NEVER touches score / action / probability / sorting /
	// stop / WatchAction / ranking. When false the section is entirely absent.
	ShowTrendExtension bool

	// ShowAI gates the report ⑭ "AI 分析" section (R11). Default false → no such section
	// and byte-identical output. Display-only; the AI layer NEVER touches score / action /
	// probability / sorting / stop / WatchAction / ranking. When AI is disabled or the
	// analysis was unavailable, the section is simply not rendered.
	ShowAI bool

	// ShowCandlestick gates the report ⑬ "K 線型態" section (R10-2). Display-only; NEVER
	// touches score / action / probability / sorting / stop / WatchAction. When false the
	// section is entirely absent (byte-identical output).
	ShowCandlestick bool

	MFScoreModifierBuilding     float64
	MFScoreModifierContinuation float64
	MFScoreModifierShiftUp      float64
	MFScoreModifierFading       float64
	MFScoreModifierShiftDown    float64
}

// vcpDepths renders a contraction-depth slice as "18→10→5".
func vcpDepths(d []float64) string {
	if len(d) == 0 {
		return "—"
	}
	parts := make([]string, len(d))
	for i, x := range d {
		parts[i] = fmt.Sprintf("%.0f", x)
	}
	return strings.Join(parts, "→")
}

// mfModifier renders the configured score modifier for a momentum flow, e.g. "+5".
// Returns template.HTML so the leading "+" is not escaped to "&#43;" (content is
// only a sign and a number — no injection risk).
func mfModifier(flow scanner.MomentumFlow, gv GuardrailViewOptions) template.HTML {
	var v float64
	switch flow {
	case scanner.MomentumBuilding:
		v = gv.MFScoreModifierBuilding
	case scanner.MomentumContinuation:
		v = gv.MFScoreModifierContinuation
	case scanner.StructuralShiftUp:
		v = gv.MFScoreModifierShiftUp
	case scanner.MomentumFading:
		v = gv.MFScoreModifierFading
	case scanner.StructuralShiftDown:
		v = gv.MFScoreModifierShiftDown
	default:
		return "0"
	}
	return template.HTML(fmt.Sprintf("%+.0f", v))
}

// guardrailSummaryView is a plain-language reading of the guardrail shadow signals.
// DISPLAY-ONLY: derived purely for presentation; never feeds score/action/prob/sort.
// The action lines are decision-support framing (空手/持有/加碼 scenarios), not trade
// instructions.
type guardrailSummaryView struct {
	Headline string
	Pros     []string
	Risks    []string
	Actions  []string
}

// guardrailSummary derives the human-readable summary from an entry's shadow signals.
// rsWatch is the RS watch threshold (GV.RSWatchThreshold) used to call RS "合格".
func guardrailSummary(e scanner.WatchlistEntry, rsWatch float64) guardrailSummaryView {
	var v guardrailSummaryView
	s := e.Shadow
	if s == nil {
		v.Headline = "尚無多因子訊號（未啟用計算）。"
		return v
	}

	var near52, fullBull, strongMTF, fading, shiftDown bool

	if s.RS != nil && s.RS.Computed {
		if s.RS.RSRankPercentile >= rsWatch {
			v.Pros = append(v.Pros, fmt.Sprintf("RS 相對強度合格（百分位 %.0f）", s.RS.RSRankPercentile))
		} else {
			v.Risks = append(v.Risks, fmt.Sprintf("RS 相對強度偏弱（百分位 %.0f）", s.RS.RSRankPercentile))
		}
	}
	if s.NewHigh != nil && s.NewHigh.Computed && (s.NewHigh.Near52wHigh || s.NewHigh.BreakoutWatch) {
		near52 = true
		v.Pros = append(v.Pros, "接近 / 創 52 週高")
	}
	if s.VCP != nil && s.VCP.Computed && s.VCP.Valid {
		v.Pros = append(v.Pros, fmt.Sprintf("VCP 型態有效（品質 %.0f）", s.VCP.QualityScore))
	}
	if m := s.MultiTimeframe; m != nil {
		switch m.AlignmentLabel {
		case "FULL_BULL":
			fullBull = true
			v.Pros = append(v.Pros, "日週趨勢共振（多頭）")
		case "CONFLICT":
			v.Risks = append(v.Risks, "日週多空不一致")
		}
		if m.SignalStrength == "STRONG" {
			strongMTF = true
		}
		switch m.LongTermFilter {
		case "BULLISH":
			v.Pros = append(v.Pros, "站上 200 日線（長期偏多）")
		case "BEARISH":
			v.Risks = append(v.Risks, "位於 200 日線下（長期偏空）")
		}
	}
	if s.Momentum != nil && s.Momentum.Computed {
		switch s.Momentum.Flow {
		case scanner.MomentumFading:
			fading = true
			v.Risks = append(v.Risks, "短線動能轉弱（FADING）")
		case scanner.StructuralShiftDown:
			shiftDown = true
			v.Risks = append(v.Risks, "動能結構轉空（SHIFT_DOWN）")
		}
		if s.Momentum.Divergence {
			v.Risks = append(v.Risks, "出現量價 / 動能背離")
		}
	}
	if near52 && (fading || shiftDown) {
		v.Risks = append(v.Risks, "高位追價風險")
	}

	strongBase := len(v.Pros) > 0

	switch {
	case shiftDown:
		v.Headline = "動能轉空，優先保護部位、避免追高。"
	case len(v.Risks) > 0 && containsConflict(s):
		v.Headline = "多週期方向分歧，宜等待一致性再行動。"
	case strongBase && fading:
		v.Headline = "強勢候選，但短線動能轉弱，暫不追高。"
	case strongBase && strongMTF && fullBull:
		v.Headline = "日週同步走強，型態與動能偏多。"
	case strongBase:
		v.Headline = "具優勢條件，留意風險後再評估介入時點。"
	case len(v.Risks) > 0:
		v.Headline = "訊號偏弱，觀望為宜。"
	default:
		v.Headline = "訊號中性，資料有限。"
	}

	switch {
	case fading || shiftDown:
		v.Actions = []string{
			"空手：等回測或重新放量轉強再評估",
			"持有：可續抱，但提高停利 / 收緊移動停損",
			"加碼：等 MomentumFlow 由 FADING 轉回 BUILDING / CONTINUATION",
		}
	case containsConflict(s):
		v.Actions = []string{
			"空手：等日週方向一致再評估",
			"持有：以較緊停損控管多週期不確定性",
			"加碼：暫不考慮，待趨勢共振成形",
		}
	case strongBase:
		v.Actions = []string{
			"空手：可分批觀察進場，依突破 / 量能確認",
			"持有：續抱，以移動停損保護獲利",
			"加碼：型態續強且未過熱時再評估",
		}
	default:
		v.Actions = []string{
			"空手：觀望，等更明確訊號",
			"持有：依既有停損紀律",
			"加碼：暫不考慮",
		}
	}
	return v
}

func containsConflict(s *scanner.ShadowSignals) bool {
	return s != nil && s.MultiTimeframe != nil && s.MultiTimeframe.AlignmentLabel == "CONFLICT"
}

// mtfLTFLabel renders the multi-timeframe long-term (200MA) filter for display.
func mtfLTFLabel(s string) string {
	switch s {
	case "BULLISH":
		return "200日線上"
	case "BEARISH":
		return "200日線下"
	default:
		return "未知"
	}
}

type reportData struct {
	Date         string
	MarketLabel  string
	Market       []scanner.StockAnalysis
	Portfolio    []scanner.StockAnalysis
	Watchlist    []scanner.WatchlistEntry
	Rotation     []scanner.SectorRotation
	PortfolioSum PortfolioSummary
	GV           GuardrailViewOptions
	NewsSummary  *news.MarketNewsSummary
	NewsEpisodes []news.EpisodeView
}

// OutputPath returns the HTML report path the scanner writes for the given date.
func (r *Report) OutputPath(date time.Time) string {
	return filepath.Join(r.cfg.OutputDir, fmt.Sprintf("report_%s.html", date.Format("20060102")))
}

func (r *Report) Generate(
	market, portfolio []scanner.StockAnalysis,
	watchlist []scanner.WatchlistEntry,
	rotation []scanner.SectorRotation,
	marketLabel string,
	date time.Time,
	gv GuardrailViewOptions,
	newsSummary *news.MarketNewsSummary,
	newsEpisodes []news.EpisodeView,
) error {
	if err := os.MkdirAll(r.cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if marketLabel == "" {
		marketLabel = "-"
	}
	data := reportData{
		Date:         date.Format("2006-01-02"),
		MarketLabel:  marketLabel,
		Market:       market,
		Portfolio:    portfolio,
		Watchlist:    watchlist,
		Rotation:     rotation,
		PortfolioSum: calcSummary(portfolio),
		GV:           gv,
		NewsSummary:  newsSummary,
		NewsEpisodes: newsEpisodes,
	}

	fname := r.OutputPath(date)
	f, err := os.Create(fname)
	if err != nil {
		return fmt.Errorf("create %s: %w", fname, err)
	}
	defer f.Close()

	funcs := template.FuncMap{
		// R12 formatters. They FORMAT and nothing else: every state, code and component
		// value is decided in internal/scanner and rendered verbatim here. The renderer
		// must never re-derive strategy.
		"slopeLabel": func(v *float64) string {
			if v == nil {
				return "—"
			}
			return fmt.Sprintf("%+.3f%% / 日", *v)
		},
		"pctPtr": func(v *float64) string {
			if v == nil {
				return "—"
			}
			return fmt.Sprintf("%+.2f%%", *v)
		},
		"rankPtr": func(v *float64) string {
			if v == nil {
				return "—"
			}
			return fmt.Sprintf("%.0f 分位", *v)
		},
		"heatOK": func(h *scanner.SectorHeatView) bool {
			return h != nil && h.Status == scanner.SectorHeatOK
		},
		"trendStateText": trendStateText,
		"trendStateCSS":  trendStateCSS,
		// R11 AI display helpers. aiOK is what keeps the ⑭ section from rendering an empty
		// shell when the analysis was unavailable — the report simply omits it, matching how
		// ⑨/⑩/⑬ handle their own unavailable states.
		"aiOK": func(a *ai.Analysis) bool { return a.OK() },
		"aiConfPct": func(c float64) string {
			if c <= 0 {
				return "—"
			}
			return fmt.Sprintf("%.0f%%", c*100)
		},
		"f2":      func(v float64) string { return fmt.Sprintf("%.2f", v) },
		"f1":      func(v float64) string { return fmt.Sprintf("%.1f", v) },
		"pct":     func(v float64) string { return fmt.Sprintf("%.1f%%", v) },
		"pctSign": func(v float64) string { return fmt.Sprintf("%+.1f%%", v) },
		"fmtMoney": func(v float64) string {
			if v < 0 {
				return fmt.Sprintf("▼ %.0f", -v)
			}
			return fmt.Sprintf("%.0f", v)
		},
		"fmtVol":            fmtVolume,
		"instRow":           instRow,
		"instStreakLabel":   instStreakLabel,
		"biasValLabel":      biasValLabel,
		"biasRiskLabel":     biasRiskLabel,
		"chipSummary":       chipSummary,
		"candleDir":         candleDirBadge,
		"candleConfPct":     candleConfPct,
		"candleAdjLabel":    candleAdjLabel,
		"candleGeneric":     candleGeneric,
		"candleStatusText":  candleStatusText,
		"candleSummaryChip": candleSummaryChip,
		"candleEvidence":    candleEvidence,
		"candleCtxLimited":  candleContextLimited,
		"etfSignalLabel":    etfSignalLabel,
		"newsSignalLabel":   newsSignalLabel,
		"newsDot":           newsDot,
		"newsEpisodeLabel":  newsEpisodeLabel,
		"newsAge":           newsAge,
		"newsSectors":       newsSectors,
		"joinDot":           joinDot,
		"inc":               func(i int) int { return i + 1 },
		"intsSlash": func(xs []int) string {
			parts := make([]string, len(xs))
			for i, x := range xs {
				parts[i] = fmt.Sprintf("%d", x)
			}
			return strings.Join(parts, " / ")
		},
		"actionCSS": func(a scanner.Action) string {
			return scanner.ActionCSS[a]
		},
		"pnlCSS": func(v float64) string {
			if v > 0 {
				return "pos"
			}
			if v < 0 {
				return "neg"
			}
			return "neu"
		},
		"volCSS": func(r float64) string {
			if r >= 1.5 {
				return "pos"
			}
			if r < 0.8 && r > 0 {
				return "neg"
			}
			return "neu"
		},
		"rsiCSS": func(rsi float64) string {
			if rsi < 30 {
				return "pos"
			}
			if rsi > 70 {
				return "neg"
			}
			return "neu"
		},
		"sub50": func(v float64) float64 { return v - 50 },
		"joinReasons": func(rs []string) template.HTML {
			var parts []string
			for _, r := range rs {
				if r != "" {
					parts = append(parts, template.HTMLEscapeString(r))
				}
			}
			return template.HTML(strings.Join(parts, "<br>"))
		},
		"bfpDots": func(points int) template.HTML {
			s := ""
			for i := 1; i <= 5; i++ {
				if i <= points {
					s += `<span class="bfp-dot pass">●</span>`
				} else {
					s += `<span class="bfp-dot fail">○</span>`
				}
			}
			return template.HTML(fmt.Sprintf(`<span class="bfp-wrap">%s <span class="bfp-count">%d/5</span></span>`, s, points))
		},
		"scoreBar": func(s int) template.HTML {
			cls := "bar-low"
			if s >= 62 {
				cls = "bar-high"
			} else if s >= 47 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, s, s))
		},
		"pvCSS": func(sig string) string {
			switch sig {
			case "價漲量增":
				return "pv-up-vol-up"
			case "價漲量縮":
				return "pv-up-vol-down"
			case "價跌量增":
				return "pv-down-vol-up"
			case "漲停鎖量":
				return "pv-locked"
			case "漲停失敗":
				return "pv-failed"
			default:
				return "pv-down-vol-down"
			}
		},
		"f0pct":    func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
		"pctSign1": func(v float64) string { return fmt.Sprintf("%+.1f%%", v) },
		"sectorScoreBar": func(v float64) template.HTML {
			s := int(v + 0.5)
			cls := "bar-low"
			if s >= 70 {
				cls = "bar-high"
			} else if s >= 50 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, s, s))
		},
		"stageCSS": func(s scanner.RotationStage) string {
			switch s {
			case scanner.EarlyRotation:
				return "stage-early"
			case scanner.ConfirmedRotation:
				return "stage-confirmed"
			case scanner.HotRotation:
				return "stage-hot"
			case scanner.LateRotation:
				return "stage-late"
			default:
				return "stage-early"
			}
		},
		"stageLabel": func(s scanner.RotationStage) string {
			switch s {
			case scanner.EarlyRotation:
				return "醞釀 EARLY"
			case scanner.ConfirmedRotation:
				return "確認 CONFIRMED"
			case scanner.HotRotation:
				return "過熱 HOT"
			case scanner.LateRotation:
				return "末段 LATE"
			default:
				return string(s)
			}
		},
		"boolMark": func(b bool) template.HTML {
			if b {
				return template.HTML(`<span class="yes">✓</span>`)
			}
			return template.HTML(`<span class="no">·</span>`)
		},
		"flowCSS": func(state string) string {
			switch state {
			case "流入":
				return "flow-in"
			case "流出":
				return "flow-out"
			default:
				return "flow-neutral"
			}
		},
		"flowLabel": func(state string) string {
			switch state {
			case "流入":
				return "流入 ↑"
			case "流出":
				return "流出 ↓"
			default:
				return "中性"
			}
		},
		"flowArrow": func(v float64) template.HTML {
			switch {
			case v >= 0.2:
				return template.HTML(`<span class="flow-in">↑</span>`)
			case v <= -0.2:
				return template.HTML(`<span class="flow-out">↓</span>`)
			default:
				return template.HTML(`<span class="flow-neutral">→</span>`)
			}
		},
		"shortDirCSS": func(dir string) string {
			switch dir {
			case scanner.FlowInflow:
				return "flow-in"
			case scanner.FlowOutflow:
				return "flow-out"
			default:
				return "flow-neutral"
			}
		},
		"shortDirLabel": func(dir string) string {
			switch dir {
			case scanner.FlowInflow:
				return "流入 ↑"
			case scanner.FlowOutflow:
				return "流出 ↓"
			default:
				return "中性"
			}
		},
		"shortStageCSS": func(st string) string {
			switch st {
			case scanner.STEarlyRotation:
				return "stage-early"
			case scanner.STConfirmedRotation:
				return "stage-confirmed"
			case scanner.STOverheated:
				return "stage-hot"
			default: // WEAKENING
				return "stage-late"
			}
		},
		"shortStageLabel": func(st string) string {
			switch st {
			case scanner.STEarlyRotation:
				return "早期輪動 EARLY"
			case scanner.STConfirmedRotation:
				return "確認輪動 CONFIRMED"
			case scanner.STOverheated:
				return "過熱 OVERHEATED"
			default:
				return "轉弱 WEAKENING"
			}
		},
		"midCSS": func(label string) string {
			switch label {
			case "強":
				return "lvl-strong"
			case "中":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"trendCSS": func(label string) string {
			switch label {
			case "確認上升":
				return "lvl-strong"
			case "尚未確認":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"rocketStageLabel": func(st scanner.RocketStage) string { return rocketStageText(st) },
		"rocketStageCSS": func(st scanner.RocketStage) string {
			switch st {
			case scanner.StagePreBreakout, scanner.StageBreakoutStart:
				return "rk-go"
			case scanner.StageMainRun:
				return "rk-run"
			case scanner.StageBaseBuilding:
				return "rk-base"
			case scanner.StageOverheated:
				return "rk-hot"
			case scanner.StageFailed:
				return "rk-fail"
			default:
				return "rk-wait"
			}
		},
		"watchActionLabel": watchActionText,
		"actionLabel":      actionLabel,
		"watchActionCSS": func(a scanner.WatchAction) string {
			switch a {
			case scanner.ActBreakoutBuy, scanner.ActPullbackBuy:
				return "act-buy"
			case scanner.ActPrepare:
				return "act-prepare"
			case scanner.ActWatchClose:
				return "act-watch"
			case scanner.ActTakeProfit:
				return "act-tp"
			case scanner.ActRemove:
				return "act-remove"
			default:
				return "act-wait"
			}
		},
		// stagePriority：階段排序優先級（數字越大＝越接近噴出，DESC 時排最前）
		"stagePriority": func(st scanner.RocketStage) int {
			switch st {
			case scanner.StageBreakoutStart:
				return 7 // 起漲 — 最可能噴
			case scanner.StagePreBreakout:
				return 6 // 突破前 — 最接近突破
			case scanner.StageMainRun:
				return 5 // 主升
			case scanner.StageBaseBuilding:
				return 4 // 築底 — 需要準備
			case scanner.StageOverheated:
				return 3 // 過熱
			case scanner.StageNotReady:
				return 2 // 未就緒
			case scanner.StageFailed:
				return 1 // 失敗
			default:
				return 0
			}
		},
		// actionPriority：操作建議排序優先級（數字越大＝越該出手，DESC 時排最前）
		"actionPriority": func(a scanner.WatchAction) int {
			switch a {
			case scanner.ActBreakoutBuy:
				return 7 // 突破買進
			case scanner.ActPullbackBuy:
				return 6 // 回拉買進
			case scanner.ActPrepare:
				return 5 // 準備進場
			case scanner.ActWatchClose:
				return 4 // 密切觀察
			case scanner.ActTakeProfit:
				return 3 // 停利
			case scanner.ActWait:
				return 2 // 等待
			case scanner.ActRemove:
				return 1 // 移除
			default:
				return 0
			}
		},
		// riskPriority：風險排序優先級（數字越大＝風險越高，DESC 時排最前）
		"riskPriority": func(label string) int {
			switch label {
			case "跌破支撐":
				return 7
			case "追高":
				return 6
			case "族群轉弱":
				return 5
			case "假突破":
				return 4
			case "量不足":
				return 3
			case "上影/收弱":
				return 2
			default: // "—" 或無風險
				return 1
			}
		},
		"bucketLabel": func(b scanner.ConsolBucket) string {
			switch b {
			case scanner.MicroBase:
				return "極短整理 MICRO (3~5日)"
			case scanner.ShortBase:
				return "短線平台 SHORT (6~10日)"
			case scanner.SwingBase:
				return "波段平台 SWING (11~20日)"
			case scanner.MidBase:
				return "中期平台 MID (21~40日)"
			case scanner.LongBase:
				return "長期底部 LONG (41~60日)"
			default:
				return "無明顯整理"
			}
		},
		"confCSS": func(c string) string {
			switch c {
			case "HIGH":
				return "lvl-strong"
			case "MEDIUM":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"rocketGauge": func(score int) template.HTML {
			cls := "bar-low"
			if score >= 75 {
				cls = "bar-high"
			} else if score >= 60 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, score, score))
		},
		"riskCSS": func(label string) string {
			if label == "—" || label == "" {
				return "lvl-weak"
			}
			return "risk-tag"
		},
		"probCSS": func(p string) string {
			switch p {
			case "HIGH":
				return "lvl-strong"
			case "MEDIUM":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"vcpDepths":        vcpDepths,
		"mfModifier":       mfModifier,
		"mtfLTFLabel":      mtfLTFLabel,
		"guardrailSummary": guardrailSummary,
	}

	tmpl, err := template.New("report").Funcs(funcs).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("render: %w", err)
	}

	log.Printf("report: %s", fname)
	printConsole(market, portfolio, watchlist, rotation, marketLabel, date)
	return nil
}

func stageText(s scanner.RotationStage) string {
	switch s {
	case scanner.EarlyRotation:
		return "醞釀 EARLY"
	case scanner.ConfirmedRotation:
		return "確認 CONFIRMED"
	case scanner.HotRotation:
		return "過熱 HOT"
	case scanner.LateRotation:
		return "末段 LATE"
	default:
		return string(s)
	}
}

func shortDirText(dir string) string {
	switch dir {
	case scanner.FlowInflow:
		return "流入↑"
	case scanner.FlowOutflow:
		return "流出↓"
	default:
		return "中性"
	}
}

func shortStageText(st string) string {
	switch st {
	case scanner.STEarlyRotation:
		return "早期輪動"
	case scanner.STConfirmedRotation:
		return "確認輪動"
	case scanner.STOverheated:
		return "過熱"
	default:
		return "轉弱"
	}
}

func fmtVolume(v int64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(v)/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.0fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func printConsole(market, portfolio []scanner.StockAnalysis, watchlist []scanner.WatchlistEntry, rotation []scanner.SectorRotation, marketLabel string, date time.Time) {
	sep := "═══════════════════════════════════════════════════════════════════"
	fmt.Printf("\n%s\n  台股掃描報告  %s\n%s\n", sep, date.Format("2006-01-02"), sep)

	if len(rotation) > 0 {
		fmt.Printf("\n[🔄 族群輪動 (Rotation) — 三層：短線(1~5日)/中期(20日)/趨勢(60日)，依機會排序]\n")
		fmt.Printf("%-4s  %-14s  %-8s  %-14s  %6s  %4s  %-10s  %6s\n",
			"Rank", "族群", "短線流向", "短線階段", "短線分", "20日", "60日趨勢", "機會分")
		var earlyCandidates []string
		for i, sr := range rotation {
			fmt.Printf("%-4d  %-14s  %-8s  %-14s  %6.0f  %4s  %-10s  %6.0f\n",
				i+1, sr.Name, shortDirText(sr.ShortTermFlowDir), shortStageText(sr.ShortTermFlowStage),
				sr.ShortTermFlowScore, sr.MidTermLabel, sr.TrendLabel, sr.OppScore)
			if sr.ShortTermFlowStage == scanner.STEarlyRotation && sr.ShortTermFlowDir == scanner.FlowInflow {
				earlyCandidates = append(earlyCandidates, sr.Name)
			}
		}
		if len(earlyCandidates) > 0 {
			fmt.Printf("  ▶ 早期輪動候選（資金剛流入、20日尚未反映）：%s\n", strings.Join(earlyCandidates, "、"))
		}
	}

	if len(portfolio) > 0 {
		fmt.Printf("\n[💼 持倉 (Positions)]\n")
		fmt.Printf("%-6s  %-8s  %7s  %7s  %8s  %-12s\n",
			"代號", "名稱", "成本", "現價", "損益%", "建議")
		for _, a := range portfolio {
			fmt.Printf("%-6s  %-8s  %7.2f  %7.2f  %+8.1f%%  %-12s\n",
				a.Symbol, a.Name, a.CostBasis, a.Close, a.PnLPct, a.Action)
		}
		sum := calcSummary(portfolio)
		fmt.Printf("  ▶ 總市值 %.0f  總成本 %.0f  損益 %+.0f (%+.1f%%)\n",
			sum.TotalValue, sum.TotalCost, sum.TotalPnL, sum.TotalPnLPct)
	}

	if len(watchlist) > 0 {
		fmt.Printf("\n[🚀 觀察清單 — 飆股候選追蹤]\n")
		fmt.Printf("%-6s  %-9s  %-12s  %5s  %-15s  %-22s  %s\n",
			"代號", "名稱", "族群", "飆股分", "階段", "操作建議", "風險")
		var imminent []string
		for _, e := range watchlist {
			fmt.Printf("%-6s  %-9s  %-12s  %5d  %-15s  %-22s  %s\n",
				e.A.Symbol, e.A.Name, e.Sector, e.RocketScore,
				rocketStageText(e.RocketStage), string(e.WatchAction), e.RiskLabel)
			if e.RocketStage == scanner.StagePreBreakout || e.RocketStage == scanner.StageBreakoutStart {
				imminent = append(imminent, fmt.Sprintf("%s(%d)", e.A.Name, e.RocketScore))
			}
		}
		if len(imminent) > 0 {
			fmt.Printf("  ▶ 最接近發動：%s\n", strings.Join(imminent, "、"))
		}
	}

	if len(market) > 0 {
		fmt.Printf("\n[📊 市場掃描(Top %s)]\n", marketLabel)
		fmt.Printf("%-6s  %-8s  %7s  %4s  %3s/5  %-12s  %7s  %7s\n",
			"代號", "名稱", "現價", "分數", "BFP", "建議", "停損", "目標1")
		for _, a := range market {
			fmt.Printf("%-6s  %-8s  %7.2f  %4d  %5d  %-12s  %7.2f  %7.2f\n",
				a.Symbol, a.Name, a.Close, a.Score, a.BFPPoints, a.Action,
				a.StopLoss, a.Target1)
		}
	}

	// Detailed reasons for positions
	if len(portfolio) > 0 {
		fmt.Printf("\n[持倉原因詳細]\n")
		for _, a := range portfolio {
			fmt.Printf("  %s %s:\n", a.Symbol, a.Name)
			for _, r := range a.Reasons {
				if r != "" {
					fmt.Printf("    %s\n", r)
				}
			}
			fmt.Printf("    → 進場 %.2f  停損 %.2f  目標1 %.2f  目標2 %.2f\n\n",
				a.EntryPrice, a.StopLoss, a.Target1, a.Target2)
		}
	}

	// Watchlist decision detail
	if len(watchlist) > 0 {
		fmt.Printf("\n[飆股候選決策詳細]\n")
		for _, e := range watchlist {
			fmt.Printf("  %s %s｜%s｜%s｜飆股分 %d｜噴出機率 %s｜觀察 %s\n",
				e.A.Symbol, e.A.Name, e.Sector, rocketStageText(e.RocketStage),
				e.RocketScore, e.ExplosionProb, e.DaysToWatch)
			fmt.Printf("    輪動: 短線 %s／20日 %s／族群階段 %s\n",
				shortDirText(e.SectorFlowDir), e.SectorMidLabel, stageText(e.SectorStage))
			fmt.Printf("    型態: %s／整理 %d 天／回測 %s 個股勝率 %.0f%%(%d)／族群勝率 %.0f%%(%d)／信心 %s\n",
				bucketText(e.Consol.Bucket), e.Consol.Days, e.Backtest.PatternName,
				e.Backtest.StockWinRate, e.Backtest.StockSampleCount,
				e.Backtest.SectorWinRate, e.Backtest.SectorSampleCount, e.Backtest.Confidence)
			fmt.Printf("    價位: 現價 %.2f／進場 %s／突破 %.2f／支撐 %.2f／停損 %.2f／停利 %s\n",
				e.A.Close, e.EntryZone, e.BreakoutPrice, e.SupportPrice, e.StopLossPrice, e.TakeProfitZone)
			fmt.Printf("    操作: %s｜風險: %s\n\n", string(e.WatchAction), e.RiskWarning)
		}
	}
}

func rocketStageText(st scanner.RocketStage) string {
	switch st {
	case scanner.StageNotReady:
		return "未就緒 NOT_READY"
	case scanner.StageBaseBuilding:
		return "築底 BASE_BUILDING"
	case scanner.StagePreBreakout:
		return "突破前 PRE_BREAKOUT"
	case scanner.StageBreakoutStart:
		return "起漲 BREAKOUT_START"
	case scanner.StageMainRun:
		return "主升 MAIN_RUN"
	case scanner.StageOverheated:
		return "過熱 OVERHEATED"
	case scanner.StageFailed:
		return "失敗 FAILED"
	default:
		return string(st)
	}
}

func bucketText(b scanner.ConsolBucket) string {
	switch b {
	case scanner.MicroBase:
		return "MICRO"
	case scanner.ShortBase:
		return "SHORT"
	case scanner.SwingBase:
		return "SWING"
	case scanner.MidBase:
		return "MID"
	case scanner.LongBase:
		return "LONG"
	default:
		return "NO_BASE"
	}
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="zh-Hant">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>股票雷達 {{ .Date }}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,"PingFang TC","Noto Sans TC",sans-serif;background:#0c1220;color:#e2e8f0;min-height:100vh;font-size:13px}
.container{max-width:1800px;margin:0 auto;padding:18px 14px}
h1{font-size:1.4rem;font-weight:700;color:#f8fafc;border-bottom:2px solid #1e3a5f;padding-bottom:10px;margin-bottom:14px}
h1 small{font-size:.78rem;color:#64748b;font-weight:400;margin-left:8px}

/* Tabs */
.tabs{display:flex;gap:0;margin-bottom:18px;border-bottom:2px solid #1e3a5f}
.tab-btn{padding:9px 20px;background:none;border:none;color:#64748b;font-size:.85rem;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;font-family:inherit;transition:all .15s}
.tab-btn:hover{color:#94a3b8}
.tab-btn.active{color:#38bdf8;border-bottom-color:#38bdf8;font-weight:600}
.tab-pane{display:none}.tab-pane.active{display:block}

/* Tables */
table{width:100%;border-collapse:collapse;background:#111827;border-radius:10px;overflow:hidden;margin-bottom:6px}
thead{background:#0c1220}
th{padding:8px 10px;text-align:left;font-weight:600;color:#475569;font-size:.68rem;text-transform:uppercase;letter-spacing:.04em;border-bottom:1px solid #1e3a5f;white-space:nowrap}
th.sortable{cursor:pointer;user-select:none}
th.sortable:hover{color:#93c5fd}
th.sort-active{color:#60a5fa}
.sort-ico{display:inline-block;width:.8em;color:#60a5fa;font-size:.85em}
th.r,td.r{text-align:right}
td{padding:7px 10px;border-bottom:1px solid #131e2e;vertical-align:top}
tr:last-child td{border-bottom:none}
tr:hover td{background:#0f1d30}

/* Action badges */
.action-badge{display:inline-block;padding:3px 8px;border-radius:4px;font-weight:700;font-size:.71rem;white-space:nowrap;letter-spacing:.02em}
.action-strong-buy{background:#052e16;color:#4ade80;border:1px solid #16a34a}
.action-buy{background:#0f2d1a;color:#86efac;border:1px solid #22c55e66}
.action-watch{background:#0c2340;color:#7dd3fc;border:1px solid #0284c766}
.action-hold{background:#1c1500;color:#fcd34d;border:1px solid #ca8a0466}
.action-reduce{background:#2d1200;color:#fdba74;border:1px solid #ea580c66}
.action-take-profit{background:#052e16;color:#a3e635;border:2px solid #65a30d;letter-spacing:.05em}
.action-stop-loss{background:#3b0a0a;color:#fca5a5;border:2px solid #dc2626;font-size:.75rem}
.action-sell{background:#1c0202;color:#f87171;border:1px solid #dc262666}

/* Score bar */
.score-bar{display:flex;align-items:center;gap:5px}
.score-bar>div{height:5px;border-radius:3px;min-width:2px}
.bar-high{background:#4ade80}.bar-mid{background:#fbbf24}.bar-low{background:#f87171}
.score-bar span{font-weight:700;font-size:.8rem;color:#f1f5f9;min-width:24px}

/* BFP dots */
.bfp-wrap{white-space:nowrap;font-size:.85rem}
.bfp-dot{margin:0 1px}
.bfp-dot.pass{color:#4ade80}.bfp-dot.fail{color:#374151}
.bfp-count{font-size:.72rem;color:#94a3b8;margin-left:3px}

/* Price targets */
.t-entry{color:#38bdf8!important}.t-stop{color:#f87171!important}
.t-t1{color:#4ade80!important}.t-t2{color:#a3e635!important}

/* P&L */
.pos{color:#4ade80}.neg{color:#f87171}.neu{color:#94a3b8}
.sym{font-weight:700;color:#f8fafc}.name-col{color:#94a3b8}

/* Reasons */
.reasons{font-size:.71rem;color:#94a3b8;line-height:1.6;max-width:360px}

/* Price-Volume signal */
.pv-up-vol-up{color:#4ade80;font-weight:600}
.pv-up-vol-down{color:#fbbf24}
.pv-down-vol-up{color:#f87171;font-weight:600}
.pv-down-vol-down{color:#64748b}
.pv-locked{color:#a3e635;font-weight:700}
.pv-failed{color:#f87171;font-weight:700}

/* Portfolio summary */
.pf-summary{background:#111827;border:1px solid #1e3a5f;border-radius:10px;padding:14px 20px;margin-bottom:14px;display:flex;gap:24px;flex-wrap:wrap;align-items:center}
.pf-item label{display:block;font-size:.68rem;color:#475569;text-transform:uppercase;letter-spacing:.05em;margin-bottom:2px}
.pf-item .val{font-size:1.15rem;font-weight:700}

/* Alert banner for STOP LOSS / TAKE PROFIT */
.alert-sl{background:#3b0a0a55;border:1px solid #dc262677;border-radius:6px;padding:4px 10px;font-size:.72rem;color:#fca5a5;margin-bottom:2px}
.alert-tp{background:#05280f55;border:1px solid #16a34a77;border-radius:6px;padding:4px 10px;font-size:.72rem;color:#86efac;margin-bottom:2px}

.empty{text-align:center;padding:30px;color:#475569;font-size:.9rem}
footer{margin-top:20px;font-size:.68rem;color:#374151;text-align:center;padding:10px 0}

/* Volume score badge */
.vol-score{display:inline-block;background:#0c1f3a;border:1px solid #1e3a5f;border-radius:4px;padding:1px 6px;font-size:.7rem;color:#38bdf8;font-weight:600}

/* ── Rotation ─────────────────────────────────────────────────────────────── */
.rot-intro{background:#0d1a2e;border:1px solid #1e3a5f;border-radius:8px;padding:10px 14px;margin-bottom:12px;font-size:.76rem;color:#94a3b8;line-height:1.7}
.rot-intro b{color:#e2e8f0}
th.c,td.c{text-align:center}
th.rotscore{min-width:120px}
.stage-badge{display:inline-block;padding:3px 9px;border-radius:4px;font-weight:700;font-size:.7rem;white-space:nowrap;letter-spacing:.02em}
.stage-early{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}          /* 機會：醞釀 */
.stage-confirmed{background:#052e16;color:#4ade80;border:1px solid #16a34a}      /* 確認 */
.stage-hot{background:#2d1200;color:#fdba74;border:1px solid #ea580c}            /* 過熱（淡化） */
.stage-late{background:#1a1a1a;color:#9ca3af;border:1px solid #4b5563}           /* 末段（淡化） */
.sector-row{cursor:pointer}
.sector-row:hover td{background:#10243d}
.exp-caret{color:#64748b;text-align:center;font-size:.8rem}
.sector-detail{display:none}
.sector-detail.open{display:table-row}
.sector-detail>td{padding:0 0 10px 0;background:#0b1422}
.sub-table{margin:0;border-radius:0;background:#0b1422;box-shadow:inset 3px 0 0 #1e3a5f}
.sub-table th{background:#0b1422;color:#475569}
.sub-table td{border-bottom:1px solid #111c2c}
.yes{color:#4ade80;font-weight:700}
.no{color:#475569}
.flow-badge{display:inline-block;padding:2px 8px;border-radius:4px;font-weight:700;font-size:.7rem;white-space:nowrap}
.flow-in{color:#4ade80}
.flow-out{color:#f87171}
.flow-neutral{color:#94a3b8}
.flow-badge.flow-in{background:#052e16;border:1px solid #16a34a66}
.flow-badge.flow-out{background:#3b0a0a;border:1px solid #dc262666}
.flow-badge.flow-neutral{background:#1a2436;border:1px solid #33415566}
.lvl-strong{color:#4ade80;font-weight:700}
.lvl-mid{color:#fbbf24;font-weight:600}
.lvl-weak{color:#94a3b8}
.sector-meta{padding:8px 14px;font-size:.72rem;color:#94a3b8;line-height:1.8;background:#0b1422}
.meta-concl{display:block;color:#e2e8f0;font-weight:600;font-size:.78rem;margin-bottom:3px}
.meta-nums b{font-weight:700}

/* ── Watchlist 飆股候選 ───────────────────────────────────────────────────── */
.rk-go{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}      /* PRE/BREAKOUT */
.rk-run{background:#052e16;color:#4ade80;border:1px solid #16a34a}      /* MAIN_RUN */
.rk-base{background:#1c2433;color:#cbd5e1;border:1px solid #475569}     /* BASE_BUILDING */
.rk-hot{background:#2d1200;color:#fdba74;border:1px solid #ea580c}      /* OVERHEATED */
.rk-fail{background:#3b0a0a;color:#fca5a5;border:1px solid #dc2626}     /* FAILED */
.rk-wait{background:#161e2e;color:#94a3b8;border:1px solid #334155}     /* NOT_READY */
.act-badge{display:inline-block;padding:2px 8px;border-radius:4px;font-weight:700;font-size:.68rem;white-space:nowrap}
.act-buy{background:#052e16;color:#4ade80;border:1px solid #16a34a}
.act-prepare{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}
.act-watch{background:#1c2433;color:#cbd5e1;border:1px solid #475569}
.act-tp{background:#052e16;color:#a3e635;border:1px solid #65a30d}
.act-remove{background:#3b0a0a;color:#fca5a5;border:1px solid #dc2626}
.act-wait{background:#161e2e;color:#94a3b8;border:1px solid #334155}
.risk-tag{display:inline-block;color:#fca5a5;font-weight:600;font-size:.72rem}
.wl-card{padding:12px 16px;background:#0b1422}
.wl-head{font-size:.85rem;color:#e2e8f0;font-weight:600;padding-bottom:10px;margin-bottom:10px;border-bottom:1px solid #1e3a5f}
.wl-head b{color:#38bdf8;font-size:1rem}
.wl-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}
.wl-sec{background:#0f1d30;border:1px solid #16263d;border-radius:8px;padding:10px 12px;font-size:.74rem;color:#cbd5e1;line-height:1.85}
.wl-sec h4{font-size:.72rem;color:#7dd3fc;margin-bottom:5px;font-weight:700}
.wl-sec b{color:#f1f5f9}
.wl-sec ul{margin:0;padding-left:16px}
.wl-sec li{margin:2px 0}
.wl-note{color:#94a3b8;font-size:.7rem;margin-top:3px;font-style:italic}
.wl-news{border-color:#243a2e}
.news-divergence{color:#fbbf24;font-weight:600;margin-top:2px}
.news-banner{background:#0f1d30;border:1px solid #16263d;border-radius:8px;padding:10px 12px;margin:0 0 12px;font-size:.8rem;color:#cbd5e1;line-height:1.9}
.news-banner-h{font-weight:700;color:#e2e8f0;margin-bottom:4px}
.news-table{border-collapse:collapse;margin:2px 0;font-size:.78rem}
.news-table th,.news-table td{padding:2px 10px 2px 0;text-align:left;white-space:nowrap;border:0}
.news-table th{color:#94a3b8;font-weight:600}
.news-table td.c,.news-table th.c{text-align:left}
.news-sec{font-weight:600;color:#e2e8f0}
.gy-intro{color:#94a3b8;font-size:.8rem;margin:6px 0 12px}
.gy-card{background:#0f1d30;border:1px solid #16263d;border-radius:8px;padding:10px 14px;margin:0 0 10px;font-size:.8rem;color:#cbd5e1;line-height:1.85}
.gy-head{font-size:.9rem;color:#e2e8f0;margin-bottom:2px}
.gy-ep{font-weight:700;color:#38bdf8}
.gy-date{color:#94a3b8;font-size:.78rem}
.gy-src{font-size:.74rem;margin-bottom:4px}
.gy-link{color:#38bdf8;text-decoration:none;margin-right:6px}
.gy-line{padding:1px 0}
.gy-ev{color:#94a3b8}
.gy-stocks{margin-top:4px;color:#cbd5e1;font-size:.76rem}
.gy-excerpt{margin-top:4px;color:#94a3b8;font-size:.76rem}
.gy-excerpt summary{cursor:pointer;color:#64748b}
.gy-excerpt>div{margin-top:4px;white-space:pre-wrap}
.wl-gs-summary{background:#0b1626;border-left:3px solid #38bdf8;border-radius:6px;padding:7px 10px;margin:4px 0 6px}
.wl-gs-headline{color:#e2e8f0;font-size:.78rem;margin-bottom:3px}
.wl-gs-h{color:#7dd3fc;font-weight:700;margin-top:4px}
.wl-gs-list{margin:0;padding-left:16px}
.wl-gs-list li{margin:1px 0}
.candle-chip{display:inline-block;background:#0b1f17;border:1px solid #14532d;color:#86efac;border-radius:6px;padding:2px 8px;font-size:.72rem;margin:2px 0}
.candle-card{border:1px solid #1e293b;border-radius:8px;padding:6px 9px;margin:4px 0;background:#0b1422}
.candle-head{font-size:.82rem;font-weight:600}
.candle-name{color:#e2e8f0;letter-spacing:.3px}
.candle-generic{color:#94a3b8;font-size:.68rem;font-weight:400;border:1px solid #334155;border-radius:4px;padding:0 4px}
.candle-badge{font-size:.72rem}
.candle-metrics{color:#cbd5e1;font-size:.74rem;margin-top:2px}
.candle-desc{color:#94a3b8;font-size:.74rem;margin-top:2px}
.candle-adj{color:#fbbf24;font-size:.68rem;margin-top:3px;font-style:italic}
.candle-evidence{font-size:.72rem;margin-top:3px;display:flex;flex-wrap:wrap;gap:6px;align-items:center}
.candle-ev-label{color:#64748b;font-weight:600;letter-spacing:.3px}
.candle-ev{color:#94a3b8}
.candle-ev.ev-yes{color:#4ade80}
.candle-ev.ev-no{color:#f87171}
.candle-ev.ev-none{color:#64748b}
.candle-ctx-limited{color:#94a3b8;font-size:.68rem;border:1px solid #334155;border-radius:4px;padding:0 4px}
.wl-risk{border-color:#3b1414;background:#1a0f12}
.wl-risk h4{color:#fca5a5}
@media(max-width:900px){.wl-grid{grid-template-columns:1fr}}

{{ if .GV.ShowTrendExtension }}
/* ⑮ 趨勢／乖離／族群熱度（R12）— 沿用 wl-sec，只加狀態色帶與指標網格 */
.wl-te .te-state{display:inline-block;border-radius:6px;padding:3px 10px;font-size:.78rem;font-weight:700;margin-bottom:8px}
.wl-te .te-state.te-good{background:#0b1f17;border:1px solid #14532d;color:#86efac}
.wl-te .te-state.te-warn{background:#2a1414;border:1px solid #7f1d1d;color:#fca5a5}
.wl-te .te-state.te-flat{background:#111827;border:1px solid #334155;color:#94a3b8}
.wl-te .te-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:4px 14px;font-size:.74rem;color:#cbd5e1}
.wl-te .te-grid span{color:#64748b}
.wl-te .te-codes{margin-top:6px;display:flex;flex-wrap:wrap;gap:5px}
.wl-te .te-code{font-size:.66rem;color:#94a3b8;border:1px solid #334155;border-radius:4px;padding:1px 5px;letter-spacing:.04em}
.wl-te .te-missing{color:#64748b;font-style:italic}
{{ end }}

{{ if .GV.ShowAI }}
/* ⑭ AI 分析（R11）— 沿用 wl-sec 既有樣式，只加多空色帶 */
.wl-ai .ai-summary{font-size:.78rem;color:#e2e8f0;line-height:1.85;margin-bottom:8px}
.wl-ai .ai-side{margin-bottom:6px;padding-left:8px;border-left:2px solid #334155}
.wl-ai .ai-side.ai-bull{border-left-color:#16a34a}
.wl-ai .ai-side.ai-bear{border-left-color:#dc2626}
.wl-ai .ai-side.ai-risk{border-left-color:#ca8a04}
.wl-ai .ai-label{font-size:.68rem;color:#64748b;letter-spacing:.05em}
.wl-ai .ai-side ul{margin:2px 0 0 16px;font-size:.74rem;color:#cbd5e1;line-height:1.75}
{{ end }}

{{ if .GV.ShowBacktestInsights }}
/* 區間策略回測面板（外掛 backtest.html，延遲載入，不影響本報告體積） */
.bt-tool{background:#0d1a2e;border:1px solid #1e3a5f;border-radius:8px;padding:12px 16px;margin-bottom:14px}
.bt-tool h4{font-size:.9rem;color:#7dd3fc;margin-bottom:6px;font-weight:700}
.bt-tool p{font-size:.76rem;color:#94a3b8;line-height:1.8;margin-bottom:8px}
.bt-tool b{color:#e2e8f0}
.bt-tool .acts{display:flex;gap:10px;align-items:center;flex-wrap:wrap}
.bt-tool button{background:#0c2340;border:1px solid #0284c7;color:#7dd3fc;border-radius:6px;padding:6px 14px;font-size:.78rem;font-family:inherit;cursor:pointer}
.bt-tool button:hover{background:#123457;color:#bae6fd}
.bt-tool a.ext{font-size:.75rem;color:#38bdf8;text-decoration:none}
.bt-tool a.ext:hover{text-decoration:underline}
.bt-tool .note{font-size:.7rem;color:#64748b}
#btFrameWrap{display:none;margin-top:12px}
#btFrame{width:100%;height:1100px;border:1px solid #1e3a5f;border-radius:8px;background:#0c1220;resize:vertical;overflow:auto}
{{ end }}
</style>
</head>
<body>
<div class="container">
<h1>📡 股票雷達<small>{{ .Date }} 盤後分析</small></h1>

<div class="tabs">
  <button class="tab-btn active" onclick="tab(event,'positions')">💼 持倉 ({{ len .Portfolio }})</button>
  <button class="tab-btn" onclick="tab(event,'watchlist')">🚀 飆股候選 ({{ len .Watchlist }})</button>
  <button class="tab-btn" onclick="tab(event,'market')">📊 市場掃描({{ .MarketLabel }})</button>
  <button class="tab-btn" onclick="tab(event,'rotation')">🔄 輪動 ({{ len .Rotation }})</button>
  {{ if and .GV.ShowNews (gt (len .NewsEpisodes) 0) }}<button class="tab-btn" onclick="tab(event,'gooaye')">🎙️ 股癌情報 ({{ len .NewsEpisodes }})</button>{{ end }}
  {{ if .GV.ShowBacktestInsights }}<button class="tab-btn" onclick="tab(event,'backtest')">🔬 回測洞察</button>{{ end }}
</div>

<!-- ══ POSITIONS ════════════════════════════════════════════════════════════ -->
<div id="tab-positions" class="tab-pane active">
{{ if eq (len .Portfolio) 0 }}
<div class="empty">持倉無資料。請在 stocks.yaml 的 positions: 區段加入持股。</div>
{{ else }}
<div class="pf-summary">
  <div class="pf-item"><label>總市值</label><div class="val">{{ fmtMoney .PortfolioSum.TotalValue }}</div></div>
  <div class="pf-item"><label>總成本</label><div class="val">{{ fmtMoney .PortfolioSum.TotalCost }}</div></div>
  <div class="pf-item"><label>損益%</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ pctSign .PortfolioSum.TotalPnLPct }}</div></div>
  <div class="pf-item"><label>損益額(元)</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ fmtMoney .PortfolioSum.TotalPnL }}</div></div>
</div>
<table>
  <thead><tr>
    <th>代號</th><th>名稱</th>
    <th class="r">成本</th><th class="r">現價</th>
    <th class="r">股數</th><th class="r">市值</th>
    <th class="r">損益%</th><th class="r">損益額</th>
    <th>BFP</th><th>評分</th><th>交易建議</th>
    <th class="r t-stop">停損</th><th class="r t-t1">目標1</th><th class="r t-t2">目標2</th>
    <th class="r">RSI</th><th>MA20</th><th class="r">量比</th><th>量價</th>
    <th>分析原因</th>
  </tr></thead>
  <tbody>
  {{ range .Portfolio }}
  <tr>
    <td class="sym">{{ .Symbol }}</td>
    <td class="name-col">{{ .Name }}</td>
    <td class="r">{{ f2 .CostBasis }}</td>
    <td class="r">{{ f2 .Close }}</td>
    <td class="r">{{ .Shares }}</td>
    <td class="r">{{ fmtMoney .PortfolioValue }}</td>
    <td class="r {{ pnlCSS .PnLPct }}">{{ pctSign .PnLPct }}</td>
    <td class="r {{ pnlCSS .PnLValue }}">{{ fmtMoney .PnLValue }}</td>
    <td>{{ bfpDots .BFPPoints }}</td>
    <td>{{ scoreBar .Score }}</td>
    <td>
      {{ if eq .Action "STOP LOSS" }}<div class="alert-sl">⛔ 建議停損</div>{{ end }}
      {{ if eq .Action "TAKE PROFIT" }}<div class="alert-tp">✅ 建議獲利了結</div>{{ end }}
      <span class="action-badge {{ actionCSS .Action }}">{{ actionLabel .Action }}</span>
    </td>
    <td class="r t-stop">{{ f2 .StopLoss }}</td>
    <td class="r t-t1">{{ f2 .Target1 }}</td>
    <td class="r t-t2">{{ f2 .Target2 }}</td>
    <td class="r {{ rsiCSS .RSI }}">{{ f1 .RSI }}</td>
    <td>{{ .MA20Trend }}</td>
    <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
    <td class="{{ pvCSS .PriceVolumeSignal }}">{{ .PriceVolumeSignal }}</td>
    <td><div class="reasons">{{ joinReasons .Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ WATCHLIST：飆股候選追蹤 ══════════════════════════════════════════════ -->
<div id="tab-watchlist" class="tab-pane">
{{ if eq (len .Watchlist) 0 }}
<div class="empty">觀察清單無資料。請在 stocks.yaml 的 watchlist: 區段加入股票。</div>
{{ else }}
<div class="rot-intro">🚀 <b>飆股候選追蹤</b>：Scanner 找標的，Watchlist 判斷它是不是<b>快變飆股</b>。點任一檔展開決策卡片，回答：要等突破還是拉回？失敗跌破哪裡移除？型態過去成功率高不高？</div>
<table>
  <thead><tr>
    <th>#</th><th>股票</th>
    <th class="sortable" onclick="sortWatch('sector',this)">族群 <span class="sort-ico"></span></th>
    <th class="rotscore sortable" onclick="sortWatch('score',this)">飆股分數 <span class="sort-ico"></span></th>
    <th class="sortable" onclick="sortWatch('stage',this)">階段 <span class="sort-ico"></span></th>
    <th class="sortable" onclick="sortWatch('action',this)">操作建議 <span class="sort-ico"></span></th>
    <th class="r">噴出</th>
    <th class="sortable" onclick="sortWatch('risk',this)">風險 <span class="sort-ico"></span></th>
    <th></th>
  </tr></thead>
  <tbody>
  {{ range $i, $e := .Watchlist }}
  <tr class="sector-row" onclick="toggleWatch({{ $i }})" data-score="{{ $e.RocketScore }}" data-sector="{{ $e.Sector }}" data-stage="{{ stagePriority $e.RocketStage }}" data-action="{{ actionPriority $e.WatchAction }}" data-risk="{{ riskPriority $e.RiskLabel }}">
    <td class="neu wl-num">{{ inc $i }}</td>
    <td><span class="sym">{{ $e.A.Symbol }}</span> <span class="name-col">{{ $e.A.Name }}</span></td>
    <td class="name-col">{{ if $e.Sector }}{{ $e.Sector }}{{ else }}—{{ end }}</td>
    <td class="rotscore">{{ rocketGauge $e.RocketScore }}</td>
    <td><span class="stage-badge {{ rocketStageCSS $e.RocketStage }}">{{ rocketStageLabel $e.RocketStage }}</span></td>
    <td><span class="act-badge {{ watchActionCSS $e.WatchAction }}">{{ watchActionLabel $e.WatchAction }}</span></td>
    <td class="r {{ probCSS $e.ExplosionProb }}">{{ $e.ExplosionProb }}</td>
    <td><span class="{{ riskCSS $e.RiskLabel }}">{{ $e.RiskLabel }}</span></td>
    <td class="exp-caret"><span id="wcaret-{{ $i }}">▸</span></td>
  </tr>
  <tr class="sector-detail" id="wdetail-{{ $i }}">
    <td colspan="9">
      <div class="wl-card">
        <div class="wl-head">{{ $e.A.Symbol }} {{ $e.A.Name }}｜{{ if $e.Sector }}{{ $e.Sector }}{{ else }}無族群{{ end }}｜<span class="stage-badge {{ rocketStageCSS $e.RocketStage }}">{{ rocketStageLabel $e.RocketStage }}</span>　飆股分數 <b>{{ $e.RocketScore }}</b>　操作 <span class="act-badge {{ watchActionCSS $e.WatchAction }}">{{ watchActionLabel $e.WatchAction }}</span>　建議觀察 {{ $e.DaysToWatch }}</div>
        <div class="wl-grid">
          <div class="wl-sec">
            <h4>① 族群輪動</h4>
            {{ if $e.HasSector }}
            <div>短線流向：<span class="{{ shortDirCSS $e.SectorFlowDir }}">{{ shortDirLabel $e.SectorFlowDir }}</span></div>
            <div>20日強度：<span class="{{ midCSS $e.SectorMidLabel }}">{{ $e.SectorMidLabel }}</span>　族群階段：{{ stageLabel $e.SectorStage }}</div>
            <div class="wl-note">{{ $e.SectorNote }}</div>
            {{ else }}<div class="wl-note">此股不在任何族群清單中，無族群輪動連動。</div>{{ end }}
          </div>
          <div class="wl-sec">
            <h4>② 型態狀態</h4>
            <div>整理型態：{{ bucketLabel $e.Consol.Bucket }}</div>
            <div>整理天數：{{ $e.Consol.Days }} 天　區間：{{ f1 $e.Consol.RangePct }}%</div>
            <div>價格壓縮：{{ f0pct $e.Consol.PriceCompressionScore }}　量縮比：{{ f2 $e.Consol.VolumeDryUpRatio }}</div>
            <div>支撐守住：{{ f0pct $e.Consol.SupportHoldScore }}　base 品質：<b>{{ f0pct $e.Consol.BaseQualityScore }}</b></div>
          </div>
          <div class="wl-sec">
            <h4>③ 回測結果</h4>
            <div>型態：{{ $e.Backtest.PatternName }}</div>
            <div>個股：{{ $e.Backtest.StockSampleCount }} 筆／勝率 {{ f0pct $e.Backtest.StockWinRate }}</div>
            <div>族群：{{ $e.Backtest.SectorSampleCount }} 筆／勝率 {{ f0pct $e.Backtest.SectorWinRate }}</div>
            <div>平均5日 <b class="{{ pnlCSS $e.Backtest.AvgReturn }}">{{ pctSign1 $e.Backtest.AvgReturn }}</b>　回撤 <b class="neg">{{ pctSign1 $e.Backtest.AvgDrawdown }}</b>　風報 {{ f2 $e.Backtest.RiskReward }}</div>
            <div>信心：<span class="{{ confCSS $e.Backtest.Confidence }}">{{ $e.Backtest.Confidence }}</span></div>
          </div>
          <div class="wl-sec">
            <h4>④ 價位計畫</h4>
            <div>現價：<b>{{ f2 $e.A.Close }}</b></div>
            <div>進場區：{{ $e.EntryZone }}</div>
            <div>突破價：<span class="t-t1">{{ f1 $e.BreakoutPrice }}</span>　支撐價：{{ f1 $e.SupportPrice }}</div>
            <div>停損價：<span class="t-stop">{{ f1 $e.StopLossPrice }}</span>　停利區：<span class="t-t2">{{ $e.TakeProfitZone }}</span></div>
          </div>
          <div class="wl-sec">
            <h4>⑤ 理由</h4>
            <ul>{{ range $e.Reasons }}<li>{{ . }}</li>{{ end }}</ul>
          </div>
          <div class="wl-sec wl-risk">
            <h4>⑥ 風險</h4>
            <div class="risk-tag">{{ $e.RiskLabel }}</div>
            <div class="wl-note">{{ $e.RiskWarning }}</div>
          </div>
          {{- if $.GV.Show }}
          <div class="wl-sec wl-guardrail">
            <h4>⑦ Guardrail Signals（實驗性）</h4>
            <div class="wl-note">此區為實驗性輔助訊號，參數尚待實機校準。</div>
            {{- if $.GV.GuardrailScoringEnabled }}
            <div class="wl-note">scoring 已啟用：以下訊號若符合條件，可能已影響分數／動作／機率（依規則為「符合觸發條件」，非保證已套用）。</div>
            {{- else }}
            <div class="wl-note">scoring 未啟用，以下僅為 shadow 訊號，不參與分數。</div>
            {{- end }}
            {{- with $e.Shadow }}
              {{- $sum := guardrailSummary $e $.GV.RSWatchThreshold }}
              <div class="wl-gs-summary">
                <div class="wl-gs-headline"><b>Guardrail Summary：</b>{{ $sum.Headline }}</div>
                {{- if $sum.Pros }}
                <div class="wl-gs-h">優勢：</div>
                <ul class="wl-gs-list">{{- range $sum.Pros }}<li>{{ . }}</li>{{- end }}</ul>
                {{- end }}
                {{- if $sum.Risks }}
                <div class="wl-gs-h">風險：</div>
                <ul class="wl-gs-list">{{- range $sum.Risks }}<li>{{ . }}</li>{{- end }}</ul>
                {{- end }}
                <div class="wl-gs-h">操作解讀：</div>
                <ul class="wl-gs-list">{{- range $sum.Actions }}<li>{{ . }}</li>{{- end }}</ul>
              </div>
              <div class="wl-note">── 以下為原始數據（raw）──</div>
              {{- with .RS }}
              <div>RS Rank：{{ f1 .RSRankPercentile }}　RS Score：{{ f1 .RSScore }}　gate threshold：{{ f1 $.GV.RSWatchThreshold }}</div>
              {{- if lt .RSRankPercentile $.GV.RSWatchThreshold }}<div><b>符合 RS gate 觸發條件（ExplosionProb 上限 MEDIUM）</b></div>{{- end }}
              {{- if not $.GV.GuardrailScoringEnabled }}<div class="wl-note">僅顯示，不參與分數</div>{{- end }}
              {{- end }}
              {{- with .NewHigh }}
              <div>距 52 週高：{{ f1 .DistanceFrom52wHighPct }}%　NewHighScore：{{ f1 .NewHighScore }}</div>
              <div>Near52wHigh：{{ .Near52wHigh }}　BreakoutWatch：{{ .BreakoutWatch }}　H20/H60/H120/H250：{{ .H20 }}/{{ .H60 }}/{{ .H120 }}/{{ .H250 }}</div>
              {{- end }}
              {{- with .VCP }}
              <div>VCP Valid：{{ .Valid }}　Grade：{{ .Grade }}　QualityScore：{{ f1 .QualityScore }}　Depths：{{ vcpDepths .Depths }}</div>
              {{- if and .Valid (gt .QualityScore $e.Consol.BaseQualityScore) }}<div><b>符合 base quality 上修條件</b></div>{{- end }}
              {{- end }}
              {{- with .Momentum }}
              <div>Flow：{{ .Flow }}　Score：{{ f1 .Score }}　Structure：{{ .StructureTrend }}　Divergence：{{ .Divergence }}</div>
              <div>modifier：{{ mfModifier .Flow $.GV }}{{- if not $.GV.GuardrailScoringEnabled }}（僅供參考，未套用）{{- end }}</div>
              {{- end }}
              {{- with .MultiTimeframe }}
              <div>Multi-Timeframe：SignalStrength {{ .SignalStrength }}　Daily {{ .Daily.TrendState }}　Weekly {{ .Weekly.TrendState }}{{ if .Weekly.Partial }}（本週未完成）{{ end }}</div>
              <div>Alignment：{{ .AlignmentLabel }}　LongTermFilter：{{ mtfLTFLabel .LongTermFilter }}</div>
              {{- if $e.MTFRiskNote }}<div class="wl-note">多週期提示：{{ $e.MTFRiskNote }}</div>{{- end }}
              {{- end }}
            {{- else }}
            <div class="wl-note">未啟用訊號計算（無 shadow）。</div>
            {{- end }}
          </div>
          {{- end }}
          {{- with $e.HorizonHint }}{{- if .Computed }}
          <div class="wl-sec wl-horizon">
            <h4>⑧ 回測觀察週期</h4>
            <div>主要觀察週期：<b>{{ .PrimaryDays }} 個交易日</b></div>
            <div>早期反應：{{ intsSlash .EarlyDays }} 日　中期參考：{{ intsSlash .ReferenceDays }} 日</div>
            <div>匹配型態：{{ .MatchedSetup }}　信心：{{ .Confidence }}</div>
            {{- if .Reason }}
            <div class="wl-gs-h">理由：</div>
            <ul class="wl-gs-list">{{- range .Reason }}<li>{{ . }}</li>{{- end }}</ul>
            {{- end }}
            {{- if .Caveat }}
            <div class="wl-gs-h">注意：</div>
            <ul class="wl-gs-list">{{- range .Caveat }}<li>{{ . }}</li>{{- end }}</ul>
            {{- end }}
          </div>
          {{- end }}{{- end }}
          {{- if $.GV.ShowETFFlow }}{{- with $e.ETFFlow }}{{- if .Computed }}
          <div class="wl-sec wl-etfflow">
            <h4>⑨ ETF Flow / Active Rotation Context</h4>
            <div class="wl-note">此為延遲揭露資料，不代表盤中即時買賣。（資料日 {{ .AsOfDate }}，延遲 {{ .DataLagDays }} 個交易日）</div>
            <div>ETF Flow Pressure：<b>{{ if .Strength }}{{ .Strength }}{{ else }}—{{ end }}</b>　{{ etfSignalLabel .Signal }}</div>
            <div>主動式 ETF：Δ{{ .ActiveDeltaShares }} 股　壓力比 {{ f1 .ActivePressureRatio }}　連續加 {{ .ActiveConsecutiveBuyDays }} 日／減 {{ .ActiveConsecutiveSellDays }} 日</div>
            <div>高股息／規則型 ETF：Δ{{ .PassiveDeltaShares }} 股　壓力比 {{ f1 .PassivePressureRatio }}　連續加 {{ .PassiveConsecutiveBuyDays }} 日／減 {{ .PassiveConsecutiveSellDays }} 日</div>
            {{- if .Reasons }}
            <div class="wl-gs-h">解讀：</div>
            <ul class="wl-gs-list">{{- range .Reasons }}<li>{{ . }}</li>{{- end }}</ul>
            {{- end }}
            {{- if .Caveats }}
            <div class="wl-gs-h">注意：</div>
            <ul class="wl-gs-list">{{- range .Caveats }}<li>{{ . }}</li>{{- end }}</ul>
            {{- end }}
          </div>
          {{- end }}{{- end }}{{- end }}
          {{- if $.GV.ShowNews }}{{- with $e.News }}{{- if .Computed }}
          <div class="wl-sec wl-news">
            <h4>⑩ 消息面（僅供參考，不影響 BUY／WATCH／SELL）</h4>
            {{- range .StockItems }}
            <div>{{ newsDot .Signal }} 個股 {{ newsSignalLabel .Signal }}　來源：{{ newsEpisodeLabel .EventID }}　強度 {{ .Strength }}/10　Age：{{ newsAge .PublishedAt }}{{ if .Conflict }}　<span class="news-divergence">⚠來源分歧</span>{{ end }}</div>
            {{- if .Summary }}<ul class="wl-gs-list"><li>{{ .Summary }}</li></ul>{{- end }}
            {{- end }}
            {{- range .SectorItems }}
            <div>{{ newsDot .Signal }} {{ newsSectors . }} {{ newsSignalLabel .Signal }}　來源：{{ newsEpisodeLabel .EventID }}　強度 {{ .Strength }}/10{{ if .Conflict }}　<span class="news-divergence">⚠來源分歧</span>{{ end }}</div>
            {{- end }}
            {{- if .Divergence }}
            <div class="news-divergence">⚠ DIVERGENCE：個股訊號與族群訊號方向分歧（僅標示，不改動作）</div>
            {{- end }}
            <div class="wl-note">消息面為延遲外部觀點，僅作 context，非交易指令。</div>
          </div>
          {{- end }}{{- end }}{{- end }}
          {{- if $.GV.ShowInstitution }}{{- with $e.Institution }}
          <div class="wl-sec">
            <h4>⑪ 三大法人籌碼（單位：張；延遲揭露，不影響 BUY／WATCH／SELL）</h4>
            <div class="wl-note">資料日 {{ .AsOfDate }}{{ if not .Completeness.DataComplete }}（⚠ 今日法人資料缺漏）{{ end }}{{ if .Completeness.MissingDates }}｜區間缺 {{ len .Completeness.MissingDates }} 日{{ end }}</div>
            <table class="news-table">
              <thead><tr><th>法人</th><th class="c">今日</th><th class="c">5日</th><th class="c">10日</th><th class="c">20日</th><th>連續狀態</th><th>轉折</th><th class="c">占量</th></tr></thead>
              <tbody>
                {{ instRow "外資" .Foreign }}
                {{ instRow "投信" .Trust }}
                {{ instRow "自營商" .Dealer }}
                {{ instRow "三大法人合計" .Total }}
              </tbody>
            </table>
            {{- if .TotalMismatch }}<div class="wl-note">⚠ 官方合計與計算合計不一致（僅標示）</div>{{- end }}
          </div>
          {{- end }}{{- end }}
          {{- if $.GV.ShowBias }}{{- with $e.Bias }}
          <div class="wl-sec">
            <h4>⑫ 乖離率（追價／超跌風險，非買賣訊號）</h4>
            <div>BIAS5 {{ biasValLabel .Bias5 }}　BIAS10 {{ biasValLabel .Bias10 }}　BIAS20 {{ biasValLabel .Bias20 }}　BIAS60 {{ biasValLabel .Bias60 }}</div>
            <div>Risk：<b>{{ biasRiskLabel .Risk }}</b></div>
          </div>
          {{- end }}{{- end }}
          {{- if or $.GV.ShowInstitution $.GV.ShowBias }}{{- with chipSummary $e }}
          <div class="wl-sec">
            <h4>🔔 籌碼／乖離重點</h4>
            <ul class="wl-gs-list">{{- range . }}<li>{{ . }}</li>{{- end }}</ul>
          </div>
          {{- end }}{{- end }}
          {{- if $.GV.ShowCandlestick }}{{- with $e.Candlestick }}
          <div class="wl-sec wl-candle">
            <h4>⑬ K 線型態（Shadow Research Only，不影響 BUY／WATCH／SELL）</h4>
            {{- if eq .Status "OK" }}
            {{- with candleSummaryChip . }}<div class="candle-chip">{{ . }}</div>{{- end }}
            {{- range .Signals }}
            <div class="candle-card">
              <div class="candle-head"><span class="candle-name">{{ .Type }}</span>{{ if candleGeneric .Type }} <span class="candle-generic">Generic Pattern</span>{{ end }}　<span class="candle-badge">{{ candleDir .Direction }}</span></div>
              <div class="candle-metrics">Confidence {{ candleConfPct .Confidence }}　Strength {{ .Strength }}/5</div>
              <div class="candle-desc">{{ .Description }}</div>
              <div class="candle-evidence"><span class="candle-ev-label">Evidence</span>{{- range candleEvidence . }} <span class="candle-ev {{ .Class }}">{{ .Glyph }} {{ .Label }}</span>{{- end }}{{- if candleCtxLimited . }} <span class="candle-ctx-limited">Context Limited</span>{{- end }}</div>
              <div class="candle-adj">Shadow Research Only — Suggested Adjustment {{ candleAdjLabel .SuggestedAdjustment }}（Not Applied）</div>
            </div>
            {{- end }}
            {{- else }}
            <div class="wl-note">{{ candleStatusText .Status }}</div>
            {{- end }}
          </div>
          {{- end }}{{- end }}
          {{- if $.GV.ShowAI }}{{- with $e.AI }}{{- if aiOK . }}
          <div class="wl-sec wl-ai">
            <h4>⑭ AI 分析（Shadow Only，不影響掃描分數與買賣訊號）</h4>
            <div class="ai-summary">{{ .Summary }}</div>
            {{- if .BullCase }}
            <div class="ai-side ai-bull"><span class="ai-label">看多依據</span>
              <ul>{{- range .BullCase }}<li>{{ . }}</li>{{- end }}</ul>
            </div>
            {{- end }}
            {{- if .BearCase }}
            <div class="ai-side ai-bear"><span class="ai-label">看空依據</span>
              <ul>{{- range .BearCase }}<li>{{ . }}</li>{{- end }}</ul>
            </div>
            {{- end }}
            {{- if .RiskFlags }}
            <div class="ai-side ai-risk"><span class="ai-label">風險提醒</span>
              <ul>{{- range .RiskFlags }}<li>⚠ {{ . }}</li>{{- end }}</ul>
            </div>
            {{- end }}
            <div class="wl-note">解讀信心 {{ aiConfPct .Confidence }}（模型對「自己這段解讀」的把握，<b>不是</b>交易信心）｜模型 {{ .Model }}</div>
            <div class="wl-note">本區塊由 AI 解讀掃描器已算出的證據產生，不改變任何分數、階段或 BUY／WATCH／SELL。</div>
          </div>
          {{- end }}{{- end }}{{- end }}
          {{- if $.GV.ShowTrendExtension }}{{- with $e.TrendExt }}
          <div class="wl-sec wl-te">
            <h4>⑮ 趨勢／乖離／族群熱度（Shadow Only，不影響 BUY／WATCH／SELL）</h4>
            <div class="te-state {{ trendStateCSS .State }}">{{ trendStateText .State }}</div>
            <div class="te-grid">
              <div><span>MA20 斜率</span> {{ slopeLabel .MA20Slope5D }}</div>
              <div><span>MA60 斜率</span> {{ slopeLabel .MA60Slope10D }}</div>
              <div><span>BIAS20</span> {{ pctPtr .Bias20 }}</div>
              <div><span>BIAS60</span> {{ pctPtr .Bias60 }}</div>
              <div><span>BIAS20 自身分位</span> {{ rankPtr .Bias20Percentile }}</div>
            </div>
            {{- with .Heat }}
            {{- if heatOK . }}
            <div class="te-grid" style="margin-top:6px">
              <div><span>族群熱度</span> <b>{{ printf "%.0f" .Heat }} / 100</b></div>
              <div><span>Breadth</span> {{ printf "%.0f" .Breadth }}</div>
              <div><span>相對動能</span> {{ printf "%.0f" .RelativeMomentum }}</div>
              <div><span>參與度</span> {{ printf "%.0f" .Participation }}</div>
              <div><span>量能確認</span> {{ printf "%.0f" .VolumeConfirmation }}</div>
              <div><span>成員</span> {{ .ValidMemberCount }} / {{ .MemberCount }}</div>
            </div>
            {{- else }}
            <div class="te-missing">族群熱度 — INSUFFICIENT_DATA（有效成員 {{ .ValidMemberCount }} / {{ .MemberCount }}，不足以計算，未以 0 代替）</div>
            {{- end }}
            {{- end }}
            {{- if .Codes }}
            <div class="te-codes">{{- range .Codes }}<span class="te-code">{{ . }}</span>{{- end }}</div>
            {{- end }}
            <div class="wl-note">趨勢方向（斜率）＋ 價格位置（乖離）＋ 族群共振（熱度）的合併判讀，僅供閱讀，不改變任何分數、階段或買賣訊號。</div>
          </div>
          {{- end }}{{- end }}
        </div>
      </div>
    </td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ MARKET SCAN ══════════════════════════════════════════════════════════ -->
<div id="tab-market" class="tab-pane">
{{- if $.GV.ShowNews }}{{- with $.NewsSummary }}
<div class="news-banner">
  <div class="news-banner-h">📰 市場消息面（as of {{ .AsOf.Format "2006-01-02" }}｜Fresh {{ .Fresh }} / Active {{ .Active }} / Expired {{ .Expired }}）</div>
  <table class="news-table">
    <thead><tr><th>族群</th>{{- range .Windows }}<th class="c">近{{ . }}日</th>{{- end }}<th>近期</th></tr></thead>
    <tbody>
    {{- range .Sectors }}
      <tr><td class="news-sec">{{ .Name }}</td>{{- range .Cells }}<td class="c">{{ .Display }}</td>{{- end }}<td>{{ newsDot .LatestType }}{{ newsSignalLabel .LatestType }}{{ if .LatestEP }}({{ .LatestEP }}){{ end }}</td></tr>
    {{- end }}
    </tbody>
  </table>
  <div class="wl-note">消息面僅作 regime／context 參考，不影響 BUY／WATCH／SELL 與排序。🟢偏多 🟡風險 🔴偏空。短視窗看輪動速度。</div>
</div>
{{- end }}{{- end }}
{{ if eq (len .Market) 0 }}
<div class="empty">市場掃描無資料（請執行 make run 或 make run-top100）</div>
{{ else }}
<table>
  <thead><tr>
    <th>#</th><th>代號</th><th>名稱</th>
    <th class="r">現價</th><th class="r">量比</th>
    <th>BFP</th><th>評分</th><th>交易建議</th>
    <th class="r t-stop">停損</th><th class="r t-t1">目標1</th><th class="r t-t2">目標2</th>
    <th class="r">RSI</th><th>MA20</th><th class="r">K</th><th class="r">D</th>
    <th>量價</th><th class="r">量分</th>
    <th>分析原因</th>
  </tr></thead>
  <tbody>
  {{ range $i, $a := .Market }}
  <tr>
    <td class="neu">{{ inc $i }}</td>
    <td class="sym">{{ $a.Symbol }}</td>
    <td class="name-col">{{ $a.Name }}</td>
    <td class="r">{{ f2 $a.Close }}</td>
    <td class="r {{ volCSS $a.VolumeRatio }}">{{ f1 $a.VolumeRatio }}x</td>
    <td>{{ bfpDots $a.BFPPoints }}</td>
    <td>{{ scoreBar $a.Score }}</td>
    <td><span class="action-badge {{ actionCSS $a.Action }}">{{ actionLabel $a.Action }}</span></td>
    <td class="r t-stop">{{ f2 $a.StopLoss }}</td>
    <td class="r t-t1">{{ f2 $a.Target1 }}</td>
    <td class="r t-t2">{{ f2 $a.Target2 }}</td>
    <td class="r {{ rsiCSS $a.RSI }}">{{ f1 $a.RSI }}</td>
    <td>{{ $a.MA20Trend }}</td>
    <td class="r">{{ f1 $a.KDJK }}</td>
    <td class="r">{{ f1 $a.KDJD }}</td>
    <td class="{{ pvCSS $a.PriceVolumeSignal }}">{{ $a.PriceVolumeSignal }}</td>
    <td class="r"><span class="vol-score">{{ $a.VolumeScore }}</span></td>
    <td><div class="reasons">{{ joinReasons $a.Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ ROTATION ═════════════════════════════════════════════════════════════ -->
<div id="tab-rotation" class="tab-pane">
{{ if eq (len .Rotation) 0 }}
<div class="empty">族群輪動無資料。請在 configs/sectors.yaml 設定族群與成員，並確認未加 --no-rotation。</div>
{{ else }}
<div class="rot-intro">🔄 <b>輪動</b>：不看「今天最強是誰」，而是找「下一波資金可能去哪裡」。三層架構 — <b>短線(1~5日)</b> 最早反映資金轉向、<b>20日</b> 中期強度較慢、<b>60日</b> 波段趨勢。排序混合短線流向與中期強度，讓<b>資金剛流入、20日尚未反映</b>的早期輪動族群提早浮上來，再淡化已過熱者。點族群可展開成員與結論。</div>
<table>
  <thead><tr>
    <th>#</th><th>族群</th>
    <th>短線流向</th><th>短線階段(1~5日)</th><th class="rotscore">短線分數</th>
    <th class="c">20日</th><th class="c">60日趨勢</th>
    <th class="rotscore">機會分數</th>
    <th class="r">新高%</th><th class="r">突破%</th>
    <th class="r">成員</th><th></th>
  </tr></thead>
  <tbody>
  {{ range $i, $s := .Rotation }}
  <tr class="sector-row" onclick="toggleSector({{ $i }})">
    <td class="neu">{{ inc $i }}</td>
    <td class="sym">{{ $s.Name }}</td>
    <td><span class="flow-badge {{ shortDirCSS $s.ShortTermFlowDir }}">{{ shortDirLabel $s.ShortTermFlowDir }}</span></td>
    <td><span class="stage-badge {{ shortStageCSS $s.ShortTermFlowStage }}">{{ shortStageLabel $s.ShortTermFlowStage }}</span></td>
    <td class="rotscore">{{ sectorScoreBar $s.ShortTermFlowScore }}</td>
    <td class="c {{ midCSS $s.MidTermLabel }}">{{ $s.MidTermLabel }}</td>
    <td class="c {{ trendCSS $s.TrendLabel }}">{{ $s.TrendLabel }}</td>
    <td class="rotscore">{{ sectorScoreBar $s.Score }}</td>
    <td class="r">{{ f0pct $s.NewHighRatio }}</td>
    <td class="r">{{ f0pct $s.BreakoutRatio }}</td>
    <td class="r">{{ len $s.Stocks }}</td>
    <td class="exp-caret"><span id="caret-{{ $i }}">▸</span></td>
  </tr>
  <tr class="sector-detail" id="detail-{{ $i }}">
    <td colspan="12">
      <div class="sector-meta">
        <span class="meta-concl">📌 {{ $s.ShortTermNote }}</span>
        <span class="meta-nums">短線 1/3/5日：<b class="{{ pnlCSS $s.Avg1dGain }}">{{ pctSign1 $s.Avg1dGain }}</b> / <b class="{{ pnlCSS $s.Avg3dGain }}">{{ pctSign1 $s.Avg3dGain }}</b> / <b class="{{ pnlCSS $s.Avg5dGain }}">{{ pctSign1 $s.Avg5dGain }}</b>　上漲家數 {{ f0pct $s.UpRatio }}　站上5/10MA {{ f0pct $s.AboveShortMARatio }}　創20日高 {{ f0pct $s.NewHigh20Ratio }}　量能放大 {{ f0pct $s.VolExpansion }}　｜　中期：相對強度 {{ f0pct $s.RelStrength }}　平均20日 <b class="{{ pnlCSS $s.AvgReturn20 }}">{{ pctSign1 $s.AvgReturn20 }}</b>　｜　趨勢：MA60↑ {{ f0pct $s.MA60Slope }}　站上MA60 {{ f0pct $s.AboveMA60Ratio }}</span>
      </div>
      <table class="sub-table">
        <thead><tr>
          <th>代號</th><th>名稱</th><th class="r">現價</th>
          <th class="r">5日</th><th class="r">20日報酬</th>
          <th class="c">20日高</th><th class="c">突破</th><th class="c">新高60</th>
          <th class="r">量比</th><th class="c">MA60↑</th><th class="c">資金</th><th>建議</th>
        </tr></thead>
        <tbody>
        {{ range $s.Stocks }}
        <tr>
          <td class="sym">{{ .Symbol }}</td>
          <td class="name-col">{{ .Name }}</td>
          <td class="r">{{ f2 .Close }}</td>
          <td class="r {{ pnlCSS .Gain5 }}">{{ pctSign1 .Gain5 }}</td>
          <td class="r {{ pnlCSS .Return20 }}">{{ pctSign1 .Return20 }}</td>
          <td class="c">{{ boolMark .NewHigh20 }}</td>
          <td class="c">{{ boolMark .Breakout }}</td>
          <td class="c">{{ boolMark .NewHigh }}</td>
          <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
          <td class="c">{{ if .MA60Valid }}{{ boolMark .MA60Up }}{{ else }}<span class="no">—</span>{{ end }}</td>
          <td class="c">{{ flowArrow .MoneyFlow }}</td>
          <td><span class="action-badge {{ actionCSS .Action }}">{{ actionLabel .Action }}</span></td>
        </tr>
        {{ end }}
        </tbody>
      </table>
    </td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ 股癌 PODCAST 情報（逐集情報卡；純外部觀點，不影響策略） ═══════════════ -->
{{ if and .GV.ShowNews (gt (len .NewsEpisodes) 0) }}
<div id="tab-gooaye" class="tab-pane">
  <div class="gy-intro">🎙️ <b>股癌 Podcast 情報</b> — 逐集情報卡（新→舊）。外部延遲觀點，僅作 context，<b>不影響 BUY／WATCH／SELL 與排序</b>。</div>
  {{- range .NewsEpisodes }}
  <div class="gy-card">
    <div class="gy-head"><span class="gy-ep">{{ .Label }}</span>　<span class="gy-date">{{ .PublishedAt.Format "2006-01-02" }}</span>{{ if .RawTitle }}　{{ .RawTitle }}{{ end }}{{ if .Conflict }}　<span class="news-divergence">⚠ 觀點分歧</span>{{ end }}</div>
    {{- if .Sources }}<div class="gy-src">來源：{{ range .Sources }}<a class="gy-link" href="{{ .URL }}" target="_blank" rel="noopener">{{ .Provider }}↗</a> {{ end }}</div>{{- end }}
    {{- range .Lines }}
    <div class="gy-line">{{ newsDot .Signal }} {{ newsSignalLabel .Signal }}　{{ .Entities }}{{ if .Evidence }}　<span class="gy-ev">「{{ .Evidence }}」</span>{{ end }}{{ if .Conflict }} <span class="news-divergence">⚠</span>{{ end }}</div>
    {{- end }}
    {{- if .Stocks }}<div class="gy-stocks">個股：{{ range $i, $s := .Stocks }}{{ if $i }}、{{ end }}{{ $s.Name }}({{ $s.Code }}){{ end }}</div>{{- end }}
    {{- if .Excerpt }}<details class="gy-excerpt"><summary>內文摘錄</summary><div>{{ .Excerpt }}</div></details>{{- end }}
  </div>
  {{- end }}
</div>
{{ end }}

<!-- ══ 回測面板：區間策略回測（互動，外掛 backtest.html；純顯示，不影響停損/排名/下單） ══ -->
{{ if .GV.ShowBacktestInsights }}
<div id="tab-backtest" class="tab-pane">
  <div class="bt-tool">
    <h4>🎛️ 區間策略回測面板（互動）</h4>
    <p>
      挑一檔股票、指定買進與賣出日期，比較「單筆全押 / 定期定額 / 預留現金逢跌狙擊 / 停利接回 /
      落袋為安」六種紀律事後分別會是什麼結果。資料取自本機價格快取，
      <b>純歷史重播，不是訊號、不會下單，也不影響上方任何掃描與停損</b>。
    </p>
    <div class="acts">
      <button id="btLoad" type="button">在此展開面板</button>
      <a class="ext" id="btOpen" href="#" target="_blank" rel="noopener">在新分頁開啟 ↗</a>
      <span class="note">面板另外產生（<code>make backtest</code> → backtest.html），沒展開就不會下載，不影響本報告載入速度。</span>
    </div>
    <div id="btFrameWrap"><iframe id="btFrame" title="區間策略回測面板" loading="lazy"></iframe></div>
  </div>
</div>
{{ end }}

<footer>Stock Radar｜資料來源：TWSE / Yahoo Finance｜僅供研究參考，非投資建議</footer>
</div>
<script>
function tab(e,n){
  document.querySelectorAll('.tab-btn').forEach(b=>b.classList.remove('active'));
  document.querySelectorAll('.tab-pane').forEach(p=>p.classList.remove('active'));
  e.currentTarget.classList.add('active');
  document.getElementById('tab-'+n).classList.add('active');
}
{{ if .GV.ShowBacktestInsights }}
// 區間策略回測面板：同一份 HTML 會出現在網站根目錄 (index.html) 與 reports/ 底下，
// 兩層的相對路徑不同，所以由 location 推算，file:// 與 GitHub Pages 都適用。
(function(){
  var load=document.getElementById('btLoad');
  if(!load)return;
  var path=/\/reports\/[^\/]*$/.test(location.pathname)?'../backtest.html':'backtest.html';
  document.getElementById('btOpen').href=path;
  var wrap=document.getElementById('btFrameWrap'),frame=document.getElementById('btFrame');
  load.addEventListener('click',function(){
    if(wrap.style.display==='block'){                // 收合，但保留已載入的內容
      wrap.style.display='none';load.textContent='在此展開面板';return;
    }
    if(!frame.getAttribute('src'))frame.setAttribute('src',path);
    wrap.style.display='block';load.textContent='收合面板';
    wrap.scrollIntoView({behavior:'smooth',block:'nearest'});
  });
})();
{{ end }}
function toggleSector(i){
  var row=document.getElementById('detail-'+i);
  var caret=document.getElementById('caret-'+i);
  if(!row)return;
  var open=row.classList.toggle('open');
  if(caret)caret.textContent=open?'▾':'▸';
}
function toggleWatch(i){
  var row=document.getElementById('wdetail-'+i);
  var caret=document.getElementById('wcaret-'+i);
  if(!row)return;
  var open=row.classList.toggle('open');
  if(caret)caret.textContent=open?'▾':'▸';
}
// 飆股候選表格排序：第一次點 DESC，再點 ASC。
// score 用數字；stage/action/risk 用自訂優先級；sector 用字串（同族群內再依分數高到低）。
var wlSort={key:null,dir:null};
function sortWatch(key,th){
  var tbody=th.closest('table').querySelector('tbody');
  // 同欄再點則反向，否則預設 DESC（高到低／優先級高到低）
  var dir=(wlSort.key===key&&wlSort.dir==='desc')?'asc':'desc';
  wlSort={key:key,dir:dir};
  // 收集主列＋明細列成對
  var pairs=[];
  tbody.querySelectorAll('tr.sector-row').forEach(function(main){
    pairs.push([main,main.nextElementSibling]);
  });
  var sign=(dir==='desc')?-1:1;
  pairs.sort(function(a,b){
    var ra=a[0],rb=b[0],c;
    if(key==='sector'){
      var sa=ra.getAttribute('data-sector')||'',sb=rb.getAttribute('data-sector')||'';
      c=sa.localeCompare(sb,'zh-Hant');
      if(c!==0)return sign*c;
      // 同族群：分數高到低，方便看「某族群裡誰最強」
      return (+rb.getAttribute('data-score'))-(+ra.getAttribute('data-score'));
    }
    c=(+ra.getAttribute('data-'+key))-(+rb.getAttribute('data-'+key));
    return sign*c;
  });
  pairs.forEach(function(p,idx){
    tbody.appendChild(p[0]);
    if(p[1])tbody.appendChild(p[1]);
    var num=p[0].querySelector('.wl-num');
    if(num)num.textContent=idx+1;
  });
  // 更新表頭排序方向 icon
  th.closest('tr').querySelectorAll('th').forEach(function(h){
    h.classList.remove('sort-active');
    var ico=h.querySelector('.sort-ico');
    if(ico)ico.textContent='';
  });
  th.classList.add('sort-active');
  var ico=th.querySelector('.sort-ico');
  if(ico)ico.textContent=(dir==='desc')?'▼':'▲';
}
</script>
</body>
</html>`
