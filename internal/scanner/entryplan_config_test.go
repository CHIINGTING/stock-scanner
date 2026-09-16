package scanner

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseEntryPlanCfg(t *testing.T, src string) Config {
	t.Helper()
	var doc struct {
		Scanner Config `yaml:"scanner"`
	}
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc.Scanner
}

// A config written before EP-6 existed must behave exactly as it did before: both flags off,
// which means the projection never runs and every EntryPlan field stays nil.
func TestEntryPlanConfigDefaultsOff(t *testing.T) {
	cfg := parseEntryPlanCfg(t, "scanner:\n  min_price: 10\n")
	if cfg.EnableEntryPlan || cfg.ShowEntryPlan {
		t.Errorf("EP-6 must default off, got enable=%v show=%v",
			cfg.EnableEntryPlan, cfg.ShowEntryPlan)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("a config that predates EP-6 must validate: %v", err)
	}
}

// The YAML keys are part of the contract — an operator's file names them, not the Go fields.
func TestEntryPlanConfigParsesBothFlags(t *testing.T) {
	cfg := parseEntryPlanCfg(t, `
scanner:
  enable_entry_plan: true
  show_entry_plan: true
`)
	if !cfg.EnableEntryPlan {
		t.Error("enable_entry_plan not parsed")
	}
	if !cfg.ShowEntryPlan {
		t.Error("show_entry_plan not parsed")
	}
}

// The EP-6 two-layer invariant, all four combinations.
//
// Compute-without-display is a legitimate combination — the plans are computed and carried
// (and persisted by EP-7) while the report's ⑲ section (EP-8) stays off — so it must stay valid. Display-without-compute is the one that has to fail: it asks for a
// section rendered from data nobody computed, and an empty shell reads as "there was no plan
// to make" rather than "nobody looked". Same asymmetry R10-2 and R14 enforce.
func TestEntryPlanShowRequiresEnable(t *testing.T) {
	for _, tc := range []struct {
		enable, show bool
		wantErr      bool
	}{
		{enable: false, show: false, wantErr: false}, // feature off entirely
		{enable: true, show: false, wantErr: false},  // compute only — the EP-6A mode
		{enable: true, show: true, wantErr: false},   // both on
		{enable: false, show: true, wantErr: true},   // display with nothing behind it
	} {
		cfg := Config{EnableEntryPlan: tc.enable, ShowEntryPlan: tc.show}
		err := cfg.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("enable=%v show=%v: Validate() = nil, want an error", tc.enable, tc.show)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("enable=%v show=%v: Validate() = %v, want nil", tc.enable, tc.show, err)
		}
	}
}

// The error has to name BOTH flags: a reader told only that show_entry_plan is invalid does
// not know what to switch on to fix it.
func TestEntryPlanValidateErrorNamesBothFlags(t *testing.T) {
	err := Config{ShowEntryPlan: true}.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"show_entry_plan", "enable_entry_plan"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// The EP-6 rule must not have displaced the R10-2 or R14 ones. Validate is the CONFIG-WIDE
// validator and each feature's check is independent; a config can be wrong in any combination
// of the three, and a fully coherent one must still pass.
func TestEntryPlanValidateDoesNotDisplaceTheOtherRules(t *testing.T) {
	if err := (Config{ShowCandlestick: true}).Validate(); err == nil {
		t.Error("show_candlestick without enable_candlestick should still be rejected")
	}
	if err := (Config{ShowTechnicalIndicators: true}).Validate(); err == nil {
		t.Error("show_technical_indicators without its enable should still be rejected")
	}
	wrongInEveryWay := Config{
		ShowCandlestick: true, ShowTechnicalIndicators: true, ShowEntryPlan: true,
	}
	if err := wrongInEveryWay.Validate(); err == nil {
		t.Error("a config wrong in all three ways should be rejected")
	}
	coherent := Config{
		EnableCandlestick: true, ShowCandlestick: true,
		EnableTechnicalIndicators: true, ShowTechnicalIndicators: true,
		EnableEntryPlan: true, ShowEntryPlan: true,
	}
	if err := coherent.Validate(); err != nil {
		t.Errorf("a coherent config was rejected: %v", err)
	}
}
