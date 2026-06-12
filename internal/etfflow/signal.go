package etfflow

import (
	"fmt"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Signal classification + strength + data lag (R8-3).
//
// Turns a per-stock StockFlow (R8-2) into a StockFlowSignal: a headline FlowSignal,
// a FlowStrength tier, and the numeric context a report/scanner can render later.
// This stays a PURE data/signal layer — no dependency on scanner / report / config.
//
// R8 is delayed-disclosure CONTEXT, never an intraday signal or trade instruction.
// Every signal carries fixed caveats and DataLagDays; reasons/caveats are kept free
// of trade-instruction tokens (see forbiddenTokens + the signal_test guard). The
// SELL/BUY substrings in the FlowSignal *enum values* are deliberate identifiers and
// are NOT subject to that text guard — only Reasons/Caveats prose is. SPEC §9.
// ──────────────────────────────────────────────────────────────────────────────

// FlowSignal is the headline classification of a stock's ETF holdings flow.
type FlowSignal string

const (
	FlowNeutral FlowSignal = "ETF_FLOW_NEUTRAL"

	PassiveSellPressure FlowSignal = "PASSIVE_ETF_SELL_PRESSURE"
	PassiveBuySupport   FlowSignal = "PASSIVE_ETF_BUY_SUPPORT"

	ActiveSellPressure FlowSignal = "ACTIVE_ETF_SELL_PRESSURE"
	ActiveBuySupport   FlowSignal = "ACTIVE_ETF_BUY_SUPPORT"

	// Consensus = several active ETFs moving the same way. Inert in the first
	// version (only one active ETF) since it needs >= 2 moved active legs; the
	// enum + logic + tests exist so multi-active rollout needs no rework. SPEC §9.1.
	ActiveConsensusSell FlowSignal = "ACTIVE_ETF_CONSENSUS_SELL"
	ActiveConsensusBuy  FlowSignal = "ACTIVE_ETF_CONSENSUS_BUY"

	AggregateSellPressure FlowSignal = "ETF_AGGREGATE_SELL_PRESSURE"
	AggregateBuySupport   FlowSignal = "ETF_AGGREGATE_BUY_SUPPORT"
)

// FlowStrength grades how forceful the flow is. StrengthNone means "below LOW" →
// the headline is ETF_FLOW_NEUTRAL.
type FlowStrength string

const (
	StrengthNone    FlowStrength = ""
	StrengthLow     FlowStrength = "LOW"
	StrengthMedium  FlowStrength = "MEDIUM"
	StrengthHigh    FlowStrength = "HIGH"
	StrengthExtreme FlowStrength = "EXTREME"
)

// StrengthThresholds parameterises the LOW/MEDIUM/HIGH/EXTREME cut-offs so nothing
// is hard-coded at call sites. Pressure comparisons are strict ">"; consecutive-day
// comparisons are ">=". Defaults are待校準 (SPEC §9.2).
type StrengthThresholds struct {
	PressureLow        float64 // > this (or ConsecutiveMin streak) → at least LOW
	PressureMedium     float64 // > this → MEDIUM
	PressureHigh       float64 // > this → HIGH
	ConsecutiveMin     int     // >= this consecutive days → at least LOW
	ConsecutiveExtreme int     // >= this consecutive days, combined with > PressureHigh → EXTREME
}

// DefaultStrengthThresholds is the v1 (待校準) threshold set.
func DefaultStrengthThresholds() StrengthThresholds {
	return StrengthThresholds{
		PressureLow:        0.2,
		PressureMedium:     0.5,
		PressureHigh:       1.0,
		ConsecutiveMin:     3,
		ConsecutiveExtreme: 5,
	}
}

// isZero reports an unset thresholds value, so Classify can fall back to defaults.
func (th StrengthThresholds) isZero() bool {
	return th == StrengthThresholds{}
}

// tier maps a pressure ratio + the relevant consecutive-day count to a strength.
func (th StrengthThresholds) tier(ratio float64, consecutive int) FlowStrength {
	switch {
	case ratio > th.PressureHigh && consecutive >= th.ConsecutiveExtreme:
		return StrengthExtreme
	case ratio > th.PressureHigh:
		return StrengthHigh
	case ratio > th.PressureMedium:
		return StrengthMedium
	case ratio > th.PressureLow || consecutive >= th.ConsecutiveMin:
		return StrengthLow
	default:
		return StrengthNone
	}
}

// tierRank orders strengths so the headline can pick the strongest level.
func tierRank(s FlowStrength) int {
	switch s {
	case StrengthLow:
		return 1
	case StrengthMedium:
		return 2
	case StrengthHigh:
		return 3
	case StrengthExtreme:
		return 4
	default:
		return 0
	}
}

// StockFlowSignal is the classified, display-ready result for one stock. It carries
// the headline Signal + Strength plus every numeric level so a later report/scanner
// layer can render detail. Computed=false means the inputs were insufficient (nil
// flow / invalid as_of_date); callers should then render nothing.
type StockFlowSignal struct {
	Computed bool `json:"computed"`

	StockCode string `json:"stock_code"`

	AsOfDate    string    `json:"as_of_date"`
	FetchedAt   time.Time `json:"fetched_at"`
	DataLagDays int       `json:"data_lag_days"`

	Signal   FlowSignal   `json:"signal"`
	Strength FlowStrength `json:"strength"`

	ActiveDeltaShares    int64 `json:"active_delta_shares"`
	PassiveDeltaShares   int64 `json:"passive_delta_shares"`
	AggregateDeltaShares int64 `json:"aggregate_delta_shares"`

	ActivePressureRatio    float64 `json:"active_pressure_ratio"`
	PassivePressureRatio   float64 `json:"passive_pressure_ratio"`
	AggregatePressureRatio float64 `json:"aggregate_pressure_ratio"`

	ActiveConsecutiveBuyDays   int `json:"active_consecutive_buy_days"`
	ActiveConsecutiveSellDays  int `json:"active_consecutive_sell_days"`
	PassiveConsecutiveBuyDays  int `json:"passive_consecutive_buy_days"`
	PassiveConsecutiveSellDays int `json:"passive_consecutive_sell_days"`

	Reasons []string `json:"reasons,omitempty"`
	Caveats []string `json:"caveats,omitempty"`
}

// ClassifyInput carries everything Classify needs, supplied by the caller so this
// package never reaches into scanner. AvgVolume20 is the stock's 20-day average
// volume (for the pressure ratio); ReportDate/AsOfDate drive DataLagDays.
type ClassifyInput struct {
	StockCode   string
	Flow        *StockFlow
	AvgVolume20 float64

	ReportDate time.Time
	AsOfDate   string
	FetchedAt  time.Time

	Thresholds StrengthThresholds // zero value → DefaultStrengthThresholds()
}

// baseCaveats are attached to every signal (the disclosure red lines, SPEC §6).
var baseCaveats = []string{
	"此為延遲揭露資料，不代表盤中即時買賣。",
	"ETF flow 不等於基本面變化。",
	"ETF 賣壓消化不等於買點，仍需等待價格確認。",
}

// DataLagDays returns report_date − as_of_date in calendar days (v1 ignores trading
// calendars). It returns -1 when asOfDate cannot be parsed, so callers can degrade.
func DataLagDays(reportDate time.Time, asOfDate string) int {
	asOf, err := time.Parse(dateLayout, asOfDate)
	if err != nil {
		return -1
	}
	r := time.Date(reportDate.Year(), reportDate.Month(), reportDate.Day(), 0, 0, 0, 0, time.UTC)
	a := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
	return int(r.Sub(a).Hours() / 24)
}

// Classify turns a stock's flow into a StockFlowSignal. It never panics and never
// produces Inf/NaN: a non-positive AvgVolume20 yields zero pressure ratios and the
// strength then rests on consecutive-day streaks alone (plus a caveat).
func Classify(in ClassifyInput) StockFlowSignal {
	th := in.Thresholds
	if th.isZero() {
		th = DefaultStrengthThresholds()
	}

	out := StockFlowSignal{
		StockCode: in.StockCode,
		AsOfDate:  in.AsOfDate,
		FetchedAt: in.FetchedAt,
		Signal:    FlowNeutral,
		Strength:  StrengthNone,
		Caveats:   append([]string(nil), baseCaveats...),
	}

	// Data lag — invalid as_of_date is a hard degrade.
	out.DataLagDays = DataLagDays(in.ReportDate, in.AsOfDate)
	if out.DataLagDays < 0 {
		out.Computed = false
		out.Caveats = append(out.Caveats, "as_of_date 無效，延遲天數無法計算。")
		return out
	}

	if in.Flow == nil {
		out.Computed = false
		out.Reasons = append(out.Reasons, "無 ETF flow 資料。")
		return out
	}
	out.Computed = true

	// Numeric levels.
	out.ActiveDeltaShares = in.Flow.TotalActiveDeltaShares
	out.PassiveDeltaShares = in.Flow.TotalPassiveDeltaShares
	out.AggregateDeltaShares = in.Flow.TotalDeltaShares

	aBuy, aSell := maxConsec(in.Flow.Legs, TypeActive)
	pBuy, pSell := maxConsec(in.Flow.Legs, TypePassive)
	gBuy, gSell := maxConsec(in.Flow.Legs, "")
	out.ActiveConsecutiveBuyDays, out.ActiveConsecutiveSellDays = aBuy, aSell
	out.PassiveConsecutiveBuyDays, out.PassiveConsecutiveSellDays = pBuy, pSell

	avgOK := in.AvgVolume20 > 0
	out.ActivePressureRatio = PressureRatio(out.ActiveDeltaShares, in.AvgVolume20)
	out.PassivePressureRatio = PressureRatio(out.PassiveDeltaShares, in.AvgVolume20)
	out.AggregatePressureRatio = PressureRatio(out.AggregateDeltaShares, in.AvgVolume20)
	if !avgOK {
		out.Caveats = append(out.Caveats, "20 日均量不可得，壓力比未計算，僅依連續天數判斷。")
	}

	active := classifyLevel(out.ActiveDeltaShares, out.ActivePressureRatio, aBuy, aSell, th)
	passive := classifyLevel(out.PassiveDeltaShares, out.PassivePressureRatio, pBuy, pSell, th)
	agg := classifyLevel(out.AggregateDeltaShares, out.AggregatePressureRatio, gBuy, gSell, th)

	// Headline selection: strongest of active/passive (active wins ties, it is the
	// "active rotation" theme); aggregate is a fallback only when neither alone fires.
	movedActive, activeDir := activeConsensus(in.Flow.Legs)
	consensus := movedActive >= 2 && active.strength != StrengthNone && activeDir != 0 && activeDir == active.dir

	switch {
	case active.strength != StrengthNone && tierRank(active.strength) >= tierRank(passive.strength):
		out.Strength = active.strength
		switch {
		case consensus && active.dir < 0:
			out.Signal = ActiveConsensusSell
		case consensus && active.dir > 0:
			out.Signal = ActiveConsensusBuy
		case active.dir < 0:
			out.Signal = ActiveSellPressure
		default:
			out.Signal = ActiveBuySupport
		}
	case passive.strength != StrengthNone:
		out.Strength = passive.strength
		if passive.dir < 0 {
			out.Signal = PassiveSellPressure
		} else {
			out.Signal = PassiveBuySupport
		}
	case agg.strength != StrengthNone:
		out.Strength = agg.strength
		if agg.dir < 0 {
			out.Signal = AggregateSellPressure
		} else {
			out.Signal = AggregateBuySupport
		}
	}

	out.Reasons = buildReasons(active, passive, agg, avgOK)
	return out
}

// levelResult is one level's (active / passive / aggregate) classification.
type levelResult struct {
	delta    int64
	ratio    float64
	buy      int
	sell     int
	dir      int // +1 net buy, -1 net sell, 0 not significant
	strength FlowStrength
}

// classifyLevel grades one level. Direction comes from the net delta sign; the
// consecutive count fed to the tier matches that direction. A StrengthNone tier is
// treated as no direction so it cannot become a headline.
func classifyLevel(delta int64, ratio float64, buy, sell int, th StrengthThresholds) levelResult {
	dir := 0
	consec := buy
	switch {
	case delta > 0:
		dir = 1
	case delta < 0:
		dir = -1
		consec = sell
	}
	st := th.tier(ratio, consec)
	if st == StrengthNone {
		dir = 0
	}
	return levelResult{delta: delta, ratio: ratio, buy: buy, sell: sell, dir: dir, strength: st}
}

// maxConsec returns the largest consecutive buy/sell streak across legs of the given
// type ("" = all types).
func maxConsec(legs []StockETFLeg, etfType string) (buy, sell int) {
	for _, l := range legs {
		if etfType != "" && l.ETFType != etfType {
			continue
		}
		if l.ConsecutiveBuyDays > buy {
			buy = l.ConsecutiveBuyDays
		}
		if l.ConsecutiveSellDays > sell {
			sell = l.ConsecutiveSellDays
		}
	}
	return buy, sell
}

// activeConsensus counts active legs that actually moved and reports their shared
// direction (+1/-1), or 0 when they are mixed. Drives ACTIVE_ETF_CONSENSUS_*.
func activeConsensus(legs []StockETFLeg) (movedCount, dir int) {
	for _, l := range legs {
		if l.ETFType != TypeActive || l.DeltaShares == 0 {
			continue
		}
		s := 1
		if l.DeltaShares < 0 {
			s = -1
		}
		if movedCount == 0 {
			dir = s
		} else if s != dir {
			dir = 0 // mixed directions → no consensus
		}
		movedCount++
	}
	return movedCount, dir
}

// buildReasons produces human-readable, trade-instruction-free summary lines. When
// avg volume is unavailable the pressure-ratio phrasing is dropped (it would be 0%).
func buildReasons(active, passive, agg levelResult, avgOK bool) []string {
	var reasons []string
	add := func(label string, lr levelResult) {
		if lr.strength == StrengthNone {
			return
		}
		streak := lr.buy
		verb := "加碼"
		if lr.dir < 0 {
			streak = lr.sell
			verb = "減碼"
		}
		switch {
		case streak > 0 && avgOK:
			reasons = append(reasons, fmt.Sprintf("%s連續 %d 日%s（估算約占 20 日均量 %.0f%%）", label, streak, verb, lr.ratio*100))
		case streak > 0:
			reasons = append(reasons, fmt.Sprintf("%s連續 %d 日%s（20 日均量不可得，未估算壓力比）", label, streak, verb))
		case avgOK:
			reasons = append(reasons, fmt.Sprintf("%s%s（估算約占 20 日均量 %.0f%%）", label, verb, lr.ratio*100))
		default:
			reasons = append(reasons, fmt.Sprintf("%s%s", label, verb))
		}
	}
	add("主動式 ETF ", active)
	add("高股息／規則型 ETF ", passive)
	if active.strength == StrengthNone && passive.strength == StrengthNone && agg.strength != StrengthNone {
		add("ETF 合計 ", agg)
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "ETF 持股變化不明顯，無顯著 flow。")
	}
	return reasons
}
