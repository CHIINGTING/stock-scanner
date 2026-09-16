package entryplan_test

import (
	"math"
	"os"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── EP-4, asserted field by field ─────────────────────────────────────────────────────

// Every named risk case must produce exactly the stop, the two targets, the ratio and the trace
// codes it was built for. This is the test that fails when a rule changes, and it names the case.
func TestTheRiskCasesProduceWhatTheyWereBuiltFor(t *testing.T) {
	var withStop, withoutStop, withT1, withoutT1, withT2, withoutT2 int

	for _, c := range riskCases() {
		t.Run(c.name, func(t *testing.T) {
			p := entryplan.ComputePlan(c.snapshot())
			if p.EntryTrace == nil {
				t.Fatalf("no trace: reasons %v", p.Reasons)
			}
			stop, targets := p.EntryTrace.Stop, p.EntryTrace.Targets

			switch {
			case c.wantStop != nil:
				withStop++
				if p.Invalidation == nil {
					t.Fatalf("no invalidation, want %v — reasons: %v; stop rejection %q",
						*c.wantStop, p.Reasons, stop.Rejection)
				}
				if p.Invalidation.Price != *c.wantStop {
					t.Errorf("invalidation = %v, want %v (from %q)", p.Invalidation.Price,
						*c.wantStop, stop.SelectedSource)
				}
			case c.noStop:
				withoutStop++
				if p.Invalidation != nil {
					t.Errorf("invalidation = %v, want NONE — a thesis-failure price was "+
						"fabricated for an input built to have none", p.Invalidation.Price)
				}
			}

			switch {
			case c.wantTarget1 != nil:
				withT1++
				if p.Target1 == nil {
					t.Fatalf("no first target, want %v — reasons: %v", *c.wantTarget1, p.Reasons)
				}
				if p.Target1.Price != *c.wantTarget1 {
					t.Errorf("target 1 = %v, want %v (binding %q, resistance %v)",
						p.Target1.Price, *c.wantTarget1, targets.Target1Binding,
						targets.Resistance.Disposition)
				}
			case c.noTarget1:
				withoutT1++
				if p.Target1 != nil {
					t.Errorf("target 1 = %v, want NONE", p.Target1.Price)
				}
			}

			switch {
			case c.wantTarget2 != nil:
				withT2++
				if p.Target2 == nil {
					t.Fatalf("no second target, want %v — reasons: %v; ceiling %q",
						*c.wantTarget2, p.Reasons, targets.ValuationCeiling)
				}
				if p.Target2.Price != *c.wantTarget2 {
					t.Errorf("target 2 = %v, want %v (ceiling %q)", p.Target2.Price,
						*c.wantTarget2, targets.ValuationCeiling)
				}
			case c.noTarget2:
				withoutT2++
				if p.Target2 != nil {
					t.Errorf("target 2 = %v, want NONE", p.Target2.Price)
				}
			}

			switch {
			case c.wantRatio != nil:
				if p.RiskReward == nil {
					t.Fatalf("no risk/reward, want %v — reasons: %v", *c.wantRatio, p.Reasons)
				}
				if !closeEnough(p.RiskReward.Ratio, *c.wantRatio) {
					t.Errorf("R:R = %v, want %v (risk %v, reward %v)", p.RiskReward.Ratio,
						*c.wantRatio, p.RiskReward.RiskPerShare, p.RiskReward.RewardPerShare)
				}
			case c.noRatio:
				if p.RiskReward != nil {
					t.Errorf("risk/reward = %+v, want NONE", *p.RiskReward)
				}
			}

			if c.wantStopSource != "" && stop.SelectedSource != c.wantStopSource {
				t.Errorf("the stop came from %q, want %q — the NUMBER can coincide between "+
					"a structural level and the volatility fallback, and the two are not the "+
					"same claim", stop.SelectedSource, c.wantStopSource)
			}
			if c.wantBinding != "" && targets.Target1Binding != c.wantBinding {
				t.Errorf("target 1 binding = %q, want %q", targets.Target1Binding, c.wantBinding)
			}
			if c.wantCeiling != "" && targets.ValuationCeiling != c.wantCeiling {
				t.Errorf("valuation ceiling = %q, want %q", targets.ValuationCeiling, c.wantCeiling)
			}

			for _, want := range c.wantReasons {
				if !hasReason(p, want) {
					t.Errorf("reason %q missing; got %v", want, p.Reasons)
				}
			}
			for _, not := range c.notReasons {
				if hasReason(p, not) {
					t.Errorf("reason %q present and must not be — the rule reported a "+
						"different failure from the one this input has; got %v", not, p.Reasons)
				}
			}
		})
	}

	// ANTI-VACUITY. All six quadrants must be populated, or one half of a switch above never
	// executed.
	if withStop == 0 || withoutStop == 0 || withT1 == 0 || withoutT1 == 0 ||
		withT2 == 0 || withoutT2 == 0 {
		t.Errorf("the table covered %d stops / %d refusals, %d first targets / %d refusals "+
			"and %d second targets / %d refusals — a quadrant with no case in it is an "+
			"assertion that never ran", withStop, withoutStop, withT1, withoutT1,
			withT2, withoutT2)
	}
}

// Between them, the named cases must reach every EP-4 reason code that is not reserved.
//
// The registry test in reasons_test.go already sweeps for dead codes, and this is the other
// half: it says the RISK CASES specifically are what reach them, so a code cannot end up covered
// only by an accident of the 280k-case evidence matrix, where nothing declares what the input
// was for.
func TestEveryEP4ReasonIsReachedByANamedCase(t *testing.T) {
	seen := map[entryplan.Reason]bool{}
	for _, c := range riskCases() {
		for _, r := range entryplan.ComputePlan(c.snapshot()).Reasons {
			seen[r] = true
		}
	}
	for _, r := range []entryplan.Reason{
		entryplan.ReasonNoValidInvalidation,
		entryplan.ReasonTarget1TickAlignmentUnavailable,
		entryplan.ReasonTarget1NotAboveEntry,
		entryplan.ReasonTarget2TickAlignmentUnavailable,
		entryplan.ReasonValuationCeilingBelowEntry,
		entryplan.ReasonTarget2BelowTarget1,
	} {
		if !seen[r] {
			t.Errorf("no named risk case produces %q — the code's behaviour is declared "+
				"nowhere, so nothing pins what input reaches it", r)
		}
	}
	// RISK_NOT_ESTABLISHED is RESERVED and must NOT be reachable from any input; its producer
	// is driven directly by TestTheRiskDenominatorIsRefusedRatherThanInvented.
	if seen[entryplan.ReasonRiskNotEstablished] {
		t.Error("a named case produced RISK_NOT_ESTABLISHED — that would mean " +
			"ComputeInvalidation published a level that is not strictly below zone.Low, or " +
			"published it on another price series")
	}
}

// ── the EP-4 vocabularies, from both sides ────────────────────────────────────────────

// The same source-scan + registry check EP-3b's vocabularies get, in the shape
// TestEveryEP3VocabularyIsPinnedFromBothSides established: the SOURCE is scanned for the
// constants, so the registry cannot list a value nobody declares and cannot miss one somebody
// added, and the count is a literal so ADDING a value fails here even when it is added
// consistently everywhere else.
//
// PlanPriceField is deliberately NOT in this list: its values are Go field names
// ("MaxChasePrice"), not SCREAMING_SNAKE codes, because they have to match the violation paths
// the checker produces. It is pinned in golden_test.go and held against Plan's own type by
// TestNoPriceFieldNameIsAPrefixOfAnother.
func ep4Registries() []registryCase {
	return []registryCase{
		{
			typeName: "StopSource", file: "stop.go", wantCount: 3,
			members: renderStrings(entryplan.AllStopSources),
			known: func(s string) bool {
				return entryplan.KnownStopSource(entryplan.StopSource(s))
			},
		},
		{
			typeName: "RiskLevelDisposition", file: "stop.go", wantCount: 11,
			members: renderStrings(entryplan.AllRiskLevelDispositions),
			known: func(s string) bool {
				return entryplan.KnownRiskLevelDisposition(entryplan.RiskLevelDisposition(s))
			},
		},
		{
			typeName: "Target1Binding", file: "targets.go", wantCount: 2,
			members: renderStrings(entryplan.AllTarget1Bindings),
		},
		{
			typeName: "ValuationCeilingOutcome", file: "targets.go", wantCount: 5,
			members: renderStrings(entryplan.AllValuationCeilingOutcomes),
		},
		{
			typeName: "RiskRejection", file: "targets.go", wantCount: 2,
			members: renderStrings(entryplan.AllRiskRejections),
		},
		{
			typeName: "BaseLowKind", file: "input.go", wantCount: 3,
			members: renderStrings(entryplan.AllBaseLowKinds),
			known: func(s string) bool {
				return entryplan.KnownBaseLowKind(entryplan.BaseLowKind(s))
			},
		},
		{
			typeName: "ValuationSuitability", file: "input.go", wantCount: 5,
			members: renderStrings(entryplan.AllValuationSuitabilities),
			known: func(s string) bool {
				return entryplan.KnownValuationSuitability(entryplan.ValuationSuitability(s))
			},
		},
	}
}

func TestEveryEP4VocabularyIsPinnedFromBothSides(t *testing.T) {
	for _, c := range ep4Registries() {
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
					t.Errorf("%q is in the registry and the Known predicate rejects it", v)
				}
			}
			for value, goName := range declared {
				if !seen[value] {
					t.Errorf("%s declares %s (%q) and the registry does not list it — a value "+
						"a producer can emit and no consumer can enumerate", c.file, goName, value)
				}
			}
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
	// And the list itself must not shrink: EP-4 declares SEVEN new persisted vocabularies,
	// and a table that lost one would report green about the six it kept.
	if len(ep4Registries()) != 7 {
		t.Errorf("%d EP-4 vocabularies are checked, want 7 — if one was added or removed, "+
			"golden_test.go's literal list moves in the same change", len(ep4Registries()))
	}
}

