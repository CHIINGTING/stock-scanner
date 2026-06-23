package etfflow

import (
	"strings"
	"testing"
)

// Every registered ezMoney ETF must be fully specified: fund code, name, active/passive
// type, and full_holdings coverage. Guards against adding an ETF but forgetting a field.
func TestEtfRegistryEntriesComplete(t *testing.T) {
	for _, m := range RegisteredETFs(ProviderEzMoney) {
		if strings.TrimSpace(m.FundCode) == "" {
			t.Errorf("%s: missing FundCode", m.ETFCode)
		}
		if strings.TrimSpace(m.Name) == "" {
			t.Errorf("%s: missing Name", m.ETFCode)
		}
		if m.Type != TypeActive && m.Type != TypePassive {
			t.Errorf("%s: invalid Type %q", m.ETFCode, m.Type)
		}
		if m.Coverage != CoverageFullHoldings {
			t.Errorf("%s: ezMoney mapping must be full_holdings, got %q", m.ETFCode, m.Coverage)
		}
		if m.Market == "" {
			t.Errorf("%s: missing Market", m.ETFCode)
		}
	}
}

func TestLookupETF(t *testing.T) {
	// code + provider → exact fund code (case-insensitive).
	if m, ok := LookupETF("00981A", ProviderEzMoney); !ok || m.FundCode != "49YTW" {
		t.Fatalf("00981A/ezmoney: ok=%v fundCode=%q want 49YTW", ok, m.FundCode)
	}
	if m, ok := LookupETF("00403a", "EZMONEY"); !ok || m.FundCode != "63YTW" {
		t.Fatalf("case-insensitive lookup failed: ok=%v fundCode=%q", ok, m.FundCode)
	}
	// provider-agnostic lookup (provider "") returns name/type defaults.
	if m, ok := LookupETF("00988A", ""); !ok || m.Type != TypeActive || m.Name == "" {
		t.Fatalf("provider-agnostic lookup failed: %+v ok=%v", m, ok)
	}
	// wrong provider must NOT match.
	if _, ok := LookupETF("00981A", ProviderMoneyDJ); ok {
		t.Fatal("00981A has no moneydj mapping; lookup should miss")
	}
	// unknown code must miss.
	if _, ok := LookupETF("99999A", ProviderEzMoney); ok {
		t.Fatal("99999A is unregistered; lookup should miss")
	}
}

func TestRegisteredETFsFilter(t *testing.T) {
	all := RegisteredETFs("")
	ez := RegisteredETFs(ProviderEzMoney)
	if len(all) == 0 || len(ez) == 0 {
		t.Fatalf("registry unexpectedly empty: all=%d ez=%d", len(all), len(ez))
	}
	if len(ez) > len(all) {
		t.Fatalf("provider-filtered set (%d) cannot exceed all (%d)", len(ez), len(all))
	}
}
