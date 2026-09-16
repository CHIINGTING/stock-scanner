package main

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6B §6-§7: the EXACT shadow-difference contract, as a structural diff.
//
// The question this file answers is "does switching enable_entry_plan on change ANYTHING
// about the scan other than the EntryPlan field", and it answers it by walking both results
// field by field rather than by comparing a hand-written list of fields.
//
// WHY NOT A FIELD LIST. `if off.Score != on.Score { ... }` is a guard that rots: it covers
// exactly the fields somebody thought of on the day it was written, and every field added to
// WatchlistEntry afterwards is a blind spot that no test reports. WatchlistEntry already
// carries fourteen shadow pointers and a StockAnalysis; the next one would be silently
// unchecked. The recursive walk below has the opposite property — a new field is compared the
// moment it exists, and the way to EXCLUDE something is to name it in the allowlist, in this
// file, where a reviewer sees it.
//
// WHY NOT JSON (§19). json.Marshal(off) vs json.Marshal(on) with `entry_plan` deleted would
// be a weaker instrument pretending to be a stronger one: `omitempty` hides a zero that the
// struct distinguishes, unexported state never appears at all, the marshaller normalises
// floats, and map key ordering is imposed by the encoder rather than by the producer. It can
// be a secondary assertion; it cannot be the correctness source.
// ──────────────────────────────────────────────────────────────────────────────

// shadowDifference is one concrete disagreement between the OFF result and the ON result.
type shadowDifference struct {
	Path string // NORMALISED path, e.g. "[i].A.Score" — see normalizeShadowPath
	Raw  string // the same path with real indices, e.g. "[3].A.Score", for locating it
	Off  string
	On   string
	Why  string
}

func (d shadowDifference) String() string {
	return fmt.Sprintf("%s (at %s): OFF=%s  ON=%s  [%s]", d.Path, d.Raw, d.Off, d.On, d.Why)
}

// entryPlanAllowedDifferencePaths is the ENTIRE set of paths at which an OFF result and an ON
// result are permitted to disagree. It is a literal set matched by EXACT EQUALITY on the
// normalised path — deliberately not strings.Contains(path, "EntryPlan"), not a "*Plan*"
// glob, and not "ignore every type from the entryplan package".
//
// Those three shortcuts all say "anything that looks entry-plan-ish may differ", which is a
// different and much weaker claim than the one this work item makes. Under a Contains rule a
// real contamination at, say, [i].EntryPlan.Status differing between two runs of the SAME
// config would pass, and so would a future WatchlistEntry field called EntryPlanScore that
// the scorer reads. The exact set says: switching the feature on may cause the EntryPlan
// FIELD to appear, and may cause nothing else whatsoever.
//
// Adding an entry here is an architecture change and must be argued for in the same commit.
var entryPlanAllowedDifferencePaths = map[string]struct{}{
	"[i].EntryPlan": {},
}

// shadowIndexPattern matches a slice/array index so paths can be compared against the literal
// allowlist above. `[3].EntryPlan` and `[0].EntryPlan` are the same rule, and the allowlist
// should not have to enumerate positions.
//
// It matches DIGITS ONLY, inside square brackets. Map keys are deliberately rendered with
// braces — {"2330"} — so that no map key can ever be collapsed into `[i]` and quietly
// widened into a rule about a whole collection.
var shadowIndexPattern = regexp.MustCompile(`\[[0-9]+\]`)

func normalizeShadowPath(raw string) string {
	return shadowIndexPattern.ReplaceAllString(raw, "[i]")
}

// shadowWalkMaxDepth bounds the recursion. Nothing in a WatchlistEntry is remotely this deep;
// the bound exists so a future self-referential type produces a loud failure rather than a
// stack overflow that looks like a crashed test.
const shadowWalkMaxDepth = 64

