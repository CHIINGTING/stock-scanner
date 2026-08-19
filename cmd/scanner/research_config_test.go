package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseConfig(t *testing.T, src string) config {
	t.Helper()
	var cfg config
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg
}

// A config written before R13 existed must behave exactly as it did before.
func TestResearchDefaultsOff(t *testing.T) {
	cfg := parseConfig(t, "scanner:\n  min_price: 10\n")
	if cfg.Research.Enabled {
		t.Error("the research store must default off")
	}
	// An absent block still defaults to something runnable, so turning the flag on is the
	// only thing a user has to do.
	d := cfg.Research.Defaulted()
	if d.Store.Path == "" || d.Store.BusyTimeoutMs <= 0 || d.MarketSnapshotDir == "" {
		t.Errorf("an absent research: block must still default to something runnable: %+v", d)
	}
	if d.Enabled {
		t.Error("Defaulted must not turn the feature on")
	}
}

func TestResearchConfigParsesBlock(t *testing.T) {
	cfg := parseConfig(t, `
research:
  enabled: true
  market_snapshot_dir: "data/other"
  store:
    path: "tmp/x.db"
    busy_timeout_ms: 250
`)
	if !cfg.Research.Enabled {
		t.Error("enabled not parsed")
	}
	if cfg.Research.Store.Path != "tmp/x.db" || cfg.Research.Store.BusyTimeoutMs != 250 {
		t.Errorf("store block not parsed: %+v", cfg.Research.Store)
	}
	if cfg.Research.MarketSnapshotDir != "data/other" {
		t.Errorf("market dir = %q", cfg.Research.MarketSnapshotDir)
	}
}

// The SHIPPED config must keep R13 off. This is a shadow layer whose first release is not
// meant to be on by default, and a flag flipped by accident in a commit is exactly the kind
// of thing nobody notices until the statistics are already polluted.
func TestShippedConfigKeepsResearchOff(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("shipped config unavailable: %v", err)
	}
	cfg := parseConfig(t, string(data))
	if cfg.Research.Enabled {
		t.Error("configs/config.yaml has research.enabled=true — R13 must ship off")
	}
}
