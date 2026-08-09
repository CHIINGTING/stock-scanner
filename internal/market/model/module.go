// Package model holds the pure data types of the Market Dashboard (Market Regime
// Engine). It has ZERO dependencies on other internal packages so every other market
// sub-package (provider, analyzer, service) and the cmd can import it without cycles.
//
// The Market Dashboard is the highest analysis layer of the scanner: it answers
// "is the market in attack or defense mode, and which sectors have the best odds?"
// BEFORE any individual stock is analysed. Phase 1 is display/context only — it never
// touches scanner scoring and never emits trade instructions.
package model

// Module identifiers — the seven contributors to the Market Score. Kept as constants so
// weights (config), analyzer output, and tests all agree on one spelling.
const (
	// MVP modules — every one is sourced from official TWSE or TAIFEX data.
	ModulePrice          = "Price"
	ModuleBreadth        = "Breadth"
	ModuleForeignFutures = "ForeignFutures"
	ModuleForeignCash    = "ForeignCash"
	ModuleMargin         = "Margin"

	// Out of MVP scope: no official TWSE/TAIFEX source exists for these, so they carry no
	// default weight (see defaultWeights). The constants and their analyzers are KEPT — the
	// code is sound and phase 2 can re-enable them once a data source is agreed.
	ModuleOptions     = "Options"
	ModuleCurrency    = "Currency"
	ModuleVIX         = "VIX"
	ModuleSectorCycle = "SectorCycle"
)

// FuturesData is the foreign institutional net position in TAIFEX index futures,
// measured in contracts (口). DailyChange is today minus the previous session.
type FuturesData struct {
	Date        string  `json:"date"`
	NetPosition float64 `json:"net_position"`
	DailyChange float64 `json:"daily_change"`
}

// ForeignCashData is the foreign institutional cash-market buy/sell, in 億 TWD.
type ForeignCashData struct {
	Date string  `json:"date"`
	Buy  float64 `json:"buy"`
	Sell float64 `json:"sell"`
}

// Net is buy minus sell (positive = net buying). Derived, never stored.
func (f ForeignCashData) Net() float64 { return f.Buy - f.Sell }

// MarginData captures whether retail is chasing price. PriceChangePct is the market
// proxy's daily % move; the balance changes are 融資 (margin long) / 融券 (short), in 億.
type MarginData struct {
	Date             string  `json:"date"`
	PriceChangePct   float64 `json:"price_change_pct"`
	MarginBalanceChg float64 `json:"margin_balance_chg"`
	ShortBalanceChg  float64 `json:"short_balance_chg"`
}

// OptionsData is the index options positioning. Phase-1 uses the OI put/call ratio;
// higher ratio = more downside hedging / fear.
type OptionsData struct {
	Date         string  `json:"date"`
	PutCallRatio float64 `json:"put_call_ratio"`
}

// CurrencyData is the USD backdrop: DXY (dollar index) and USD/TWD, plus daily changes.
// A stronger USD / weaker TWD leans risk-off for Taiwan equities.
type CurrencyData struct {
	Date      string  `json:"date"`
	DXY       float64 `json:"dxy"`
	DXYChg    float64 `json:"dxy_chg"`
	USDTWD    float64 `json:"usd_twd"`
	USDTWDChg float64 `json:"usd_twd_chg"`
}

// VIXData is the equity volatility gauge (US VIX proxy in phase 1).
type VIXData struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// SectorScore is one sector's standing, reusing the scanner's existing sector context.
// CycleScore is 0..100 (景氣循環 strength); NewsScore is -100..100 (news-shadow sentiment).
type SectorScore struct {
	Name       string  `json:"name"`
	CycleScore float64 `json:"cycle_score"`
	NewsScore  float64 `json:"news_score"`
}
