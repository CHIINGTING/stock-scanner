// Package options is R15 M5: the PCR and options-structure contract
// (docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §3.1, §6, §10.8).
//
// # It is a pure leaf, like M4
//
// Nothing here opens a socket or a database. It imports internal/derivatives/provider (the
// decoder), internal/derivatives/institutional (M4's ObservedMetric, Policy, baseline rules
// and percentile rule) and the standard library, and nothing else — in particular NOT
// internal/derivatives, which owns persistence and would drag database/sql behind every pure
// function here. The point-in-time reader that turns stored rows into these observations lives
// in internal/derivatives/optionshistory, the way M4's lives in internal/derivatives/history.
//
// # The two measurements are two TYPES
//
//	VolumePCR = Σ put TradingVolume / Σ call TradingVolume     trading activity
//	OIPCR     = Σ put OpenInterest  / Σ call OpenInterest      held exposure
//
// Neither is ever called just "PCR" (§10.8). They are different measurements of different
// things, and §10.4's institutional table shows how far apart activity and exposure can be —
// 投信 traded +1,785 lots on a day it held +76,174. VolumePCR and OIPCR are therefore distinct
// types and the compiler refuses to assign one to the other.
//
// Both are RATIOS in the domain — 1.05, never 105. The x100 is a presentation choice and
// happens once, at the boundary.
//
// # The session policy is per METRIC, and it is a data fact
//
// Measured on the committed fixture (TXO, 2026-09-04): every 盤後 row carries
// OpenInterest "-", which §3.1 makes ABSENT rather than zero. So an AFTER_HOURS OI PCR does
// not exist — computing it is 0/0 and zero-filling it fabricates a ratio from a session that
// reports no open interest — while volume needs BOTH sessions: 一般 alone gives 41,529 put
// against the 63,014 both sessions produce.
//
//	OI PCR      DAY only
//	Volume PCR  DAY, AFTER_HOURS, and their sum
//
// The combined volume figure is admissible because non-overlap is structural rather than
// assumed: every row carries exactly one TradingSession and M3 partitions on it, so the two
// sets are disjoint by construction. It is also the only figure that reconciles with the
// official PutCallRatio.
package options

