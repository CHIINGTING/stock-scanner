// Command ep8_mutation performs the EP-8 mutation audit (report section ⑲ 進場計畫) against the
// dirty source tree.
//
// Style follows scripts/ep7_mutation. Each mutant is one or more exact replacements in ONE file.
// For every mutant the command: requires every anchor to occur exactly once; requires at least one
// changed line to be code rather than blank or a // comment; requires the SHA-256 to change;
// requires `go build ./...` to pass on the mutant (otherwise INVALID); runs the named focused test
// anchored with ^…$; restores the file and verifies its SHA-256 equals the baseline before
// continuing. A mutant counts as KILLED only when Go reports an assertion failure in the intended
// test (`--- FAIL: <Test> `). Before the first mutant it records the SHA-256 of every watched file
// and runs every distinct test command once on the unmutated tree; after the last mutant it
// re-verifies every watched file.
//
// It then writes docs/EP8_MUTATION_AUDIT.md from what it measured, including a comparison of the
// hash blocks in docs/EP6G_MUTATION_AUDIT.md and docs/EP7_MUTATION_AUDIT.md with the current
// SHA-256 of the files they list. Nothing in that file's tables is typed by hand.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type mutation struct {
	id, description, file, old, replacement string
	pkg, test                               string
	// also holds further exact replacements in the SAME file, each held to the same anchor rule.
	also [][2]string
}

const (
	viewFile = "internal/report/report_entryplan.go"
	tmplFile = "internal/report/report.go"
	rpt      = "./internal/report"
)

