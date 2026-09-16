package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/study"
)

// TestTheMetricsCSVCarriesItsProvenance pins EP-9 §26/§27 on the most DETACHABLE artifact the
// study produces.
//
// A metrics CSV is opened in a spreadsheet, pasted into a message and quoted long after
// SUMMARY.md is out of sight. Without provenance on its own face it asserts, by omission,
// that these are production numbers — which is precisely the claim the user's B9 ruling
// forbids. The earlier version of this file matched nothing for "replayed", "provenance",
// "caveat" or "survivorship" across all 238 rows.
func TestTheMetricsCSVCarriesItsProvenance(t *testing.T) {
	run := study.Execute(nil, entryplanbacktest.RegimeReplayedPIT, recon.ReplayCaveats())
	// Execute on an empty population still produces the header and the run-level provenance,
	// which is exactly what this test is about; the row content is covered elsewhere.
	path := filepath.Join(t.TempDir(), "metrics.csv")
	writeCSV(path, run)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)

	// 1. The leading comment block carries the ruling and BOTH measured biases.
	for _, want := range []string{
		"REPLAYED_PIT",
		"conclusions_hold_under",
		"UNDER REPLAYED REGIME ONLY",
		"NOT a validation of production behaviour",
		"caveat_1",
		"caveat_2",
		"posture",
		"R4",
		"R5",
		"survivorship",
		"3.91",
		"8.21",
		"HEURISTIC — NOT BACKTEST-FITTED",
		"validated profitable",
		"§18",
		"NEGATIVE means the fill was BELOW",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the metrics CSV never says %q. A detached metrics table with no "+
				"'under replayed regime only' statement asserts by omission that these are "+
				"production numbers.\n---\n%s", want, head(text, 1200))
		}
	}

	// 2. Every comment line is a comment, so a naive parser skips them rather than reading
	// one as data.
	var header []string
	for _, l := range strings.Split(text, "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		header = strings.Split(l, ",")
		break
	}
	if len(header) == 0 {
		t.Fatal("the CSV has no data header")
	}
	if header[0] != "regime_provenance" || header[1] != "population" {
		t.Fatalf("the first two columns are %v, want regime_provenance then population — a "+
			"reader who filters out the comment block must still see the provenance per row",
			header[:2])
	}

	// 3. And the file still parses as CSV once the comments are stripped.
	var body strings.Builder
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "#") || l == "" {
			continue
		}
		body.WriteString(l)
		body.WriteString("\n")
	}
	if _, err := csv.NewReader(strings.NewReader(body.String())).ReadAll(); err != nil {
		t.Fatalf("the CSV body does not parse: %v", err)
	}
}

// TestEveryMetricsRowCarriesTheProvenanceColumn checks the per-row half on a run that has rows.
func TestEveryMetricsRowCarriesTheProvenanceColumn(t *testing.T) {
	run := study.Execute(nil, entryplanbacktest.RegimeReplayedPIT, recon.ReplayCaveats())
	path := filepath.Join(t.TempDir(), "metrics.csv")
	writeCSV(path, run)
	raw, _ := os.ReadFile(path)

	var rows [][]string
	var body strings.Builder
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "#") || l == "" {
			continue
		}
		body.WriteString(l + "\n")
	}
	rows, err := csv.NewReader(strings.NewReader(body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("only %d CSV lines; the per-row check would be vacuous", len(rows))
	}
	for i, r := range rows[1:] {
		if r[0] != string(entryplanbacktest.RegimeReplayedPIT) {
			t.Fatalf("row %d carries provenance %q, want %q", i, r[0],
				entryplanbacktest.RegimeReplayedPIT)
		}
		if r[1] == "" {
			t.Fatalf("row %d names no population", i)
		}
	}
}

func head(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// EP-10: the same provenance, as NAMED KEYS. The prose block above is what a reader skims; a
// key=value line is what a reader greps and what a script can assert. This test names each key
// separately so a partial deletion is caught, and asserts the two that are most consequential
// are spelled with the exact value the EP-9 review settled on.
func TestTheMetricsCSVCarriesItsProvenanceAsNamedKeys(t *testing.T) {
	run := study.Execute(nil, entryplanbacktest.RegimeReplayedPIT, recon.ReplayCaveats())
	path := filepath.Join(t.TempDir(), "metrics.csv")
	writeCSV(path, run)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"methodology_version:",
		"entryplan_rule_version:",
		"regime_study_arm: REPLAYED_PIT",
		"execution_arm:",
		"wait_window:",
		"horizon:",
		"adjustment_cohort:",
		"population_class:",
		"production_historical_execution: NOT_EVALUABLE",
		"replay_not_production_wired: true",
		"archived_regime_n: 0",
		"posture: UNKNOWN",
		"posture_dependent_rules: NOT_EVALUABLE",
		"breadth_survivorship_bias: true",
		"NON_EXECUTABLE_COUNTERFACTUAL",
		"POST_HOC_DISCOVERY",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the metrics CSV never says %q", want)
		}
	}
	// The one sentence this file must never permit: a REPLAYED_PIT number described as
	// production performance.
	for _, forbidden := range []string{
		"production_historical_execution: EVALUATED",
		"production historical performance",
		"validated strategy",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the metrics CSV says %q", forbidden)
		}
	}
}
