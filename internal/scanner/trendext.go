package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// Trend / Extension / Sector-Heat shadow layer (R12).
//
// Three readings that are NOT three independent buy signals:
//
//	Slope      → which way the trend is pointing
//	BIAS       → where price currently sits inside that trend
//	SectorHeat → whether the move has group resonance behind it
//
// Combined into ONE deterministic state. The whole point is that the same slope means
// different things depending on location and company: a rising MA20 with price back at the
// MA is a pullback to buy, and a rising MA20 with price stretched far above it is the same
// trend at the wrong moment.
//
// SHADOW ONLY. TrendExtensionView never reaches RocketScore, WatchAction, Stage,
// ExplosionProb, ranking, sorting, stops or position sizing. It is attached by a post-pass
// AFTER computeRocket, on a dedicated WatchlistEntry field outside ShadowSignals — which is
// the only structure the C6b guardrail scoring reads — so it cannot reach the scoring path
// even by mistake.
// ──────────────────────────────────────────────────────────────────────────────

// TrendExtState is the combined verdict. Deterministic: one ordered decision table, no
// natural-language judgement anywhere.
type TrendExtState string

const (
	// TrendExtInsufficient — slope or BIAS could not be computed. Never guessed.
	TrendExtInsufficient TrendExtState = "INSUFFICIENT_DATA"
	// TrendConfirmed — trend up, location normal, sector behind it.
	TrendConfirmed TrendExtState = "TREND_CONFIRMED"
	// PullbackInUptrend — trend intact, price back at the MA, sector not falling apart.
	//
	// DESCRIPTIVE ONLY: a technical pullback inside an existing uptrend. R12 validation did
	// NOT support treating it as a superior entry — 20d win rate 29.9% vs a 34.7% baseline,
	// with no mean-return advantage and only a slightly shallower drawdown. It must not be
	// worded as a better entry, an add-on point, a buy-the-dip edge, or a high-probability
	// setup. See docs/R12_VALIDATION_RESULTS.md §2.
	PullbackInUptrend TrendExtState = "PULLBACK_IN_UPTREND"
	// TrendExtended — trend up but price is stretched for THIS stock. A do-not-chase
	// warning, NOT a stronger buy. This distinction is the entire reason the state exists.
	TrendExtended TrendExtState = "EXTENDED"
	// SectorConfirmed — sector resonance is there and the short trend is up, but the
	// intermediate trend (MA60) has not turned yet. Group leads, structure lags.
	SectorConfirmed TrendExtState = "SECTOR_CONFIRMED"
	// SectorDivergence — the stock is rising alone. Lowers trend confidence.
	SectorDivergence TrendExtState = "SECTOR_DIVERGENCE"
	// TrendWeakening — trend down and price below the MA: weakness confirmed, not a dip.
	TrendWeakening TrendExtState = "WEAKENING"
	// TrendExtNeutral — computable, but no cell of the table fires.
	TrendExtNeutral TrendExtState = "NEUTRAL"
)

// Evidence codes. Deterministic, emitted by the analyzer and rendered verbatim — the report
// never re-derives any of this.
const (
	CodeMA20SlopeUp   = "MA20_SLOPE_UP"
	CodeMA20SlopeDown = "MA20_SLOPE_DOWN"
	CodeMA20SlopeFlat = "MA20_SLOPE_FLAT"
	CodeMA60SlopeUp   = "MA60_SLOPE_UP"
	CodeMA60SlopeDown = "MA60_SLOPE_DOWN"
	CodeSlopeAligned  = "SLOPE_ALIGNED_UP"

	CodeBias20Extended = "BIAS20_EXTENDED"
	CodeBias20NearMA   = "BIAS20_NEAR_MA"
	CodeBias20Below    = "BIAS20_BELOW_MA"
	// CodeReboundInDowntrend — price above a FALLING MA20: a bounce inside a deteriorating
	// trend, which reads bullish on price alone and is not.
	CodeReboundInDowntrend = "REBOUND_IN_DOWNTREND"

	CodeSectorHeatStrong          = "SECTOR_HEAT_STRONG"
	CodeSectorHeatWeak            = "SECTOR_HEAT_WEAK"
	CodeSectorParticipationStrong = "SECTOR_PARTICIPATION_STRONG"
	CodeSectorVolumeConfirm       = "SECTOR_VOLUME_CONFIRM"
	CodeSectorHeatUnknown         = "SECTOR_HEAT_UNKNOWN"
)

