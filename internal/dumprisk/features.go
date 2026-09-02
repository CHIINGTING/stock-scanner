// Package dumprisk measures the observable, same-session conditions under which a rally is
// more likely to be handed back on the following sessions — the 隔日沖 / 次日倒貨 question.
//
// The package is deliberately SMALL and PURE. It answers one thing: given the bars a scanner
// could see at the close of day T, which risk factors are present? It does not score, does
// not rank, and does not know what happens on T+1 — the forward outcome lives in the research
// harness (internal/r6backtest/dumprisk.go), so a feature can never accidentally read its own
// answer.
//
// # Why not just "大漲 + 爆量"
//
// A large gain on heavy volume is the ENTRY condition for both readings of the same tape: a
// genuine breakout with real demand, and a short-term ramp that is distributed the next
// morning. What separates them is not the size of the move but WHERE the session ended
// inside its own range and how much of the advance was already given back before the close.
// That is what CloseLocationValue and UpperShadowPct measure.
//
// # Reuse, not re-derivation
//
// The bar geometry comes from internal/candlestick — the same Metrics, the same validation
// (High==Low, non-finite, inconsistent bounds), so a degenerate bar is rejected here exactly
// as it is there and no second definition of "upper shadow" can drift from the first.
// Magnitude bands come from scanner.DefaultPriceMoveThresholds so this measures the
// classification that actually ships.
package dumprisk

import (
	"math"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
)

// Features are the same-session, causally-available measurements for one bar.
//
// Every field is derived from bars at or before T. Optional inputs that were unavailable
// leave their *OK flag false; a consumer must branch on the flag rather than read a zero,
// because "no day-trading data for this stock" and "zero day-trading" are different facts and
// the repo treats MISSING ≠ ZERO everywhere.
type Features struct {
	// ── Bar geometry (always present when Valid) ─────────────────────────────
	// CloseLocationValue is (Close-Low)/(High-Low), in [0,1]. 1 = closed on the high,
	// 0 = closed on the low. The single most direct expression of "did the buyers hold
	// the advance into the close, or was it sold into".
	CloseLocationValue float64
	// UpperShadowPct is (High-max(Open,Close))/(High-Low), in [0,1] — the fraction of the
	// day's range spent above the body, i.e. intraday advance that was rejected.
	UpperShadowPct float64
	// LowerShadowPct and BodyPct complete the geometry; carried so a study can tell a weak
	// close that never rallied from one that rallied and gave it all back.
	LowerShadowPct float64
	BodyPct        float64

	// GiveBackPct is how much of the session's HIGH-water advance was surrendered by the
	// close, as a fraction of the day's range: (High-Close)/(High-Low). It differs from
	// UpperShadowPct whenever the close is below the open — the upper shadow measures to
	// the body top, this measures to the close.
	GiveBackPct float64

	// ── Move magnitude ───────────────────────────────────────────────────────
	// PriceChangePct is close-to-close, in percent. Valid only when ChangeOK.
	PriceChangePct float64
	ChangeOK       bool
	// PriceChangeATR expresses the same move in ATR(14) multiples, so a 4% day on a quiet
	// large-cap and on a volatile small-cap are not read as the same event. ATROK is false
	// during ATR warm-up.
	PriceChangeATR float64
	ATROK          bool

	// ── Participation ────────────────────────────────────────────────────────
	// VolumeRatio is the session's volume over its own trailing 20-day mean.
	VolumeRatio float64
	VolumeOK    bool

	// DayTradingRatio is 當沖成交股數 / 當日成交股數 for this stock, in [0,1]. This is the
	// most direct available measure of how much of the day's turnover never intended to
	// hold overnight. Optional: false when no 當沖 snapshot covers this stock-day.
	DayTradingRatio float64
	DayTradingOK    bool

	// ── Structural context ───────────────────────────────────────────────────
	// NearLimitUp marks a session that closed at or very near the +10% daily cap. Taiwan
	// caps a session at ±10%, so a +9.9% close is a queue at the limit, not a free-floating
	// large gain, and it is held out rather than pooled with ordinary big up days.
	NearLimitUp bool

	// Valid is false when the bar failed candlestick validation (non-finite, inconsistent
	// bounds, or zero range such as a limit-locked day). Geometry fields are then zero and
	// MUST NOT be read; the bar carries no shape information.
	Valid     bool
	InvalidBy error
}

