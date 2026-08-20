package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseTechCfg(t *testing.T, src string) Config {
	t.Helper()
	var doc struct {
		Scanner Config `yaml:"scanner"`
	}
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc.Scanner
}

// A config written before R14 existed must behave exactly as it did before.
func TestTechnicalConfigDefaultsOff(t *testing.T) {
	cfg := parseTechCfg(t, "scanner:\n  min_price: 10\n")
	if cfg.EnableTechnicalIndicators || cfg.ShowTechnicalIndicators {
		t.Errorf("R14 must default off, got enable=%v show=%v",
			cfg.EnableTechnicalIndicators, cfg.ShowTechnicalIndicators)
	}
	// An absent technical: block still yields a runnable parameter set.
	d := cfg.Technical.Defaulted()
	if d.ADXPeriod != 14 || d.MACDSlow != 26 || d.KeltnerMultiplier != 2 {
		t.Errorf("an absent technical: block must default to the textbook baseline: %+v", d)
	}
	if err := cfg.Technical.Validate(); err != nil {
		t.Errorf("an absent block must validate: %v", err)
	}
}

func TestTechnicalConfigParsesBlock(t *testing.T) {
	cfg := parseTechCfg(t, `
scanner:
  enable_technical_indicators: true
  show_technical_indicators: true
  technical:
    adx_period: 20
    macd_fast: 8
    macd_slow: 21
    macd_signal: 5
    keltner_multiplier: 2.5
    pivot_near_pct: 0.75
`)
	if !cfg.EnableTechnicalIndicators || !cfg.ShowTechnicalIndicators {
		t.Error("switches not parsed")
	}
	if cfg.Technical.ADXPeriod != 20 || cfg.Technical.MACDSlow != 21 ||
		cfg.Technical.KeltnerMultiplier != 2.5 || cfg.Technical.PivotNearPct != 0.75 {
		t.Errorf("technical block not parsed: %+v", cfg.Technical)
	}
	// A partial block keeps what was given and defaults the rest.
	if cfg.Technical.Defaulted().RSIPeriod != 14 {
		t.Error("an omitted period should fall back to the default")
	}
	if err := cfg.Technical.Validate(); err != nil {
		t.Errorf("a coherent custom block must validate: %v", err)
	}
}

// A config that would produce nonsense must fail loudly rather than be silently corrected.
func TestTechnicalConfigRejectsNonsense(t *testing.T) {
	cfg := parseTechCfg(t, `
scanner:
  technical:
    macd_fast: 30
    macd_slow: 26
`)
	if err := cfg.Technical.Validate(); err == nil {
		t.Error("a fast EMA longer than the slow one should be rejected")
	}
}

// The shipped config must stay COHERENT.
//
// It deliberately no longer asserts that R14 is off. Whether the feature runs is the
// operator's decision, and a test that fights it would just be deleted the first time
// somebody enables it. What is still worth protecting is that the shipped file cannot put
// the scanner into a state that is broken or silently useless:
//
//   - the parameters must validate, so turning the feature on cannot fail at startup;
//   - the display flag must not be on while the compute flag is off. The two are
//     independent by design (compute without display is a valid, intended combination), but
//     the reverse renders a section for data nobody computed — an empty shell that looks
//     like "there was nothing to show". internal/candlestick guards the same asymmetry in
//     its Config.Validate; R14 has no such guard yet, so this is where it is caught.
//
// The "a config written before R14 defaults to off" guarantee lives in
// TestTechnicalConfigDefaultsOff, which tests an ABSENT block and is unaffected by whatever
// the operator has switched on here.
func TestShippedConfigIsCoherent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Skipf("shipped config unavailable: %v", err)
	}
	cfg := parseTechCfg(t, string(data))

	if err := cfg.Technical.Validate(); err != nil {
		t.Errorf("the shipped technical block does not validate: %v", err)
	}
	if cfg.ShowTechnicalIndicators && !cfg.EnableTechnicalIndicators {
		t.Error("show_technical_indicators is on while enable_technical_indicators is off — " +
			"the report would render a section for data nobody computed")
	}
}

// The R14 two-layer invariant, all four combinations.
//
// Compute-without-display is the combination R14 is actually meant to be run in — indicators
// persisted as evidence for later validation while the report stays unchanged — so it must
// stay valid. Display-without-compute is the one that has to fail: the report would render a
// section for data nobody computed, and an empty shell reads as "there was nothing to show"
// rather than "nobody looked". Same asymmetry internal/candlestick has enforced since R10-2.
func TestTechnicalShowRequiresEnable(t *testing.T) {
	for _, tc := range []struct {
		enable, show bool
		wantErr      bool
	}{
		{enable: false, show: false, wantErr: false}, // feature off entirely
		{enable: true, show: false, wantErr: false},  // compute only — the intended R14 mode
		{enable: true, show: true, wantErr: false},   // both on
		{enable: false, show: true, wantErr: true},   // display with nothing behind it
	} {
		cfg := Config{
			EnableTechnicalIndicators: tc.enable,
			ShowTechnicalIndicators:   tc.show,
		}
		err := cfg.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("enable=%v show=%v: Validate() = nil, want an error",
				tc.enable, tc.show)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("enable=%v show=%v: Validate() = %v, want nil", tc.enable, tc.show, err)
		}
	}
}

// The error has to name BOTH flags, because the fix is not obvious from either one alone:
// a reader seeing only "show_technical_indicators is invalid" would not know what to turn on.
func TestTechnicalValidateErrorNamesBothFlags(t *testing.T) {
	err := Config{ShowTechnicalIndicators: true}.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"show_technical_indicators", "enable_technical_indicators"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// The R14 rule must not have displaced the R10-2 one — they are independent checks, and a
// config can be wrong in either way.
func TestValidateStillEnforcesCandlestick(t *testing.T) {
	if err := (Config{ShowCandlestick: true}).Validate(); err == nil {
		t.Error("show_candlestick without enable_candlestick should still be rejected")
	}
	// And a config that is wrong in both ways still fails.
	both := Config{ShowCandlestick: true, ShowTechnicalIndicators: true}
	if err := both.Validate(); err == nil {
		t.Error("a config wrong in both ways should be rejected")
	}
	// A fully coherent config passes.
	ok := Config{
		EnableCandlestick: true, ShowCandlestick: true,
		EnableTechnicalIndicators: true, ShowTechnicalIndicators: true,
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("a coherent config was rejected: %v", err)
	}
}