// Slope windows. MA20 over a trading week and MA60 over a fortnight match the scanner's
// 1–4 week swing horizon: shorter reads noise, longer cannot turn inside the holding period.
const (
	trendMA20SlopeLookback = 5
	trendMA60SlopeLookback = 10
)

// Decision thresholds. Every one of these is a BASELINE HYPOTHESIS to be tested by the R12
// validation run, not a calibrated constant — the literature on technical-rule data snooping
// (Brock et al.; Sullivan/Timmermann/White) is precisely about numbers chosen because they
// looked good on the sample they were chosen from. They live here, in one block, so a
// calibration run has exactly one place to change.
const (
	// trendSlopeFlatPct is the dead band around zero, in % per day. Below it a moving
	// average is treated as flat rather than as weakly directional: 0.04%/day is ~0.2% over
	// the MA20 window, which is inside the noise of a daily average.
	trendSlopeFlatPct = 0.04

	// trendNearMAPct is the "price is back at the MA" band, |BIAS20| in %. Kept as a raw
	// band rather than a percentile because proximity to the MA is an absolute geometric
	// fact, and the research on price-to-MA distance puts constructive entries within a few
	// percent of the shorter averages.
	trendNearMAPct = 3.0

	// trendExtendedPercentile is the primary extension test: BIAS20 in the top decile of
	// THIS stock's own trailing year. Per-stock by design — a fixed "+10% is extended" rule
	// calls a quiet utility overheated and a volatile small cap normal on the same day.
	trendExtendedPercentile = 90
	trendBiasWindow         = 252
	trendBiasMinSample      = 120

	// Sector heat bands, 0–100.
	heatStrongMin = 65
	heatWeakMax   = 35
	// heatHealthyMin is the "sector is not a problem" floor used by the pullback cell.
	heatHealthyMin = 45

	heatParticipationStrongMin = 60
	heatVolumeConfirmMin       = 50
)

// TrendExtThresholds exposes the decision numbers as data.
//
// It exists so the R12 backtest measures the SHIPPED hypothesis rather than a copy of it:
// a validation run against mirrored constants validates whatever the mirror happened to say,
// and the two drift apart the first time one side is tuned. Read-only by construction — the
// state machine reads the constants directly, so nothing can be reconfigured through here.
type TrendExtThresholds struct {
	MA20SlopeLookback int
	MA60SlopeLookback int
	SlopeFlatPct      float64
	NearMAPct         float64
	ExtendedPct       float64
	BiasWindow        int
	BiasMinSample     int
	HeatStrongMin     float64
	HeatWeakMax       float64
	HeatHealthyMin    float64
}

// DefaultTrendExtThresholds returns the thresholds the shipped state machine actually uses.
func DefaultTrendExtThresholds() TrendExtThresholds {
	return TrendExtThresholds{
		MA20SlopeLookback: trendMA20SlopeLookback,
		MA60SlopeLookback: trendMA60SlopeLookback,
		SlopeFlatPct:      trendSlopeFlatPct,
		NearMAPct:         trendNearMAPct,
		ExtendedPct:       trendExtendedPercentile,
		BiasWindow:        trendBiasWindow,
		BiasMinSample:     trendBiasMinSample,
		HeatStrongMin:     heatStrongMin,
		HeatWeakMax:       heatWeakMax,
		HeatHealthyMin:    heatHealthyMin,
	}
}

