// Command ep10_mutation performs the EP-10 mutation audit — final integration, ⑲/④ convergence
// presentation, legacy isolation, architecture and EP-9 artifact provenance — against the dirty
// source tree.
//
// Style follows scripts/ep8_mutation and scripts/ep9_mutation, and the rules are theirs. Each
// mutant is one or more exact replacements in ONE file. For every mutant the command: requires
// every anchor to occur exactly once; requires at least one changed line to be code rather than
// blank or a // comment; requires the SHA-256 to change; requires `go build ./...` to pass on
// the mutant (otherwise INVALID); runs the named focused test anchored with ^…$; restores the
// file and verifies its SHA-256 equals the baseline before continuing. A mutant counts as
// KILLED only when Go reports an assertion failure in the intended test (`--- FAIL: <Test> `).
// Before the first mutant it records the SHA-256 of every watched file and runs every distinct
// test command once on the unmutated tree; after the last mutant it re-verifies every watched
// file.
//
// It then writes docs/EP10_MUTATION_AUDIT.md from what it measured, including a comparison of
// the hash blocks in the EP-6G, EP-7, EP-8 and EP-9 audit documents with the current SHA-256 of
// the files they list. Nothing in that file's tables is typed by hand, and it contains no
// review-status text.
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
	// class is the DECLARED bucket for a mutant that is expected NOT to be killed, with the
	// reason it cannot be. Empty means the mutant must be KILLED.
	//
	// EP-10 reports four buckets and no others:
	//
	//	KILLED          the intended test failed on the mutant.
	//	EQUIVALENT      the mutant compiles and changes no observable behaviour, because the
	//	                branch it alters is unreachable under premises stated in `why` and
	//	                machine-pinned by a named test. NOT a defect and NOT a kill.
	//	VACUOUS         the mutant is observably a no-op ON THE POPULATION IT TOUCHES, so it
	//	                could never have been killed. It is a defect IN THE MUTANT, recorded
	//	                rather than deleted so nobody re-proposes it.
	//	TRUE_SURVIVOR   a mutant that should have been killed and was not. Must be zero.
	//
	// Production semantics are NEVER changed to move a row out of a bucket.
	class, why string
}

const (
	classKilled       = "KILLED"
	classEquivalent   = "EQUIVALENT"
	classVacuous      = "VACUOUS"
	classTrueSurvivor = "TRUE_SURVIVOR"
	classInvalid      = "INVALID"
	// classMisclassified is a declared EQUIVALENT/VACUOUS mutant that WAS killed. The
	// declaration is then false and the run fails, exactly as a TRUE_SURVIVOR does.
	classMisclassified = "MISCLASSIFIED"
)

const (
	viewFile    = "internal/report/report_entryplan.go"
	tmplFile    = "internal/report/report.go"
	attachFile  = "internal/scanner/entryplan_attach.go"
	allowFile   = "internal/scanner/entryplan_symbol_guard_test.go"
	observeFile = "internal/entryplanbacktest/study/observe.go"
	renderFile  = "cmd/ep9-study/render.go"

	rpt   = "./internal/report"
	scn   = "./internal/scanner"
	main9 = "./cmd/ep9-study"
	cmdsc = "./cmd/scanner"
	study = "./internal/entryplanbacktest/study"
)