// Input is one bar plus the trailing context needed to place it. Everything here is knowable
// at the close of the bar itself.
type Input struct {
	Open, High, Low, Close float64
	Volume                 float64

	// PrevClose <= 0 means there is no measurable previous session; the move is then
	// reported as unavailable rather than as 0%.
	PrevClose float64
	// VolMA20 is the trailing 20-session mean volume. <= 0 means unavailable.
	VolMA20 float64
	// ATR is ATR(14) in price units. <= 0 means warm-up / unavailable.
	ATR float64

	// DayTradeShares / TotalShares carry 當沖 participation when a snapshot covers this
	// stock-day. DayTradingOK is set only when TotalShares > 0 AND HasDayTrading is true,
	// so an absent snapshot can never masquerade as a 0% day-trading session.
	HasDayTrading  bool
	DayTradeShares float64
	TotalShares    float64
}

// LimitUpPct is the Taiwan daily price cap. A close within LimitUpNearPct of it is treated as
// "at the limit" — the exchange rounds to the tick, so an exact 10.00% is not reachable for
// most price levels and a fixed 9.5 cut is what the shipped scanner already uses
// (scorer.go's prevLocked test).
const (
	LimitUpPct     = 10.0
	LimitUpNearPct = 9.5
)

// boundaryEps absorbs the representation error of a derived percentage at an exactly-stated
// cut. A close of 109.50 against a previous close of 100 is 9.5% by construction, but
// (109.50/100-1)*100 evaluates to 9.499999999999998, so a bare >= would put a textbook
// boundary case on the wrong side. The repo's candlestick thresholds are inclusive by a
// LOCKED policy (SPEC R10-2 §5.2); this keeps that policy true for a ratio that cannot be
// represented exactly.
const boundaryEps = 1e-9

// Compute derives the same-session features for one bar.
//
// Bar validation is delegated to internal/candlestick so that "what counts as a usable bar"
// has exactly one definition in this repo. A bar that fails it yields Valid=false with the
// geometry fields left at zero — never NaN, never Inf, and never a fabricated 0.5 midpoint.
func Compute(in Input) Features {
	var f Features

	m, err := candlestick.ComputeMetrics(candlestick.Candle{
		Open:  in.Open,
		High:  in.High,
		Low:   in.Low,
		Close: in.Close,
	})
	if err != nil {
		f.InvalidBy = err
	} else {
		f.Valid = true
		f.UpperShadowPct = m.UpperPct
		f.LowerShadowPct = m.LowerPct
		f.BodyPct = m.BodyPct
		// Range > 0 is guaranteed by ComputeMetrics, so neither ratio can divide by zero.
		f.CloseLocationValue = clamp01((in.Close - in.Low) / m.Range)
		f.GiveBackPct = clamp01((in.High - in.Close) / m.Range)
	}

	if in.PrevClose > 0 && finite(in.PrevClose) && finite(in.Close) {
		f.PriceChangePct = (in.Close/in.PrevClose - 1) * 100
		f.ChangeOK = true
		if in.ATR > 0 && finite(in.ATR) {
			f.PriceChangeATR = (in.Close - in.PrevClose) / in.ATR
			f.ATROK = true
		}
		f.NearLimitUp = f.PriceChangePct >= LimitUpNearPct-boundaryEps
	}

	if in.VolMA20 > 0 && finite(in.VolMA20) && finite(in.Volume) {
		f.VolumeRatio = in.Volume / in.VolMA20
		f.VolumeOK = true
	}

	// TotalShares is the stock's own session volume in 股; DayTradeShares is the 當沖 half of
	// it. A ratio above 1 would mean the exchange reported more day-trade shares than total
	// volume — a data fault, not a 300% day-trading session — so it is rejected rather than
	// clamped, which would hide the fault behind a plausible number.
	if in.HasDayTrading && in.TotalShares > 0 && finite(in.DayTradeShares) && finite(in.TotalShares) {
		r := in.DayTradeShares / in.TotalShares
		if r >= 0 && r <= 1 {
			f.DayTradingRatio = r
			f.DayTradingOK = true
		}
	}

	return f
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// clamp01 guards the boundary only. Both callers divide a non-negative numerator by a
// strictly positive validated range, so a value outside [0,1] would already be a contract
// violation upstream; clamping keeps a float rounding artefact at 1.0000000000000002 from
// leaking into a bucket comparison.
func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}