var mutations = []mutation{
	{"M1", "render the ⑲ section when show_entry_plan=false", tmplFile,
		"{{- if $.GV.ShowEntryPlan }}{{- with entryPlanView $e }}",
		"{{- if true }}{{- with entryPlanView $e }}",
		rpt, "TestEntryPlanSectionAbsentWhenShowFalse", nil},
	{"M1b", "emit the ⑲ styles when show_entry_plan=false", tmplFile,
		"</style>{{ if .GV.ShowEntryPlan }}<style id=\"ep19-styles\">",
		"</style>{{ if true }}<style id=\"ep19-styles\">",
		rpt, "TestEntryPlanSectionAbsentWhenShowFalse", nil},
	{"M2", "render a missing price as 0.00", viewFile,
		"func epPricePtr(v *float64) string {\n\tif v == nil {\n\t\treturn epUnavailable\n\t}",
		"func epPricePtr(v *float64) string {\n\tif v == nil {\n\t\treturn \"0.00\"\n\t}",
		rpt, "TestEntryPlanSectionMissingValuesRenderDashNeverZero", nil},
	{"M3", "use legacy StockAnalysis.EntryPrice as the zone", viewFile,
		"Zone:          epZone(p.IdealEntry),",
		"Zone:          epPrice(e.A.EntryPrice),",
		rpt, "TestEntryPlanSectionNeverShowsLegacyPriceFields", nil},
	{"M3b", "use legacy WatchlistEntry.EntryZone as the zone", viewFile,
		"Zone:          epZone(p.IdealEntry),",
		"Zone:          e.EntryZone,",
		rpt, "TestEntryPlanSectionNeverShowsLegacyPriceFields", nil},
	{"M4", "recompute Target2 as zone high + 3.5R", viewFile,
		"Target2:       epTarget(p.Target2),",
		"Target2: func() string {\n\t\t\tif p.IdealEntry != nil && p.RiskReward != nil {\n\t\t\t\treturn epPrice(p.IdealEntry.High + 3.5*p.RiskReward.RiskPerShare)\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionValuationCappedTarget2ThroughBridge", nil},
	{"M5", "recover the VALUATION_BASE_TARGET evidence value when Target2 is absent", viewFile,
		"Target2:       epTarget(p.Target2),",
		"Target2: func() string {\n\t\t\tif p.Target2 == nil {\n\t\t\t\tfor _, ev := range p.Evidence {\n\t\t\t\t\tif ev.Key == entryplan.EvidenceValuationBaseTarget && ev.Value != nil {\n\t\t\t\t\t\treturn epPrice(*ev.Value)\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionSellSideRendersNoRecoveredPrice", nil},
	{"M5b", "recover the trace valuation target when Target2 is absent", viewFile,
		"Target2:       epTarget(p.Target2),",
		"Target2: func() string {\n\t\t\tif p.Target2 == nil && p.EntryTrace != nil && p.EntryTrace.Targets.ValuationTarget != nil {\n\t\t\t\treturn epPrice(*p.EntryTrace.Targets.ValuationTarget)\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionSellSideRendersNoRecoveredPrice", nil},
	{"M5c", "sell-side: render the plan's current price as the zone when no zone is published", viewFile,
		"Zone:          epZone(p.IdealEntry),",
		"Zone: func() string {\n\t\t\tif p.IdealEntry == nil && cur > 0 {\n\t\t\t\treturn epPrice(cur)\n\t\t\t}\n\t\t\treturn epZone(p.IdealEntry)\n\t\t}(),",
		rpt, "TestEntryPlanSectionSellSideActionsThroughBridge", nil},
	{"M6", "hardcode RuleVersion to entryplan.RuleVersion", viewFile,
		"RuleVersion:   epText(p.RuleVersion),",
		"RuleVersion:   entryplan.RuleVersion,",
		rpt, "TestEntryPlanSectionRuleVersionComesFromPlan", nil},
	{"M7", "treat Current == Zone.High as above the zone", viewFile,
		"if zone.Low <= cur && cur <= zone.High {",
		"if zone.Low <= cur && cur < zone.High {",
		rpt, "TestEntryPlanRelationZoneBoundaries",
		[][2]string{{"\tif cur > zone.High {\n", "\tif cur >= zone.High {\n"}}},
	{"M8", "treat Current == MaxChase as above the chase ceiling", viewFile,
		"if hasCeiling && cur > *p.MaxChasePrice {",
		"if hasCeiling && cur >= *p.MaxChasePrice {",
		rpt, "TestEntryPlanRelationCurrentEqualsMaxChase", nil},
	{"M9", "omit the shadow-only disclaimer from the heading", tmplFile,
		"<h4>⑲ 進場計畫（Shadow Only，不改變 BUY／WATCH／SELL）</h4>",
		"<h4>⑲ 進場計畫</h4>",
		rpt, "TestEntryPlanSectionShadowOnlyDisclaimer", nil},
	{"M10", "collapse NO_VALID_ENTRY into WATCH-like wording", viewFile,
		"entryplan.StatusNoValidEntry:     \"無有效進場價位\",",
		"entryplan.StatusNoValidEntry:     \"觀察（WATCH）\",",
		rpt, "TestEntryPlanSectionRendersEveryCanonicalStatus", nil},
	{"M11", "treat Current == Zone.Low as below the zone", viewFile,
		"if zone.Low <= cur && cur <= zone.High {",
		"if zone.Low < cur && cur <= zone.High {",
		rpt, "TestEntryPlanRelationZoneBoundaries", nil},
	{"M12", "drop an unmapped reason code", viewFile,
		"\t\t} else {\n\t\t\tout = append(out, string(r))\n\t\t}\n",
		"\t\t}\n",
		rpt, "TestEntryPlanSectionReasonsAndCaveats", nil},
	{"M13", "rendering mutates the plan (clears EntryTrace after reading the policy)", viewFile,
		"\t\tv.ConfidenceLabel = \"未知信心值\"\n\t}\n\treturn v\n",
		"\t\tv.ConfidenceLabel = \"未知信心值\"\n\t}\n\tp.EntryTrace = nil\n\treturn v\n",
		rpt, "TestEntryPlanDisplayChangesOnlyTheEntryPlanBlockAndMutatesNothing", nil},
	{"M14", "current price from scanner A.Close instead of the plan's evidence", viewFile,
		"cur, curOK := epPlanCurrentPrice(p)",
		"cur, curOK := e.A.Close, e.A.Close > 0",
		rpt, "TestEntryPlanCurrentPriceComesFromPlanEvidenceOnly", nil},
	{"M15", "unknown status renders silently", viewFile,
		"v.StatusLabel = epUnknownStatusLabel",
		"v.StatusLabel = \"\"",
		rpt, "TestEntryPlanSectionUnknownStatusIsVisiblyUnknown", nil},
	{"M16", "recompute the R:R ratio from per-share figures", viewFile,
		"out := fmt.Sprintf(\"%.2f\", rr.Ratio)",
		"out := fmt.Sprintf(\"%.2f\", rr.RewardPerShare/rr.RiskPerShare)",
		rpt, "TestEntryPlanSectionRendersPublishedPlan", nil},
	{"M17", "withdrawn Target2 renders the raw trace Target2", viewFile,
		"Target2:       epTarget(p.Target2),",
		"Target2: func() string {\n\t\t\tif p.Target2 == nil && p.EntryTrace != nil && p.EntryTrace.Targets.Target2Raw != nil {\n\t\t\t\treturn epPrice(*p.EntryTrace.Targets.Target2Raw)\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionWithdrawnTarget2RendersDash", nil},
	{"M18", "sort reasons instead of preserving the plan's order", viewFile,
		"\t\t}\n\t}\n\treturn out\n}\n\nfunc epCaveats(",
		"\t\t}\n\t}\n\tsort.Strings(out)\n\treturn out\n}\n\nfunc epCaveats(",
		rpt, "TestEntryPlanSectionReasonsAndCaveats",
		[][2]string{{"\t\"math\"\n", "\t\"math\"\n\t\"sort\"\n"}}},
	{"M19", "fabricate an INSUFFICIENT_DATA plan (with a planted buy-side semantic row, so it reaches the display gate) for a nil plan", viewFile,
		"\tif p == nil || !epBuySideSemantic(p) {\n\t\treturn nil\n\t}\n",
		"\tif p == nil {\n\t\tp = &entryplan.Plan{Status: entryplan.StatusInsufficientData,\n\t\t\tEvidence: []entryplan.Evidence{{Key: entryplan.EvidenceEntrySemantic,\n\t\t\t\tStatus: entryplan.Available, Text: string(entryplan.EntrySemanticPullback)}}}\n\t}\n\tif !epBuySideSemantic(p) {\n\t\treturn nil\n\t}\n",
		rpt, "TestEntryPlanNilPlanWithShowTrueRendersNothing", nil},
	{"M20", "confidence label ignores the plan (always MEDIUM)", viewFile,
		"Confidence:    epText(string(p.Confidence)),",
		"Confidence:    string(entryplan.ConfidenceMedium),",
		rpt, "TestEntryPlanSectionConfidence", nil},
	{"M21", "call entryplan.DecideStatus from report code", viewFile,
		"\tcur, curOK := epPlanCurrentPrice(p)\n",
		"\tcur, curOK := epPlanCurrentPrice(p)\n\t_ = entryplan.DecideStatus(entryplan.DecisionInput{})\n",
		rpt, "TestEntryPlanReportCodeComputesNothing", nil},
	{"M22", "render a section for an entry with no buy-side entry semantic (exit actions)", viewFile,
		"\tif p == nil || !epBuySideSemantic(p) {",
		"\tif p == nil {",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil},
	{"M22b", "accept any ENTRY_SEMANTIC row, resolved or not, as buy-side", viewFile,
		"\t\treturn ev.Status == entryplan.Available && entryplan.EntrySemantic(ev.Text).Resolved()",
		"\t\treturn ev.Status == entryplan.Available || ev.Text != \"\"",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil},
	{"M22c", "hide the sell-side contradiction (S1) section as if it were an exit action", viewFile,
		"\tif p == nil || !epBuySideSemantic(p) {",
		"\tif p == nil || !epBuySideSemantic(p) || p.Status == entryplan.StatusNoValidEntry {",
		rpt, "TestEntryPlanSellSideContradictionStillRenders", nil},
	{"M23", "print a non-positive R:R ratio as a measurement", viewFile,
		"\tif rr == nil || !epIsPrice(rr.Ratio) {",
		"\tif rr == nil || math.IsNaN(rr.Ratio) || math.IsInf(rr.Ratio, 0) {",
		rpt, "TestEntryPlanNonPositiveRatioAndRMultipleRenderDash", nil},
	{"M23b", "print a non-positive R multiple as a measurement", viewFile,
		"\tif t.RMultiple != nil && epIsPrice(*t.RMultiple) {",
		"\tif t.RMultiple != nil && !math.IsNaN(*t.RMultiple) && !math.IsInf(*t.RMultiple, 0) {",
		rpt, "TestEntryPlanNonPositiveRatioAndRMultipleRenderDash", nil},
	{"W1", "wire the ⑲ display flag from another config flag", "cmd/scanner/main.go",
		"ShowEntryPlan:               cfg.Scanner.ShowEntryPlan,",
		"ShowEntryPlan:               cfg.Scanner.ShowTechnicalIndicators,",
		"./cmd/scanner", "TestReportShowEntryPlanIsWiredFromConfig", nil},
}