var mutations = []mutation{
	// ── M1..M5: the ⑲ report contract (EP-8, user-settled; EP-10 must not have moved it) ──
	{"M1", "the ⑲ visibility gate keys on the scanner Action instead of the canonical EntrySemantic", viewFile,
		"\tif p == nil || !epBuySideSemantic(p) {",
		"\tif p == nil || e.A.Action != scanner.ActionBuy {",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil, "", ""},
	{"M2", "an UNKNOWN / unresolved entry semantic renders a ⑲ section", viewFile,
		"\t\treturn ev.Status == entryplan.Available && entryplan.EntrySemantic(ev.Text).Resolved()",
		"\t\treturn ev.Status != entryplan.Unavailable",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil, "", ""},
	{"M2b", "any ENTRY_SEMANTIC row at all, resolved or not, counts as buy-side", viewFile,
		"\t\treturn ev.Status == entryplan.Available && entryplan.EntrySemantic(ev.Text).Resolved()",
		"\t\treturn true",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil, "", ""},
	{"M3", "the sell-side contradiction (S1) is hidden as if it were an exit action", viewFile,
		"\tif p == nil || !epBuySideSemantic(p) {",
		"\tif p == nil || !epBuySideSemantic(p) || p.Status == entryplan.StatusNoValidEntry {",
		rpt, "TestEntryPlanSellSideContradictionStillRenders", nil, "", ""},
	{"M4", "a sell-side contradiction leaks an executable price (Target1 recovered from the trace)", viewFile,
		"\t\tTarget1:       epTarget(p.Target1),",
		"\t\tTarget1: func() string {\n\t\t\tif p.Target1 == nil && p.EntryTrace != nil && p.EntryTrace.Targets.Target1 != nil {\n\t\t\t\treturn epPrice(*p.EntryTrace.Targets.Target1)\n\t\t\t}\n\t\t\treturn epTarget(p.Target1)\n\t\t}(),",
		rpt, "TestEntryPlanSectionSellSideRendersNoRecoveredPrice", nil, "", ""},
	{"M5", "show_entry_plan=false still renders the ⑲ section", tmplFile,
		"{{- if $.GV.ShowEntryPlan }}{{- with entryPlanView $e }}",
		"{{- if true }}{{- with entryPlanView $e }}",
		rpt, "TestEntryPlanSectionAbsentWhenShowFalse", nil, "", ""},

	// ── M6: legacy isolation. EntryPlan must move no decision and no rank. ──
	{"M6", "attaching the plan also moves RocketScore and WatchAction", attachFile,
		"\t\tp := entryplan.ComputePlan(snap)\n\t\tentries[i].EntryPlan = &p\n",
		"\t\tp := entryplan.ComputePlan(snap)\n\t\tentries[i].EntryPlan = &p\n\t\tentries[i].RocketScore++\n",
		cmdsc, "TestEntryPlanIsTheOnlyDifferenceBetweenOffAndOn", nil, "", ""},
	{"M6b", "attaching the plan reorders the watchlist (the EntryPlan pass becomes a comparator term)", attachFile,
		"\tfor i := range entries {\n\t\tsnap := entryPlanSnapshotOf(&entries[i], candlesByCode[entries[i].A.Symbol], market)",
		"\tdefer func() {\n\t\tsort.SliceStable(entries, func(a, b int) bool {\n\t\t\treturn entries[a].EntryPlan.Symbol \u003e entries[b].EntryPlan.Symbol\n\t\t})\n\t}()\n\tfor i := range entries {\n\t\tsnap := entryPlanSnapshotOf(&entries[i], candlesByCode[entries[i].A.Symbol], market)",
		cmdsc, "TestEntryPlanDoesNotChangeWatchlistOrder",
		[][2]string{{"import (\n", "import (\n\t\"sort\"\n"}}, "", ""},

	// ── M7, M8: the exact .EntryPlan reader allowlist ──
	{"M7", "an unapproved .EntryPlan reader is added to production code", tmplFile,
		"func chipSummary(e scanner.WatchlistEntry) []string {",
		"func chipSummary(e scanner.WatchlistEntry) []string {\n\t_ = e.EntryPlan",
		scn, "TestOnlyAttachEntryPlanTouchesTheEntryPlanFieldRepoWide", nil, "", ""},
	{"M8", "a stale allowlist entry names a reader that does not exist", allowFile,
		"var entryPlanFieldFunctions = map[string]string{",
		"var entryPlanFieldFunctions = map[string]string{\n\t\"internal/scanner/does_not_exist.go#ghostReader\": \"a reader that is not there\",",
		scn, "TestOnlyAttachEntryPlanTouchesTheEntryPlanFieldRepoWide", nil, "", ""},

	// ── M9, M10: the EP-9 §18 executable population ──
	{"M9", "INSUFFICIENT_DATA enters the EP-9 executable headline population", observeFile,
		"\tentryplan.StatusTooExtended:  true,\n}",
		"\tentryplan.StatusTooExtended:  true,\n\tentryplan.StatusInsufficientData: true,\n}",
		study, "TestTheExecutablePopulationIsTheFourEntryStatuses", nil, "", ""},
	{"M10", "NO_VALID_ENTRY enters the EP-9 executable headline population", observeFile,
		"\tentryplan.StatusTooExtended:  true,\n}",
		"\tentryplan.StatusTooExtended:  true,\n\tentryplan.StatusNoValidEntry: true,\n}",
		study, "TestTheExecutablePopulationIsTheFourEntryStatuses", nil, "", ""},

	// ── M11, M12: the standalone artifact's provenance ──
	{"M11", "the standalone metrics CSV loses its named provenance keys", renderFile,
		"\tout = append(out,\n\t\tcsvCommentPrefix+\"methodology_version: \"+nonEmpty(r.Meta.MethodologyVersion),",
		"\tout = append(out[:len(out):len(out)],\n\t\tcsvCommentPrefix+\"(provenance removed)\",\n\t\tcsvCommentPrefix+\"x: \"+nonEmpty(r.Meta.MethodologyVersion),",
		main9, "TestTheMetricsCSVCarriesItsProvenanceAsNamedKeys", nil, "", ""},
	{"M12", "a REPLAYED_PIT metrics table labels itself production historical performance", renderFile,
		"\t\tcsvCommentPrefix+\"production_historical_execution: NOT_EVALUABLE\",",
		"\t\tcsvCommentPrefix+\"production_historical_execution: EVALUATED (production historical performance)\",",
		main9, "TestTheMetricsCSVCarriesItsProvenanceAsNamedKeys", nil, "", ""},

	// ── M13: legacy ④ must not start printing EntryPlan prices ──
	{"M13", "legacy ④ prints the ⑲ entry zone instead of its own", tmplFile,
		"            <div>進場區：{{ $e.EntryZone }}</div>",
		"            <div>進場區：{{ with entryPlanView $e }}{{ .Zone }}{{ else }}{{ $e.EntryZone }}{{ end }}</div>",
		rpt, "TestLegacyPriceBlockIsLabelledLegacyOnlyBesideARenderedEntryPlan", nil, "", ""},

	// ── M14: ⑲ must CONSUME the final plan, never recompute it ──
	{"M14", "⑲ recomputes Target2 as zone high + 3.5R instead of reading the published plan", viewFile,
		"\t\tTarget2:       epTarget(p.Target2),",
		"\t\tTarget2: func() string {\n\t\t\tif p.IdealEntry != nil && p.RiskReward != nil {\n\t\t\t\treturn epPrice(p.IdealEntry.High + 3.5*p.RiskReward.RiskPerShare)\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionValuationCappedTarget2ThroughBridge", nil, "", ""},
	{"M14b", "⑲ calls a plan-computing entryplan function from report code", viewFile,
		"\tcur, curOK := epPlanCurrentPrice(p)\n",
		"\tcur, curOK := epPlanCurrentPrice(p)\n\t_ = entryplan.DecideStatus(entryplan.DecisionInput{})\n",
		rpt, "TestEntryPlanReportCodeComputesNothing", nil, "", ""},
	{"M14c", "⑲ recovers a withdrawn valuation target from the evidence row", viewFile,
		"\t\tTarget2:       epTarget(p.Target2),",
		"\t\tTarget2: func() string {\n\t\t\tif p.Target2 == nil {\n\t\t\t\tfor _, ev := range p.Evidence {\n\t\t\t\t\tif ev.Key == entryplan.EvidenceValuationBaseTarget && ev.Value != nil {\n\t\t\t\t\t\treturn epPrice(*ev.Value)\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t\treturn epTarget(p.Target2)\n\t\t}(),",
		rpt, "TestEntryPlanSectionSellSideRendersNoRecoveredPrice", nil, "", ""},

	// ── EP-10's own additions ──
	{"M15", "the ④ legacy label is emitted even where no ⑲ section renders", tmplFile,
		"            <h4>④ 價位計畫{{ if and $.GV.ShowEntryPlan (entryPlanView $e) }}（舊版掃描器價位指引）{{ end }}</h4>",
		"            <h4>④ 價位計畫（舊版掃描器價位指引）</h4>",
		rpt, "TestLegacyPriceBlockIsLabelledLegacyOnlyBesideARenderedEntryPlan", nil, "", ""},
	{"M15b", "labelling ④ suppresses one of its own legacy price fields", tmplFile,
		"            <div>進場區：{{ $e.EntryZone }}</div>",
		"            {{- if not $.GV.ShowEntryPlan }}<div>進場區：{{ $e.EntryZone }}</div>{{- end }}",
		rpt, "TestLegacyPriceBlockIsLabelledLegacyOnlyBesideARenderedEntryPlan", nil, "", ""},
	{"M16", "a reason code entryplan registers has no ⑲ presentation label", viewFile,
		"\tentryplan.ReasonStatusRequirementNotMet:        \"權限附帶的必要條件在這裡不成立\",\n",
		"",
		rpt, "TestEntryPlanReasonLabelsCoverTheRegistryExactly", nil, "", ""},
	{"M16b", "⑲ maps a reason entryplan does not register", viewFile,
		"var epReasonLabels = map[entryplan.Reason]string{\n",
		"var epReasonLabels = map[entryplan.Reason]string{\n\t\"EP10_GHOST_REASON\": \"不存在的理由\",\n",
		rpt, "TestEntryPlanReasonLabelsCoverTheRegistryExactly", nil, "", ""},
	{"M16c", "\"not evaluated\" is presented as a pass", viewFile,
		"\tentryplan.ReasonStatusRequirementNotEvaluated:   \"權限附帶的必要條件本系統尚無法評估（未評估，不等於通過）\",",
		"\tentryplan.ReasonStatusRequirementNotEvaluated:   \"權限附帶的必要條件視為符合條件\",",
		rpt, "TestEntryPlanReasonLabelsAreDistinctAndKeepTheirCode", nil, "", ""},
	{"M16d", "a mapped reason drops its canonical code", viewFile,
		"\t\t\tout = append(out, label+\"（\"+string(r)+\"）\")",
		"\t\t\tout = append(out, label)",
		rpt, "TestEntryPlanReasonLabelsAreDistinctAndKeepTheirCode", nil, "", ""},
	{"M17", "a zone basis is printed for a zone that was never published", viewFile,
		"\tif epZone(z) == epUnavailable {\n\t\treturn \"\"\n\t}",
		"\tif z == nil {\n\t\treturn \"\"\n\t}",
		rpt, "TestEntryPlanVisualAdversarialFixtureRendersSafely", nil, "", ""},
	{"M17b", "the basis test is NARROWER than the zone test: a half-zone whose High is not a price still prints its basis", viewFile,
		"\tif epZone(z) == epUnavailable {\n\t\treturn \"\"\n\t}",
		"\tif z == nil || !epIsPrice(z.Low) {\n\t\treturn \"\"\n\t}",
		rpt, "TestEntryPlanZoneBasisIsWithheldExactlyWhenTheZoneIs", nil, "", ""},
	{"M17c", "the mirror: a half-zone whose Low is not a price still prints its basis", viewFile,
		"\tif epZone(z) == epUnavailable {\n\t\treturn \"\"\n\t}",
		"\tif z == nil || !epIsPrice(z.High) {\n\t\treturn \"\"\n\t}",
		rpt, "TestEntryPlanZoneBasisIsWithheldExactlyWhenTheZoneIs", nil, "", ""},
	{"M17d", "⑲ DERIVES a basis from the plan's own PriceBasis when the zone published none", viewFile,
		"\t\tZoneBasis:     epZoneBasis(p.IdealEntry),",
		"\t\tZoneBasis: func() string {\n\t\t\tif b := epZoneBasis(p.IdealEntry); b != \"\" {\n\t\t\t\treturn b\n\t\t\t}\n\t\t\tif p.IdealEntry != nil {\n\t\t\t\treturn strings.Join(p.IdealEntry.Basis, \"・\")\n\t\t\t}\n\t\t\treturn \"\"\n\t\t}(),",
		rpt, "TestEntryPlanZoneBasisIsWithheldExactlyWhenTheZoneIs", nil, "", ""},
	{"M17e", "the TEMPLATE fabricates a basis chip when the view published none", tmplFile,
		"<div><span>理想進場區</span> {{ .Zone }}{{ with .ZoneBasis }} <span class=\"ep-code\">{{ . }}</span>{{ end }}</div>",
		"<div><span>理想進場區</span> {{ .Zone }}{{ with .ZoneBasis }} <span class=\"ep-code\">{{ . }}</span>{{ else }} <span class=\"ep-code\">MA20・RAW</span>{{ end }}</div>",
		rpt, "TestEntryPlanZoneBasisIsWithheldExactlyWhenTheZoneIs", nil, "", ""},
	{"M18", "a missing level renders as a fabricated 0.00 in the adversarial fixture", viewFile,
		"func epPricePtr(v *float64) string {\n\tif v == nil {\n\t\treturn epUnavailable\n\t}",
		"func epPricePtr(v *float64) string {\n\tif v == nil {\n\t\treturn \"0.00\"\n\t}",
		rpt, "TestEntryPlanVisualAdversarialFixtureRendersSafely", nil, "", ""},
	{"M18b", "a non-finite value reaches the ⑲ section", viewFile,
		"func epIsPrice(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }",
		"func epIsPrice(v float64) bool { return v > 0 || math.IsNaN(v) || math.IsInf(v, 0) }",
		rpt, "TestEntryPlanVisualFixturesHoldTheirContract", nil, "", ""},

	// ── DECLARED NON-KILLS. They are run exactly like every other mutant; the only
	// difference is that the runner EXPECTS them to survive and fails if one is killed
	// (MISCLASSIFIED). No production semantics were changed to produce either row. ──
	{"M2e", "drop ONLY the Resolved() half of the ⑲ buy-side gate", viewFile,
		"\t\treturn ev.Status == entryplan.Available && entryplan.EntrySemantic(ev.Text).Resolved()",
		"\t\treturn ev.Status == entryplan.Available",
		rpt, "TestEntryPlanSectionRendersOnlyBuySideSemantics", nil,
		classEquivalent,
		"On every plan this repo can produce, `row.Status == AVAILABLE` ALREADY IMPLIES " +
			"`Resolved(row.Text)`, so the dropped half decides nothing. Premises: " +
			"(P1) ComputePlan is the only producer of the ENTRY_SEMANTIC evidence row " +
			"(plan.go:188-207); (P2) it sets that row to AVAILABLE only inside the " +
			"`in.EntrySemantic.Usable()` branch and fills Text from the same value there " +
			"(plan.go:190-193); (P3) `EntrySemanticEvidence.Usable()` requires " +
			"`Status.OK() && Semantic.Valid() && Semantic.Resolved()` (input.go:226-228); " +
			"(P4) every other arm publishes INSUFFICIENT_DATA or UNAVAILABLE (plan.go:194-205). " +
			"P3 is the premise a future change could break, so it is MACHINE-PINNED by " +
			"`TestTheEntrySemanticRowIsAvailableOnlyWhenResolved` " +
			"(internal/report/report_entryplan_visual_test.go), which drives ComputePlan over the " +
			"whole cross product of declared EntrySemantic and Availability values and asserts the " +
			"implication on the PUBLISHED row. The Resolved() half stays in the source because it " +
			"is the gate's own statement of what it means; M2 and M2b are the reachable mutations " +
			"of the same line and both are KILLED."},
	{"M6v", "reorder the watchlist by PLAN PRESENCE at the end of AttachEntryPlan", attachFile,
		"\tfor i := range entries {\n\t\tsnap := entryPlanSnapshotOf(&entries[i], candlesByCode[entries[i].A.Symbol], market)",
		"\tdefer func() {\n\t\tsort.SliceStable(entries, func(a, b int) bool {\n\t\t\treturn entries[a].EntryPlan != nil \u0026\u0026 entries[b].EntryPlan == nil\n\t\t})\n\t}()\n\tfor i := range entries {\n\t\tsnap := entryPlanSnapshotOf(&entries[i], candlesByCode[entries[i].A.Symbol], market)",
		cmdsc, "TestEntryPlanDoesNotChangeWatchlistOrder",
		[][2]string{{"import (\n", "import (\n\t\"sort\"\n"}},
		classVacuous,
		"The sort key is constant over the population it sorts: AttachEntryPlan assigns " +
			"`entries[i].EntryPlan = &p` for EVERY entry it processes (entryplan_attach.go), so " +
			"`EntryPlan != nil` is true for all of them and the comparator returns false for every " +
			"pair. The mutant is an observable no-op and could never have been killed by any test. " +
			"It is kept in the audit rather than deleted because it is the FIRST reordering mutant " +
			"EP-10 wrote, and it SURVIVED — recording it is what stops it being re-proposed as " +
			"evidence that rank is unguarded. The reachable version is M6b (sort by the plan's own " +
			"Symbol), which is KILLED by the same test."},
}

