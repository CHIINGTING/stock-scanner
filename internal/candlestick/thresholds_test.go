package candlestick

import "testing"

func TestDefaultThresholds(t *testing.T) {
	d := DefaultThresholds()
	if d.DojiBodyMax != 0.10 || d.MarubozuBodyMin != 0.90 || d.LongShadowMin != 0.50 {
		t.Errorf("unexpected defaults: %+v", d)
	}
	// sanity ordering the geometry layer relies on
	if !(d.DojiBodyMax < d.MarubozuBodyMin) {
		t.Errorf("DojiBodyMax must be < MarubozuBodyMin")
	}
	if !(d.ShortShadowMax < d.LongShadowMin) {
		t.Errorf("ShortShadowMax must be < LongShadowMin")
	}
	for name, v := range map[string]float64{
		"DojiBodyMax": d.DojiBodyMax, "MarubozuBodyMin": d.MarubozuBodyMin,
		"MarubozuShadowMax": d.MarubozuShadowMax, "LongShadowMin": d.LongShadowMin,
		"ShortShadowMax": d.ShortShadowMax, "ShadowBodyMax": d.ShadowBodyMax,
		"NearPct": d.NearPct, "TrendReturnMin": d.TrendReturnMin, "VolConfirm": d.VolConfirm,
	} {
		if v <= 0 {
			t.Errorf("%s must be > 0, got %v", name, v)
		}
	}
}

func TestThresholdsDefaulted(t *testing.T) {
	// zero-value config → fully defaulted
	got := Thresholds{}.Defaulted()
	if got != DefaultThresholds() {
		t.Errorf("zero config should default fully, got %+v", got)
	}
	// partial override preserved, rest defaulted
	p := Thresholds{DojiBodyMax: 0.05}.Defaulted()
	if p.DojiBodyMax != 0.05 {
		t.Errorf("override lost: %v", p.DojiBodyMax)
	}
	if p.MarubozuBodyMin != 0.90 {
		t.Errorf("unspecified field should default: %v", p.MarubozuBodyMin)
	}
}
