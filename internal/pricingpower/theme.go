package pricingpower

import "strings"

// sectorTheme maps a configs/sectors.yaml sector NAME directly to a Theme. Names match
// the current sectors.yaml (see the refreshed 功率 / 二極體 / 封測 / 被動元件 / 記憶體 /
// CCL 銅箔基板 / ABF 載板 groups).
var sectorTheme = map[string]Theme{
	"記憶體":      ThemeMemory,
	"被動元件":     ThemePassive,
	"電感":       ThemePassive, // inductors are passive components
	"功率":       ThemePowerSemi,
	"二極體":      ThemePowerSemi, // diodes are power semis
	"封測":       ThemePackaging,
	"CCL 銅箔基板": ThemePCBMaterial,
	"ABF 載板":   ThemePCBMaterial,
	"PCB":      ThemePCBMaterial,
}

// keywordTheme maps a (case-insensitive) substring found in a sector name or an explicit
// hint to a Theme. Ordered most-specific first so MOSFET wins over the broader POWER.
var keywordTheme = []struct {
	kw    string
	theme Theme
}{
	{"mosfet", ThemeMOSFET},
	{"類比", ThemeAnalogIC}, {"analog", ThemeAnalogIC}, {"pmic", ThemeAnalogIC}, {"電源管理", ThemeAnalogIC},
	{"mlcc", ThemePassive}, {"被動", ThemePassive}, {"電阻", ThemePassive}, {"電容", ThemePassive},
	{"dram", ThemeMemory}, {"nand", ThemeMemory}, {"nor", ThemeMemory}, {"hbm", ThemeMemory}, {"記憶體", ThemeMemory}, {"memory", ThemeMemory},
	{"igbt", ThemePowerSemi}, {"sic", ThemePowerSemi}, {"gan", ThemePowerSemi}, {"diode", ThemePowerSemi}, {"二極體", ThemePowerSemi}, {"功率", ThemePowerSemi},
	{"封測", ThemePackaging}, {"packaging", ThemePackaging}, {"osat", ThemePackaging},
	{"ccl", ThemePCBMaterial}, {"abf", ThemePCBMaterial}, {"載板", ThemePCBMaterial}, {"銅箔", ThemePCBMaterial},
}

// ThemeFor classifies a stock's pricing-cycle theme from its sector name plus optional
// hints (e.g. news themes / product keywords). Resolution order, most specific first:
//  1. an explicit hint matching a keyword (MOSFET, 類比IC…)
//  2. an exact sector-name mapping (記憶體 → MEMORY)
//  3. a keyword found inside the sector name (功率元件 → POWER via "功率")
//  4. OTHER (never guesses)
func ThemeFor(sector string, hints ...string) Theme {
	for _, h := range hints {
		if t, ok := matchKeyword(h); ok {
			return t
		}
	}
	if t, ok := sectorTheme[strings.TrimSpace(sector)]; ok {
		return t
	}
	if t, ok := matchKeyword(sector); ok {
		return t
	}
	return ThemeOther
}

func matchKeyword(s string) (Theme, bool) {
	ls := strings.ToLower(s)
	for _, e := range keywordTheme {
		if strings.Contains(ls, e.kw) {
			return e.theme, true
		}
	}
	return ThemeOther, false
}