// watched are hashed before the first mutant and re-verified at the end, in addition to every
// mutated file.
var watched = []string{
	viewFile,
	tmplFile,
	attachFile,
	allowFile,
	observeFile,
	renderFile,
	"internal/report/report_entryplan_test.go",
	"internal/report/report_entryplan_guard_test.go",
	"internal/report/report_entryplan_visual_test.go",
	"cmd/ep9-study/render_test.go",
	"cmd/ep9-study/followups.go",
	"cmd/scanner/main.go",
	"cmd/scanner/entryplan_pipeline_test.go",
	"scripts/ep10_convergence/main.go",
	"internal/entryplan/reasons.go",
	"internal/entryplan/types.go",
}

const auditDoc = "docs/EP10_MUTATION_AUDIT.md"

var priorAudits = []string{
	"docs/EP6G_MUTATION_AUDIT.md",
	"docs/EP7_MUTATION_AUDIT.md",
	"docs/EP8_MUTATION_AUDIT.md",
	"docs/EP9_MUTATION_AUDIT.md",
}

// notes are fixed commentary printed under the results table. They explain HOW a mutant is
// killed; the result itself is always the runner's.
var notes = []string{
	"**M1 is the gate the EP-8 review settled**: the ⑲ section is keyed on the plan's own " +
		"canonical EntrySemantic, never on the scanner's Action. The mutant renders for every " +
		"BUY-Action entry, including the PREPARE_ENTRY / WATCH_CLOSELY / WAIT ones that name no " +
		"entry shape.",
	"**M3 and M4 are opposite errors on the same case.** M3 hides the EP-6D/6G sell-side " +
		"contradiction, which must stay VISIBLE as NO_VALID_ENTRY; M4 leaves it visible and lets " +
		"one executable price through. Both must fail.",
	"**M6 and M6b are the legacy-isolation pair.** M6 moves a decision (RocketScore, " +
		"WatchAction), M6b moves the rank. Both are killed in cmd/scanner by the OFF-vs-ON " +
		"pipeline comparison, not in the report package.",
	"**M7 and M8 are the two directions of the .EntryPlan allowlist.** M7 adds a reader the " +
		"allowlist does not name; M8 names a reader that does not exist. A prefix or package-wide " +
		"exemption would survive one of them.",
	"**M9 and M10 restate EP-9's §18 population in EP-10's own audit.** INSUFFICIENT_DATA and " +
		"NO_VALID_ENTRY are NOT executable, and the plans excluded by them are " +
		"EVALUATOR-BLOCKED ENTRY CANDIDATES rather than would-be BUY_NOW.",
	"**M12 is mechanically enforceable**, so it is a mutant rather than a statement: the CSV " +
		"provenance test forbids the strings 'production historical performance' and " +
		"'validated strategy' on the artifact's own face.",
	"**M13, M15 and M15b are the convergence boundary.** ④ must not start printing ⑲'s prices " +
		"(M13), must not be relabelled where no ⑲ renders (M15), and must lose none of its own " +
		"fields to the labelling (M15b).",
	"**M14, M14b and M14c are the 'consume, never recompute' rule**: a recomputed target, a " +
		"plan-computing call from report code, and a price recovered from evidence the plan " +
		"deliberately withheld.",
	"**M16..M16d police the completed reason map**: a registered code with no label, a label " +
		"for an unregistered code, 'not evaluated' presented as a pass, and a label that drops " +
		"its canonical code.",
	"**M17 and M18/M18b are the browser findings turned into tests**: a basis printed for an " +
		"absent zone, a fabricated 0.00, and a non-finite value on screen.",
	"**M17b and M17c pin the basis test as EXACTLY the zone test, not merely a correct-looking " +
		"one.** Both mutants are NARROWER predicates that still suppress the basis for a nil zone " +
		"— M17 would not catch either — and both print a basis beside a `—` zone for a HALF-ZONE " +
		"with one unusable bound. They are killed by the half-zone fixtures, not by the nil one.",
	"**No mutant targets a strategy parameter.** The 0.5-ATR zone half-width, MaxChaseATR, the " +
		"2R / 3.5R targets, BaseQualityScore >= 60, the regime permission table and the status " +
		"precedence are inputs to EP-10 and are not touched by this audit; EP-10 tunes nothing " +
		"and RuleVersion stays EP6G-v1.",
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
			r.result, r.message = classInvalid, "mutant does not build"
			fmt.Fprintf(os.Stderr, "%s build output:\n%s\n", m.id, buildOut)
		case runErr == nil && m.class != "":
			// Declared EQUIVALENT / VACUOUS, and it behaved as declared.
			r.result, r.message = m.class, "survived as declared"
		case runErr == nil:
			r.result, r.message = classTrueSurvivor, "intended test passed on the mutant"
		case !strings.Contains(out, "--- FAIL: "+m.test+" "):
			r.result, r.message = classInvalid, "no assertion failure in the intended test"
			fmt.Fprintf(os.Stderr, "%s output:\n%s\n", m.id, out)
		case m.class != "":
			// Declared as a non-kill and yet killed: the DECLARATION is false.
			r.result, r.test, r.message = classMisclassified, m.test, firstMessage(out, root)
		default:
			killed++
			r.result, r.test, r.message = classKilled, m.test, firstMessage(out, root)
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
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.result]++
	}
	fmt.Printf("summary: %d killed, %d equivalent, %d vacuous, %d true survivors, %d invalid, "+
		"%d misclassified of %d; final hashes match baseline\n",
		killed, counts[classEquivalent], counts[classVacuous], counts[classTrueSurvivor],
		counts[classInvalid], counts[classMisclassified], len(mutations))
	// The run FAILS on a true survivor, an invalid mutant or a false declaration. It does NOT
	// fail on a declared EQUIVALENT or VACUOUS row: those are outcomes, not defects, and the
	// alternative — changing production semantics to reach "0 survived" — is forbidden.
	exit := 0
	if counts[classTrueSurvivor] > 0 || counts[classInvalid] > 0 || counts[classMisclassified] > 0 {
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

	w("# EP-10 mutation audit\n\n")
	w("Generated by `go run ./scripts/ep10_mutation`. Do not edit this file by hand; re-run the runner.\n\n")
	w("Scope: EP-10 (final integration, ⑲/④ convergence presentation, legacy isolation, the\n")
	w("`.EntryPlan` reader allowlist and the standalone EP-9 artifact's provenance). EP-10 changes\n")
	w("no production decision semantics and tunes no parameter, so no mutant here targets a\n")
	w("strategy threshold. This file records one audit run.\n\n")
	w("## Run\n\n")
	w("- Started %s, finished %s, exit status %d.\n", started.Format(stamp), finished.Format(stamp), exit)
	w("- Worktree: uncommitted tree on branch `%s`.\n", strings.TrimSpace(string(branch)))
	w("- Command: `go run ./scripts/ep10_mutation` (child `go` commands use `GOCACHE=$TMPDIR/ep10-mutation-gocache`).\n\n")

	w("## What the runner checks (scripts/ep10_mutation/main.go)\n\n")
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
	w("The run's exit status is non-zero if any mutant is a TRUE_SURVIVOR, is INVALID, or was\n")
	w("declared EQUIVALENT/VACUOUS and then killed (MISCLASSIFIED). A declared EQUIVALENT or\n")
	w("VACUOUS row does NOT fail the run: changing production semantics to reach a zero-survivor\n")
	w("table is forbidden, so the honest outcome is classified instead of engineered away.\n\n")
	w("After the last mutant it re-checks every watched file against its baseline. A deferred restore\n")
	w("rewrites every watched file on a panic; it does not run on `os.Exit`, which is only called before\n")
	w("the first mutation, after the current file has been written back, or after this file is written.\n\n")

	w("## Classification\n\n")
	w("Every mutant lands in exactly ONE of four buckets. `SURVIVED` is not a bucket: a mutant that\n")
	w("is not killed is either EQUIVALENT (unreachable branch, premises stated and machine-pinned),\n")
	w("VACUOUS (an observable no-op on the population it touches — a defect in the MUTANT), or a\n")
	w("TRUE_SURVIVOR (a gap in the tests). **TRUE_SURVIVOR and INVALID must both be zero**, and no\n")
	w("production semantics were changed to move any row between buckets.\n\n")
	w("| Bucket | Meaning |\n| --- | --- |\n")
	w("| `KILLED` | the intended test reported an assertion failure on the mutant |\n")
	w("| `EQUIVALENT` | declared in advance; the mutated branch is unreachable, the premises are listed below and at least one is pinned by a named test |\n")
	w("| `VACUOUS` | declared in advance; the mutation is a no-op on the population it touches, so no test could kill it |\n")
	w("| `TRUE_SURVIVOR` | not killed and not declared — a real gap |\n\n")

	w("## Results\n\n")
	w("| Mutant | File | Mutation | Bucket | Red test | First assertion message |\n")
	w("| --- | --- | --- | --- | --- | --- |\n")
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.result]++
		test := ""
		if r.test != "" {
			test = "`" + r.test + "`"
		}
		w("| %s | %s | %s | %s | %s | `%s` |\n", r.m.id, r.m.file,
			cell(r.m.description), r.result, test, strings.ReplaceAll(r.message, "`", "'"))
	}
	w("\nSummary of %d mutants: **%d KILLED, %d EQUIVALENT, %d VACUOUS, %d TRUE_SURVIVOR, "+
		"%d INVALID, %d MISCLASSIFIED**.\n\n", len(rows), counts[classKilled],
		counts[classEquivalent], counts[classVacuous], counts[classTrueSurvivor],
		counts[classInvalid], counts[classMisclassified])

	declared := 0
	for _, r := range rows {
		if r.m.class == "" {
			continue
		}
		if declared == 0 {
			w("### Declared non-kills, with their premises\n\n")
		}
		declared++
		w("**%s — `%s`** (%s, `%s`)\n\n%s\n\n", r.m.id, r.result, r.m.file, r.m.test, r.m.why)
	}
	if declared == 0 {
		w("### Declared non-kills\n\nNone.\n\n")
	}

	w("### Per-mutant hashes\n\n```text\n")
	for _, r := range rows {
		w("%-5s before=%s mutated=%s restored=%s  %s\n", r.m.id, r.before, r.mutated, r.restored, r.m.file)
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
	c.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "ep10-mutation-gocache"))
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