import (
	"fmt"
	"math"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// FeatureVersion labels every value this package computes with the rule set that produced it.
const FeatureVersion = "R15-M5-v1"

// StructureRuleVersion labels the dominance rule separately: the M5 aggregation can move
// without the description of what a ratio MEANS moving, and the other way round.
const StructureRuleVersion = "R15-M5-structure-v1"

// PercentileRuleVersion labels M5's percentile rule set. The RULE is M4's — the tie handling
// comes from institutional.PercentileRatioAtOrBelow, the window and minimum from
// institutional.Policy — and only the PARTITIONING is M5's own (§10.8: metric x family x
// session x selection policy).
const PercentileRuleVersion = "R15-M5-percentile-v1"

// ── the two measurements ──────────────────────────────────────────────────────────────

// MetricType is which of §10.8's two PCRs a value is. It is part of every name, every key and
// every stored row, because "PCR" alone names neither.
type MetricType string

const (
	// MetricVolumePCR is Σ put volume / Σ call volume: TRADING ACTIVITY during the session.
	MetricVolumePCR MetricType = "VOLUME_PCR"
	// MetricOIPCR is Σ put OI / Σ call OI: EXPOSURE HELD at the close.
	MetricOIPCR MetricType = "OI_PCR"
)

// MetricTypes is the closed set, so a test can walk it and a new metric cannot be added
// without every switch here noticing.
var MetricTypes = []MetricType{MetricVolumePCR, MetricOIPCR}

// Valid reports whether m is one of the two.
func (m MetricType) Valid() bool { return m == MetricVolumePCR || m == MetricOIPCR }

// Semantics is §3's stored field for this metric: volume is FLOW, open interest is POSITION.
//
// It is what makes the numerator and denominator of a PCR say, on their own, whether they are
// traded lots or held lots — the same rule M4 applies to an institutional net.
func (m MetricType) Semantics() institutional.ValueSemantics {
	if m == MetricVolumePCR {
		return institutional.SemanticsFlow
	}
	return institutional.SemanticsPosition
}

// Column is the feed column this metric reads, for a reason line a human can check against the
// response.
func (m MetricType) Column() string {
	if m == MetricVolumePCR {
		return "Volume"
	}
	return "OpenInterest"
}

// valueOf is THE field selector, and the only place either metric decides which number it is
// made of.
//
// One function rather than two, so that reading the wrong column is a single visible edit
// rather than a plausible line inside a longer aggregation loop. On the 2026-09-04 fixture the
// two answers differ by an order of magnitude — TXO put volume 63,014 against put open
// interest 5,651 — so a swap changes every published figure while leaving every status
// AVAILABLE.
func (m MetricType) valueOf(r provider.OptionStrikeRow) *float64 {
	if m == MetricVolumePCR {
		return r.Volume
	}
	return r.OI
}

// ExcludesSettlingExpiry reports whether this metric drops the expiry whose final settlement
// day is the session being aggregated (§3.1).
//
// Open interest only, and the asymmetry is the rule: the settling expiry's lots are still
// printed in that day's report (126,594 for 202609F1) and are no longer live exposure, while
// its volume was genuinely traded and stays in the volume sum. Including it took the live
// 2026-09-04 open interest from 96,420 to 223,014 — 2.31x, with the status staying AVAILABLE.
func (m MetricType) ExcludesSettlingExpiry() bool { return m == MetricOIPCR }

// RequiresSettlementFeed reports whether this metric may not be published without a successful
// read of FinalSettlementPriceIndexOptions (§3.1).
//
// Open interest only, for the same asymmetry: without the settlement feed the OI aggregate
// would silently include a settled expiry and publish it as AVAILABLE. The volume half has a
// different dependency and is unaffected.
func (m MetricType) RequiresSettlementFeed() bool { return m == MetricOIPCR }

// SessionPolicy is §10.8's per-metric session policy, fixed by the data contract and never by
// a clock.
//
// Returned as a fresh slice so a caller cannot edit the policy by editing the answer.
func SessionPolicy(m MetricType) []string {
	if m == MetricVolumePCR {
		return []string{provider.SessionDay, provider.SessionAfterHours, provider.SessionCombined}
	}
	return []string{provider.SessionDay}
}

// SessionInPolicy reports whether this metric is PUBLISHED for this session.
//
// A session outside the policy is still computable — §10.8 requires the AFTER_HOURS OI PCR to
// answer INSUFFICIENT_DATA rather than to be unaskable — and the result carries
// WarnSessionOutsidePolicy so nobody renders it as a headline figure.
func SessionInPolicy(m MetricType, session string) bool {
	for _, s := range SessionPolicy(m) {
		if s == session {
			return true
		}
	}
	return false
}

// Sessions is the closed set of session values a request may carry.
//
// COMBINED here means the DISJOINT UNION of the two sessions' rows, not a third session the
// exchange reports: every row carries exactly one TradingSession and M3 partitions on it, so
// summing the two double-counts nothing.
var Sessions = []string{provider.SessionDay, provider.SessionAfterHours, provider.SessionCombined}

// ValidSession reports whether s is one of the three.
func ValidSession(s string) bool {
	for _, k := range Sessions {
		if k == s {
			return true
		}
	}
	return false
}

// componentSessions is the set of row-level sessions a request's session selects.
func componentSessions(session string) []string {
	if session == provider.SessionCombined {
		return []string{provider.SessionDay, provider.SessionAfterHours}
	}
	return []string{session}
}

// ── contract families ─────────────────────────────────────────────────────────────────

// ContractFamily is which series a result aggregates over. WEEKLY and MONTHLY come from M3's
// ClassifyExpiry — the code-shape rule — and there is no second classifier anywhere in M5.
type ContractFamily string

const (
	// FamilyAll is every live series of the product: the only family the exchange publishes
	// an official figure for, and therefore the only one the reconciliation gate can check.
	FamilyAll ContractFamily = "ALL"
	// FamilyWeekly is the W and F series.
	FamilyWeekly ContractFamily = "WEEKLY"
	// FamilyMonthly is the bare YYYYMM series.
	FamilyMonthly ContractFamily = "MONTHLY"
)

// ContractFamilies is the closed set.
var ContractFamilies = []ContractFamily{FamilyAll, FamilyWeekly, FamilyMonthly}

// Valid reports whether f is one of the three.
func (f ContractFamily) Valid() bool {
	for _, k := range ContractFamilies {
		if k == f {
			return true
		}
	}
	return false
}

// admits decides whether a row of this expiry KIND belongs in this family's aggregate, and
// names the exclusion reason when it does not.
//
// The three non-obvious rules, all from §3.1:
//
//	COMBINATION is excluded from EVERY family. "202609/202610" is one spread order across two
//	expiries, not a series; it is YYYYMM-prefixed, so a rule that only looked at the prefix
//	would file it under WEEKLY.
//
//	UNRESOLVED is COUNTED in ALL and excluded from WEEKLY and MONTHLY. §3.1 is explicit that
//	an unresolved code is never silently dropped from a total — the draft rule that dropped
//	the F series undershot the official open interest by 1,310 lots while staying AVAILABLE —
//	but it cannot be filed under a family whose name it does not carry.
//
//	Everything else is the kind the CODE says it is.
func (f ContractFamily) admits(kind string) (bool, string) {
	switch kind {
	case provider.ExpiryCombination:
		return false, ExcludedCombinationCode
	case provider.ExpiryUnresolved:
		if f == FamilyAll {
			return true, ""
		}
		return false, ExcludedUnresolvedExpiry
	case provider.ExpiryWeekly:
		return f == FamilyAll || f == FamilyWeekly, ExcludedOtherFamily
	case provider.ExpiryMonthly:
		return f == FamilyAll || f == FamilyMonthly, ExcludedOtherFamily
	}
	return false, ExcludedOtherFamily
}

// ── selection policy ──────────────────────────────────────────────────────────────────

// SelectionPolicy names how the contracts behind a figure were chosen. It is part of every
// key: a coverage figure or a percentile whose selection rule is unknown cannot be compared
// with another day's.
type SelectionPolicy string

// SelectProductFamilyExact is v1's only policy: exactly one product, matched on the feed's own
// Contract code, one contract family from the code-shape rule, and — for open interest only —
// the expiry settling on the session being aggregated removed.
const SelectProductFamilyExact SelectionPolicy = "PRODUCT_FAMILY_EXACT"

// ── the ratio's own status vocabulary ─────────────────────────────────────────────────

// RatioStatus is what a caller switches on for a PCR.
//
// It carries §6's eight FETCH outcomes through unchanged — the constants are conversions of
// institutional's, not a third spelling — plus the states only a RATIO can be in. §6.1 forbids
// merging vocabularies that answer different questions; this is the same legitimate meeting
// point MetricStatus is, one level further out: a reader has to be able to tell "the exchange
// has not published this yet" from "the denominator is zero" in one switch, and neither of the
// latter may ever be written into a data-health record. IsSourceStatus is how a caller checks.
type RatioStatus string

const (
	// RatioAvailable — there is a ratio. Includes an observed zero (put volume really was 0
	// against a positive call volume).
	RatioAvailable RatioStatus = RatioStatus(institutional.StatusAvailable)
	// RatioPartial — observed, and the aggregate may not be published: only one side is
	// present, or the snapshot behind it was PARTIAL. §6.2: a PCR computed over most of the
	// rows is not a PCR, and it would reconcile against nothing. The missing side is never
	// inferred from the present one.
	RatioPartial RatioStatus = RatioStatus(institutional.StatusPartial)
	// RatioMissing — not observed. Also the answer when the open-interest half has no
	// settlement-feed read behind it (§3.1) and when the official PutCallRatio that would
	// have checked it is itself absent.
	RatioMissing RatioStatus = RatioStatus(institutional.StatusMissing)
	// RatioStale — observed, older than this dataset expects.
	RatioStale RatioStatus = RatioStatus(institutional.StatusStale)
	// RatioError — the fetch or parse failed, or the computed figure disagreed with the
	// exchange's own.
	RatioError RatioStatus = RatioStatus(institutional.StatusError)
	// RatioNotAvailable — no authorized source exists.
	RatioNotAvailable RatioStatus = RatioStatus(institutional.StatusNotAvailable)
	// RatioNoSession — the exchange did not trade.
	RatioNoSession RatioStatus = RatioStatus(institutional.StatusNoSession)
	// RatioNotPublished — the session happened; this dataset is not out yet.
	RatioNotPublished RatioStatus = RatioStatus(institutional.StatusNotPublished)

	// RatioZeroDenominator — DERIVED. The call side was OBSERVED and is 0. The ratio is
	// absent; it is not +Inf, it is not 0 and it is not neutral.
	RatioZeroDenominator RatioStatus = "ZERO_DENOMINATOR"
	// RatioInsufficientData — DERIVED. A side is absent rather than zero, so there is
	// nothing to divide. This is the AFTER_HOURS open-interest answer (§10.8).
	RatioInsufficientData RatioStatus = "INSUFFICIENT_DATA"
	// RatioRolloverBoundary — DERIVED, changes only. The two readings are aggregates over
	// different contracts, so their difference is not a change in anything. OI falling to
	// zero at settlement is the contract ending, not the market collapsing.
	RatioRolloverBoundary RatioStatus = "ROLLOVER_BOUNDARY"
	// RatioInsufficientHistory — DERIVED, changes only. Fewer than N valid observations, so
	// ChangeN has no baseline. The horizon is never shortened to fit.
	RatioInsufficientHistory RatioStatus = RatioStatus(institutional.StatusInsufficientHistory)
	// RatioStaleBaseline — DERIVED, changes only. A baseline was found beyond the tolerance.
	RatioStaleBaseline RatioStatus = RatioStatus(institutional.StatusStaleBaseline)
	// RatioScopeMismatch — DERIVED, changes only. The two readings are not the same
	// measurement: a different product, family, session or selection policy.
	RatioScopeMismatch RatioStatus = "SCOPE_MISMATCH"
)

// DerivedRatioStatuses are the states that describe a COMPUTATION rather than a fetch.
var DerivedRatioStatuses = []RatioStatus{
	RatioZeroDenominator, RatioInsufficientData, RatioRolloverBoundary,
	RatioInsufficientHistory, RatioStaleBaseline, RatioScopeMismatch,
}

// SourceRatioStatuses is §6's closed set as RatioStatus values, derived from M4's list rather
// than retyped, so a status added there cannot go missing here.
var SourceRatioStatuses = func() []RatioStatus {
	out := make([]RatioStatus, 0, len(institutional.SourceStatuses))
	for _, s := range institutional.SourceStatuses {
		out = append(out, RatioStatus(s))
	}
	return out
}()

// RatioStatuses is every status a PCR or a PCR change can carry.
var RatioStatuses = append(append([]RatioStatus{}, SourceRatioStatuses...), DerivedRatioStatuses...)

// Valid reports whether s is one of the fourteen.
func (s RatioStatus) Valid() bool {
	for _, k := range RatioStatuses {
		if k == s {
			return true
		}
	}
	return false
}

// IsSourceStatus reports whether s is one of §6's fetch outcomes — i.e. whether it may be
// written back to a data-health record.
func (s RatioStatus) IsSourceStatus() bool {
	for _, k := range SourceRatioStatuses {
		if k == s {
			return true
		}
	}
	return false
}

// IsDerived reports whether s describes a computation rather than a fetch.
func (s RatioStatus) IsDerived() bool { return s.Valid() && !s.IsSourceStatus() }

// Numeric reports whether a reader may render a value in this state as a number. AVAILABLE
// only — PARTIAL included would be R14's INSUFFICIENT_DATA-rendered-as-0 again.
func (s RatioStatus) Numeric() bool { return s == RatioAvailable }

// FromSourceStatus converts M4's (and §6's) status into this vocabulary.
//
// An unknown token is an error, never a default: a new status silently becoming MISSING is how
// a fetch failure turns into "there was nothing there".
func FromSourceStatus(s institutional.MetricStatus) (RatioStatus, error) {
	r := RatioStatus(s)
	if !r.IsSourceStatus() {
		return "", fmt.Errorf("options: %q is not one of the source statuses %v",
			s, SourceRatioStatuses)
	}
	return r, nil
}

// ── the ratio itself ──────────────────────────────────────────────────────────────────

// RatioScale records that a PCR is in the ratio domain, not the percent domain.
//
// Stored on every value for the same reason PercentileScale is: 1.05 and 105 are the same
// evidence at two scales, and a renderer that multiplies twice produces 10,500 with no way to
// tell.
const RatioScale = "RATIO"

// ObservedRatio is a PCR that might not exist, and says which.
//
// The sibling of institutional.ObservedMetric, with the same invariant: A CALLER MUST NEVER
// NEED value == 0 TO LEARN WHETHER THERE IS DATA. Ratio is nil whenever Observed is false, an
// observed zero has Observed == true with a non-nil zero behind it, and Display is never "0"
// or "-" for a value that does not exist.
//
// It is not institutional.ObservedMetric because that type's unit is lots and its statuses are
// M4's. A ratio has neither.
type ObservedRatio struct {
	Metric MetricType `json:"metric"`
	// Observed is the single question "is there a ratio here". True for an observed zero.
	Observed bool `json:"observed"`
	// Ratio is meaningful only when Observed. A pointer, so a genuine 0 — no put volume at
	// all against real call volume — stays distinct from "no reading".
	Ratio  *float64    `json:"ratio,omitempty"`
	Status RatioStatus `json:"status"`
	// Display is what a UI prints, always. In the RATIO domain: the x100 belongs at the
	// presentation boundary and happens once, there.
	Display string `json:"display"`
	Scale   string `json:"scale"`
	Reason  string `json:"reason,omitempty"`
}

// Value returns the ratio and whether there is one. The only supported way to read it.
func (r ObservedRatio) Value() (float64, bool) {
	if !r.Observed || r.Ratio == nil {
		return 0, false
	}
	return *r.Ratio, true
}

// IsObservedZero distinguishes the reading a `== 0` check destroys: no puts traded at all,
// against real call volume, as opposed to every kind of absence.
func (r ObservedRatio) IsObservedZero() bool {
	v, ok := r.Value()
	return ok && v == 0
}

// Absent is the complement of Observed.
func (r ObservedRatio) Absent() bool { return !r.Observed }

// newObservedRatio builds a ratio that has a number.
//
// A non-finite input is rejected rather than formatted. §10.8: no Inf, no NaN, anywhere — not
// in the domain, not in JSON, not in SQLite — and "+Inf" renders enough like output to be
// believed. Everything upstream already refuses to divide by an observed zero; this is the
// backstop that makes the rule true by construction rather than by inspection.
func newObservedRatio(m MetricType, v float64) ObservedRatio {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return newAbsentRatio(m, RatioError,
			"the computed ratio is not finite, which can only come from a division this "+
				"package is supposed to have refused")
	}
	x := v
	return ObservedRatio{
		Metric:   m,
		Observed: true,
		Ratio:    &x,
		Status:   RatioAvailable,
		Display:  formatRatio(v),
		Scale:    RatioScale,
	}
}

// newAbsentRatio builds a ratio that has no number. status must not be AVAILABLE.
func newAbsentRatio(m MetricType, status RatioStatus, reason string) ObservedRatio {
	if status == RatioAvailable || !status.Valid() {
		reason = fmt.Sprintf("internal: absent ratio built with status %q (%s)", status, reason)
		status = RatioError
	}
	return ObservedRatio{
		Metric:   m,
		Observed: false,
		Status:   status,
		Display:  string(status),
		Scale:    RatioScale,
		Reason:   reason,
	}
}

// formatRatio renders a ratio in the ratio domain, with no percent sign anywhere.
func formatRatio(v float64) string {
	if v == 0 {
		// Catches negative zero, which %.4f renders as "-0.0000".
		return "0.0000"
	}
	return strings.TrimSpace(fmt.Sprintf("%.4f", v))
}
