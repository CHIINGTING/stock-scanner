// Command ep6g_mutation performs the EP-6G mutation audit against the dirty source tree.
//
// Each mutant is one or more exact source replacements in ONE production file (R8a2 needs two).
// For every mutant the command: requires each anchor to occur exactly once; requires the SHA-256
// to change; requires at least one changed line to be code rather than a // comment; requires
// `go build ./...` to pass on the mutant (otherwise INVALID); runs the named focused test;
// restores the file and verifies its SHA-256 equals the baseline before continuing; and finally
// re-verifies every touched file against its baseline. A test failure counts as a KILL only when
// Go reports an assertion failure (`--- FAIL:`). It prints sha256 before / mutated / restored and
// the first assertion message for each mutant.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type mutation struct {
	id, description, file, old, replacement string
	test                                    []string
	// extra holds further exact replacements in the same file, applied after old→replacement.
	extra [][2]string
}

var mutations = []mutation{
	{"M1", "treat UNSUITABLE as SUITABLE", "internal/entryplan/input.go",
		"return KnownValuationSuitability(s) && s != SuitabilityUnsuitable",
		"return KnownValuationSuitability(s)",
		[]string{"go", "test", "./internal/entryplan", "-run", "TestTheRiskCasesProduceWhatTheyWereBuiltFor", "-count=1"}, nil},
	{"M2", "treat UNAVAILABLE as SUITABLE", "internal/entryplan/input.go",
		"return v.Status.OK() && v.BaseTargetPrice != nil && finitePositive(*v.BaseTargetPrice)",
		"return v.BaseTargetPrice != nil && finitePositive(*v.BaseTargetPrice)",
		[]string{"go", "test", "./internal/scanner", "-run", "TestUnavailableValuationCannotFabricateACeiling", "-count=1"}, nil},
	{"M3", "replace BASE target with another scenario", "internal/valuation/entryplan_evidence.go",
		"tp.Scenarios[i].Name == ScenarioBase && tp.Scenarios[i].Status == Available",
		"tp.Scenarios[i].Name == ScenarioBull && tp.Scenarios[i].Status == Available",
		[]string{"go", "test", "./internal/valuation", "-run", "TestBuildEntryPlanEvidence", "-count=1"}, nil},
	{"M4", "remove Target2 ceiling", "internal/entryplan/targets.go",
		"if ceiling := *in.Valuation.BaseTargetPrice; ceiling < raw {",
		"if ceiling := *in.Valuation.BaseTargetPrice; false && ceiling < raw {",
		[]string{"go", "test", "./internal/entryplan", "-run", "TestTheValuationCeilingLowersTargetTwoAndNeverRemovesIt", "-count=1"}, nil},
	{"M5", "use max instead of min for ceiling", "internal/entryplan/targets.go",
		"if ceiling := *in.Valuation.BaseTargetPrice; ceiling < raw {",
		"if ceiling := *in.Valuation.BaseTargetPrice; ceiling > raw {",
		[]string{"go", "test", "./internal/entryplan", "-run", "TestTheValuationCeilingLowersTargetTwoAndNeverRemovesIt", "-count=1"}, nil},
	{"M6", "allow valuation target below Target1", "internal/entryplan/targets.go",
		"if !finitePositive(target) || target < target1 {",
		"if !finitePositive(target) {",
		[]string{"go", "test", "./internal/entryplan", "-run", "TestAnInvertedTargetPairIsWithdrawnRatherThanReordered", "-count=1"}, nil},
	{"M7", "allow valuation to affect status", "internal/entryplan/plan.go",
		"p.Status = decision.Status\n",
		"p.Status = decision.Status\n\tif in.Valuation.Usable() {\n\t\tp.Status = StatusNoValidEntry\n\t}\n",
		[]string{"go", "test", "./internal/scanner", "-run", "TestEntryPlanValuationProjectionIsShadowOnlyAndOptional", "-count=1"}, nil},
	{"M8", "allow valuation through sell-side contradiction", "internal/entryplan/plan.go",
		"contradicted := DecideStatus(DecisionInput{Semantic: in.EntrySemantic.Semantic,\n\t\tPolicy: policy, Thesis: in.Thesis}).Rule == RuleThesisContradicted",
		"contradicted := DecideStatus(DecisionInput{Semantic: in.EntrySemantic.Semantic,\n\t\tPolicy: policy, Thesis: in.Thesis}).Rule == RuleThesisContradicted && !in.Valuation.Usable()",
		[]string{"go", "test", "./internal/scanner", "-run", "TestEntryPlanValuationCannotEscapeSellContradiction", "-count=1"}, nil},
	{"M9", "leak valuation target into contradicted trace", "internal/entryplan/plan.go",
		"if contradicted {\n\t\t// No step ran, so there is no absence to explain",
		"if contradicted {\n\t\tif in.Valuation.BaseTargetPrice != nil { p.EntryTrace.Targets.ValuationTarget = copyFloat(*in.Valuation.BaseTargetPrice) }\n\t\t// No step ran, so there is no absence to explain",
		[]string{"go", "test", "./internal/scanner", "-run", "TestEntryPlanValuationCannotEscapeSellContradiction", "-count=1"}, nil},
	{"M10", "use later-loaded valuation for historical AsOf", "cmd/scanner/main.go",
		"if entryDate != loadDate {",
		"if entryDate > loadDate {",
		[]string{"go", "test", "./cmd/scanner", "-run", "TestEntryPlanValuationRejectsEntryBeforeResearchLoadAsOf", "-count=1"}, nil},
	{"M11", "bypass target tick rounding", "internal/entryplan/targets.go",
		"target, ok := pricerule.RoundToTick(clamped, pricerule.RoundDown)",
		"target, ok := clamped, true",
		[]string{"go", "test", "./internal/entryplan", "-run", "TestEveryEP4PriceIsAlignedDownFromItsOwnRawValue", "-count=1"}, nil},
	{"M12", "fabricate zero/default target when unavailable", "internal/entryplan/plan.go",
		"} else if valEv.Status == Available || valEv.Status == \"\" {\n\t\tvalEv.Status = Unavailable\n\t}",
		"} else {\n\t\tz := 0.0\n\t\tvalEv.Status, valEv.Value = Available, &z\n\t}",
		[]string{"go", "test", "./internal/scanner", "-run", "TestUnavailableValuationCannotFabricateACeiling", "-count=1"}, nil},
	// ── EP-6G review fix rounds 1 and 2 ──────────────────────────────────────────────────
	{"R8a", "accept a future TrailingDate", "internal/valuation/entryplan_evidence.go",
		`|| v.TrailingDate == "" || v.TrailingDate > asOf {`,
		`|| v.TrailingDate == "" {`,
		[]string{"go", "test", "./internal/valuation", "-run", "^TestBuildEntryPlanEvidenceRefusesAFutureTrailingDate$", "-count=1"}, nil},
	{"R8a2", "accept a future TrailingDate AND a close after asOf", "internal/valuation/entryplan_evidence.go",
		`|| v.TrailingDate == "" || v.TrailingDate > asOf {`,
		`|| v.TrailingDate == "" {`,
		[]string{"go", "test", "./internal/valuation", "-run", "^TestBuildEntryPlanEvidenceRefusesAFutureTrailingDate$", "-count=1"},
		[][2]string{{"if d == v.TrailingDate && d <= asOf && bars", "if d == v.TrailingDate && bars"}}},
	{"R8b", "use an earlier close than TrailingDate as EPS base", "internal/valuation/entryplan_evidence.go",
		"if d == v.TrailingDate && d <= asOf",
		"if d <= v.TrailingDate && d <= asOf",
		[]string{"go", "test", "./internal/valuation", "-run", "^TestBuildEntryPlanEvidenceRequiresTheCloseOnTheTrailingDate$", "-count=1"}, nil},
	{"R8d", "bridge accepts valuation for another session", "internal/scanner/entryplan_attach.go",
		"ok && v.AsOf == snap.AsOf {",
		"ok {",
		[]string{"go", "test", "./internal/scanner", "-run", "^TestAttachEntryPlanIgnoresValuationForAnotherSession$", "-count=1"}, nil},
	{"R8e", "accept a filing published after asOf", "internal/valuation/entryplan_evidence.go",
		`!f.Financials.PublishedAt.IsZero() && f.Financials.PublishedAt.Format("2006-01-02") <= asOf {`,
		`!f.Financials.PublishedAt.IsZero() && true {`,
		[]string{"go", "test", "./internal/valuation", "-run", "^TestBuildEntryPlanEvidenceTreatsAFuturePublishedFilingAsAbsent$", "-count=1"}, nil},
	{"R8e2", "accept a filing observed after asOf", "internal/valuation/entryplan_evidence.go",
		`!f.ObservedAt.IsZero() && f.ObservedAt.Format("2006-01-02") <= asOf && f.Financials != nil`,
		`!f.ObservedAt.IsZero() && f.Financials != nil`,
		[]string{"go", "test", "./internal/valuation", "-run", "^TestBuildEntryPlanEvidenceTreatsAFutureObservedFilingAsAbsent$", "-count=1"}, nil},
	{"F1R", "revert F-1: S1 valuation row carries the target value", "internal/entryplan/plan.go",
		"\tif contradicted {\n\t\tvalEv.Status = Unavailable\n\t\tvalEv.Text = string(DispositionNotScreened)\n\t} else if in.Valuation.Usable() {",
		"\tif in.Valuation.Usable() {",
		[]string{"go", "test", "./internal/scanner", "-run", "^TestSellSidePlanJSONCarriesNoValuationTarget$", "-count=1"}, nil},
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func main() {
	root, err := repoRoot()
	must(err)
	original := map[string][]byte{}
	for _, m := range mutations {
		if _, ok := original[m.file]; ok {
			continue
		}
		original[m.file], err = os.ReadFile(filepath.Join(root, m.file))
		must(err)
	}
	defer restoreAll(root, original)
	files := make([]string, 0, len(original))
	for file := range original {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		fmt.Printf("baseline %s %s\n", digest(original[file]), file)
	}

	// A baseline failure cannot honestly kill a mutant, so exercise every distinct command first.
	seen := map[string]bool{}
	for _, m := range mutations {
		key := strings.Join(m.test, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out, runErr := run(root, m.test)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "baseline failed: %s\n%s", strings.Join(m.test, " "), out)
			os.Exit(2)
		}
	}

	killed := 0
	for _, m := range mutations {
		path := filepath.Join(root, m.file)
		base := original[m.file]
		if err := os.WriteFile(path, base, 0o644); err != nil {
			must(err)
		}
		edits := append([][2]string{{m.old, m.replacement}}, m.extra...)
		mutant, invalid := base, ""
		for _, e := range edits {
			if n := bytes.Count(mutant, []byte(e[0])); n != 1 {
				invalid = fmt.Sprintf("anchor count %d for %q", n, e[0])
				break
			}
			if !changesCode(e[0], e[1]) {
				invalid = fmt.Sprintf("edit %q changes only comments/blank lines", e[0])
				break
			}
			mutant = bytes.Replace(mutant, []byte(e[0]), []byte(e[1]), 1)
		}
		if invalid != "" {
			fmt.Printf("| %s | INVALID | %s |\n", m.id, invalid)
			continue
		}
		if digest(mutant) == digest(base) {
			fmt.Printf("| %s | INVALID | hash unchanged |\n", m.id)
			continue
		}
		must(os.WriteFile(path, mutant, 0o644))
		mutated := digestFile(path)
		buildOut, buildErr := run(root, []string{"go", "build", "./..."})
		var out string
		var runErr error
		if buildErr == nil {
			out, runErr = run(root, m.test)
		}
		must(os.WriteFile(path, base, 0o644))
		restored := digestFile(path)
		fmt.Printf("hashes %s %s before=%s mutated=%s restored=%s\n", m.id, m.file, digest(base), mutated, restored)
		if restored != digest(base) {
			fmt.Fprintf(os.Stderr, "%s restore hash mismatch\n", m.id)
			os.Exit(2)
		}
		if buildErr != nil {
			fmt.Printf("| %s | INVALID | mutant does not build |\n", m.id)
			fmt.Fprintf(os.Stderr, "%s build output:\n%s\n", m.id, buildOut)
			continue
		}
		if runErr == nil {
			fmt.Printf("| %s | SURVIVED | %s |\n", m.id, m.description)
			continue
		}
		if !strings.Contains(out, "--- FAIL:") {
			fmt.Printf("| %s | INVALID | test command failed without an assertion failure |\n", m.id)
			fmt.Fprintf(os.Stderr, "%s output:\n%s\n", m.id, out)
			continue
		}
		killed++
		line := firstFailure(out)
		fmt.Printf("| %s | KILLED | %s |\n", m.id, line)
		fmt.Printf("message %s %s\n", m.id, firstMessage(out))
	}

	for _, file := range files {
		if digestFile(filepath.Join(root, file)) != digest(original[file]) {
			fmt.Fprintf(os.Stderr, "final hash mismatch: %s\n", file)
			os.Exit(2)
		}
	}
	fmt.Printf("summary: %d killed, %d total; final production hashes match baseline\n", killed, len(mutations))
	if killed != len(mutations) {
		os.Exit(1)
	}
}

func repoRoot() (string, error) {
	c := exec.Command("git", "rev-parse", "--show-toplevel")
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}

func run(root string, argv []string) (string, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = root
	c.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "ep6g-mutation-gocache"))
	b, err := c.CombinedOutput()
	return string(b), err
}

func restoreAll(root string, original map[string][]byte) {
	for file, content := range original {
		_ = os.WriteFile(filepath.Join(root, file), content, 0o644)
	}
}

func digestFile(path string) string { b, err := os.ReadFile(path); must(err); return digest(b) }

func firstFailure(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- FAIL:") {
			return strings.TrimPrefix(line, "--- ")
		}
	}
	return "assertion failure"
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

// firstMessage returns the first assertion message line (file_test.go:N: ...), truncated.
func firstMessage(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "_test.go:") {
			if len(line) > 300 {
				line = line[:300] + "…"
			}
			return line
		}
	}
	return "(no _test.go message)"
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