// watched are hashed before the first mutant and re-verified at the end, in addition to every
// mutated file.
var watched = []string{
	viewFile,
	tmplFile,
	"internal/report/report_entryplan_test.go",
	"internal/report/report_entryplan_guard_test.go",
	"cmd/scanner/main.go",
	"cmd/scanner/entryplan_report_wiring_test.go",
	"internal/scanner/entryplan_symbol_guard_test.go",
	"internal/scanner/entryplan_attach.go",
	"internal/research/entryplan_evidence_test.go",
	"internal/entryplan/decide.go",
	"internal/entryplan/types.go",
	"internal/entryplan/confidence.go",
}

const auditDoc = "docs/EP8_MUTATION_AUDIT.md"

var priorAudits = []string{"docs/EP6G_MUTATION_AUDIT.md", "docs/EP7_MUTATION_AUDIT.md"}

// notes are fixed commentary printed under the results table. They explain HOW a mutant is
// killed; the result itself is always the runner's.
var notes = []string{
	"**M1, M1b and M9 mutate the html/template text inside `internal/report/report.go`**; the code-line " +
		"check counts template lines as code because they are not `//` comments.",
	"**M5, M5b and M17 are killed by synthetic plans.** A plan produced by the real bridge for a " +
		"sell-side Action carries no valuation value on its evidence row and no step trace (EP-6G F-1), " +
		"so a renderer that recovered those fields would still print `—` for it. " +
		"`TestEntryPlanSectionSellSideActionsThroughBridge` is the real-bridge test; M5c is the mutant aimed at it.",
	"**M7 applies two replacements** (the inclusive-bound check and the `above` check) so that " +
		"Current == Zone.High lands in the above-zone arm rather than falling through to below.",
	"**M13 changes no HTML.** It is killed only by the deep-compare half of the isolation test.",
	"**M16 depends on a deliberately inconsistent fixture** (RewardPerShare/RiskPerShare = 1.51, Ratio = 1.2641).",
	"**M20's first message can vary between runs**, because `TestEntryPlanSectionConfidence` iterates a map " +
		"of the Confidence constants parsed from `confidence.go`; any non-MEDIUM value fails first.",
	"**M22 / M22b / M22c cover the EP-8 review decision** (only a plan publishing a buy-side entry " +
		"semantic renders ⑲). M22c is the opposite error: hiding the EP-6D/6G sell-side contradiction, " +
		"which must still render `NO_VALID_ENTRY`.",
	"**M23 / M23b are defensive.** `internal/entryplan` cannot publish a non-positive Ratio or R " +
		"multiple today, so the mutants are killed by a synthetic plan, not by production output.",
	"**M21 is killed only structurally** by the report purity guard; the call has no effect on output.",
	"**W1 is killed only structurally**: `TestReportShowEntryPlanIsWiredFromConfig` parses `cmd/scanner/main.go`.",
}

