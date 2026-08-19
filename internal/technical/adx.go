package technical

import "math"

// ADXResult is Wilder's Average Directional Index with its two directional components.
//
// The single most important thing about this struct is that Strength and Direction are
// INDEPENDENT. ADX measures how directional the move is, not which way it goes: ADX 40 with
// -DI above +DI is a very strong DOWNtrend. Keeping them in separate fields — rather than
// folding them into one "bullishness" number — is what stops that from being misread, and
// TestStrongDowntrendIsVeryStrongAndBearish asserts it.
type ADXResult struct {
	Status Status `json:"status"`

	Period int `json:"period"`

	ADX     float64 `json:"adx"`
	PlusDI  float64 `json:"plus_di"`
	MinusDI float64 `json:"minus_di"`

	Direction string `json:"direction"` // DirBullish | DirBearish | DirNeutral
	Strength  string `json:"strength"`  // StrengthWeak | Forming | Strong | VeryStrong
}

// ADX strength bands. Textbook values, and therefore HYPOTHESES: R14's outcome validation
// exists to find out whether they mean anything on this market, and nothing may be scored on
// them until it does.
const (
	adxWeakBelow     = 20.0 // < 20     → WEAK
	adxFormingBelow  = 25.0 // [20, 25) → FORMING
	adxVeryStrongMin = 40.0 // [25, 40) → STRONG;  >= 40 → VERY_STRONG
)

// ComputeADX runs Wilder's ADX over the bars.
//
// Wilder needs two windows stacked: `period` bars to smooth TR/+DM/-DM, then another
// `period` DX values to average into the first ADX. So 2*period bars is the true minimum,
// not period+1 — publishing anything before that would be an average of a partly warm-up
// series dressed up as a reading.
func ComputeADX(bars []Bar, cfg Config) ADXResult {
	cfg = cfg.Defaulted()
	out := ADXResult{Period: cfg.ADXPeriod, Direction: DirNeutral, Strength: StrengthUnknown}

	s, err := newSeries(bars)
	if err != nil {
		out.Status = statusFor(err)
		return out
	}
	p := cfg.ADXPeriod
	if s.n < 2*p {
		out.Status = StatusInsufficientData
		return out
	}

	// Directional movement and true range, both defined from index 1.
	tr := make([]float64, s.n)
	plusDM := make([]float64, s.n)
	minusDM := make([]float64, s.n)
	for i := 1; i < s.n; i++ {
		hl := s.highs[i] - s.lows[i]
		hc := math.Abs(s.highs[i] - s.closes[i-1])
		lc := math.Abs(s.lows[i] - s.closes[i-1])
		tr[i] = math.Max(hl, math.Max(hc, lc))

		up := s.highs[i] - s.highs[i-1]
		down := s.lows[i-1] - s.lows[i]
		// Only the LARGER of the two moves counts, and only if it is positive. An inside
		// bar (both negative) contributes no directional movement at all — which is the
		// point of the construction: it refuses to read direction out of noise.
		if up > down && up > 0 {
			plusDM[i] = up
		}
		if down > up && down > 0 {
			minusDM[i] = down
		}
	}

	// Wilder's running accumulation: seed with the sum of the first window, then decay one
	// period's worth per bar and add the new value.
	smTR, smPlus, smMinus := 0.0, 0.0, 0.0
	for i := 1; i <= p; i++ {
		smTR += tr[i]
		smPlus += plusDM[i]
		smMinus += minusDM[i]
	}

	fp := float64(p)
	dx := make([]float64, s.n)
	var lastPlusDI, lastMinusDI float64
	for i := p; i < s.n; i++ {
		if i > p {
			smTR = smTR - smTR/fp + tr[i]
			smPlus = smPlus - smPlus/fp + plusDM[i]
			smMinus = smMinus - smMinus/fp + minusDM[i]
		}
		// A market with literally no range has no true range to divide by. That is not
		// missing data — it is a flat tape, whose honest reading is "no directional
		// movement" — so the DIs stay at zero rather than becoming NaN.
		plusDI, okP := safeDiv(100*smPlus, smTR)
		minusDI, okM := safeDiv(100*smMinus, smTR)
		if !okP {
			plusDI = 0
		}
		if !okM {
			minusDI = 0
		}
		lastPlusDI, lastMinusDI = plusDI, minusDI

		if v, ok := safeDiv(100*math.Abs(plusDI-minusDI), plusDI+minusDI); ok {
			dx[i] = v
		}
	}

	// The first ADX is the plain mean of the first `period` DX values; after that, Wilder.
	adx := 0.0
	for i := p; i < 2*p; i++ {
		adx += dx[i]
	}
	adx /= fp
	for i := 2 * p; i < s.n; i++ {
		adx = (adx*(fp-1) + dx[i]) / fp
	}

	if !allFinite(adx, lastPlusDI, lastMinusDI) {
		out.Status = StatusInvalidBar
		return out
	}
	out.Status = StatusOK
	out.ADX, out.PlusDI, out.MinusDI = adx, lastPlusDI, lastMinusDI
	out.Direction = diDirection(lastPlusDI, lastMinusDI, cfg.DINeutralPct)
	out.Strength = adxStrength(adx)
	return out
}

// diDirection compares the two directional indicators, treating a near-tie as NEUTRAL.
//
// The tie band is proportional (a percentage of the two DIs' sum) rather than absolute,
// because DI magnitudes scale with how trending the instrument is — a fixed 1-point gap
// means something very different at DI 8 than at DI 45.
func diDirection(plusDI, minusDI, neutralPct float64) string {
	sum := plusDI + minusDI
	if sum <= 0 {
		return DirNeutral // no directional movement at all
	}
	gap, ok := safeDiv(100*math.Abs(plusDI-minusDI), sum)
	if !ok || gap < neutralPct {
		return DirNeutral
	}
	if plusDI > minusDI {
		return DirBullish
	}
	return DirBearish
}

// adxStrength buckets the index. It says how strong, never which way — see the ADXResult
// doc comment.
func adxStrength(adx float64) string {
	switch {
	case adx < adxWeakBelow:
		return StrengthWeak
	case adx < adxFormingBelow:
		return StrengthForming
	case adx < adxVeryStrongMin:
		return StrengthStrong
	default:
		return StrengthVeryStrong
	}
}
