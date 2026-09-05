package healthcheck

import (
	"strconv"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The technical block is a PROJECTION of the scanner's own reading.
//
// Not one indicator is computed here. Every value is lifted from the WatchlistEntry that
// scanner.EnrichWatchlist produced, so "the dashboard's RSI" and "the report's RSI" are the
// same number by construction rather than by two implementations that happen to agree.
//
// R14 also does not invent indicators. The spec lists MA20 / MA60 / MA120 / RSI / volume /
// trend / BIAS / candlestick / stage / sector rotation, qualified with "只有現有功能存在才使用".
// What exists is what appears below; MA60 and MA120 have no field on StockAnalysis, and the
// nearest existing readings — the MA60 slope inside the trend-extension layer, and the 60-day
// BIAS — are surfaced instead of deriving new averages here.

// TechnicalBlock is the chart read.
type TechnicalBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	// ── the scanner's own decision sheet, labelled as the scanner's throughout ──
	//
	// These are carried so a user can see what the daily scan already thinks. They are
	// READ-ONLY here, and nothing R14 computes feeds back into them.
	ScannerAction string `json:"scanner_action,omitempty"`
	WatchAction   string `json:"watch_action,omitempty"`
	Stage         string `json:"stage,omitempty"`
	RocketScore   int    `json:"rocket_score"`
	ExplosionProb string `json:"explosion_probability,omitempty"`
	ScannerScore  int    `json:"scanner_score"`

	// ── indicators ──
	MA20 Value `json:"ma20"`
	// MA20DistancePct is an ALIAS of Bias.Bias20, not a second calculation of it.
	MA20DistancePct Value  `json:"ma20_distance_pct"`
	MA20Trend       string `json:"ma20_trend,omitempty"`
	RSI             Value  `json:"rsi"`
	ATR             Value  `json:"atr"`
	AvgVolume20     Value  `json:"avg_volume_20"`

	PriceVolumeSignal string `json:"price_volume_signal,omitempty"`
	PriceVolumeState  string `json:"price_volume_state,omitempty"`
	PriceMove         string `json:"price_move,omitempty"`
	LimitStatus       string `json:"limit_status,omitempty"`

	// ── BIAS (乖離率) ──
	Bias   BiasBlock  `json:"bias"`
	Trend  TrendBlock `json:"trend"`
	Levels LevelBlock `json:"levels"`

	// Indicators is the R14-technical-indicator layer (ADX / RSI / MACD / Keltner / pivots)
	// plus its aggregated context, carried as the domain's own object. Nil when that layer
	// is switched off in the scanner config — DISABLED, which is different from "computed
	// and empty".
	Indicators any `json:"indicators,omitempty"`
	// Candlestick is the K-line reading, likewise the domain's own object, nil when off.
	Candlestick any `json:"candlestick,omitempty"`

	RiskLabel   string   `json:"risk_label,omitempty"`
	RiskWarning string   `json:"risk_warning,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
}

// BiasBlock is the deviation-from-average risk read. BIAS is a RISK, not a trade signal.
type BiasBlock struct {
	Status Status `json:"status"`
	Bias5  Value  `json:"bias_5"`
	Bias10 Value  `json:"bias_10"`
	Bias20 Value  `json:"bias_20"`
	Bias60 Value  `json:"bias_60"`
	Risk   string `json:"risk,omitempty"`
}

// TrendBlock is the trend-extension reading: direction (slope) plus location (deviation).
type TrendBlock struct {
	Status           Status `json:"status"`
	State            string `json:"state,omitempty"`
	MA20Slope5D      Value  `json:"ma20_slope_5d"`
	MA60Slope10D     Value  `json:"ma60_slope_10d"`
	Bias20Percentile Value  `json:"bias_20_percentile"`
	Sector           string `json:"sector,omitempty"`
	SectorHeat       string `json:"sector_heat,omitempty"`
}

// LevelBlock is the scanner's own price plan. It is shown because a health check that says
// "wait" is far more useful beside the level being waited for.
type LevelBlock struct {
	Status         Status `json:"status"`
	BreakoutPrice  Value  `json:"breakout_price"`
	SupportPrice   Value  `json:"support_price"`
	StopLossPrice  Value  `json:"stop_loss_price"`
	EntryZone      string `json:"entry_zone,omitempty"`
	TakeProfitZone string `json:"take_profit_zone,omitempty"`
}

const scannerSource = "scanner.WatchlistEntry"

func technicalBlock(ev *Evidence) TechnicalBlock {
	if ev.Entry == nil {
		reason := ev.PriceReason
		if reason == "" {
			reason = "no scanner entry for this stock"
		}
		none := Missing(StatusUnavailable, reason)
		return TechnicalBlock{
			Status: StatusUnavailable, Reason: reason,
			MA20: none, MA20DistancePct: none, RSI: none, ATR: none, AvgVolume20: none,
			Bias:   BiasBlock{Status: StatusUnavailable, Bias5: none, Bias10: none, Bias20: none, Bias60: none},
			Trend:  TrendBlock{Status: StatusUnavailable, MA20Slope5D: none, MA60Slope10D: none, Bias20Percentile: none},
			Levels: LevelBlock{Status: StatusUnavailable, BreakoutPrice: none, SupportPrice: none, StopLossPrice: none},
		}
	}

	e := ev.Entry
	a := e.A
	b := TechnicalBlock{
		Status:            StatusAvailable,
		ScannerAction:     string(a.Action),
		WatchAction:       string(e.WatchAction),
		Stage:             string(e.RocketStage),
		RocketScore:       e.RocketScore,
		ExplosionProb:     e.ExplosionProb,
		ScannerScore:      a.Score,
		MA20Trend:         a.MA20Trend,
		PriceVolumeSignal: a.PriceVolumeSignal,
		PriceVolumeState:  a.PriceVolumeState,
		PriceMove:         a.PriceMove,
		LimitStatus:       a.LimitStatus,
		// RiskLabel / RiskWarning are set below, through hasRisk.
		Reasons: e.Reasons,
	}

	// A moving average of 0 means the warm-up never completed — it is not a price.
	if a.MA20 > 0 {
		b.MA20 = Num(a.MA20, "TWD", 2, scannerSource)
	} else {
		b.MA20 = Missing(StatusInsufficientData, "20-day average not yet formed")
	}
	if a.RSI > 0 {
		b.RSI = Num(a.RSI, "", 1, scannerSource)
	} else {
		b.RSI = Missing(StatusInsufficientData, "RSI warm-up incomplete")
	}
	if a.ATR > 0 {
		b.ATR = Num(a.ATR, "TWD", 2, scannerSource)
	} else {
		b.ATR = Missing(StatusInsufficientData, "ATR warm-up incomplete")
	}
	if a.AvgVolume20 > 0 {
		b.AvgVolume20 = Num(float64(a.AvgVolume20), "股", 0, scannerSource)
	} else {
		b.AvgVolume20 = Missing(StatusInsufficientData, "20-day average volume not yet formed")
	}

	// The scanner writes "—" when a stock has NO risk label. That sentinel is a PRESENTATION
	// artefact, and this is the boundary where the API is built, so it stops here: a field
	// meaning "the risk label, if any" is absent when there is none, and `omitempty` drops it
	// from the JSON entirely.
	//
	// Fixing it here rather than in each consumer is the point. The same sentinel had already
	// been mistaken for content by the assessment, the evidence table, the model's prompt and
	// the dashboard's price card — four independent `!= ""` tests, one of which reached the
	// user as 「風險：—」. Downstream guards remain as defence in depth; this is the source.
	if hasRisk(e.RiskLabel) {
		b.RiskLabel = e.RiskLabel
		b.RiskWarning = e.RiskWarning
	}

	b.Bias = biasBlock(e.Bias)
	// (close - MA20) / MA20 IS 20-day bias. Recomputing it here would put a second,
	// separately-derived copy of one number on the same page: two cells that must agree
	// but are free to drift, and — worse — a cell that keeps reporting a bias while the
	// 乖離 section right beside it says DISABLED because enable_bias is off. There is one
	// bias in this repo, indicator.BIAS via scanner.BiasView, so this is that value.
	b.MA20DistancePct = b.Bias.Bias20
	b.Trend = trendBlock(e.TrendExt)
	b.Levels = levelBlock(e)
	if e.Technical != nil {
		b.Indicators = e.Technical
	}
	if e.Candlestick != nil {
		b.Candlestick = e.Candlestick
	}
	return b
}

func biasBlock(v *scanner.BiasView) BiasBlock {
	if v == nil {
		// nil means enable_bias is off — a decision, not a data fact.
		none := Missing(StatusDisabled, "enable_bias is off in the scanner config")
		return BiasBlock{Status: StatusDisabled, Bias5: none, Bias10: none, Bias20: none, Bias60: none}
	}
	return BiasBlock{
		Status: StatusAvailable,
		Bias5:  biasValue(v.Bias5, 5),
		Bias10: biasValue(v.Bias10, 10),
		Bias20: biasValue(v.Bias20, 20),
		Bias60: biasValue(v.Bias60, 60),
		Risk:   v.Risk,
	}
}

func biasValue(p *float64, window int) Value {
	if p == nil {
		return Missing(StatusInsufficientData,
			"not enough history for the "+strconv.Itoa(window)+"-day average")
	}
	return Num(*p, "%", 2, scannerSource)
}

func trendBlock(v *scanner.TrendExtensionView) TrendBlock {
	if v == nil {
		none := Missing(StatusDisabled, "enable_trend_extension is off in the scanner config")
		return TrendBlock{Status: StatusDisabled, MA20Slope5D: none, MA60Slope10D: none, Bias20Percentile: none}
	}
	b := TrendBlock{
		Status:           StatusAvailable,
		State:            string(v.State),
		Sector:           v.Sector,
		MA20Slope5D:      pctValue(v.MA20Slope5D, "5 日 MA20 斜率不足以計算"),
		MA60Slope10D:     pctValue(v.MA60Slope10D, "10 日 MA60 斜率不足以計算"),
		Bias20Percentile: pctValue(v.Bias20Percentile, "本股歷史不足以定位乖離百分位"),
	}
	if v.Heat != nil {
		b.SectorHeat = heatLabel(v.Heat)
	}
	if !v.Computed() {
		b.Status = StatusInsufficientData
	}
	return b
}

// heatLabel reports the sector heat as its STATUS when the reading is not usable, and as the
// composite score otherwise. A sector with too few measurable members reports
// INSUFFICIENT_DATA rather than a heat of 0, which would rank it as the coldest sector on the
// board — the precise mistake internal/scanner already refuses to make.
func heatLabel(h *scanner.SectorHeatView) string {
	if h == nil {
		return ""
	}
	if h.Status != scanner.SectorHeatOK {
		return h.Status
	}
	return formatNumber(h.Heat, "", 1)
}

func pctValue(p *float64, reason string) Value {
	if p == nil {
		return Missing(StatusInsufficientData, reason)
	}
	return Num(*p, "%", 2, scannerSource)
}

func levelBlock(e *scanner.WatchlistEntry) LevelBlock {
	b := LevelBlock{
		Status:         StatusAvailable,
		EntryZone:      e.EntryZone,
		TakeProfitZone: e.TakeProfitZone,
		BreakoutPrice:  priceLevel(e.BreakoutPrice),
		SupportPrice:   priceLevel(e.SupportPrice),
		StopLossPrice:  priceLevel(e.StopLossPrice),
	}
	return b
}

// priceLevel treats a zero level as ABSENT. A stop-loss of 0 is not a price a human would
// ever act on, and printing it as one is how a page invites a catastrophic order.
func priceLevel(v float64) Value {
	if v <= 0 {
		return Missing(StatusUnavailable, "the scanner produced no level here")
	}
	return Num(v, "TWD", 2, scannerSource)
}