// TrendExtensionView is one stock's combined reading.
//
// Pointer fields are unavailable-aware: nil means "could not be computed", never 0. A flat
// slope and an unknown slope are different facts and must not collapse into the same value.
type TrendExtensionView struct {
	State TrendExtState

	// Slope, % per trading day.
	MA20Slope5D  *float64
	MA60Slope10D *float64

	// Location, % deviation from the moving average.
	Bias20 *float64
	Bias60 *float64
	// Bias20Percentile ranks Bias20 against this stock's own trailing history (0–100).
	// nil when the history is too short — the state machine then falls back explicitly.
	Bias20Percentile *float64

	// Sector heat, copied from the sector's rotation entry. nil when the stock has no
	// sector; Status INSUFFICIENT_DATA when the sector was too thin to measure.
	Sector string
	Heat   *SectorHeatView

	Codes []string
}

// Computed reports whether the reading carries a usable state.
func (v *TrendExtensionView) Computed() bool {
	return v != nil && v.State != "" && v.State != TrendExtInsufficient
}

// trendExtInput is everything the state machine reads. Explicit rather than pulled from a
// WatchlistEntry so the decision table can be tested directly, without constructing candles.
type trendExtInput struct {
	MA20Slope *float64
	MA60Slope *float64
	Bias20    *float64
	Bias60    *float64
	BiasPct   *float64 // Bias20 percentile, 0–100
	BiasRisk  string   // R10-1 label, used ONLY as the percentile fallback
	Sector    string
	Heat      *SectorHeatView
}

// computeTrendExtension runs the ordered decision table.
//
// Order is load-bearing and deliberately puts the warnings first: EXTENDED is evaluated
// before TREND_CONFIRMED so a stretched stock in a hot sector can never be reported as a
// clean confirmation. That single ordering is what stops this layer from turning a
// do-not-chase into a super-buy.
func computeTrendExtension(in trendExtInput) TrendExtensionView {
	v := TrendExtensionView{
		MA20Slope5D:      in.MA20Slope,
		MA60Slope10D:     in.MA60Slope,
		Bias20:           in.Bias20,
		Bias60:           in.Bias60,
		Bias20Percentile: in.BiasPct,
		Sector:           in.Sector,
		Heat:             in.Heat,
	}

	// Slope and location are both required. Sector heat is optional — a stock with no
	// sector still has a trend and a location; it just cannot be group-confirmed.
	if in.MA20Slope == nil || in.Bias20 == nil {
		v.State = TrendExtInsufficient
		return v
	}
	slope20, bias20 := *in.MA20Slope, *in.Bias20

	up20 := slope20 > trendSlopeFlatPct
	down20 := slope20 < -trendSlopeFlatPct
	// MA60 missing is NOT treated as bearish: it usually means a young listing. The cells
	// that need long-trend agreement require it explicitly; the rest do not consult it.
	slope60Known := in.MA60Slope != nil
	up60 := slope60Known && *in.MA60Slope > trendSlopeFlatPct
	notDown60 := slope60Known && *in.MA60Slope >= -trendSlopeFlatPct

	extended := isExtended(in)
	nearMA := abs(bias20) <= trendNearMAPct

	heatKnown := in.Heat != nil && in.Heat.Status == SectorHeatOK
	heatStrong := heatKnown && in.Heat.Heat >= heatStrongMin
	heatHealthy := heatKnown && in.Heat.Heat >= heatHealthyMin
	heatWeak := heatKnown && in.Heat.Heat <= heatWeakMax

	v.Codes = trendCodes(in, slope20, bias20, up20, down20, up60, extended, nearMA, heatKnown)

	switch {
	// 1. Weakness confirmed: the trend is down and price is below the average. Nothing here
	//    is a dip to buy.
	case down20 && bias20 <= 0:
		v.State = TrendWeakening

	// 2. Stretched. Evaluated before every bullish cell — this is the do-not-chase gate.
	case up20 && extended:
		v.State = TrendExtended

	// 3. The setup this layer is for: trend up, MA60 not fighting it, price back at the MA,
	//    sector not falling apart.
	case up20 && notDown60 && nearMA && !heatWeak:
		v.State = PullbackInUptrend

	// 4. Rising alone. Known-weak sector only — an UNKNOWN sector is not divergence.
	case up20 && heatWeak:
		v.State = SectorDivergence

	// 5. Trend aligned across both averages and the group is behind it.
	case up20 && notDown60 && heatStrong:
		v.State = TrendConfirmed

	// 6. Group resonance is there but the intermediate trend has not turned yet.
	case up20 && heatStrong:
		v.State = SectorConfirmed

	// 7. Trend aligned, sector merely healthy or unknown.
	case up20 && up60 && heatHealthy:
		v.State = TrendConfirmed

	default:
		v.State = TrendExtNeutral
	}
	return v
}

