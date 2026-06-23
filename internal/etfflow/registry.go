package etfflow

import "strings"

// ──────────────────────────────────────────────────────────────────────────────
// ETF source registry (R8-6d).
//
// Single source of truth for "which provider serves this ETF's full holdings, and
// under what internal fund code". Previously the 00981A→49YTW mapping was hardcoded in
// the CLI; centralising it here means adding a new 統一投信 active ETF is a one-line
// registry entry — no CLI or parser change. Parsers stay mapping-free: a parser knows
// HOW to read a provider, the registry knows WHICH fund code to read.
//
// Provider values mirror CLI --source values (e.g. "ezmoney"). Every entry MUST have
// been confirmed against the issuer's own public page (ezMoney DataFundList:
// sStockNo ↔ sFundCode) — never guess a fund code. See
// docs/SPEC_R8_6D_EZMONEY_FULL_HOLDINGS_PIPELINE.md §"新增 mapping".
// ──────────────────────────────────────────────────────────────────────────────

// Provider ids (mirror CLI --source).
const (
	ProviderEzMoney = "ezmoney"
	ProviderMoneyDJ = "moneydj"
)

// ETFMapping records how one ETF's holdings are sourced from one provider.
type ETFMapping struct {
	ETFCode  string // listed code, e.g. "00981A"
	Provider string // ProviderEzMoney | ...
	FundCode string // issuer-internal fund code the provider addresses by, e.g. "49YTW"
	Market   string // "TW"
	Coverage string // CoverageFullHoldings | CoverageTopNOnly | ...
	Name     string // ETF display name (provenance / CLI default)
	Type     string // TypeActive | TypePassive
}

// etfRegistry is the full mapping table. All ezMoney fund codes below were confirmed
// from 統一投信 ezMoney's own DataFundList (sStockNo ↔ sFundCode).
var etfRegistry = []ETFMapping{
	{ETFCode: "00981A", Provider: ProviderEzMoney, FundCode: "49YTW", Market: "TW", Coverage: CoverageFullHoldings, Name: "主動統一台股增長", Type: TypeActive},
	{ETFCode: "00403A", Provider: ProviderEzMoney, FundCode: "63YTW", Market: "TW", Coverage: CoverageFullHoldings, Name: "主動統一升級50", Type: TypeActive},
	{ETFCode: "00988A", Provider: ProviderEzMoney, FundCode: "61YTW", Market: "TW", Coverage: CoverageFullHoldings, Name: "主動統一全球創新", Type: TypeActive},
}

// LookupETF finds the mapping for etfCode. If provider is non-empty, only a mapping for
// that provider matches; if provider is "", the first mapping for the code is returned
// (handy for provider-agnostic name/type defaults). Lookups are case-insensitive.
func LookupETF(etfCode, provider string) (ETFMapping, bool) {
	etfCode = strings.TrimSpace(etfCode)
	provider = strings.TrimSpace(provider)
	for _, m := range etfRegistry {
		if !strings.EqualFold(m.ETFCode, etfCode) {
			continue
		}
		if provider != "" && !strings.EqualFold(m.Provider, provider) {
			continue
		}
		return m, true
	}
	return ETFMapping{}, false
}

// RegisteredETFs returns mappings for a provider (or all if provider is ""), so the CLI
// / docs can enumerate what is supported without exposing the slice.
func RegisteredETFs(provider string) []ETFMapping {
	provider = strings.TrimSpace(provider)
	out := make([]ETFMapping, 0, len(etfRegistry))
	for _, m := range etfRegistry {
		if provider != "" && !strings.EqualFold(m.Provider, provider) {
			continue
		}
		out = append(out, m)
	}
	return out
}
