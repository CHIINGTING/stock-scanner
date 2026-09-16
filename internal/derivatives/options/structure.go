package options

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Contract identity, the rollover rule, and what a PCR is allowed to SAY.

// ── contract identity ─────────────────────────────────────────────────────────────────

// RolloverRule names how contract identity is compared. Carried on every change so a stored
// figure says which rule produced it.
//
// STRICT_EXPIRY_SET is the only rule in v1: two aggregates are the same measurement only if
// they are over exactly the same set of expiry codes. §10.8 says a PCR change across a
// contract-identity change is not a change, and does not define identity for a family that
// holds several series at once — the fixture's MONTHLY family holds five. The strict reading
// is chosen deliberately: it errs towards ROLLOVER_BOUNDARY (a stated absence) rather than
// towards a difference between two different populations, and the added and removed codes
// travel with the answer so the reason is visible rather than inferred.
//
// The cost is real and is stated rather than hidden: WEEKLY and ALL will report
// ROLLOVER_BOUNDARY on any day a series is listed or settles, which for the F-series is often.
// §0 licenses that — a layer that only informs may say "no number" for long stretches — and
// M6, which reads this metadata to tell an expiring wall from a collapsing one, needs the
// conservative answer rather than the available one.
const RolloverRule = "STRICT_EXPIRY_SET"

// ContractIdentity is WHAT a figure is an aggregate over.
type ContractIdentity struct {
	Product string          `json:"product"`
	Family  ContractFamily  `json:"family"`
	Session string          `json:"session"`
	Policy  SelectionPolicy `json:"selection_policy"`
	Metric  MetricType      `json:"metric"`
	// Expiries are the series in scope for this metric, sorted. For open interest this is
	// AFTER the settling expiry has been removed: a contract that stopped being live
	// exposure at this session's close is not part of what the figure measures.
	Expiries []string `json:"expiries"`
	// ExcludedExpiries are the series deliberately left out — today, only the settling one.
	ExcludedExpiries []string `json:"excluded_expiries,omitempty"`
	// Distances are days to expiry where a settlement date was supplied, and nothing where
	// it was not. Never inferred from the code's shape.
	Distances []ExpiryDistance `json:"distances,omitempty"`
	Rule      string           `json:"rollover_rule"`
}

// Same reports whether two aggregates are over the same contracts.
func (c ContractIdentity) Same(o ContractIdentity) bool {
	if c.Product != o.Product || c.Family != o.Family || c.Session != o.Session ||
		c.Policy != o.Policy || c.Metric != o.Metric {
		return false
	}
	if len(c.Expiries) != len(o.Expiries) {
		return false
	}
	for i := range c.Expiries {
		if c.Expiries[i] != o.Expiries[i] {
			return false
		}
	}
	return true
}