// The vocabularies production ATTACHES TO EVERY PLAN must be exactly what production emits —
// the same bidirectional check AllReasons and the level vocabularies get, over the same union.
func TestTheRiskVocabulariesAreExactlyWhatProductionEmits(t *testing.T) {
	sources := map[entryplan.StopSource]string{}
	dispositions := map[entryplan.RiskLevelDisposition]string{}
	bindings := map[entryplan.Target1Binding]string{}
	ceilings := map[entryplan.ValuationCeilingOutcome]string{}
	kinds := map[entryplan.BaseLowKind]string{}
	suitabilities := map[entryplan.ValuationSuitability]string{}

	for _, c := range allSnapshots() {
		tr := entryplan.ComputePlan(c.in).EntryTrace
		if tr == nil {
			continue
		}
		for _, cand := range tr.Stop.Candidates {
			if _, ok := sources[cand.Source]; !ok {
				sources[cand.Source] = c.name
			}
			if _, ok := dispositions[cand.Disposition]; !ok {
				dispositions[cand.Disposition] = c.name
			}
		}
		if d := tr.Targets.Resistance.Disposition; d != "" {
			if _, ok := dispositions[d]; !ok {
				dispositions[d] = c.name
			}
		}
		if b := tr.Targets.Target1Binding; b != "" {
			if _, ok := bindings[b]; !ok {
				bindings[b] = c.name
			}
		}
		if v := tr.Targets.ValuationCeiling; v != "" {
			if _, ok := ceilings[v]; !ok {
				ceilings[v] = c.name
			}
		}
		if k := tr.Stop.BaseLowKind; k != "" {
			if _, ok := kinds[k]; !ok {
				kinds[k] = c.name
			}
		}
		if su := tr.Targets.ValuationSuitability; su != "" {
			if _, ok := suitabilities[su]; !ok {
				suitabilities[su] = c.name
			}
		}
	}

	// forward: nothing production emits may be outside its registry.
	for src, where := range sources {
		if !entryplan.KnownStopSource(src) {
			t.Errorf("production emits stop source %q (first at %s) and it is not in the "+
				"registry", src, where)
		}
	}
	for d, where := range dispositions {
		if !entryplan.KnownRiskLevelDisposition(d) {
			t.Errorf("production emits disposition %q (first at %s) and it is not in the "+
				"registry", d, where)
		}
	}
	for k, where := range kinds {
		if !entryplan.KnownBaseLowKind(k) {
			t.Errorf("production emits base low kind %q (first at %s) and it is not in the "+
				"registry", k, where)
		}
	}
	for su, where := range suitabilities {
		if !entryplan.KnownValuationSuitability(su) {
			t.Errorf("production emits suitability %q (first at %s) and it is not in the "+
				"registry", su, where)
		}
	}

	// backward — the half that is usually skipped, and the one that catches a code no input
	// can reach. A dead disposition is a branch a reader of an archived plan will never see.
	for _, src := range entryplan.AllStopSources {
		if _, ok := sources[src]; !ok {
			t.Errorf("AllStopSources lists %q and no input produces it", src)
		}
	}
	for _, d := range entryplan.AllRiskLevelDispositions {
		if _, ok := dispositions[d]; !ok {
			t.Errorf("AllRiskLevelDispositions lists %q and no input produces it", d)
		}
	}
	for _, b := range entryplan.AllTarget1Bindings {
		if _, ok := bindings[b]; !ok {
			t.Errorf("AllTarget1Bindings lists %q and no input produces it", b)
		}
	}
	for _, v := range entryplan.AllValuationCeilingOutcomes {
		if _, ok := ceilings[v]; !ok {
			t.Errorf("AllValuationCeilingOutcomes lists %q and no input produces it — the "+
				"five outcomes are five different reasons Target2 is or is not capped, and a "+
				"dead one is a sentence no reader will ever see", v)
		}
	}
	for _, k := range entryplan.AllBaseLowKinds {
		if _, ok := kinds[k]; !ok {
			t.Errorf("AllBaseLowKinds lists %q and no input produces it", k)
		}
	}
	for _, su := range entryplan.AllValuationSuitabilities {
		if _, ok := suitabilities[su]; !ok {
			t.Errorf("AllValuationSuitabilities lists %q and no input produces it — the "+
				"projection's whole content is that its five values are valuation's five", su)
		}
	}
	if len(sources) == 0 || len(dispositions) == 0 {
		t.Fatal("the sweep collected nothing — every assertion above is vacuous")
	}
	// The empty disposition is NOT a disposition: it is a row nobody filled in.
	if where, ok := dispositions[""]; ok {
		t.Errorf("a stop candidate was published with no disposition (first at %s)", where)
	}
}

