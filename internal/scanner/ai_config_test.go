package scanner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type aiCfgDoc struct {
	Scanner Config `yaml:"scanner"`
}

func parseScannerYAML(t *testing.T, src string) Config {
	t.Helper()
	var doc aiCfgDoc
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc.Scanner
}

// Both switches default off, so a config written before this feature existed behaves exactly
// as it did before.
func TestAIConfigDefaultsOff(t *testing.T) {
	cfg := parseScannerYAML(t, "scanner:\n  min_score: 60\n")
	if cfg.EnableAI || cfg.ShowAI {
		t.Errorf("AI must default off, got enable=%v show=%v", cfg.EnableAI, cfg.ShowAI)
	}
	// An absent ai: block still yields a usable analyzer config through Defaulted().
	d := cfg.AI.Defaulted()
	if d.Model == "" || d.TimeoutSec <= 0 || d.MaxStocks <= 0 {
		t.Errorf("an absent ai: block must still default to something runnable: %+v", d)
	}
}

func TestAIConfigParsesBlock(t *testing.T) {
	cfg := parseScannerYAML(t, `
scanner:
  enable_ai: true
  show_ai: true
  ai:
    model: "gpt-4.1-mini"
    timeout_sec: 45
    max_stocks: 5
    temperature: 0.1
`)
	if !cfg.EnableAI || !cfg.ShowAI {
		t.Errorf("switches not parsed: %+v", cfg.AI)
	}
	if cfg.AI.Model != "gpt-4.1-mini" || cfg.AI.TimeoutSec != 45 || cfg.AI.MaxStocks != 5 {
		t.Errorf("ai block not parsed: %+v", cfg.AI)
	}
	if cfg.AI.Temperature != 0.1 {
		t.Errorf("temperature = %v", cfg.AI.Temperature)
	}
	// A partial block keeps what was given and defaults the rest.
	partial := parseScannerYAML(t, "scanner:\n  ai:\n    max_stocks: 3\n").AI.Defaulted()
	if partial.MaxStocks != 3 || partial.Model == "" || partial.TimeoutSec <= 0 {
		t.Errorf("partial block defaulted wrong: %+v", partial)
	}
}

// The shipped config must keep AI off, so cloning the repo and running the scanner never
// makes a paid API call by surprise.
func TestShippedConfigKeepsAIOff(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Skipf("configs/config.yaml unavailable: %v", err)
	}
	cfg := parseScannerYAML(t, string(b))
	if cfg.EnableAI || cfg.ShowAI {
		t.Errorf("configs/config.yaml ships with AI on (enable=%v show=%v)", cfg.EnableAI, cfg.ShowAI)
	}
}

// No config file may carry a credential. The key belongs in OPENAI_API_KEY and nowhere else;
// this test is what stops a convenient "just paste it here" from ever being committed.
func TestNoAPIKeyInConfigFiles(t *testing.T) {
	// OpenAI key shapes (sk-…, sk-proj-…) plus any key-ish assignment near an ai/openai word.
	secret := regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`)
	keyField := regexp.MustCompile(`(?i)^\s*(openai_)?(api_)?(key|token|secret)\s*:\s*\S`)

	files, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no configs to scan")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if secret.MatchString(line) {
				t.Errorf("%s:%d looks like a committed API key", f, i+1)
			}
			if keyField.MatchString(line) {
				t.Errorf("%s:%d declares a credential field; secrets belong in the environment", f, i+1)
			}
		}
	}
}
