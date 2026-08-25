package scanner

import "math"

// ──────────────────────────────────────────────────────────────────────────────
// Price-move magnitude
//
// The scanner has always known which WAY price moved — analyzeVolume's priceUp is
// closes[n-1] > closes[n-2] — but never HOW FAR. That made +0.2% and +7.5% the same
// "價漲量縮", and −0.3% and −6.8% the same "價跌量縮", which is the difference between a
// quiet pullback and real damage that happened to be thin.
//
// This file adds the magnitude WITHOUT touching the existing signal. PriceVolumeSignal keeps
// its exact Chinese vocabulary because six consumers read it — report.pvCSS switches on the
// literal strings, tests assert them, and every historical SQLite / analysis-history row
// already carries them. Changing that enum would silently mis-colour new labels (pvCSS falls
// through to pv-down-vol-down) and make past rows incomparable with future ones. So the new
// information is ADDITIVE: the old label stays, and a machine-readable magnitude sits beside it.
// ──────────────────────────────────────────────────────────────────────────────

// PriceMove classifies one session's move by direction AND size.
const (
	MoveLargeUp   = "LARGE_UP"
	MoveSmallUp   = "SMALL_UP"
	MoveFlat      = "FLAT"
	MoveSmallDown = "SMALL_DOWN"
	MoveLargeDown = "LARGE_DOWN"
	MoveUnknown   = "UNKNOWN" // no previous close to measure against
)

// Volume state, expressed in the SAME ratio bands the scoring already uses: analyzeVolume
// treats >= 1.2 as expansion, and its score table treats < 0.8 as the thin bucket. Reusing
// them means this adds no new magic number and cannot disagree with the score about what
// "量增" means.
const (
	VolExpansion = "VOLUME_EXPANSION"
	VolNormalTag = "VOLUME_NORMAL"
	VolShrink    = "VOLUME_SHRINK"
)

// PriceMoveThresholds are the band edges, in percent.
//
// FIXED PERCENTAGES, not ATR multiples or percentiles — deliberately, and for a reason
// specific to this market. TWSE/TPEx cap a stock at ±10% per session, so a daily move lives
// on a BOUNDED scale that means the same thing for every stock: 3% is 30% of the maximum the
// day could have produced. That is why detectLimitStatus already classifies daily gain with
// fixed 9.0/9.5 cuts rather than normalising them. R12 reached for a per-stock percentile
// because BIAS is unbounded and 15% means different things on different stocks; a daily move
// is not that quantity.
//
// The ATR-relative view is still available — PriceChangeATR carries it as a NUMBER, so
// "3% on a quiet large-cap" and "3% on a volatile small-cap" remain distinguishable — but it
// is deliberately not a second classification competing with this one.
//
// These numbers are UNVALIDATED BASELINES. Following R12's precedent they are code constants
// with an exported accessor rather than config knobs: a knob invites tuning before there is
// any evidence, and the R14 backtest harness is where they should earn their values.
type PriceMoveThresholds struct {
	// FlatPct: |change| below this is noise rather than direction.
	FlatPct float64
	// LargePct: |change| at or above this is a significant move.
	LargePct float64
	// VolExpansionRatio / VolShrinkRatio mirror the existing scoring bands.
	VolExpansionRatio float64
	VolShrinkRatio    float64
}

// DefaultPriceMoveThresholds is the shipped baseline. Exported so a backtest measures the
// values that actually run, never a private copy.
func DefaultPriceMoveThresholds() PriceMoveThresholds {
	return PriceMoveThresholds{
		FlatPct:           0.5, // 5% of the daily limit — below this the sign is noise
		LargePct:          3.0, // 30% of the daily limit
		VolExpansionRatio: volExpansionRatio,
		VolShrinkRatio:    volShrinkRatio,
	}
}

// The two ratio bands already in use elsewhere in this package.
const (
	volExpansionRatio = 1.2 // analyzeVolume's volUp
	volShrinkRatio    = 0.8 // the score table's thin-volume floor
)

// priceMoveResult is what the classifier produces for one stock.
type priceMoveResult struct {
	changePct float64
	changeATR float64
	move      string
	state     string
}

// classifyPriceMove derives the session's change, its magnitude class, and the combined
// price×volume state.
//
// prevClose <= 0 (a missing or invalid previous session) yields MoveUnknown and a zero
// change rather than a fabricated 0.0% — a stock whose previous close is unknown has not
// "moved 0%", and the scanner's MISSING ≠ NEUTRAL contract applies here as everywhere else.
//
// atrPct is the ATR as a percentage of price; zero or non-finite means the ATR was not
// available (warm-up, or a perfectly flat tape) and PriceChangeATR stays 0.
func classifyPriceMove(closes []float64, atr float64, volRatio float64, th PriceMoveThresholds) priceMoveResult {
	// An unmeasurable move makes the COMBINED state unmeasurable too: "UNKNOWN_VOLUME_SHRINK"
	// would be a half-formed reading that no consumer wants and none would surface.
	out := priceMoveResult{move: MoveUnknown, state: MoveUnknown}

	n := len(closes)
	if n < 2 {
		return out
	}
	prev, last := closes[n-2], closes[n-1]
	if prev <= 0 || !finiteNum(prev) || !finiteNum(last) {
		return out
	}

	chg := (last/prev - 1) * 100
	if !finiteNum(chg) {
		return out
	}
	out.changePct = chg
	out.move = moveClass(chg, th)
	out.state = out.move + "_" + volumeState(volRatio, th)

	// How many ATRs did it travel? Answers "is 3% big for THIS stock" without becoming a
	// competing classification. Zero when ATR is unavailable — never a division artefact.
	if atr > 0 && last > 0 {
		if atrPct := atr / last * 100; atrPct > 0 && finiteNum(atrPct) {
			if v := chg / atrPct; finiteNum(v) {
				out.changeATR = math.Round(v*100) / 100
			}
		}
	}
	return out
}

// moveClass buckets a percentage change. Boundaries are inclusive at the upper edge of each
// band's magnitude, so exactly ±0.5% is FLAT and exactly ±3.0% is LARGE.
func moveClass(chg float64, th PriceMoveThresholds) string {
	abs := math.Abs(chg)
	switch {
	case abs <= th.FlatPct:
		return MoveFlat
	case chg >= th.LargePct:
		return MoveLargeUp
	case chg > 0:
		return MoveSmallUp
	case chg <= -th.LargePct:
		return MoveLargeDown
	default:
		return MoveSmallDown
	}
}

// volumeState buckets the volume ratio. A ratio of exactly 0 means the volume MA was
// unavailable, which is not "thin trading" — it is no measurement.
func volumeState(ratio float64, th PriceMoveThresholds) string {
	switch {
	case ratio <= 0 || !finiteNum(ratio):
		return MoveUnknown
	case ratio >= th.VolExpansionRatio:
		return VolExpansion
	case ratio <= th.VolShrinkRatio:
		return VolShrink
	default:
		return VolNormalTag
	}
}

func finiteNum(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