// isExtended decides whether price is stretched for THIS stock.
//
// The percentile is authoritative when available. The fallback is the EXISTING R10-1 BIAS
// risk label rather than a fresh set of numbers — the repo already has four places that
// decide what "extended" means, and adding a fifth definition would be the actual harm.
func isExtended(in trendExtInput) bool {
	if in.BiasPct != nil {
		return *in.BiasPct >= trendExtendedPercentile
	}
	return in.BiasRisk == BiasRiskHigh || in.BiasRisk == BiasRiskExtreme
}

func trendCodes(in trendExtInput, slope20, bias20 float64,
	up20, down20, up60, extended, nearMA, heatKnown bool) []string {

	var codes []string
	switch {
	case up20:
		codes = append(codes, CodeMA20SlopeUp)
	case down20:
		codes = append(codes, CodeMA20SlopeDown)
	default:
		codes = append(codes, CodeMA20SlopeFlat)
	}
	if in.MA60Slope != nil {
		if up60 {
			codes = append(codes, CodeMA60SlopeUp)
		} else if *in.MA60Slope < -trendSlopeFlatPct {
			codes = append(codes, CodeMA60SlopeDown)
		}
	}
	if up20 && up60 {
		codes = append(codes, CodeSlopeAligned)
	}

	switch {
	case extended:
		codes = append(codes, CodeBias20Extended)
	case nearMA:
		codes = append(codes, CodeBias20NearMA)
	case bias20 < 0:
		codes = append(codes, CodeBias20Below)
	}
	// A bounce inside a falling average: bullish on price alone, and not bullish.
	if down20 && bias20 > 0 {
		codes = append(codes, CodeReboundInDowntrend)
	}

	if !heatKnown {
		codes = append(codes, CodeSectorHeatUnknown)
	} else {
		codes = append(codes, in.Heat.Codes...)
	}
	return codes
}

// buildTrendExtInput derives slope and BIAS from a candle history, reusing the existing
// indicator primitives. No moving average is recomputed with a private formula: SMA, BIAS
// and the percentile all come from internal/indicator.
func buildTrendExtInput(candles []fetcher.Candle, biasRisk, sector string, heat *SectorHeatView) trendExtInput {
	in := trendExtInput{BiasRisk: biasRisk, Sector: sector, Heat: heat}
	closes := closeSlice(candles)
	if len(closes) == 0 {
		return in
	}

	if s, ok := indicator.MASlopePerDay(indicator.SMA(closes, 20), trendMA20SlopeLookback); ok {
		in.MA20Slope = &s
	}
	if s, ok := indicator.MASlopePerDay(indicator.SMA(closes, 60), trendMA60SlopeLookback); ok {
		in.MA60Slope = &s
	}
	if b, ok := indicator.BIAS(closes, 20); ok {
		in.Bias20 = &b
	}
	if b, ok := indicator.BIAS(closes, 60); ok {
		in.Bias60 = &b
	}
	if p, ok := indicator.BIASPercentile(closes, 20, trendBiasWindow, trendBiasMinSample); ok {
		in.BiasPct = &p
	}
	return in
}
