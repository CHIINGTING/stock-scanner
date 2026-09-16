package recon

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// Funnel is the EP-9 §8 coverage funnel for one arm over one set of sessions.
//
// Every line is a COUNT OF STOCK-SESSIONS, and the lines are of three different kinds, which
// is why they are not one column of subtractions:
//
//	SEQUENTIAL   candidates → scanner observations → plans generated.  Each is a subset of
//	             the one above it and the difference is a named drop.
//	PARTITION    the six statuses.  They sum exactly to plans generated.
//	NESTED       Zone ⊇ MaxChase, Zone ⊇ Invalidation ⊇ Target1, Target1 ⊇ Target2.  They are
//	             subsets, never a partition, because the invariant gate withdraws a violated
//	             field AND its dependents (internal/entryplan/enforce.go).
//
// Reconcile() checks the two claims that can actually be checked; it does not pretend the
// nested lines add up to anything.
type Funnel struct {
	Arm      Arm     `json:"arm"`
	Context  Context `json:"context"`
	Sessions int     `json:"sessions"`

	// Candidates is every (symbol, session) pair that traded on the session with at least
	// EligibilityBars of history — the population before any scanner filter.
	Candidates int `json:"candidates"`
	// ScannerObservations is how many of those EnrichWatchlist actually returned an entry
	// for. The gap is its own 30-bar skip (watchlist.go:187).
	ScannerObservations int `json:"scanner_observations"`

	// SemanticResolved / SemanticUnresolved partition the scanner observations by whether
	// the WatchAction projected onto a PULLBACK or BREAKOUT entry shape.
	SemanticResolved   int `json:"semantic_resolved"`
	SemanticUnresolved int `json:"semantic_unresolved"`

	// PlansGenerated is how many entries carry a non-nil Plan. AttachEntryPlan is total, so
	// this equals ScannerObservations whenever the feature is enabled; it is counted rather
	// than assumed so that a silent nil would show up as a gap instead of as nothing.
	PlansGenerated int `json:"plans_generated"`

	ByStatus   map[entryplan.EntryStatus]int   `json:"by_status"`
	BySemantic map[entryplan.EntrySemantic]int `json:"by_semantic"`

	WithZone         int `json:"with_zone"`
	WithMaxChase     int `json:"with_max_chase"`
	WithInvalidation int `json:"with_invalidation"`
	WithTarget1      int `json:"with_target_1"`
	WithTarget2      int `json:"with_target_2"`
	// WithSuitableValuationCeiling counts plans whose Target2 was capped by a valuation
	// target that was AVAILABLE and whose suitability permitted the ceiling.
	WithSuitableValuationCeiling int `json:"with_suitable_valuation_ceiling"`
}

// NewFunnel returns an empty funnel for one arm/context.
func NewFunnel(arm Arm, ctx Context) *Funnel {
	return &Funnel{Arm: arm, Context: ctx,
		ByStatus:   map[entryplan.EntryStatus]int{},
		BySemantic: map[entryplan.EntrySemantic]int{}}
}

// Add folds one reconstructed session into the funnel.
func (f *Funnel) Add(res SessionResult) {
	f.Sessions++
	f.Candidates += res.Universe
	f.ScannerObservations += len(res.Entries)
	for i := range res.Entries {
		f.addEntry(&res.Entries[i])
	}
}

func (f *Funnel) addEntry(e *scanner.WatchlistEntry) {
	p := e.EntryPlan
	if p == nil {
		return
	}
	f.PlansGenerated++
	f.ByStatus[p.Status]++

	sem := PlanSemantic(p)
	f.BySemantic[sem]++
	if sem.Resolved() {
		f.SemanticResolved++
	} else {
		f.SemanticUnresolved++
	}

	if p.IdealEntry != nil {
		f.WithZone++
	}
	if p.MaxChasePrice != nil {
		f.WithMaxChase++
	}
	if p.Invalidation != nil {
		f.WithInvalidation++
	}
	if p.Target1 != nil {
		f.WithTarget1++
	}
	if p.Target2 != nil {
		f.WithTarget2++
	}
	if valuationCeilingApplied(p) {
		f.WithSuitableValuationCeiling++
	}
}

