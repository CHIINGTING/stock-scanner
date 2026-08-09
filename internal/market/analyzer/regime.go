package analyzer

import (
	"fmt"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// minModulesForRegime is the floor below which we cannot responsibly call a regime.
const minModulesForRegime = 2

// Classify turns the Market Score into a RegimeInfo using the baseline decision table (first
// match wins). "Deterioration" — foreign cash selling or elevated fear — downgrades an
// otherwise-bullish score from Bull Market to Distribution (a top, not an uptrend). All
// numeric cutoffs come from th (config); this function is pure and side-effect free.
//
// RED LINE: the returned text is regime CONTEXT only — never a trade instruction.
func Classify(ms model.MarketScore, th model.RegimeThresholds, confidenceMinModules int) model.RegimeInfo {
	if ms.AvailableCount < minModulesForRegime {
		return withGuidance(model.RegimeInfo{
			Regime:     model.RegimeUnknown,
			Confidence: model.ConfidenceLow,
			Reasons:    []string{fmt.Sprintf("可用模組不足 (%d)，無法判斷 regime", ms.AvailableCount)},
		})
	}

	deteriorating, detReasons := deterioration(ms)

	var regime model.Regime
	switch {
	case ms.Total < th.BearMax:
		regime = model.RegimeBear
	case ms.Total >= th.BullMin && deteriorating:
		regime = model.RegimeDistribution
	case ms.Total >= th.BullMin:
		regime = model.RegimeBull
	case ms.Total >= th.BullPullbackMin:
		regime = model.RegimeBullPullback
	default:
		regime = model.RegimeSideways
	}

	info := model.RegimeInfo{Regime: regime}
	info.Reasons = append(info.Reasons, fmt.Sprintf("Market Score %.0f/100（%d 個模組）", ms.Total, ms.AvailableCount))
	info.Reasons = append(info.Reasons, moduleStateReasons(ms)...)
	if regime == model.RegimeDistribution {
		info.Reasons = append(info.Reasons, detReasons...)
	}

	// Confidence: MEDIUM when enough modules agree; LOW otherwise, or near a boundary.
	info.Confidence = model.ConfidenceMedium
	if ms.AvailableCount < confidenceMinModules {
		info.Confidence = model.ConfidenceLow
		info.Caveats = append(info.Caveats, fmt.Sprintf("僅 %d 個模組可用，信心降級", ms.AvailableCount))
	}
	if near := nearBoundary(ms.Total, th); near != "" {
		info.Confidence = model.ConfidenceLow
		info.Caveats = append(info.Caveats, near)
	}
	// Extreme foreign-futures short is a hedging caveat, not a bearish confirmation.
	if fut := findModule(ms, model.ModuleForeignFutures); fut != nil &&
		(strings.Contains(fut.Detail, ExtremeExtremeShort) || strings.Contains(fut.Detail, ExtremeHeavyShort)) {
		info.Caveats = append(info.Caveats, "外資期貨淨空單偏極端，可能為避險，不宜單獨解讀為看空")
	}

	return withGuidance(info)
}

// deterioration reports whether risk-off cracks (foreign cash selling / fear) are showing,
// with the human reasons. Used to separate Distribution from a clean Bull Market.
func deterioration(ms model.MarketScore) (bool, []string) {
	var reasons []string
	if cash := findModule(ms, model.ModuleForeignCash); cash != nil &&
		(cash.State == CashSell || cash.State == CashStrongSell) {
		reasons = append(reasons, "外資現貨轉為賣超")
	}
	if vix := findModule(ms, model.ModuleVIX); vix != nil &&
		(vix.State == VIXFear || vix.State == VIXExtremeFear) {
		reasons = append(reasons, "波動度升高（VIX 進入 Fear）")
	}
	return len(reasons) > 0, reasons
}

// moduleStateReasons summarizes each available module as "Name State" for the Reasons list.
func moduleStateReasons(ms model.MarketScore) []string {
	var out []string
	for _, m := range ms.Modules {
		if !m.Available {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", displayName(m.Name), m.State))
	}
	return out
}

// nearBoundary returns a caveat string if the score sits within 2 points of a regime edge.
func nearBoundary(total float64, th model.RegimeThresholds) string {
	const band = 2.0
	for _, b := range []struct {
		edge float64
		name string
	}{{th.BearMax, "Bear"}, {th.BullPullbackMin, "Bull Pullback"}, {th.BullMin, "Bull"}} {
		if total >= b.edge-band && total <= b.edge+band {
			return fmt.Sprintf("分數接近 %s 邊界（%.0f），regime 可能擺盪", b.name, b.edge)
		}
	}
	return ""
}

func findModule(ms model.MarketScore, name string) *model.ModuleScore {
	for i := range ms.Modules {
		if ms.Modules[i].Name == name {
			return &ms.Modules[i]
		}
	}
	return nil
}

func displayName(module string) string {
	switch module {
	case model.ModuleForeignFutures:
		return "外資期貨"
	case model.ModuleForeignCash:
		return "外資現貨"
	case model.ModuleMargin:
		return "融資"
	case model.ModuleOptions:
		return "選擇權"
	case model.ModuleCurrency:
		return "匯率"
	case model.ModuleVIX:
		return "VIX"
	case model.ModuleSectorCycle:
		return "產業循環"
	default:
		return module
	}
}

// withGuidance attaches the fixed Description / Strategy / Risk text for the regime.
func withGuidance(info model.RegimeInfo) model.RegimeInfo {
	g := regimeGuidance[info.Regime]
	info.Description, info.Strategy, info.Risk = g.desc, g.strategy, g.risk
	return info
}

type guidance struct{ desc, strategy, risk string }

// regimeGuidance is the fixed per-regime context (from the feature spec). Context only —
// no trade instructions.
var regimeGuidance = map[model.Regime]guidance{
	model.RegimeBull: {
		desc:     "多頭環境，結構健康。",
		strategy: "以強勢族群為主軸觀察，順勢操作。",
		risk:     "留意過度樂觀、追高無量。",
	},
	model.RegimeBullPullback: {
		desc:     "長期仍偏多，短期修正。",
		strategy: "優先布局強勢族群，等回檔後的再攻擊。",
		risk:     "避免追高；修正未完成前分批。",
	},
	model.RegimeSideways: {
		desc:     "區間震盪，方向未明。",
		strategy: "箱型高出低進、觀察突破/跌破確認，持有週期偏短。",
		risk:     "假突破與假跌破機率升高。",
	},
	model.RegimeDistribution: {
		desc:     "指數偏高但內部轉弱，疑似出貨。",
		strategy: "降低曝險，觀察是否轉為下跌；只留最強、抗跌標的。",
		risk:     "強勢股補跌、突破失敗風險升高。",
	},
	model.RegimeBear: {
		desc:     "空頭環境，保護資金優先。",
		strategy: "以觀察相對抗跌與是否止跌為主，不做突破追價解讀。",
		risk:     "回檔可能變破底；停損保護價值升高。",
	},
	model.RegimeUnknown: {
		desc:     "資料不足，無法判斷市場環境。",
		strategy: "等待更多資料再解讀個股訊號。",
		risk:     "此時任何 regime 解讀都不可靠。",
	},
}