// shadowWalkMinimumLeaves is the ANTI-VACUITY floor (§17). A comparator that reached no
// scalars would report no differences and would pass every test in this file while asserting
// nothing at all. The real walk over two full production results compares tens of thousands
// of leaves, so this floor only ever fires when the walk has stopped descending.
const shadowWalkMinimumLeaves = 500

type shadowWalker struct {
	diffs  []shadowDifference
	leaves int      // scalar comparisons actually performed
	errs   []string // kinds the walker cannot compare — a loud failure, never a skip
}

func (w *shadowWalker) record(raw, off, on, why string) {
	w.diffs = append(w.diffs, shadowDifference{
		Path: normalizeShadowPath(raw), Raw: raw, Off: off, On: on, Why: why,
	})
}

// walk compares two values of the same static type, recording every disagreement.
//
// It NEVER calls reflect.Value.Interface(), which panics on a value obtained from an
// unexported field. Every comparison goes through a kind-specific accessor (Int/Uint/Float/
// String/Bool/Len/Pointer), all of which are legal on read-only values — that is what lets
// this walk descend into time.Time and into any other struct with unexported state instead of
// stopping at it and calling the halt "equal".
func (w *shadowWalker) walk(raw string, off, on reflect.Value, depth int) {
	if depth > shadowWalkMaxDepth {
		w.errs = append(w.errs, fmt.Sprintf("%s: deeper than %d levels — the comparator "+
			"stopped descending and anything below this point is UNCHECKED", raw, shadowWalkMaxDepth))
		return
	}
	if off.Type() != on.Type() {
		w.record(raw, off.Type().String(), on.Type().String(), "different types")
		return
	}

	switch off.Kind() {
	case reflect.Pointer:
		if off.IsNil() || on.IsNil() {
			w.leaves++
			if off.IsNil() != on.IsNil() {
				w.record(raw, presence(off), presence(on),
					"the field is present on one side and absent on the other")
			}
			return
		}
		// *time.Location is compared by identity rather than walked. The zone database it
		// points at is large, immutable and shared; two results produced in one process
		// either share the pointer or genuinely came from different loads, and walking a
		// zone table would cost far more than it can ever catch.
		if off.Type().Elem() == reflect.TypeOf(time.Location{}) {
			w.leaves++
			if off.Pointer() != on.Pointer() {
				w.record(raw, fmt.Sprintf("loc@%x", off.Pointer()),
					fmt.Sprintf("loc@%x", on.Pointer()), "different *time.Location")
			}
			return
		}
		w.walk(raw, off.Elem(), on.Elem(), depth+1)

	case reflect.Interface:
		if off.IsNil() || on.IsNil() {
			w.leaves++
			if off.IsNil() != on.IsNil() {
				w.record(raw, presence(off), presence(on),
					"the interface holds a value on one side only")
			}
			return
		}
		w.walk(raw, off.Elem(), on.Elem(), depth+1)

	case reflect.Struct:
		t := off.Type()
		for i := 0; i < t.NumField(); i++ {
			w.walk(raw+"."+t.Field(i).Name, off.Field(i), on.Field(i), depth+1)
		}

	case reflect.Slice:
		if off.IsNil() != on.IsNil() {
			w.record(raw, nilness(off), nilness(on),
				"one side is a nil slice and the other is an allocated one — different "+
					"states, and JSON renders them differently too")
		}
		w.walkIndexed(raw, off, on, depth)

	case reflect.Array:
		w.walkIndexed(raw, off, on, depth)

	case reflect.Map:
		if off.IsNil() != on.IsNil() {
			w.record(raw, nilness(off), nilness(on), "one side is a nil map")
		}
		w.walkMap(raw, off, on, depth)

	case reflect.Bool:
		w.leaves++
		if off.Bool() != on.Bool() {
			w.record(raw, fmt.Sprint(off.Bool()), fmt.Sprint(on.Bool()), "bool differs")
		}
	case reflect.String:
		w.leaves++
		if off.String() != on.String() {
			w.record(raw, fmt.Sprintf("%q", off.String()), fmt.Sprintf("%q", on.String()),
				"string differs")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.leaves++
		if off.Int() != on.Int() {
			w.record(raw, fmt.Sprint(off.Int()), fmt.Sprint(on.Int()), "integer differs")
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.leaves++
		if off.Uint() != on.Uint() {
			w.record(raw, fmt.Sprint(off.Uint()), fmt.Sprint(on.Uint()), "integer differs")
		}
	case reflect.Float32, reflect.Float64:
		w.leaves++
		// BIT comparison, not ==. It makes the check exact in both directions that == gets
		// wrong: NaN != NaN would report a difference between a result and itself, and
		// +0 == -0 would hide one. §15 asks for bit-identical determinism, so bits it is.
		if math.Float64bits(off.Float()) != math.Float64bits(on.Float()) {
			w.record(raw, fmt.Sprintf("%v", off.Float()), fmt.Sprintf("%v", on.Float()),
				"float differs (compared as bits)")
		}
	case reflect.Complex64, reflect.Complex128:
		w.leaves++
		if off.Complex() != on.Complex() {
			w.record(raw, fmt.Sprint(off.Complex()), fmt.Sprint(on.Complex()), "complex differs")
		}

	default:
		// Func, Chan, UnsafePointer. Not silently skipped: a skipped kind is an
		// unchecked region of the result, and this comparator's whole value is that it
		// has none.
		w.errs = append(w.errs, fmt.Sprintf("%s: cannot compare kind %s — extend the "+
			"comparator rather than letting it pass over the value", raw, off.Kind()))
	}
}

func (w *shadowWalker) walkIndexed(raw string, off, on reflect.Value, depth int) {
	if off.Len() != on.Len() {
		w.record(raw, fmt.Sprintf("len=%d", off.Len()), fmt.Sprintf("len=%d", on.Len()),
			"collection length differs — an element was added or dropped")
	}
	n := off.Len()
	if on.Len() < n {
		n = on.Len()
	}
	for i := 0; i < n; i++ {
		w.walk(fmt.Sprintf("%s[%d]", raw, i), off.Index(i), on.Index(i), depth+1)
	}
}

func (w *shadowWalker) walkMap(raw string, off, on reflect.Value, depth int) {
	offKeys := w.mapByKey(raw, off)
	onKeys := w.mapByKey(raw, on)
	seen := map[string]bool{}
	var keys []string
	for k := range offKeys {
		keys, seen[k] = append(keys, k), true
	}
	for k := range onKeys {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	// Sorted so the report is stable; map iteration order must never reach the output of a
	// comparator whose job includes proving determinism.
	sort.Strings(keys)
	for _, k := range keys {
		path := raw + "{" + k + "}"
		ov, oOK := offKeys[k]
		nv, nOK := onKeys[k]
		switch {
		case oOK && nOK:
			w.walk(path, ov, nv, depth+1)
		case oOK:
			w.record(path, "present", "absent", "map key exists only in the OFF result")
		default:
			w.record(path, "absent", "present", "map key exists only in the ON result")
		}
	}
}

func (w *shadowWalker) mapByKey(raw string, m reflect.Value) map[string]reflect.Value {
	out := map[string]reflect.Value{}
	if m.IsNil() {
		return out
	}
	it := m.MapRange()
	for it.Next() {
		k, ok := shadowMapKey(it.Key())
		if !ok {
			w.errs = append(w.errs, fmt.Sprintf("%s: map key kind %s cannot be rendered — "+
				"extend shadowMapKey rather than skipping the map", raw, it.Key().Kind()))
			continue
		}
		out[k] = it.Value()
	}
	return out
}

// shadowMapKey renders a map key for the path. Braces, not brackets, so a key can never be
// mistaken for a slice index by normalizeShadowPath.
func shadowMapKey(k reflect.Value) (string, bool) {
	switch k.Kind() {
	case reflect.String:
		return fmt.Sprintf("%q", k.String()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprint(k.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprint(k.Uint()), true
	case reflect.Bool:
		return fmt.Sprint(k.Bool()), true
	default:
		return "", false
	}
}

func presence(v reflect.Value) string {
	if v.IsNil() {
		return "nil"
	}
	return "present(" + v.Type().String() + ")"
}

func nilness(v reflect.Value) string {
	if v.IsNil() {
		return "nil"
	}
	return fmt.Sprintf("non-nil(len=%d)", v.Len())
}

// shadowDifferences walks two complete production results and returns every disagreement,
// plus the number of scalar comparisons it actually performed.
func shadowDifferences(off, on []scanner.WatchlistEntry) ([]shadowDifference, int, error) {
	w := &shadowWalker{}
	w.walk("", reflect.ValueOf(off), reflect.ValueOf(on), 0)
	if len(w.errs) > 0 {
		return w.diffs, w.leaves, fmt.Errorf("comparator could not compare part of the "+
			"result:\n  %s", strings.Join(w.errs, "\n  "))
	}
	return w.diffs, w.leaves, nil
}

// entryPlanShadowOnlyViolations returns the differences that are NOT the one allowed
// difference. An empty slice means "these two results differ by the EntryPlan field and by
// nothing else in the entire structure".
func entryPlanShadowOnlyViolations(off, on []scanner.WatchlistEntry) ([]shadowDifference, int, error) {
	diffs, leaves, err := shadowDifferences(off, on)
	if err != nil {
		return diffs, leaves, err
	}
	var violations []shadowDifference
	for _, d := range diffs {
		if _, ok := entryPlanAllowedDifferencePaths[d.Path]; ok {
			continue
		}
		violations = append(violations, d)
	}
	return violations, leaves, nil
}

// assertEntryPlanShadowOnlyDifference is the §6-§8 assertion: the ON result may differ from
// the OFF result ONLY by the presence of the EntryPlan field.
//
// It is the PRIMARY guard for the fields EP-6B has to protect, and it protects them by
// structure rather than by name, so nothing is protected only until someone adds a field.
//
// What that means ON THIS FIXTURE, stated so nobody reads more into a green run than it
// carries. Substantively compared, because pipelineConfig populates them: Decision, Score,
// RocketScore, WatchAction, ExplosionProb, the ranking fields, the sort keys, Reasons, the
// technical/fundamental/valuation reads, and the Shadow, HoldingHorizon, HorizonHint,
// TrendExt, Bias, Technical and Candlestick shadow signals.
//
// Compared but EMPTY on both sides, so the comparison is structurally present and empirically
// trivial here: AI, ETFFlow, News, Institution and Confluence are nil OFF and nil ON. That is
// not an oversight — pipelineConfig's own comment explains why those layers are off (they
// need the network, snapshot directories or a clock, and would make the fixture's determinism
// depend on the machine). The walk WOULD report them if they ever diverged; it simply has
// nothing to say about them until a fixture populates them.
func assertEntryPlanShadowOnlyDifference(t *testing.T, off, on []scanner.WatchlistEntry) {
	t.Helper()
	violations, leaves, err := entryPlanShadowOnlyViolations(off, on)
	if err != nil {
		t.Fatalf("shadow comparison is incomplete, so it proves nothing: %v", err)
	}
	if leaves < shadowWalkMinimumLeaves {
		t.Fatalf("the shadow comparator compared only %d scalars (floor %d) — it is not "+
			"reaching the data, so a clean result here means nothing",
			leaves, shadowWalkMinimumLeaves)
	}
	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range violations {
		b.WriteString("\n  " + v.String())
	}
	t.Fatalf("ENTRY PLAN IS NOT SHADOW-ONLY: turning enable_entry_plan on changed %d "+
		"thing(s) that are not the EntryPlan field.\n"+
		"Both results came from the SAME fixture candles through the SAME production "+
		"pipeline (buildWatchlist); the only difference in the inputs was the flag. Every "+
		"path below is therefore something the entry plan moved, directly or as a side "+
		"effect.%s\n"+
		"The only permitted difference is the exact path(s) %v.",
		len(violations), b.String(), allowedPathList())
}

// assertBitIdentical is the §15 determinism assertion: no allowlist at all, nothing may
// differ. Used to compare two runs of the SAME config.
func assertBitIdentical(t *testing.T, a, b []scanner.WatchlistEntry, what string) {
	t.Helper()
	diffs, leaves, err := shadowDifferences(a, b)
	if err != nil {
		t.Fatalf("%s: comparison incomplete, so it proves nothing: %v", what, err)
	}
	if leaves < shadowWalkMinimumLeaves {
		t.Fatalf("%s: compared only %d scalars (floor %d) — the walk is not reaching the data",
			what, leaves, shadowWalkMinimumLeaves)
	}
	if len(diffs) > 0 {
		var sb strings.Builder
		for _, d := range diffs {
			sb.WriteString("\n  " + d.String())
		}
		t.Fatalf("%s: two runs of the same fixture with the same config disagree in %d "+
			"place(s). The pipeline is reading a clock, a map iteration order, a random "+
			"source or some state outside its inputs.%s", what, len(diffs), sb.String())
	}
}

func allowedPathList() []string {
	var out []string
	for p := range entryPlanAllowedDifferencePaths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ──────────────────────────────────────────────────────────────────────────────
// ANTI-VACUITY (§17, §18). The tests below test THE COMPARATOR, not the scanner.
//
// A structural diff is the kind of instrument that can look thorough and compare nothing: a
// walk that stops at the first pointer, an allowlist that swallows everything, a comparator
// that sorts before comparing. Each test below is the mutation form of one of those failures
// — it plants a difference that MUST be reported, and fails if the comparator stays quiet.
// ──────────────────────────────────────────────────────────────────────────────

// §17 / mutation M8: a planted Score change must be reported, and the message must name the
// field. This is the test that proves the walk reaches actual scan data rather than passing
// because it never descended.
func TestComparatorCatchesAPlantedScoreChange(t *testing.T) {
	off := runProductionWatchlist(t, pipelineConfig(false))
	on := runProductionWatchlist(t, pipelineConfig(false))

	on[0].A.Score = off[0].A.Score + 1

	violations, leaves, err := entryPlanShadowOnlyViolations(off, on)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	if leaves < shadowWalkMinimumLeaves {
		t.Fatalf("walk compared only %d scalars — vacuous", leaves)
	}
	if len(violations) == 0 {
		t.Fatal("a planted Score difference was NOT reported. The comparator is vacuous: " +
			"it would also stay silent if the entry plan really did move a score, which is " +
			"the single thing EP-6B exists to detect")
	}
	var found bool
	for _, v := range violations {
		if strings.HasSuffix(v.Path, ".Score") {
			found = true
		}
	}
	if !found {
		t.Errorf("the planted Score difference was reported, but no message names a "+
			"*.Score path, so the operator cannot tell WHAT moved: %v", violations)
	}
}

// §18 / mutation M10, first half: swapping two entries must be reported. A comparator that
// sorted or keyed by symbol before comparing would pass this and would then be blind to the
// exact contamination §9 is about — the plan becoming a tie-break and reordering the page.
func TestComparatorCatchesSwappedEntries(t *testing.T) {
	off := runProductionWatchlist(t, pipelineConfig(false))
	on := runProductionWatchlist(t, pipelineConfig(false))
	if len(on) < 2 {
		t.Fatalf("fixture produced %d entries; a swap test needs at least 2", len(on))
	}
	on[0], on[1] = on[1], on[0]

	violations, _, err := entryPlanShadowOnlyViolations(off, on)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("two entries were swapped and the comparator reported nothing. It is " +
			"normalising order before comparing, which makes it structurally incapable of " +
			"seeing a ranking change")
	}
}

// §18 / mutation M10, second half: dropping an entry must be reported, by length.
func TestComparatorCatchesADroppedEntry(t *testing.T) {
	off := runProductionWatchlist(t, pipelineConfig(false))
	on := runProductionWatchlist(t, pipelineConfig(false))
	if len(on) < 2 {
		t.Fatalf("fixture produced %d entries; a drop test needs at least 2", len(on))
	}
	on = on[:len(on)-1]

	violations, _, err := entryPlanShadowOnlyViolations(off, on)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	var sawLength bool
	for _, v := range violations {
		if strings.Contains(v.Why, "length differs") {
			sawLength = true
		}
	}
	if !sawLength {
		t.Fatalf("an entry was dropped and no length difference was reported: %v", violations)
	}
}

// §7 / mutation M9: the allowlist is an EXACT path set, not a pattern.
//
// Both sides carry a plan here, and the plans disagree in a nested field. The path of that
// disagreement — [i].EntryPlan.Status — CONTAINS the string "EntryPlan" and would be waved
// through by strings.Contains, by a *Plan* glob, or by "ignore everything from the entryplan
// package". It must be reported: the permitted difference is the FIELD APPEARING, never a
// plan whose content moved.
func TestAllowlistIsExactRatherThanAPattern(t *testing.T) {
	a := runProductionWatchlist(t, pipelineConfig(true))
	b := runProductionWatchlist(t, pipelineConfig(true))
	if a[0].EntryPlan == nil || b[0].EntryPlan == nil {
		t.Fatal("fixture produced no plan with enable_entry_plan=true — this test cannot " +
			"say anything about nested plan paths")
	}
	mutated := *b[0].EntryPlan
	mutated.Symbol = mutated.Symbol + "-MUTATED"
	b[0].EntryPlan = &mutated

	violations, _, err := entryPlanShadowOnlyViolations(a, b)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("a difference INSIDE the plan ([i].EntryPlan.Symbol) was allowed through. " +
			"The allowlist has become a pattern over EntryPlan-like paths, which permits " +
			"far more than 'the field may appear' — including a non-deterministic plan")
	}
	for _, v := range violations {
		if v.Path == "[i].EntryPlan" {
			t.Errorf("the whole-field path was reported as a violation for two results that "+
				"both carry a plan: %v", v)
		}
	}
}

// The allowlist must stay narrow in the literal sense as well: one entry, naming the field
// and nothing else. A reviewer reading only this test should be able to see the entire
// permitted difference surface.
func TestAllowlistContainsOnlyTheEntryPlanField(t *testing.T) {
	want := []string{"[i].EntryPlan"}
	got := allowedPathList()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the permitted difference set is %v, expected exactly %v. Widening it is an "+
			"architecture change: it means the entry plan is allowed to alter something else "+
			"about the scan, and that has to be argued for, not added", got, want)
	}
}

// The normaliser must collapse INDICES and nothing else. If it ever collapsed map keys too,
// an allowlist entry would silently become a rule about every key in a map.
func TestPathNormalizationCollapsesIndicesOnly(t *testing.T) {
	cases := map[string]string{
		"[0].EntryPlan":         "[i].EntryPlan",
		"[12].A.Score":          "[i].A.Score",
		"[3].Reasons[7]":        "[i].Reasons[i]",
		`[1].Shadow.RS{"2330"}`: `[i].Shadow.RS{"2330"}`,
		"[2].Consol.Bucket":     "[i].Consol.Bucket",
	}
	for raw, want := range cases {
		if got := normalizeShadowPath(raw); got != want {
			t.Errorf("normalizeShadowPath(%q) = %q, want %q", raw, got, want)
		}
	}
}