type row struct {
	m                         mutation
	result, test, message     string
	before, mutated, restored string
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func main() {
	root, err := repoRoot()
	must(err)
	started := time.Now()
	fmt.Printf("started %s\n", started.Format("2006-01-02 15:04:05 MST"))
	original := map[string][]byte{}
	for _, f := range watched {
		original[f], err = os.ReadFile(filepath.Join(root, f))
		must(err)
	}
	for _, m := range mutations {
		if _, ok := original[m.file]; ok {
			continue
		}
		original[m.file], err = os.ReadFile(filepath.Join(root, m.file))
		must(err)
	}
	defer restoreAll(root, original)
	files := make([]string, 0, len(original))
	for f := range original {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Printf("baseline %s  %s\n", digest(original[f]), f)
	}

	seen := map[string]bool{}
	for _, m := range mutations {
		key := m.pkg + "\x00" + m.test
		if seen[key] {
			continue
		}
		seen[key] = true
		if out, runErr := run(root, testCmd(m)); runErr != nil {
			fmt.Fprintf(os.Stderr, "baseline failed: %s\n%s", strings.Join(testCmd(m), " "), out)
			os.Exit(2)
		}
	}

	var rows []row
	killed := 0
	for _, m := range mutations {
		path := filepath.Join(root, m.file)
		base := original[m.file]
		must(os.WriteFile(path, base, 0o644))
		r := row{m: m, before: digest(base)}
		mutant, invalid := apply(base, m)
		if invalid != "" {
			r.result, r.message = "INVALID", invalid
			fmt.Printf("| %s | INVALID | %s |\n", m.id, invalid)
			rows = append(rows, r)
			continue
		}
		must(os.WriteFile(path, mutant, 0o644))
		r.mutated = digestFile(path)
		buildOut, buildErr := run(root, []string{"go", "build", "./..."})
		var out string
		var runErr error
		if buildErr == nil {
			out, runErr = run(root, testCmd(m))
		}
		must(os.WriteFile(path, base, 0o644))
		r.restored = digestFile(path)
		fmt.Printf("hashes %s %s before=%s mutated=%s restored=%s\n", m.id, m.file, r.before, r.mutated, r.restored)
		if r.restored != r.before {
			fmt.Fprintf(os.Stderr, "%s restore hash mismatch\n", m.id)
			os.Exit(2)
		}
		switch {
		case buildErr != nil:
			r.result, r.message = "INVALID", "mutant does not build"
			fmt.Fprintf(os.Stderr, "%s build output:\n%s\n", m.id, buildOut)
		case runErr == nil:
			r.result, r.message = "SURVIVED", "intended test passed on the mutant"
		case !strings.Contains(out, "--- FAIL: "+m.test+" "):
			r.result, r.message = "INVALID", "no assertion failure in the intended test"
			fmt.Fprintf(os.Stderr, "%s output:\n%s\n", m.id, out)
		default:
			killed++
			r.result, r.test, r.message = "KILLED", m.test, firstMessage(out, root)
		}
		fmt.Printf("| %s | %s | %s | %s |\n", m.id, r.result, r.test, r.message)
		rows = append(rows, r)
	}

	for _, f := range files {
		if digestFile(filepath.Join(root, f)) != digest(original[f]) {
			fmt.Fprintf(os.Stderr, "final hash mismatch: %s\n", f)
			os.Exit(2)
		}
	}
	finished := time.Now()
	fmt.Printf("finished %s\n", finished.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("summary: %d killed, %d total; final hashes match baseline\n", killed, len(mutations))
	exit := 0
	if killed != len(mutations) {
		exit = 1
	}
	must(os.WriteFile(filepath.Join(root, auditDoc),
		[]byte(renderDoc(root, started, finished, exit, rows, files, original)), 0o644))
	fmt.Printf("wrote %s\n", auditDoc)
	os.Exit(exit)
}

// apply performs every replacement of m on base, or returns why the mutant is invalid.
func apply(base []byte, m mutation) ([]byte, string) {
	edits := append([][2]string{{m.old, m.replacement}}, m.also...)
	out := base
	code := false
	for i, e := range edits {
		if n := bytes.Count(out, []byte(e[0])); n != 1 {
			return nil, fmt.Sprintf("anchor %d count %d", i, n)
		}
		code = code || changesCode(e[0], e[1])
		out = bytes.Replace(out, []byte(e[0]), []byte(e[1]), 1)
	}
	if !code {
		return nil, "changes only comments/blank lines"
	}
	if digest(out) == digest(base) {
		return nil, "hash unchanged"
	}
	return out, ""
}

func renderDoc(root string, started, finished time.Time, exit int, rows []row, files []string, original map[string][]byte) string {
	const stamp = "2006-01-02 15:04:05 MST"
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	branch, _ := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()

	w("# EP-8 mutation audit\n\n")
	w("Generated by `go run ./scripts/ep8_mutation`. Do not edit this file by hand; re-run the runner.\n\n")
	w("Scope: EP-8 (report section ⑲ 進場計畫, presentation only). Review status is tracked in README.md.\n")
	w("This file records one audit run. It is not a review verdict.\n\n")
	w("## Run\n\n")
	w("- Started %s, finished %s, exit status %d.\n", started.Format(stamp), finished.Format(stamp), exit)
	w("- Worktree: uncommitted tree on branch `%s`.\n", strings.TrimSpace(string(branch)))
	w("- Command: `go run ./scripts/ep8_mutation` (child `go` commands use `GOCACHE=$TMPDIR/ep8-mutation-gocache`).\n\n")

	w("## What the runner checks (scripts/ep8_mutation/main.go)\n\n")
	w("Before the first mutant it records the SHA-256 of every watched file and runs every distinct test\n")
	w("command once on the unmutated tree, aborting if any fails. For each mutant (one or more exact\n")
	w("replacements in one file) it:\n\n")
	w("1. requires every anchor to occur exactly once;\n")
	w("2. requires at least one changed line to be code, not blank and not a `//` comment;\n")
	w("3. requires the file's SHA-256 to change;\n")
	w("4. requires `go build ./...` to pass on the mutant (otherwise the row is `INVALID`);\n")
	w("5. runs `go test <pkg> -run ^<Test>$ -count=1`; the row is `KILLED` only when the output contains\n")
	w("   `--- FAIL: <Test> `, i.e. an assertion failure in the intended test;\n")
	w("6. writes the original bytes back and re-checks the SHA-256 against the baseline (a mismatch aborts).\n\n")
	w("After the last mutant it re-checks every watched file against its baseline. A deferred restore\n")
	w("rewrites every watched file on a panic; it does not run on `os.Exit`, which is only called before\n")
	w("the first mutation, after the current file has been written back, or after this file is written.\n\n")

	w("## Results\n\n")
	w("| Mutant | File | Mutation | Result | Red test | First assertion message |\n")
	w("| --- | --- | --- | --- | --- | --- |\n")
	killed, survived, invalid := 0, 0, 0
	for _, r := range rows {
		switch r.result {
		case "KILLED":
			killed++
		case "SURVIVED":
			survived++
		default:
			invalid++
		}
		test := ""
		if r.test != "" {
			test = "`" + r.test + "`"
		}
		w("| %s | %s | %s | %s | %s | `%s` |\n", r.m.id, strings.TrimPrefix(r.m.file, "internal/"),
			cell(r.m.description), r.result, test, strings.ReplaceAll(r.message, "`", "'"))
	}
	w("\nSummary: **%d killed, %d survived, %d invalid** of %d.\n\n", killed, survived, invalid, len(rows))

	w("### Per-mutant hashes\n\n```text\n")
	for _, r := range rows {
		w("%-4s before=%s mutated=%s restored=%s  %s\n", r.m.id, r.before, r.mutated, r.restored, r.m.file)
	}
	w("```\n\n### Notes\n\n")
	for _, n := range notes {
		w("- %s\n", n)
	}

	w("\n## Hashes\n\nSHA-256 of every watched file, recorded before the first mutant and re-verified after the last:\n\n```text\n")
	for _, f := range files {
		w("%s  %s\n", digest(original[f]), f)
	}
	w("```\n\nThese hashes describe this run only. Any later edit to these files makes the table stale.\n\n")

	w("## Earlier audits\n\n")
	w("The runner parsed the hash block of each earlier audit document and compared each entry with the\n")
	w("file's current SHA-256. A `MISMATCH` line means that document is stale and its runner must be re-run.\n")
	for _, doc := range priorAudits {
		w("\n`%s`:\n\n```text\n", doc)
		for _, line := range hashCheck(root, doc) {
			w("%s\n", line)
		}
		w("```\n")
	}
	return b.String()
}

var hashLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

func hashCheck(root, docPath string) []string {
	doc, err := os.ReadFile(filepath.Join(root, docPath))
	if err != nil {
		return []string{"UNREADABLE " + err.Error()}
	}
	var out []string
	for _, l := range strings.Split(string(doc), "\n") {
		m := hashLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, m[2]))
		switch {
		case err != nil:
			out = append(out, "MISSING  "+m[2])
		case digest(b) == m[1]:
			out = append(out, "OK       "+m[2])
		default:
			out = append(out, "MISMATCH "+m[2])
		}
	}
	if len(out) == 0 {
		return []string{"NO HASH LINES FOUND"}
	}
	return out
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func testCmd(m mutation) []string {
	return []string{"go", "test", m.pkg, "-run", "^" + m.test + "$", "-count=1"}
}

func repoRoot() (string, error) {
	b, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	return strings.TrimSpace(string(b)), err
}

func run(root string, argv []string) (string, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = root
	c.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "ep8-mutation-gocache"))
	b, err := c.CombinedOutput()
	return string(b), err
}

func restoreAll(root string, original map[string][]byte) {
	for f, content := range original {
		_ = os.WriteFile(filepath.Join(root, f), content, 0o644)
	}
}

func digestFile(path string) string {
	b, err := os.ReadFile(path)
	must(err)
	return digest(b)
}

// changesCode reports whether replacing old with replacement alters at least one line that is
// neither blank nor a // comment.
func changesCode(old, replacement string) bool {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[strings.TrimSpace(l)]++
		}
		return m
	}
	a, b := count(old), count(replacement)
	for _, pair := range [][2]map[string]int{{a, b}, {b, a}} {
		for l, n := range pair[0] {
			if n != pair[1][l] && l != "" && !strings.HasPrefix(l, "//") {
				return true
			}
		}
	}
	return false
}

// firstMessage returns the first assertion message line (file_test.go:N: ...), with the repo root
// shortened to "..." and truncated.
func firstMessage(out, root string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "_test.go:") {
			line = strings.ReplaceAll(line, root, "...")
			if r := []rune(line); len(r) > 240 {
				line = string(r[:240]) + "…"
			}
			return cell(line)
		}
	}
	return "(no _test.go message)"
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
