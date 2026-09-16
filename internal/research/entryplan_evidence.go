package research

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// ── EP-7: EntryPlan persistence as evidence rows ────────────────────────────────────────
//
// A watchlist entry's published entryplan.Plan is written into the EXISTING R13 chain
// (scan_runs → stock_snapshots → evidence) under category store.CategoryEntryPlan. There is no
// entry-plan table and no schema change.
//
// What is persisted is the PUBLISHED PLAN, field by field — never EntryTrace arithmetic, never
// Reasons, Caveats, Confidence, the evidence census, InvariantCheck or any presentation text.
// The one read below that touches EntryTrace is ep_policy, which copies a registry LABEL
// (entryplan.PolicyName), not a number; see projectEntryPlan.
//
// Absence semantics follow the rest of this package: an unavailable field leaves NO ROW. A nil
// plan (enable_entry_plan off) leaves no ep_* row at all, not a DISABLED status.
//
// Write-only: nothing in this repository reads these rows back into a score, action, stage,
// ranking or price. entryplan_evidence_guard_test.go pins that structurally.

// Source labels. ep_policy names its own source because it is the one key not read from a
// top-level Plan field.
const (
	srcEntryPlan       = "entryplan.Plan"
	srcEntryPlanPolicy = "entryplan.EntryTrace.Policy.Name"
)

// The canonical ep_* key registry. Every key the projection can emit is one of these
// constants, and entryPlanEvidenceKeys lists them in emission order.
const (
	epKeyStatus       = "ep_status"
	epKeyRuleVersion  = "ep_rule_version"
	epKeyPolicy       = "ep_policy"
	epKeyZoneLow      = "ep_zone_low"
	epKeyZoneHigh     = "ep_zone_high"
	epKeyMaxChase     = "ep_max_chase"
	epKeyInvalidation = "ep_invalidation"
	epKeyTarget1      = "ep_target_1"
	epKeyTarget2      = "ep_target_2"
	epKeyRRRatio      = "ep_rr_ratio"
)

// entryPlanEvidenceKeys is the registry in the order projectEntryPlan emits it.
var entryPlanEvidenceKeys = []string{
	epKeyStatus, epKeyRuleVersion, epKeyPolicy,
	epKeyZoneLow, epKeyZoneHigh, epKeyMaxChase,
	epKeyInvalidation, epKeyTarget1, epKeyTarget2, epKeyRRRatio,
}

// Units. Prices are on the plan's own price basis, in TWD like every other price row here;
// the ratio is reward per unit of risk.
const (
	epUnitPrice = "TWD"
	epUnitRatio = "x"
)

// buildEntryPlanEvidence appends the ep_* rows for one watchlist entry.
//
// It is the ONE function outside the scanner bridge allowed to read WatchlistEntry.EntryPlan
// (see entryPlanFieldFunctions in internal/scanner/entryplan_symbol_guard_test.go). It does
// nothing but hand the plan to projectEntryPlan. On error nothing is appended.
func buildEntryPlanEvidence(c *collector, e scanner.WatchlistEntry, snap store.StockSnapshot) error {
	items, err := projectEntryPlan(e.EntryPlan, snap)
	if err != nil {
		return err
	}
	c.items = append(c.items, items...)
	return nil
}

// projectEntryPlan turns a published plan into evidence rows for snap. Pure and deterministic:
// fixed emission order, no map iteration, clock, randomness or I/O.
//
//   - nil plan → no rows, no error.
//   - the plan must describe the snapshot: Plan.Symbol == snap.Symbol and
//     Plan.AsOf == snap.TradingDate. Otherwise → no rows and an error (the caller logs it).
//     The plan is never re-dated to fit: a run whose trading date differs from the plan's last
//     bar (weekend, holiday, pre-open) records no ep_* row, and that is intended.
//   - there is no session gating: no clock, no market-close check. A same-symbol, same-date
//     plan is recorded whenever the run happens (TestEntryPlanPersistenceHasNoClockOrSessionReference
//     is the structural check).
//   - a plan whose Status is not a registered EntryStatus, whose RuleVersion is empty, or
//     which carries a published price/ratio that is not finite and strictly positive is
//     malformed → no rows and an error. Nothing is repaired or partially written.
//
// Field mapping (each key absent exactly when its source is absent):
//
//	ep_status        Plan.Status
//	ep_rule_version  Plan.RuleVersion
//	ep_policy        Plan.EntryTrace.Policy.Name   (absent when EntryTrace is nil or Name is "")
//	ep_zone_low      Plan.IdealEntry.Low           (absent when IdealEntry is nil)
//	ep_zone_high     Plan.IdealEntry.High          (absent when IdealEntry is nil)
//	ep_max_chase     *Plan.MaxChasePrice           (absent when nil)
//	ep_invalidation  Plan.Invalidation.Price       (absent when nil)
//	ep_target_1      Plan.Target1.Price            (absent when nil)
//	ep_target_2      Plan.Target2.Price            (absent when nil; the FINAL published value)
//	ep_rr_ratio      Plan.RiskReward.Ratio         (absent when nil)
func projectEntryPlan(p *entryplan.Plan, snap store.StockSnapshot) ([]store.Evidence, error) {
	if p == nil {
		return nil, nil
	}
	if p.Symbol != snap.Symbol || p.AsOf != snap.TradingDate {
		return nil, fmt.Errorf("entry plan identity %s/%s does not match snapshot %s/%s",
			p.Symbol, p.AsOf, snap.Symbol, snap.TradingDate)
	}
	if !p.Status.Valid() {
		return nil, fmt.Errorf("entry plan status %q is not a registered EntryStatus", p.Status)
	}
	if p.RuleVersion == "" {
		return nil, fmt.Errorf("entry plan carries no rule version")
	}

	var c collector
	var bad []string
	price := func(key string, v float64, unit string) {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			bad = append(bad, key)
			return
		}
		c.num(store.CategoryEntryPlan, key, v, unit, srcEntryPlan)
	}

	c.text(store.CategoryEntryPlan, epKeyStatus, string(p.Status), srcEntryPlan)
	c.text(store.CategoryEntryPlan, epKeyRuleVersion, p.RuleVersion, srcEntryPlan)
	// The policy NAME is a stable registry code (entryplan.AllPolicyNames) chosen by the regime
	// lookup. It is a label about which policy applied, not arithmetic, and it carries no
	// price. It lives only on EntryTrace.Policy because Plan has no top-level policy field.
	if tr := p.EntryTrace; tr != nil {
		c.text(store.CategoryEntryPlan, epKeyPolicy, string(tr.Policy.Name), srcEntryPlanPolicy)
	}
	if z := p.IdealEntry; z != nil {
		price(epKeyZoneLow, z.Low, epUnitPrice)
		price(epKeyZoneHigh, z.High, epUnitPrice)
	}
	if v := p.MaxChasePrice; v != nil {
		price(epKeyMaxChase, *v, epUnitPrice)
	}
	if v := p.Invalidation; v != nil {
		price(epKeyInvalidation, v.Price, epUnitPrice)
	}
	if v := p.Target1; v != nil {
		price(epKeyTarget1, v.Price, epUnitPrice)
	}
	if v := p.Target2; v != nil {
		price(epKeyTarget2, v.Price, epUnitPrice)
	}
	if v := p.RiskReward; v != nil {
		price(epKeyRRRatio, v.Ratio, epUnitRatio)
	}

	if len(bad) != 0 {
		return nil, fmt.Errorf("entry plan publishes non-positive or non-finite %v", bad)
	}
	return c.items, nil
}
