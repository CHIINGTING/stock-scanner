// Command ep7_mutation performs the EP-7 mutation audit against the dirty source tree.
//
// Style follows scripts/ep6g_mutation. Each mutant is one or more exact replacements in ONE file.
// For every mutant the command: requires every anchor to occur exactly once; requires at least one
// changed line to be code rather than blank or a // comment; requires the SHA-256 to change;
// requires `go build ./...` to pass on the mutant (otherwise INVALID); runs the named focused test
// anchored with ^…$; restores the file and verifies its SHA-256 equals the baseline before
// continuing. A mutant counts as KILLED only when Go reports an assertion failure (`--- FAIL:`)
// AND the failing test is the intended one. Before the first mutant it records the SHA-256 of
// every watched file (every mutated file plus every EP-7 production file) and runs every distinct
// test command once on the unmutated tree; after the last mutant it re-verifies every watched file.
//
// It then writes docs/EP7_MUTATION_AUDIT.md from what it measured: run times, one table row per
// mutant (result, red test and first assertion message as reported by `go test`), the SHA-256 of
// every watched file, and a comparison of the files hashed in docs/EP6G_MUTATION_AUDIT.md with
// their current SHA-256. Nothing in that file's tables is typed by hand.
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

const evFile = "internal/research/entryplan_evidence.go"

