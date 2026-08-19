package scanner

import (
	"os"
	"path/filepath"
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

// The SHIPPED config must keep R14 off. A flag flipped by accident in a commit is exactly
// what nobody notices until a scan is already behaving differently.
func TestShippedConfigKeepsTechnicalOff(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Skipf("shipped config unavailable: %v", err)
	}
	cfg := parseTechCfg(t, string(data))
	if cfg.EnableTechnicalIndicators || cfg.ShowTechnicalIndicators {
		t.Error("configs/config.yaml has R14 enabled — the first release must ship off")
	}
	// And the shipped parameters must be coherent, so turning it on cannot fail at startup.
	if err := cfg.Technical.Validate(); err != nil {
		t.Errorf("the shipped technical block does not validate: %v", err)
	}
}