// PlanSemantic reads the entry shape the plan was decided about off the plan's own trace,
// falling back to its evidence. It does NOT re-derive the semantic from the WatchAction:
// re-deriving would produce a second opinion about the very thing being counted.
func PlanSemantic(p *entryplan.Plan) entryplan.EntrySemantic {
	if p.EntryTrace != nil {
		if s := p.EntryTrace.Decision.Semantic; s != "" {
			return s
		}
		if s := p.EntryTrace.Zone.Semantic; s != "" {
			return s
		}
	}
	// Fallback: the evidence row. Reached for an S1 (sell-side contradiction) plan, where
	// no entry step runs at all and neither trace carries a semantic.
	for _, ev := range p.Evidence {
		if ev.Key == entryplan.EvidenceEntrySemantic && ev.Text != "" {
			return entryplan.EntrySemantic(ev.Text)
		}
	}
	return ""
}

// valuationCeilingApplied reports whether a valuation target actually capped Target2.
//
// It reads the TARGET TRACE's own outcome code, not the valuation evidence row: "a valuation
// was available and suitable" and "the valuation target was the binding one" are different
// facts, and only the second is a plan a valuation ceiling changed. entryplan spells that
// difference itself — ValuationCeilingNotBinding means the ceiling applied and sat ABOVE the
// 3.5R target (targets.go:106-108), so only ValuationCeilingApplied lowered a Target2.
func valuationCeilingApplied(p *entryplan.Plan) bool {
	if p.EntryTrace == nil {
		return false
	}
	return p.EntryTrace.Targets.ValuationCeiling == entryplan.ValuationCeilingApplied
}

// Reconcile returns an error naming the first line that does not add up.
func (f *Funnel) Reconcile() error {
	if f.ScannerObservations > f.Candidates {
		return fmt.Errorf("recon: %d scanner observations from %d candidates",
			f.ScannerObservations, f.Candidates)
	}
	if f.PlansGenerated > f.ScannerObservations {
		return fmt.Errorf("recon: %d plans from %d scanner observations",
			f.PlansGenerated, f.ScannerObservations)
	}
	var byStatus int
	for _, v := range f.ByStatus {
		byStatus += v
	}
	if byStatus != f.PlansGenerated {
		return fmt.Errorf("recon: statuses sum to %d but %d plans were generated — the six "+
			"statuses are a partition of the plans", byStatus, f.PlansGenerated)
	}
	if f.SemanticResolved+f.SemanticUnresolved != f.PlansGenerated {
		return fmt.Errorf("recon: resolved %d + unresolved %d != %d plans",
			f.SemanticResolved, f.SemanticUnresolved, f.PlansGenerated)
	}
	// The nested lines: subsets, in dependency order.
	if f.WithMaxChase > f.WithZone {
		return fmt.Errorf("recon: %d chase ceilings on %d zones — a ceiling needs a zone",
			f.WithMaxChase, f.WithZone)
	}
	if f.WithTarget1 > f.WithInvalidation {
		return fmt.Errorf("recon: %d Target1 on %d invalidations — a target needs a risk "+
			"denominator", f.WithTarget1, f.WithInvalidation)
	}
	if f.WithSuitableValuationCeiling > f.WithTarget2 {
		return fmt.Errorf("recon: %d valuation ceilings on %d Target2",
			f.WithSuitableValuationCeiling, f.WithTarget2)
	}
	return nil
}

// StatusLines renders the status partition in a stable order for printing.
func (f *Funnel) StatusLines() []string {
	keys := make([]string, 0, len(f.ByStatus))
	for k := range f.ByStatus {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%-20s %8d", k, f.ByStatus[entryplan.EntryStatus(k)]))
	}
	return out
}