var mutations = []mutation{
	{"M1", "persist missing Target2 as zero", evFile,
		"\tif v := p.Target2; v != nil {\n\t\tprice(epKeyTarget2, v.Price, epUnitPrice)\n\t}\n",
		"\tif v := p.Target2; v != nil {\n\t\tprice(epKeyTarget2, v.Price, epUnitPrice)\n\t} else {\n\t\tc.num(store.CategoryEntryPlan, epKeyTarget2, 0, epUnitPrice, srcEntryPlan)\n\t}\n",
		"./internal/research", "TestEntryPlanUnavailableFieldsLeaveTheirKeysAbsent", nil},
	{"M2", "persist ep_* keys when Plan is nil", evFile,
		"\tif p == nil {\n\t\treturn nil, nil\n\t}\n",
		"\tif p == nil {\n\t\tp = &entryplan.Plan{Symbol: snap.Symbol, AsOf: snap.TradingDate, Status: entryplan.StatusInsufficientData, RuleVersion: \"DISABLED\"}\n\t}\n",
		"./internal/research", "TestEntryPlanDisabledWritesNoEvidenceEndToEnd", nil},
	{"M3", "drop the ep_ prefix so ep_target_2 collides with technical target_2", evFile,
		"epKeyTarget2      = \"ep_target_2\"",
		"epKeyTarget2      = \"target_2\"",
		"./internal/research", "TestEntryPlanKeysCollideWithNoExistingEvidenceKey", nil},
	{"M4", "hardcode RuleVersion instead of Plan.RuleVersion", evFile,
		"c.text(store.CategoryEntryPlan, epKeyRuleVersion, p.RuleVersion, srcEntryPlan)",
		"c.text(store.CategoryEntryPlan, epKeyRuleVersion, entryplan.RuleVersion, srcEntryPlan)",
		"./internal/research", "TestEntryPlanRuleVersionIsCopiedFromThePlan", nil},
	{"M5", "persist a trace Target2 when the plan publishes none (sell contradiction)", evFile,
		"\tif v := p.Target2; v != nil {\n\t\tprice(epKeyTarget2, v.Price, epUnitPrice)\n\t}\n",
		"\tif v := p.Target2; v != nil {\n\t\tprice(epKeyTarget2, v.Price, epUnitPrice)\n\t} else if tr := p.EntryTrace; tr != nil && tr.Targets.Target2 != nil {\n\t\tprice(epKeyTarget2, *tr.Targets.Target2, epUnitPrice)\n\t}\n",
		"./internal/research", "TestEntryPlanSellContradictionPersistsStatusOnlyEvenWithTraceNumbers", nil},
	{"M6", "omit ep_status", evFile,
		"\tc.text(store.CategoryEntryPlan, epKeyStatus, string(p.Status), srcEntryPlan)\n",
		"\t_ = epKeyStatus\n",
		"./internal/research", "TestEntryPlanCompletePlanProjectsExactRows", nil},
	{"M7", "swap zone_low / zone_high", evFile,
		"price(epKeyZoneLow, z.Low, epUnitPrice)\n\t\tprice(epKeyZoneHigh, z.High, epUnitPrice)",
		"price(epKeyZoneLow, z.High, epUnitPrice)\n\t\tprice(epKeyZoneHigh, z.Low, epUnitPrice)",
		"./internal/research", "TestEntryPlanCompletePlanProjectsExactRows", nil},
	{"M8", "persist Target1's value as ep_target_2", evFile,
		"price(epKeyTarget2, v.Price, epUnitPrice)",
		"price(epKeyTarget2, p.Target1.Price, epUnitPrice)",
		"./internal/research", "TestEntryPlanValuationCappedTarget2PersistsTheFinalValue", nil},
	{"M9", "allow cross-session bleed: drop the AsOf identity check", evFile,
		"if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"if p.Symbol != snap.Symbol {",
		"./internal/research", "TestEntryPlanRowsBelongToTheirOwnRunSnapshotAndAsOf", nil},
	{"M9b", "allow cross-symbol bleed: drop the Symbol identity check", evFile,
		"if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"if p.AsOf != snap.TradingDate {",
		"./internal/research", "TestEntryPlanRowsBelongToTheirOwnRunSnapshotAndAsOf", nil},
	{"M10a", "an unlisted research function reads WatchlistEntry.EntryPlan", "internal/research/research.go",
		"\t\tcountByCategory(byCat, c.items)\n\t\tid, ok := r.persist(ctx, run, snap, c.items, &res)",
		"\t\tif e.EntryPlan != nil && e.RocketScore < 0 {\n\t\t\tcontinue\n\t\t}\n\t\tcountByCategory(byCat, c.items)\n\t\tid, ok := r.persist(ctx, run, snap, c.items, &res)",
		"./internal/scanner", "TestOnlyAttachEntryPlanTouchesTheEntryPlanFieldRepoWide", nil},
	{"M10b", "scanner ranking comparator references an ep_* key", "internal/scanner/watchlist.go",
		"return entries[i].RocketScore > entries[j].RocketScore\n\t})\n\tstart := 0",
		"return entries[i].RocketScore+0*len(\"ep_rr_ratio\") > entries[j].RocketScore\n\t})\n\tstart := 0",
		"./internal/research", "TestNoProductionCodeReadsEntryPlanEvidenceBack", nil},
	{"M10c", "widen the .EntryPlan allowlist to a package prefix (test helper)", "internal/scanner/entryplan_symbol_guard_test.go",
		"if _, ok := entryPlanFieldFunctions[decl]; !ok {",
		"if _, ok := entryPlanFieldFunctions[decl]; !ok && !strings.HasPrefix(decl, \"internal/research/\") {",
		"./internal/scanner", "TestOnlyAttachEntryPlanTouchesTheEntryPlanFieldRepoWide", nil},
	{"M11", "revert F1: mixed repair stamped PRICES_WITHDRAWN", "internal/entryplan/enforce.go",
		"outcome = InvariantCheckPricesAndStatusWithdrawn",
		"outcome = InvariantCheckPricesWithdrawn",
		"./internal/entryplan", "TestTheGateRecordsMixedPriceAndStatusWithdrawal", nil},
	{"M12", "persist a present non-positive price instead of refusing the plan", evFile,
		"if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {",
		"if math.IsNaN(v) || math.IsInf(v, 0) {",
		"./internal/research", "TestEntryPlanMissingNeverBecomesNumericZero", nil},
	{"M13", "ep_policy from SemanticAllowance instead of the policy Name", evFile,
		"string(tr.Policy.Name), srcEntryPlanPolicy)",
		"string(tr.Policy.SemanticAllowance), srcEntryPlanPolicy)",
		"./internal/research", "TestEntryPlanPolicyIsTheTracePolicyNameAsText", nil},
	{"M14", "ep_rr_ratio from RewardPerShare instead of Ratio", evFile,
		"price(epKeyRRRatio, v.Ratio, epUnitRatio)",
		"price(epKeyRRRatio, v.RewardPerShare, epUnitRatio)",
		"./internal/research", "TestEntryPlanRiskRewardRatioIsCopiedWhenPresent", nil},
	// ── post-EP-7 closure: user decision 1 (identity match) and decision 2 (no session gating) ──
	{"D1", "drop the AsOf equality check", evFile,
		"if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"if p.Symbol != snap.Symbol {",
		"./internal/research", "TestEntryPlanAsOfMismatchPersistsNoEntryPlanRows", nil},
	{"D2", "drop the Symbol equality check", evFile,
		"if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"if p.AsOf != snap.TradingDate {",
		"./internal/research", "TestEntryPlanSymbolMismatchPersistsNoEntryPlanRows", nil},
	{"D3", "re-date a copy of the plan to the snapshot date before comparing", evFile,
		"if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"if p.AsOf != snap.TradingDate {\n\t\tredated := *p\n\t\tredated.AsOf = snap.TradingDate\n\t\tp = &redated\n\t}\n\tif p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"./internal/research", "TestEntryPlanAsOfMismatchPersistsNoEntryPlanRows", nil},
	{"D4", "time.Now date gate in RecordScan: hold plans whose date is not before today", "internal/research/research.go",
		"\t\tif err := buildEntryPlanEvidence(&c, e, snap); err != nil {",
		"\t\tif snap.TradingDate >= time.Now().Format(\"2006-01-02\") {\n\t\t\tr.logf(\"research: %s entry plan held until the session completes\", e.A.Symbol)\n\t\t} else if err := buildEntryPlanEvidence(&c, e, snap); err != nil {",
		"./internal/research", "TestEntryPlanSameIdentityPlanIsPersistedWithoutSessionGating", nil},
	{"D4s", "the D4 gate, checked by the structural guard", "internal/research/research.go",
		"\t\tif err := buildEntryPlanEvidence(&c, e, snap); err != nil {",
		"\t\tif snap.TradingDate >= time.Now().Format(\"2006-01-02\") {\n\t\t\tr.logf(\"research: %s entry plan held until the session completes\", e.A.Symbol)\n\t\t} else if err := buildEntryPlanEvidence(&c, e, snap); err != nil {",
		"./internal/research", "TestEntryPlanPersistenceHasNoClockOrSessionReference", nil},
	{"D4b", "time-of-day gate in the projection: refuse before 14:00 Asia/Taipei", evFile,
		"\tif p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"\tif loc, err := time.LoadLocation(\"Asia/Taipei\"); err == nil && time.Now().In(loc).Hour() < 14 {\n\t\treturn nil, fmt.Errorf(\"session not closed\")\n\t}\n\tif p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {",
		"./internal/research", "TestEntryPlanPersistenceHasNoClockOrSessionReference",
		[][2]string{{"\t\"math\"\n", "\t\"math\"\n\t\"time\"\n"}}},
}

// watched are hashed before the first mutant and re-verified at the end, in addition to every
// mutated file. The two research test files are watched because the D-series kills depend on them.
var watched = []string{
	evFile,
	"internal/research/research.go",
	"internal/research/evidence.go",
	"internal/research/entryplan_evidence_test.go",
	"internal/research/entryplan_evidence_guard_test.go",
	"internal/store/model.go",
	"internal/entryplan/enforce.go",
	"internal/entryplan/types.go",
}

const (
	auditDoc     = "docs/EP7_MUTATION_AUDIT.md"
	ep6gAuditDoc = "docs/EP6G_MUTATION_AUDIT.md"
)

// notes are fixed commentary printed under the results table. They explain HOW a mutant is
// killed; the result itself is always the runner's.
var notes = []string{
	"**M10c mutates a test file**, because the `.EntryPlan` allowlist and its decision helper " +
		"(`entryPlanFieldOffenders`) live in `internal/scanner/entryplan_symbol_guard_test.go`.",
	"**M10b is killed only structurally.** The mutant changes no ranking value, so no behaviour test " +
		"could see it. It shows that the AST guard catches an `ep_` literal in a scanner function.",
	"**M11 covers the F1 code, not the F1 comments.** No test checks that a comment describes the code.",
	"**M12's first message can vary between runs**, because that test iterates a map of malformed-price cases.",
	"**D1/D2 share their anchor with M9/M9b** but are aimed at the single-cause tests: every case in " +
		"`TestEntryPlanAsOfMismatchPersistsNoEntryPlanRows` has a matching Symbol, and every case in " +
		"`TestEntryPlanSymbolMismatchPersistsNoEntryPlanRows` has a matching AsOf.",
	"**D3 re-dates a copy**, so the caller's plan is untouched and only the stored rows can show it.",
	"**D4 and D4s are the same mutant** (a `time.Now` date gate in `RecordScan`'s watchlist loop), run " +
		"against the behaviour test and against the structural guard. The behaviour test kills it through " +
		"its 2099-12-31 case, so that kill depends on the runner's clock being earlier than that date.",
	"**D4b (time-of-day gate) is killed only structurally.** Whether a behaviour test would fail on it " +
		"depends on the hour the tests run; the structural guard flags it regardless. The guard is a " +
		"name-based AST check over two scopes, not a proof that no clock is reachable.",
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

	w("# EP-7 mutation audit\n\n")
	w("Generated by `go run ./scripts/ep7_mutation`. Do not edit this file by hand; re-run the runner.\n\n")
	w("Scope: EP-7 (EntryPlan persistence as evidence rows, plus F1). The D-series rows pin the post-EP-7\n")
	w("user decisions (identity match required; no session gating). Review status is tracked in README.md.\n")
	w("This file records one audit run. It is not a review verdict.\n\n")
	w("## Run\n\n")
	w("- Started %s, finished %s, exit status %d.\n", started.Format(stamp), finished.Format(stamp), exit)
	w("- Worktree: uncommitted tree on branch `%s`.\n", strings.TrimSpace(string(branch)))
	w("- Command: `go run ./scripts/ep7_mutation` (child `go` commands use `GOCACHE=$TMPDIR/ep7-mutation-gocache`).\n\n")

	w("## What the runner checks (scripts/ep7_mutation/main.go)\n\n")
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

	w("## EP-6G audit\n\n")
	w("The runner parsed the hash block of `%s` and compared each entry with the file's current SHA-256:\n\n```text\n", ep6gAuditDoc)
	for _, line := range ep6gHashCheck(root) {
		w("%s\n", line)
	}
	w("```\n\nA `MISMATCH` line means `%s` is stale and `go run ./scripts/ep6g_mutation` must be re-run.\n", ep6gAuditDoc)
	return b.String()
}

var hashLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

func ep6gHashCheck(root string) []string {
	doc, err := os.ReadFile(filepath.Join(root, ep6gAuditDoc))
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
	c.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "ep7-mutation-gocache"))
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