// Diff names what entered and what left, so ROLLOVER_BOUNDARY is never just a refusal.
func (c ContractIdentity) Diff(prev ContractIdentity) (added, removed []string) {
	was := map[string]bool{}
	for _, e := range prev.Expiries {
		was[e] = true
	}
	now := map[string]bool{}
	for _, e := range c.Expiries {
		now[e] = true
	}
	for _, e := range c.Expiries {
		if !was[e] {
			added = append(added, e)
		}
	}
	for _, e := range prev.Expiries {
		if !now[e] {
			removed = append(removed, e)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// newIdentity records what an aggregate was over, and how far its series are from settling.
func newIdentity(req ObservationRequest, m MetricType, expiries []string, sel selection,
	excluded []string) ContractIdentity {

	kinds := map[string]string{}
	for i, r := range sel.rows {
		kinds[r.ExpiryCode] = sel.kinds[i]
	}
	id := ContractIdentity{
		Product:          req.Product,
		Family:           req.Family,
		Session:          req.Session,
		Policy:           SelectProductFamilyExact,
		Metric:           m,
		Expiries:         append([]string{}, expiries...),
		ExcludedExpiries: append([]string{}, excluded...),
		Rule:             RolloverRule,
	}
	for _, code := range id.Expiries {
		d := ExpiryDistance{ExpiryCode: code, ExpiryKind: kinds[code]}
		if settle, ok := req.SettlementDates[code]; ok && settle != "" {
			d.SettlementDate = settle
			if days, err := calendarDaysBetween(req.TradingDate, settle); err == nil {
				n := days
				d.Days = &n
			}
		}
		id.Distances = append(id.Distances, d)
	}
	return id
}

func calendarDaysBetween(from, to string) (int, error) {
	a, err := time.Parse(institutional.DateLayout, from)
	if err != nil {
		return 0, err
	}
	b, err := time.Parse(institutional.DateLayout, to)
	if err != nil {
		return 0, err
	}
	return int(b.Sub(a).Hours() / 24), nil
}

// ── what a PCR is allowed to say ──────────────────────────────────────────────────────

// StructureLabel is §10.8's vocabulary, and it is deliberately not a direction.
//
// Open interest says how many calls and puts are OPEN. It does not say who is long or short —
// the same OI is consistent with buyers or with writers dominating, and the feed carries no
// long/short identity for it at all. So M5 describes structure and never BULLISH or BEARISH,
// and produces no validated direction of any kind.
type StructureLabel string

const (
	// StructurePutOIDominant — more put open interest than call.
	StructurePutOIDominant StructureLabel = "PUT_OI_DOMINANT"
	// StructureBalanced — the two sides are within the band of each other.
	StructureBalanced StructureLabel = "BALANCED"
	// StructureCallOIDominant — more call open interest than put.
	StructureCallOIDominant StructureLabel = "CALL_OI_DOMINANT"
	// StructureUndetermined — there is no ratio, so there is no structure. Never BALANCED:
	// balanced is a reading, and this is the absence of one. R14 spent a milestone on the
	// equivalent confusion.
	StructureUndetermined StructureLabel = "UNDETERMINED"
	// StructureNotApplicable — the metric is not open interest. §10.8's vocabulary describes
	// held exposure; applying it to traded volume would label an activity figure with a
	// structural claim it cannot support.
	StructureNotApplicable StructureLabel = "NOT_APPLICABLE"
)

// StructureLabels is the closed set.
var StructureLabels = []StructureLabel{
	StructurePutOIDominant, StructureBalanced, StructureCallOIDominant,
	StructureUndetermined, StructureNotApplicable,
}

// DefaultBalancedBand is the half-width of the BALANCED band, as a fraction: a ratio between
// 1/(1+band) and 1+band is balanced.
//
// 0.10 is HEURISTIC and NOT BACKTEST-FITTED. It exists so that "BALANCED" means something
// stated rather than "not exactly 1.0", and it is symmetric in the ratio's own multiplicative
// domain — an additive band would call 1.10 balanced and 0.91 dominant, which are the same
// imbalance seen from the two sides.
const DefaultBalancedBand = 0.10

// InterpretationType and ValidationStatus values, for readings M5 carries but does not assert.
const (
	// InterpretationHeuristic — a folk reading with a plausible opposite. Never a finding.
	InterpretationHeuristic = "HEURISTIC"
	// ValidationShadow — carried for research, never validated, never a decision input.
	ValidationShadow = "SHADOW"
)

// Interpretation is a traditional reading of a structure, WITH the contrary reading beside it.
//
// §10.8: "High put OI means put-selling support" and "high call OI means call-writing
// resistance" are folk readings with a plausible opposite; if either is carried at all it is
// HEURISTIC and SHADOW with the contrary reading stated beside it. Contrary is therefore a
// required field, not an optional one — an interpretation without its opposite is the thing
// this type exists to prevent.
type Interpretation struct {
	Reading  string `json:"reading"`
	Contrary string `json:"contrary"`
	Type     string `json:"interpretation_type"`
	Status   string `json:"validation_status"`
}

// Structure is what a PCR is allowed to say about itself.
type Structure struct {
	Label StructureLabel `json:"label"`
	Band  float64        `json:"balanced_band"`
	// Interpretations are the folk readings, each with its opposite. Empty unless there is a
	// label to interpret.
	Interpretations []Interpretation `json:"interpretations,omitempty"`
	Reason          string           `json:"reason,omitempty"`
	RuleVersion     string           `json:"rule_version"`
}

// describeStructure labels an open-interest ratio and refuses to label anything else.
func describeStructure(m MetricType, r ObservedRatio) Structure {
	s := Structure{Band: DefaultBalancedBand, RuleVersion: StructureRuleVersion}
	if m != MetricOIPCR {
		s.Label = StructureNotApplicable
		s.Band = 0
		s.Reason = "PUT_OI_DOMINANT / BALANCED / CALL_OI_DOMINANT describes HELD exposure; " +
			string(m) + " measures trading activity and is not labelled with it"
		return s
	}
	v, ok := r.Value()
	if !ok {
		s.Label = StructureUndetermined
		s.Reason = "there is no ratio (" + string(r.Status) + "), so there is no structure — " +
			"and an absent reading is not a balanced one"
		return s
	}
	switch {
	case v > 1+DefaultBalancedBand:
		s.Label = StructurePutOIDominant
	case v < 1/(1+DefaultBalancedBand):
		s.Label = StructureCallOIDominant
	default:
		s.Label = StructureBalanced
	}
	s.Interpretations = interpretationsFor(s.Label)
	return s
}

// interpretationsFor carries the folk readings, each beside its opposite.
//
// The wording is deliberate: no sentence here contains a direction word, because a reader who
// skims one line of a shadow panel will remember the direction and not the caveat. The feed
// carries no long/short identity for open interest, so neither reading is observed and both
// are stated as claims about what people say rather than about the market.
func interpretationsFor(label StructureLabel) []Interpretation {
	switch label {
	case StructurePutOIDominant:
		return []Interpretation{{
			Reading: "a common reading is that put open interest concentrated below the " +
				"market has been WRITTEN, and that its writers defend those strikes",
			Contrary: "the same open interest is equally consistent with puts having been " +
				"BOUGHT as protection, which implies demand for downside cover rather than " +
				"support; the feed carries no long/short identity, so neither is observed",
			Type:   InterpretationHeuristic,
			Status: ValidationShadow,
		}}
	case StructureCallOIDominant:
		return []Interpretation{{
			Reading: "a common reading is that call open interest concentrated above the " +
				"market has been WRITTEN, and that its writers cap those strikes",
			Contrary: "the same open interest is equally consistent with calls having been " +
				"BOUGHT for upside participation, which implies demand rather than a cap; " +
				"the feed carries no long/short identity, so neither is observed",
			Type:   InterpretationHeuristic,
			Status: ValidationShadow,
		}}
	case StructureBalanced:
		return []Interpretation{{
			Reading: "a common reading is that neither side of the book dominates",
			Contrary: "equal open interest on the two sides is also what a market with large " +
				"and opposite positioning on both sides looks like; the aggregate cannot " +
				"distinguish the two",
			Type:   InterpretationHeuristic,
			Status: ValidationShadow,
		}}
	}
	return nil
}

// ── warnings ──────────────────────────────────────────────────────────────────────────

// Warning codes. A closed set, because a caller cannot switch on prose.
const (
	// WarnSessionOutsidePolicy — this metric is not PUBLISHED for this session (§10.8).
	WarnSessionOutsidePolicy = "SESSION_OUTSIDE_METRIC_POLICY"
	// WarnOneSideOnly — one side of the ratio has no rows.
	WarnOneSideOnly = "ONE_SIDE_ONLY"
	// WarnZeroDenominator — the call side was observed and is zero.
	WarnZeroDenominator = "ZERO_DENOMINATOR"
	// WarnSettlementFeedMissing — the open-interest half has no settlement read behind it.
	WarnSettlementFeedMissing = "SETTLEMENT_FEED_MISSING"
	// WarnPartialSnapshot — rows were rejected, so §6.2 forbids publishing the aggregate.
	WarnPartialSnapshot = "PARTIAL_SNAPSHOT"
	// WarnSessionAbsent — a session this figure should span carried no rows.
	WarnSessionAbsent = "SESSION_ABSENT"
	// WarnUnresolvedExpiries — codes that parse as neither monthly nor weekly were seen.
	WarnUnresolvedExpiries = "UNRESOLVED_EXPIRIES"
	// WarnRolloverBoundary — a change spans a contract-identity change.
	WarnRolloverBoundary = "ROLLOVER_BOUNDARY"
	// WarnInsufficientSample — a percentile could not be published from the reference set.
	WarnInsufficientSample = "INSUFFICIENT_SAMPLE"
	// WarnReconciliationMismatch — the computed figure disagreed with the exchange's own.
	WarnReconciliationMismatch = "RECONCILIATION_MISMATCH"
	// WarnReconciliationUnchecked — no official figure was available to check against.
	WarnReconciliationUnchecked = "RECONCILIATION_UNCHECKED"
)

// Warning is one condition a reader must see before acting on a number.
type Warning struct {
	Code    string     `json:"code"`
	Metric  MetricType `json:"metric,omitempty"`
	Subject string     `json:"subject,omitempty"`
	Message string     `json:"message"`
}

// ── reconciliation against the exchange's own figure ──────────────────────────────────

// Reconciliation outcomes, matching options_aggregate.reconciled plus the two cases a stored
// enum of three cannot express.
const (
	// ReconciledMatched — the computed figures equal the exchange's, within tolerance.
	ReconciledMatched = "MATCHED"
	// ReconciledMismatch — they do not. §3.1: the aggregate is ERROR with the discrepancy
	// recorded, never a number.
	ReconciledMismatch = "MISMATCH"
	// ReconciledUnchecked — no check has been run yet.
	ReconciledUnchecked = "UNCHECKED"
	// ReconciledUnavailable — the official figure was not available. For open interest that
	// makes the aggregate MISSING (§3.1): an unchecked number that happens to be right is
	// indistinguishable from one that happens to be wrong.
	ReconciledUnavailable = "UNAVAILABLE"
	// ReconciledNotApplicable — the exchange publishes no figure at this granularity. It
	// publishes one product-wide pair, so a WEEKLY or MONTHLY aggregate has nothing to be
	// compared against and saying UNCHECKED would read as an omission.
	ReconciledNotApplicable = "NOT_APPLICABLE"
)

// ReconciliationTolerance is the absolute lot tolerance for the gate. Zero: both sides are
// integer lot counts summed from the same exchange's rows, so anything but an exact match is
// a rule disagreeing, not a rounding.
const ReconciliationTolerance = 0.0

// Reconciliation is the gate's record. §10.1 makes it an acceptance gate rather than a sanity
// check: it is the only thing standing between §3.1's aggregation rules and a
// plausible-looking wrong number.
type Reconciliation struct {
	Status       string   `json:"status"`
	OfficialPut  *float64 `json:"official_put,omitempty"`
	OfficialCall *float64 `json:"official_call,omitempty"`
	ComputedPut  *float64 `json:"computed_put,omitempty"`
	ComputedCall *float64 `json:"computed_call,omitempty"`
	Tolerance    float64  `json:"tolerance"`
	Reason       string   `json:"reason,omitempty"`
}

func unreconciled(m MetricType, f ContractFamily) Reconciliation {
	if f != FamilyAll {
		return Reconciliation{
			Status:    ReconciledNotApplicable,
			Tolerance: ReconciliationTolerance,
			Reason: "the exchange publishes one product-wide put/call pair, so a " +
				string(f) + " aggregate has no official counterpart to be checked against",
		}
	}
	return Reconciliation{
		Status:    ReconciledUnchecked,
		Tolerance: ReconciliationTolerance,
		Reason:    "no reconciliation has been run against the official " + provider.SourcePutCallRatio,
	}
}

// Reconcile checks a product-wide aggregate against the exchange's own figure and returns the
// PCR the gate allows to be published.
//
// Three outcomes and none of them is "publish it anyway":
//
//   - the figures match         → unchanged, MATCHED recorded
//   - the figures disagree      → the ratio becomes ERROR with the discrepancy recorded (§3.1)
//   - no official figure at all → open interest becomes MISSING, because its backstop is gone
//     and an unchecked number that happens to be right is indistinguishable from one that
//     happens to be wrong. Volume is left as computed and marked UNCHECKED: §3.1 makes the
//     MISSING ruling for the open-interest half specifically, whose OTHER dependency (the
//     settlement feed) is the reason it can be wrong by 2.31x without anything noticing.
//
// A non-ALL family is NOT_APPLICABLE and is returned untouched.
func Reconcile(p PCR, official *provider.PutCallRatioRow) PCR {
	if p.Key.Family != FamilyAll {
		p.Reconciliation = unreconciled(p.Metric, p.Key.Family)
		return p
	}
	rec := Reconciliation{Tolerance: ReconciliationTolerance}
	if v, ok := p.Numerator.Lots(); ok {
		rec.ComputedPut = &v
	}
	if v, ok := p.Denominator.Lots(); ok {
		rec.ComputedCall = &v
	}

	officialPut, officialCall := officialSides(p.Metric, official)
	rec.OfficialPut, rec.OfficialCall = officialPut, officialCall

	if official == nil || officialPut == nil || officialCall == nil {
		rec.Status = ReconciledUnavailable
		rec.Reason = "the official " + provider.SourcePutCallRatio + " figures for " +
			p.TradingDate + " were not available, so this aggregate has no backstop"
		p.Reconciliation = rec
		p.Warnings = append(p.Warnings, Warning{
			Code: WarnReconciliationUnchecked, Metric: p.Metric, Message: rec.Reason,
		})
		if p.Metric == MetricOIPCR {
			p.Ratio = newAbsentRatio(p.Metric, RatioMissing, rec.Reason)
			p.Status = p.Ratio.Status
			p.Reason = rec.Reason
			p.Structure = describeStructure(p.Metric, p.Ratio)
		}
		sortWarnings(p.Warnings)
		return p
	}

	if rec.ComputedPut == nil || rec.ComputedCall == nil {
		rec.Status = ReconciledUnchecked
		rec.Reason = "the aggregate has no numerator or no denominator, so there is nothing " +
			"to compare with the official figure"
		p.Reconciliation = rec
		return p
	}

	putGap := math.Abs(*rec.ComputedPut - *officialPut)
	callGap := math.Abs(*rec.ComputedCall - *officialCall)
	if putGap > ReconciliationTolerance || callGap > ReconciliationTolerance {
		rec.Status = ReconciledMismatch
		rec.Reason = fmt.Sprintf(
			"computed put=%v call=%v against official put=%v call=%v for %s; §3.1 records the "+
				"discrepancy and publishes no number",
			*rec.ComputedPut, *rec.ComputedCall, *officialPut, *officialCall, p.TradingDate)
		p.Reconciliation = rec
		p.Ratio = newAbsentRatio(p.Metric, RatioError, rec.Reason)
		p.Status = p.Ratio.Status
		p.Reason = rec.Reason
		p.Structure = describeStructure(p.Metric, p.Ratio)
		p.Warnings = append(p.Warnings, Warning{
			Code: WarnReconciliationMismatch, Metric: p.Metric, Message: rec.Reason,
		})
		sortWarnings(p.Warnings)
		return p
	}
	rec.Status = ReconciledMatched
	p.Reconciliation = rec
	return p
}

// officialSides picks the two columns this metric reconciles against. Volume against volume,
// open interest against open interest — the swap this function exists to make a single visible
// line is the same one MetricType.valueOf guards one level down.
func officialSides(m MetricType, r *provider.PutCallRatioRow) (put, call *float64) {
	if r == nil {
		return nil, nil
	}
	if m == MetricVolumePCR {
		return r.PutVolume, r.CallVolume
	}
	return r.PutOI, r.CallOI
}
