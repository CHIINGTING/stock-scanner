package entryplan_test

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── EP-3b's stable vocabularies ───────────────────────────────────────────────────────
//
// Seven new string vocabularies arrive with this work item, and every one of them is
// PERSISTED: on the evidence rows, on the plan's trace, in an archive, in an agent prompt. So
// each gets the treatment TestTheEntrySemanticSetIsExactlyPullbackBreakoutUnknown established
// for EntrySemantic — pinned from BOTH sides, with the production side read off the SOURCE
// rather than retyped into the test.
//
// The retyped version is the failure this pattern exists to prevent: a test that builds its
// own list and then asserts len() over it is asserting about its own literal, and a fifth
// constant added consistently everywhere else leaves the suite green.

// constDecl matches `Name Type = "VALUE"` at the start of a line.
func constDecl(typeName string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*(\w+)\s+` + typeName + `\s*=\s*"([A-Z0-9_]+)"`)
}

// registryCase is one vocabulary: where it is declared, and what the exported registry says.
type registryCase struct {
	typeName string
	file     string
	// members is the exported registry, rendered as strings.
	members []string
	// known is the registry's membership predicate, so Known* cannot drift from the list.
	known func(string) bool
	// wantCount is the size the work item decided on. A literal, so ADDING a value fails
	// here even when it is added consistently — which is the moment somebody has to say what
	// the new value means.
	wantCount int
}

func ep3Registries() []registryCase {
	return []registryCase{
		{
			typeName: "LevelSource", file: "levels.go", wantCount: 4,
			members: renderStrings(entryplan.AllLevelSources),
			known: func(s string) bool {
				return entryplan.KnownLevelSource(entryplan.LevelSource(s))
			},
		},
		{
			typeName: "LevelDisposition", file: "levels.go", wantCount: 7,
			members: renderStrings(entryplan.AllLevelDispositions),
			known: func(s string) bool {
				return entryplan.KnownLevelDisposition(entryplan.LevelDisposition(s))
			},
		},
		{
			typeName: "HalfWidthBinding", file: "zone.go", wantCount: 2,
			members: renderStrings(entryplan.AllHalfWidthBindings),
		},
		{
			typeName: "WidthRejection", file: "zone.go", wantCount: 4,
			members: renderStrings(entryplan.AllWidthRejections),
		},
		{
			typeName: "TickDirection", file: "trace.go", wantCount: 3,
			members: renderStrings(entryplan.AllTickDirections),
		},
		{
			typeName: "ClampOutcome", file: "trace.go", wantCount: 2,
			members: renderStrings(entryplan.AllClampOutcomes),
		},
		{
			// EP-5 added STATUS_WITHDRAWN for a verdict that contradicted its own numbers:
			// eight of the nine status invariants (EP-5's seven plus EP-6D's
			// BUY_NOW_THESIS_NOT_CONTRADICTED) have no price to withdraw, so the gate sends the
			// status back to INSUFFICIENT_DATA and stamps that — because a reader following a
			// PRICES_WITHDRAWN stamp would go looking for a number that is still there.
			// F1 (delivered and reviewed as part of EP-7; not EP-6G) adds the fourth outcome,
			// PRICES_AND_STATUS_WITHDRAWN, for a repair that withdrew both a price and the
			// verdict, so neither half is hidden behind the other's stamp.
			typeName: "InvariantCheckOutcome", file: "trace.go", wantCount: 4,
			members: renderStrings(entryplan.AllInvariantCheckOutcomes),
		},
	}
}

func renderStrings(v any) []string {
	rv := reflect.ValueOf(v)
	out := make([]string, 0, rv.Len())
	for n := 0; n < rv.Len(); n++ {
		out = append(out, rv.Index(n).String())
	}
	return out
}

func TestEveryEP3VocabularyIsPinnedFromBothSides(t *testing.T) {
	for _, c := range ep3Registries() {
		t.Run(c.typeName, func(t *testing.T) {
			src, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatalf("cannot read %s: %v", c.file, err)
			}
			matches := constDecl(c.typeName).FindAllStringSubmatch(string(src), -1)
			if len(matches) == 0 {
				t.Fatalf("no %s constants found in %s — the scan is not reading the "+
					"vocabulary and would pass for the wrong reason", c.typeName, c.file)
			}
			declared := map[string]string{}
			for _, m := range matches {
				declared[m[2]] = m[1]
			}

			// The registry, well-formed: unique, non-empty, stable SCREAMING_SNAKE.
			seen := map[string]bool{}
			for _, v := range c.members {
				if v == "" {
					t.Errorf("empty value in the %s registry", c.typeName)
					continue
				}
				if seen[v] {
					t.Errorf("%q listed twice", v)
				}
				seen[v] = true
				for _, ch := range v {
					if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
						t.Errorf("%q is not a stable SCREAMING_SNAKE code", v)
						break
					}
				}
				if c.known != nil && !c.known(v) {
					t.Errorf("%q is in the registry and the Known predicate rejects it — the "+
						"list and the predicate have stopped being one list", v)
				}
			}

			// forward: every constant the source declares is in the registry
			for value, goName := range declared {
				if !seen[value] {
					t.Errorf("%s declares %s (%q) and the registry does not list it — a value "+
						"a producer can emit and no consumer can enumerate", c.file, goName, value)
				}
			}
			// backward: no dead registry entry
			for _, v := range c.members {
				if _, ok := declared[v]; !ok {
					t.Errorf("the registry lists %q and %s declares no constant for it",
						v, c.file)
				}
			}
			if len(seen) != c.wantCount {
				t.Errorf("%s has %d members %v, want %d — if a value was added, say in the "+
					"commit what it means and update this number in the same change",
					c.typeName, len(seen), c.members, c.wantCount)
			}
			if c.known != nil && c.known("NOT_A_VALUE") {
				t.Errorf("the %s membership predicate accepts a value not in the registry",
					c.typeName)
			}
		})
	}
}

// The two vocabularies production ATTACHES TO EVERY PLAN must be exactly what production
// emits — the same bidirectional check AllReasons gets, over the same union of inputs.
func TestTheLevelVocabulariesAreExactlyWhatProductionEmits(t *testing.T) {
	sources := map[entryplan.LevelSource]string{}
	dispositions := map[entryplan.LevelDisposition]string{}

	for _, c := range allSnapshots() {
		tr := entryplan.ComputePlan(c.in).EntryTrace
		if tr == nil {
			continue
		}
		for _, cand := range tr.Zone.Candidates {
			if _, ok := sources[cand.Source]; !ok {
				sources[cand.Source] = c.name
			}
			if _, ok := dispositions[cand.Disposition]; !ok {
				dispositions[cand.Disposition] = c.name
			}
		}
	}

	// forward
	for src, where := range sources {
		if !entryplan.KnownLevelSource(src) {
			t.Errorf("production emits level source %q (first at %s) and it is not in the "+
				"registry", src, where)
		}
	}
	for d, where := range dispositions {
		if !entryplan.KnownLevelDisposition(d) {
			t.Errorf("production emits disposition %q (first at %s) and it is not in the "+
				"registry", d, where)
		}
	}
	// backward — the half that is usually skipped, and the one that catches a code no input
	// can reach.
	for _, src := range entryplan.AllLevelSources {
		if _, ok := sources[src]; !ok {
			t.Errorf("AllLevelSources lists %q and no input produces it", src)
		}
	}
	for _, d := range entryplan.AllLevelDispositions {
		if _, ok := dispositions[d]; !ok {
			t.Errorf("AllLevelDispositions lists %q and no input produces it — a dead "+
				"disposition is a branch a reader of an archived plan will never see", d)
		}
	}
	if len(sources) == 0 || len(dispositions) == 0 {
		t.Fatal("the sweep collected nothing — every assertion above is vacuous")
	}
	// The empty disposition is NOT a disposition: it is a row nobody filled in.
	if _, ok := dispositions[""]; ok {
		t.Errorf("a candidate was published with no disposition (first at %s)", dispositions[""])
	}
}

// ── step 1: collect ───────────────────────────────────────────────────────────────────

// The candidate list is ALWAYS the full width of the registry, whatever the semantic and
// whatever is missing. A variable-width list is how a reader loses the difference between
// "considered and rejected" and "this version of the rule never heard of it".
func TestTheCandidateListIsAlwaysTheWidthOfTheRegistry(t *testing.T) {
	for _, sem := range []entryplan.EntrySemantic{
		entryplan.EntrySemanticPullback,
		entryplan.EntrySemanticBreakout,
		entryplan.EntrySemanticUnknown,
		entryplan.EntrySemantic(""),
	} {
		for _, in := range []entryplan.Snapshot{zoneBase(), {}} {
			got := entryplan.CandidateLevelsFor(in, sem)
			if len(got) != len(entryplan.AllLevelSources) {
				t.Fatalf("semantic %q: %d candidates, want %d", sem, len(got),
					len(entryplan.AllLevelSources))
			}
			for n, src := range entryplan.AllLevelSources {
				if got[n].Source != src {
					t.Errorf("semantic %q: candidate %d is %q, want %q (registry order is "+
						"what makes the tie-break deterministic)", sem, n, got[n].Source, src)
				}
			}
		}
	}
}

// Membership is per SEMANTIC, and the sources that are not candidates say so rather than
// arriving blank.
func TestMembershipIsDecidedByTheEntrySemantic(t *testing.T) {
	want := map[entryplan.EntrySemantic]map[entryplan.LevelSource]bool{
		entryplan.EntrySemanticPullback: {
			entryplan.LevelMA20: true, entryplan.LevelMA60: true, entryplan.LevelBaseLow: true,
		},
		entryplan.EntrySemanticBreakout: {
			entryplan.LevelBreakoutHigh: true,
		},
		entryplan.EntrySemanticUnknown: {},
	}
	for sem, members := range want {
		got := entryplan.CandidateLevelsFor(zoneBase(), sem)
		for _, c := range got {
			isMember := c.Disposition != entryplan.DispositionWrongSemantic
			if isMember != members[c.Source] {
				t.Errorf("semantic %q: %q membership = %v, want %v", sem, c.Source,
					isMember, members[c.Source])
			}
			if !c.Source.SupportsSemantic(sem) && c.Disposition != entryplan.DispositionWrongSemantic {
				t.Errorf("semantic %q: %q is not a candidate and its disposition is %q",
					sem, c.Source, c.Disposition)
			}
		}
	}
}

// Collection copies, never aliases, and never publishes a value that is not a price.
func TestCollectionCopiesAndNeverPublishesANonPrice(t *testing.T) {
	level := 94.0
	in := zoneBase()
	in.MA20 = entryplan.PriceObservation{Status: entryplan.Available, Value: &level}

	got := entryplan.CandidateLevelsFor(in, entryplan.EntrySemanticPullback)
	var ma20 entryplan.CandidateLevel
	for _, c := range got {
		if c.Source == entryplan.LevelMA20 {
			ma20 = c
		}
	}
	if ma20.Value == nil || *ma20.Value != 94 {
		t.Fatalf("MA20 candidate = %+v", ma20)
	}
	if ma20.Value == &level {
		t.Error("the candidate ALIASES the caller's float — a caller writing through its own " +
			"pointer would change an already-returned plan")
	}
	level = -1
	if *ma20.Value != 94 {
		t.Errorf("the candidate value moved with the caller's float: %v", *ma20.Value)
	}

	// A labelled-available level that is not a price: the label is not trusted, the value is
	// dropped, and the row says UNAVAILABLE.
	for _, bad := range []float64{0, -5, math.NaN(), math.Inf(1)} {
		in := zoneBase()
		in.MA20 = obs(bad)
		for _, c := range entryplan.CandidateLevelsFor(in, entryplan.EntrySemanticPullback) {
			if c.Source != entryplan.LevelMA20 {
				continue
			}
			if c.Value != nil {
				t.Errorf("level %v was published as a candidate value", *c.Value)
			}
			if c.Status == entryplan.Available {
				t.Errorf("level %v is still labelled AVAILABLE", bad)
			}
		}
	}
}

// ── step 2: screen ────────────────────────────────────────────────────────────────────

// Each condition, one at a time, with the disposition it must produce. This is the step where
// "PriceBasis compatibility" and "strictly below the market" actually live.
func TestScreeningNamesTheConditionThatFailed(t *testing.T) {
	const quote = 100.0
	cases := []struct {
		name string
		cand entryplan.CandidateLevel
		want entryplan.LevelDisposition
	}{
		{
			name: "eligible",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				Value: f(94), PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionEligibleNotSelected,
		},
		{
			name: "no value",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Unavailable,
				PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionLevelUnavailable,
		},
		{
			name: "labelled available with no value",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionLevelUnavailable,
		},
		{
			name: "on the adjusted series while the quote is raw",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				Value: f(94), PriceBasis: entryplan.PriceBasisAdjusted},
			want: entryplan.DispositionBasisMismatch,
		},
		{
			name: "on no series at all",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				Value: f(94)},
			want: entryplan.DispositionBasisMismatch,
		},
		{
			name: "above the market",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				Value: f(101), PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionNotBelowCurrent,
		},
		{
			name: "exactly at the market",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
				Value: f(quote), PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionNotBelowCurrent,
		},
		{
			// THE ASYMMETRY: the below-the-market screen does not apply to a resistance
			// level. A pivot below the last price is "the breakout already happened", which
			// is a STATUS reading and not an availability fact.
			name: "a pivot below the market is still eligible",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelBreakoutHigh,
				Status: entryplan.Available, Value: f(90), PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionEligibleNotSelected,
		},
		{
			name: "a pivot above the market is eligible too",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelBreakoutHigh,
				Status: entryplan.Available, Value: f(110), PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.DispositionEligibleNotSelected,
		},
		{
			name: "a source that is not a candidate keeps its own disposition",
			cand: entryplan.CandidateLevel{Source: entryplan.LevelBreakoutHigh,
				Status: entryplan.Available, Value: f(110), PriceBasis: entryplan.PriceBasisRaw,
				Disposition: entryplan.DispositionWrongSemantic},
			want: entryplan.DispositionWrongSemantic,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := entryplan.ScreenCandidateLevels([]entryplan.CandidateLevel{c.cand},
				obs(quote), entryplan.PriceBasisRaw)
			if len(got) != 1 {
				t.Fatalf("%d rows out, 1 in", len(got))
			}
			if got[0].Disposition != c.want {
				t.Errorf("disposition = %q, want %q", got[0].Disposition, c.want)
			}
		})
	}
}

// Without a quote or a basis to screen against, NOTHING is rejected: every candidate is
// NOT_SCREENED, which is a state and not a verdict.
func TestScreeningRefusesToJudgeWithoutAQuote(t *testing.T) {
	cand := entryplan.CandidateLevel{Source: entryplan.LevelMA20, Status: entryplan.Available,
		Value: f(94), PriceBasis: entryplan.PriceBasisRaw}

	for _, c := range []struct {
		name  string
		quote entryplan.PriceObservation
		basis entryplan.PriceBasis
	}{
		{"no quote", entryplan.PriceObservation{}, entryplan.PriceBasisRaw},
		{"quote of 0", obs(0), entryplan.PriceBasisRaw},
		{"NaN quote", obs(math.NaN()), entryplan.PriceBasisRaw},
		{"no basis", obs(100), ""},
		{"unsupported basis", obs(100), entryplan.PriceBasis("TOTAL_RETURN")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := entryplan.ScreenCandidateLevels([]entryplan.CandidateLevel{cand}, c.quote, c.basis)
			if got[0].Disposition != entryplan.DispositionNotScreened {
				t.Errorf("disposition = %q, want NOT_SCREENED — a condition that was never "+
					"evaluated must not be reported as a condition that failed",
					got[0].Disposition)
			}
		})
	}
}

// The screen is pure: it returns a new slice and does not write through the input.
func TestScreeningDoesNotMutateItsInput(t *testing.T) {
	in := []entryplan.CandidateLevel{
		{Source: entryplan.LevelMA20, Status: entryplan.Available, Value: f(94),
			PriceBasis: entryplan.PriceBasisRaw},
	}
	before := fmt.Sprintf("%+v", in[0])
	out := entryplan.ScreenCandidateLevels(in, obs(100), entryplan.PriceBasisRaw)
	if after := fmt.Sprintf("%+v", in[0]); after != before {
		t.Errorf("the input row was modified: %s → %s", before, after)
	}
	if out[0].Disposition == in[0].Disposition {
		t.Fatal("the screen changed nothing, so the assertion above proves nothing")
	}

	// SelectCenter likewise.
	screened := entryplan.ScreenCandidateLevels(in, obs(100), entryplan.PriceBasisRaw)
	snapshot := fmt.Sprintf("%+v", screened[0])
	entryplan.SelectCenter(screened, entryplan.EntrySemanticPullback)
	if after := fmt.Sprintf("%+v", screened[0]); after != snapshot {
		t.Errorf("SelectCenter modified its input: %s → %s", snapshot, after)
	}
}

// The candidate rows reach the EVIDENCE CENSUS with their dispositions on them, one row per
// source, under the registry's own keys.
func TestEveryCandidateReachesTheEvidenceCensus(t *testing.T) {
	in := zoneBase()
	in.MA20 = obs(102)                                // above the market
	in.MA60 = obsOn(90, entryplan.PriceBasisAdjusted) // wrong series
	in.BaseLow = obs(92)                              // the winner
	in.PivotHigh = entryplan.PriceObservation{}       // absent, and not a candidate anyway

	p := entryplan.ComputePlan(in)
	want := map[string]struct {
		disposition entryplan.LevelDisposition
		value       *float64
	}{
		entryplan.EvidenceLevelMA20:      {entryplan.DispositionNotBelowCurrent, f(102)},
		entryplan.EvidenceLevelMA60:      {entryplan.DispositionBasisMismatch, f(90)},
		entryplan.EvidenceLevelBaseLow:   {entryplan.DispositionSelected, f(92)},
		entryplan.EvidenceLevelPivotHigh: {entryplan.DispositionWrongSemantic, nil},
	}
	var found int
	for _, e := range p.Evidence {
		w, ok := want[e.Key]
		if !ok {
			continue
		}
		found++
		if e.Text != string(w.disposition) {
			t.Errorf("%s: disposition = %q, want %q", e.Key, e.Text, w.disposition)
		}
		switch {
		case w.value == nil && e.Value != nil:
			t.Errorf("%s: value = %v, want none", e.Key, *e.Value)
		case w.value != nil && e.Value == nil:
			t.Errorf("%s: no value, want %v — refusing to USE a level is not a reason to "+
				"hide what it was", e.Key, *w.value)
		case w.value != nil && e.Value != nil && *e.Value != *w.value:
			t.Errorf("%s: value = %v, want %v", e.Key, *e.Value, *w.value)
		}
	}
	if found != len(want) {
		t.Fatalf("only %d of the %d level rows were found: census %v", found, len(want),
			evidenceKeysOf(p))
	}
}

// ── the evidence key registry, from both ends ─────────────────────────────────────────

// Every key production puts on a row is in AllEvidenceKeys, and every member is put on a row.
//
// The backward half is the one that matters: a key in the registry that no plan carries is a
// column a consumer will SELECT and never find, and the forward half is what catches a row
// added without a registry entry — which is how an archive grows a field nobody can enumerate.
func TestEvidenceKeyRegistryIsExactlyWhatProductionEmits(t *testing.T) {
	seen := map[string]string{}
	for _, c := range allSnapshots() {
		for _, e := range entryplan.ComputePlan(c.in).Evidence {
			if _, ok := seen[e.Key]; !ok {
				seen[e.Key] = c.name
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no evidence row was produced anywhere — every assertion below is vacuous")
	}
	for key, where := range seen {
		if !entryplan.KnownEvidenceKey(key) {
			t.Errorf("production emits the row %q (first at %s) and it is not in "+
				"AllEvidenceKeys", key, where)
		}
	}
	for _, key := range entryplan.AllEvidenceKeys {
		if _, ok := seen[key]; !ok {
			t.Errorf("AllEvidenceKeys lists %q and no plan carries it", key)
		}
	}
	if len(seen) != len(entryplan.AllEvidenceKeys) {
		t.Errorf("production emits %d distinct keys and the registry has %d", len(seen),
			len(entryplan.AllEvidenceKeys))
	}
}

// ── liquidity is recorded and gates nothing ───────────────────────────────────────────

// AvgVolume20 must change EXACTLY ONE THING: its own evidence row.
//
// It is a placeholder, and the reason it is not a gate is that this repo has no backtested
// "minimum volume at which a chase fills" contract. A threshold invented here would be a
// tradeability claim with nothing behind it — and it would silently delete entry plans for
// every thinly-traded stock, which is a strategy decision disguised as a data check.
func TestLiquidityIsRecordedAndNeverGates(t *testing.T) {
	// A stock that barely trades, a normal one, and one with no liquidity evidence at all.
	volumes := []*float64{nil, f(0), f(1), f(120), f(1_500_000), f(900_000_000)}

	var ref entryplan.Plan
	var rows int
	for n, vol := range volumes {
		in := zoneBase()
		in.AvgVolume20 = vol
		p := entryplan.ComputePlan(in)

		if p.IdealEntry == nil || p.MaxChasePrice == nil {
			t.Fatalf("volume %v: the plan lost its prices; reasons %v — there is no volume "+
				"threshold in this package, and inventing one here would be a tradeability "+
				"claim with no study behind it", vol, p.Reasons)
		}
		if n == 0 {
			ref = p
		} else {
			if p.IdealEntry.Low != ref.IdealEntry.Low || p.IdealEntry.High != ref.IdealEntry.High {
				t.Errorf("volume %v moved the zone: %v → %v", vol, ref.IdealEntry, p.IdealEntry)
			}
			if *p.MaxChasePrice != *ref.MaxChasePrice {
				t.Errorf("volume %v moved the ceiling: %v → %v", vol, *ref.MaxChasePrice,
					*p.MaxChasePrice)
			}
			if p.Status != ref.Status || p.Confidence != ref.Confidence {
				t.Errorf("volume %v changed the verdict or the grade: %q/%q → %q/%q", vol,
					ref.Status, ref.Confidence, p.Status, p.Confidence)
			}
			if !reflect.DeepEqual(p.Reasons, ref.Reasons) {
				t.Errorf("volume %v changed the reasons: %v → %v", vol, ref.Reasons, p.Reasons)
			}
		}

		// And the row IS there, with the number on it when there is one. The found flag is
		// the anti-vacuity guard: without it, deleting the append would leave nothing to
		// iterate and this half of the test would pass for the absence of the very thing it
		// inspects.
		var found bool
		for _, e := range p.Evidence {
			if e.Key != entryplan.EvidenceAvgVolume20 {
				continue
			}
			found = true
			rows++
			switch {
			case vol == nil:
				if e.Status == entryplan.Available || e.Value != nil {
					t.Errorf("no volume was supplied and the row reads %+v", e)
				}
			default:
				if e.Status != entryplan.Available || e.Value == nil || *e.Value != *vol {
					t.Errorf("volume %v: the row reads %+v", *vol, e)
				}
			}
			if e.PriceBasis != "" {
				t.Errorf("the liquidity row carries a price basis (%q) — a share count is "+
					"not restated by an adjustment factor", e.PriceBasis)
			}
		}
		if !found {
			t.Errorf("volume %v: the liquidity row is missing; census %v", vol,
				evidenceKeysOf(p))
		}
	}
	if rows != len(volumes) {
		t.Errorf("inspected %d liquidity rows over %d inputs", rows, len(volumes))
	}
}