// ── tick alignment: DOWN, and the direction is checked WITHOUT calling pricerule ──────

// EVERY EP-4 PRICE IS THE GREATEST GRID POINT AT OR BELOW ITS OWN RAW VALUE.
//
// That is the full characterisation of "aligned DOWN", and it is asserted against the
// INDEPENDENT tick table at the top of zone_test.go rather than by asking pricerule what it did
// — the pattern chase_test.go's independentLimitUp established, for the reason it established
// it: a direction asserted by calling the thing that chose the direction cannot fail.
//
// Three properties per price, and the third is the one that catches UP:
//
//  1. published <= raw          UP breaks this for any off-grid raw
//  2. published is on the grid  a local math.Round(v*10)/10 breaks this
//  3. published + tick > raw    a rule that rounded DOWN TWICE, or to a coarser grid than the
//     tier requires, breaks this — "not above the raw" alone is
//     satisfied by any sufficiently small number
//
// THE LEDGER at the bottom is what stops it going vacuous: on a fixture whose raw values happen
// to be on the grid already, DOWN and UP agree and every assertion above passes for both. So the
// sweep must have seen prices that actually MOVED.
func TestEveryEP4PriceIsAlignedDownFromItsOwnRawValue(t *testing.T) {
	type aligned struct {
		what      string
		raw, done *float64
	}
	var checked, moved int

	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.EntryTrace == nil {
			continue
		}
		tr := p.EntryTrace
		for _, a := range []aligned{
			{"invalidation", tr.Stop.RawPrice, tr.Stop.Price},
			{"target 1", tr.Targets.Target1Raw, tr.Targets.Target1},
			{"target 2", tr.Targets.Target2Clamped, tr.Targets.Target2},
		} {
			if a.raw == nil || a.done == nil {
				continue
			}
			raw, done := *a.raw, *a.done
			checked++
			if done > raw+1e-9 {
				t.Errorf("%s: %s aligned to %v from a raw %v — that is UP. For a stop it "+
					"declares the thesis dead nearer the entry than the rule said and shrinks "+
					"the risk (raising the published R:R); for a target it asks the market for "+
					"more than the arithmetic authorised", c.name, a.what, done, raw)
			}
			if !onTickGrid(done) {
				tickCents, _ := tickCentsFor(done)
				t.Errorf("%s: %s = %v is not on the tick grid (the tier here is %d cents)",
					c.name, a.what, done, tickCents)
			}
			tickCents, ok := tickCentsFor(done)
			if !ok {
				t.Errorf("%s: %s = %v has no tick tier", c.name, a.what, done)
				continue
			}
			tick := float64(tickCents) / 100
			if done+tick <= raw-1e-9 {
				t.Errorf("%s: %s = %v is more than one tick (%v) below its raw value %v — the "+
					"rule rounded further than the grid requires, so the published number is "+
					"not the level it claims to be", c.name, a.what, done, tick, raw)
			}
			if math.Abs(done-raw) > 1e-9 {
				moved++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no aligned price was inspected — every assertion above is vacuous")
	}
	if moved == 0 {
		t.Fatalf("all %d aligned prices were already on the grid, so DOWN and UP would give "+
			"the same answer everywhere and the direction is untested. The named cases "+
			"pullback/an_off_grid_invalidation_is_aligned_down and "+
			"pullback/off_grid_targets_are_aligned_down exist for exactly this", checked)
	}
	if testing.Verbose() {
		t.Logf("%d aligned prices inspected, %d actually moved onto the grid", checked, moved)
	}
}

// ── evidence reconstructability ───────────────────────────────────────────────────────

// EVERY EP-4 NUMBER MUST BE RECOMPUTABLE BY HAND FROM THE PLAN.
//
// This is the strongest property in the work item and it is what makes the rest of the file
// checkable by a reader rather than only by this suite. For every plan that publishes a risk
// side, the test recomputes — from the PLAN's own fields and its trace, using only arithmetic
// written out here — the entry used, the risk, the 2R level, the 3.5R level, the clamps, both
// targets, the reward and the ratio, and requires each to equal what production published.
//
// It is deliberately NOT a call into production: if it were, it would be asserting that the code
// agrees with itself. The formulas below are the ones in doc.go, retyped.
func TestEveryEP4NumberCanBeRecomputedByHand(t *testing.T) {
	var seenStop, seenT1, seenT2, seenRR, seenClamp int

	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.EntryTrace == nil {
			continue
		}
		tr := p.EntryTrace

		// 1. THE INVALIDATION IS ONE OF THE CANDIDATES ON ITS OWN TRACE, aligned down.
		// A fabricated stop — zone.Low × 0.99, entry × 0.93, entry − 2 × ATR — would be a
		// number no candidate row holds, and this is where it would be caught.
		if p.Invalidation != nil {
			seenStop++
			var found bool
			for _, cand := range tr.Stop.Candidates {
				if cand.Disposition != entryplan.RiskLevelSelected {
					continue
				}
				found = true
				if cand.Value == nil {
					t.Errorf("%s: the SELECTED stop candidate carries no value", c.name)
					continue
				}
				if tr.Stop.RawPrice == nil || *tr.Stop.RawPrice != *cand.Value {
					t.Errorf("%s: the trace's raw stop is %v and the selected candidate is %v",
						c.name, tr.Stop.RawPrice, *cand.Value)
				}
				if cand.Source != tr.Stop.SelectedSource {
					t.Errorf("%s: the SELECTED row is %q and the trace names %q", c.name,
						cand.Source, tr.Stop.SelectedSource)
				}
				// The published price is the aligned form of that candidate, never anything
				// else, and it is strictly below the entry band.
				if p.Invalidation.Price > *cand.Value+1e-9 {
					t.Errorf("%s: invalidation %v is above the candidate %v it claims to come "+
						"from", c.name, p.Invalidation.Price, *cand.Value)
				}
				if p.IdealEntry != nil && p.Invalidation.Price >= p.IdealEntry.Low {
					t.Errorf("%s: invalidation %v is not strictly below zone.Low %v", c.name,
						p.Invalidation.Price, p.IdealEntry.Low)
				}
				// And the fallback is LABELLED, so a reader can never mistake a heuristic
				// buffer for an observed level.
				wantHeuristic := cand.Source == entryplan.StopSourceZoneATRBuffer
				var gotHeuristic bool
				for _, b := range p.Invalidation.Basis {
					if b == "HEURISTIC_VOLATILITY_BUFFER" {
						gotHeuristic = true
					}
				}
				if wantHeuristic != gotHeuristic {
					t.Errorf("%s: the stop came from %q and its basis is %v — the volatility "+
						"fallback must say so on the row and an observed level must not",
						c.name, cand.Source, p.Invalidation.Basis)
				}
			}
			if !found {
				t.Errorf("%s: an invalidation of %v and no SELECTED candidate on the trace — "+
					"the number cannot be traced to anything", c.name, p.Invalidation.Price)
			}
			// The ATR fallback's own arithmetic, recomputed: zone.Low − 0.5 × ATR.
			if tr.Stop.SelectedSource == entryplan.StopSourceZoneATRBuffer {
				if tr.Stop.ZoneLow == nil || tr.Stop.ATR == nil || tr.Stop.ATRMultiple == nil {
					t.Errorf("%s: the fallback was used and the trace does not carry all three "+
						"terms (zone low %v, ATR %v, multiple %v)", c.name, tr.Stop.ZoneLow,
						tr.Stop.ATR, tr.Stop.ATRMultiple)
				} else if want := *tr.Stop.ZoneLow - *tr.Stop.ATRMultiple**tr.Stop.ATR; !closeEnough(*tr.Stop.RawPrice, want) {
					t.Errorf("%s: the raw fallback is %v and zone.Low − multiple × ATR is %v",
						c.name, *tr.Stop.RawPrice, want)
				}
			}
		}

		tg := tr.Targets
		if p.Target1 == nil && p.Target2 == nil && p.RiskReward == nil {
			continue
		}

		// 2. THE ENTRY USED IS zone.High, and the risk is measured from it.
		if p.IdealEntry == nil || tg.EntryUsed == nil {
			t.Fatalf("%s: a target with no zone or no recorded entry", c.name)
		}
		if *tg.EntryUsed != p.IdealEntry.High {
			t.Errorf("%s: the entry used is %v and zone.High is %v — the MIDPOINT would be %v, "+
				"and using it inflates the R:R twice over", c.name, *tg.EntryUsed,
				p.IdealEntry.High, (p.IdealEntry.Low+p.IdealEntry.High)/2)
		}
		if tg.Invalidation == nil || p.Invalidation == nil ||
			*tg.Invalidation != p.Invalidation.Price {
			t.Fatalf("%s: the trace's invalidation and the plan's disagree: %v vs %v",
				c.name, tg.Invalidation, p.Invalidation)
		}
		risk := p.IdealEntry.High - p.Invalidation.Price
		if tg.Risk == nil || !closeEnough(*tg.Risk, risk) {
			t.Errorf("%s: the trace's risk is %v and zone.High − invalidation is %v",
				c.name, tg.Risk, risk)
		}

		// 3. TARGET 1 = min(entry + 2R, resistance).
		if p.Target1 != nil {
			seenT1++
			want2R := p.IdealEntry.High + 2.0*risk
			if tg.Target1RTarget == nil || !closeEnough(*tg.Target1RTarget, want2R) {
				t.Errorf("%s: the trace's 2R target is %v and entry + 2 × risk is %v — if the "+
					"multiple has moved off 2.0 this is where it shows", c.name,
					tg.Target1RTarget, want2R)
			}
			wantRaw := want2R
			if tg.Resistance.Disposition == entryplan.RiskLevelSelected {
				switch res := tg.Resistance.Value; {
				case res == nil:
					t.Errorf("%s: the resistance is SELECTED and carries no value", c.name)
				case *res < wantRaw:
					wantRaw = *res
				default:
					t.Errorf("%s: the resistance %v was SELECTED and it is NOT nearer than "+
						"the 2R target %v — the rule took the max, which publishes a target "+
						"beyond a level the stock has not cleared", c.name, *res, want2R)
				}
			}
			if tg.Target1Raw == nil || !closeEnough(*tg.Target1Raw, wantRaw) {
				t.Errorf("%s: the trace's raw target 1 is %v, want min(2R, resistance) = %v",
					c.name, tg.Target1Raw, wantRaw)
			}
			if p.Target1.Price > wantRaw+1e-9 {
				t.Errorf("%s: target 1 %v is above the min() it came from (%v)", c.name,
					p.Target1.Price, wantRaw)
			}
			if p.Target1.RMultiple == nil ||
				!closeEnough(*p.Target1.RMultiple, (p.Target1.Price-p.IdealEntry.High)/risk) {
				t.Errorf("%s: target 1 carries R multiple %v and (target − entry)/risk is %v "+
					"— the multiple must describe the PUBLISHED price, not the raw one",
					c.name, p.Target1.RMultiple,
					(p.Target1.Price-p.IdealEntry.High)/risk)
			}
		}

		// 4. TARGET 2 = min(entry + 3.5R, valuation ceiling) — from the ORIGINAL risk.
		if tg.Target2Raw != nil {
			want35R := p.IdealEntry.High + 3.5*risk
			if !closeEnough(*tg.Target2Raw, want35R) {
				t.Errorf("%s: the trace's 3.5R target is %v and entry + 3.5 × risk is %v — "+
					"deriving it from Target1 instead would give %v", c.name, *tg.Target2Raw,
					want35R, valueOr(tg.Target1, 0)+1.5*risk)
			}
		}
		if p.Target2 != nil {
			seenT2++
			wantClamped := *tg.Target2Raw
			if tg.ValuationCeiling == entryplan.ValuationCeilingApplied {
				seenClamp++
				if tg.ValuationTarget == nil {
					t.Errorf("%s: the ceiling was APPLIED and the trace carries no valuation "+
						"target", c.name)
				} else {
					if *tg.ValuationTarget >= wantClamped {
						t.Errorf("%s: the ceiling was APPLIED and the valuation target %v is "+
							"not below the 3.5R target %v", c.name, *tg.ValuationTarget,
							wantClamped)
					}
					wantClamped = *tg.ValuationTarget
				}
			}
			if tg.Target2Clamped == nil || !closeEnough(*tg.Target2Clamped, wantClamped) {
				t.Errorf("%s: the trace's clamped target 2 is %v, want %v", c.name,
					tg.Target2Clamped, wantClamped)
			}
			if p.Target2.Price > wantClamped+1e-9 {
				t.Errorf("%s: target 2 %v is above the min() it came from (%v)", c.name,
					p.Target2.Price, wantClamped)
			}
			if p.Target1 != nil && p.Target2.Price < p.Target1.Price {
				t.Errorf("%s: target 2 %v is below target 1 %v and was published anyway",
					c.name, p.Target2.Price, p.Target1.Price)
			}
		}

		// 5. THE RATIO, from the two published prices.
		if p.RiskReward != nil {
			seenRR++
			rr := *p.RiskReward
			if !closeEnough(rr.RiskPerShare, risk) {
				t.Errorf("%s: RiskPerShare = %v and zone.High − invalidation = %v — a "+
					"denominator of CurrentPrice − invalidation would be a different number "+
					"the plan does not publish", c.name, rr.RiskPerShare, risk)
			}
			if p.Target1 == nil {
				t.Errorf("%s: a risk/reward with no first target to measure to", c.name)
				continue
			}
			reward := p.Target1.Price - p.IdealEntry.High
			if !closeEnough(rr.RewardPerShare, reward) {
				t.Errorf("%s: RewardPerShare = %v and target1 − zone.High = %v",
					c.name, rr.RewardPerShare, reward)
			}
			if !closeEnough(rr.Ratio, reward/risk) {
				t.Errorf("%s: Ratio = %v and reward/risk = %v", c.name, rr.Ratio, reward/risk)
			}
			if tg.Reward == nil || !closeEnough(*tg.Reward, reward) ||
				tg.Ratio == nil || !closeEnough(*tg.Ratio, rr.Ratio) {
				t.Errorf("%s: the trace does not restate the reward and the ratio (%v, %v) — "+
					"a report that wants to print \"Reward 40 / Risk 20 = 2.00R\" needs every "+
					"term on the row", c.name, tg.Reward, tg.Ratio)
			}
		}
	}

	if seenStop == 0 || seenT1 == 0 || seenT2 == 0 || seenRR == 0 || seenClamp == 0 {
		t.Fatalf("the sweep recomputed %d stops, %d first targets, %d second targets, %d "+
			"ratios and %d applied ceilings — a count of zero means that half of the "+
			"reconstruction never ran", seenStop, seenT1, seenT2, seenRR, seenClamp)
	}
	if testing.Verbose() {
		t.Logf("recomputed %d stops, %d/%d targets, %d ratios, %d applied ceilings",
			seenStop, seenT1, seenT2, seenRR, seenClamp)
	}
}

func valueOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}
