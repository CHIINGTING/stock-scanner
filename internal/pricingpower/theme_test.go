package pricingpower

import "testing"

func TestThemeFor(t *testing.T) {
	cases := []struct {
		name   string
		sector string
		hints  []string
		want   Theme
	}{
		{"memory", "記憶體", nil, ThemeMemory},
		{"passive", "被動元件", nil, ThemePassive},
		{"inductor→passive", "電感", nil, ThemePassive},
		{"power (功率)", "功率", nil, ThemePowerSemi},
		{"power via keyword (功率元件)", "功率元件", nil, ThemePowerSemi},
		{"diode→power", "二極體", nil, ThemePowerSemi},
		{"packaging", "封測", nil, ThemePackaging},
		{"pcb material", "CCL 銅箔基板", nil, ThemePCBMaterial},
		{"abf→pcb material", "ABF 載板", nil, ThemePCBMaterial},
		{"mosfet via hint wins over 功率", "功率", []string{"MOSFET"}, ThemeMOSFET},
		{"analog ic via hint", "", []string{"類比IC"}, ThemeAnalogIC},
		{"analog via english hint", "IC設計", []string{"analog PMIC"}, ThemeAnalogIC},
		{"other fallback", "金融股", nil, ThemeOther},
		{"other fallback empty", "", nil, ThemeOther},
	}
	for _, c := range cases {
		if got := ThemeFor(c.sector, c.hints...); got != c.want {
			t.Errorf("%s: ThemeFor(%q,%v)=%q want %q", c.name, c.sector, c.hints, got, c.want)
		}
	}
}
