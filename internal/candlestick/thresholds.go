package candlestick

// Thresholds centralizes every tunable geometry/context number (SPEC §8). No magic numbers
// live outside this struct. BASELINE values — not calibrated; P1 may make them regime/
// volatility-aware. All are fractions of the candle range unless noted.
type Thresholds struct {
	DojiBodyMax       float64 // BodyPct <= this → Doji geometry (default 0.10)
	MarubozuBodyMin   float64 // BodyPct >= this → Marubozu candidate (default 0.90)
	MarubozuShadowMax float64 // each shadow pct <= this for Marubozu (default 0.08)
	LongShadowMin     float64 // a shadow pct >= this → "long" (default 0.50)
	ShortShadowMax    float64 // opposite shadow pct <= this for hammer/star family (default 0.20)
	ShadowBodyMax     float64 // BodyPct <= this for hammer/star family small body (default 0.35)
	NearPct           float64 // within this % of 20D high/low → Near flag (default 3.0)
	TrendReturnMin    float64 // |Return10D| %% to help confirm trend with MA (default 3.0)
	VolConfirm        float64 // VolumeRatio >= this → volume-confirmed (default 1.5)
	VolWeak           float64 // VolumeRatio < this → weak volume (default 0.7)
}

// DefaultThresholds returns the P0 baseline (SPEC §8).
func DefaultThresholds() Thresholds {
	return Thresholds{
		DojiBodyMax:       0.10,
		MarubozuBodyMin:   0.90,
		MarubozuShadowMax: 0.08,
		LongShadowMin:     0.50,
		ShortShadowMax:    0.20,
		ShadowBodyMax:     0.35,
		NearPct:           3.0,
		TrendReturnMin:    3.0,
		VolConfirm:        1.5,
		VolWeak:           0.7,
	}
}

// Defaulted fills any zero field with its default, so a zero-value or partially-specified
// config (e.g. from YAML) is always usable.
func (t Thresholds) Defaulted() Thresholds {
	d := DefaultThresholds()
	if t.DojiBodyMax == 0 {
		t.DojiBodyMax = d.DojiBodyMax
	}
	if t.MarubozuBodyMin == 0 {
		t.MarubozuBodyMin = d.MarubozuBodyMin
	}
	if t.MarubozuShadowMax == 0 {
		t.MarubozuShadowMax = d.MarubozuShadowMax
	}
	if t.LongShadowMin == 0 {
		t.LongShadowMin = d.LongShadowMin
	}
	if t.ShortShadowMax == 0 {
		t.ShortShadowMax = d.ShortShadowMax
	}
	if t.ShadowBodyMax == 0 {
		t.ShadowBodyMax = d.ShadowBodyMax
	}
	if t.NearPct == 0 {
		t.NearPct = d.NearPct
	}
	if t.TrendReturnMin == 0 {
		t.TrendReturnMin = d.TrendReturnMin
	}
	if t.VolConfirm == 0 {
		t.VolConfirm = d.VolConfirm
	}
	if t.VolWeak == 0 {
		t.VolWeak = d.VolWeak
	}
	return t
}
