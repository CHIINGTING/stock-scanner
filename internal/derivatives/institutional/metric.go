// Package institutional is M4: the FLOW / POSITION / CHANGE contract for TAIFEX institutional
// derivatives positions (docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §3, §10.4).
//
// # It is a pure leaf, deliberately
//
// Nothing here opens a socket or a database. It imports internal/derivatives/provider (the
// decoder, itself a leaf) and the standard library, and nothing else — in particular NOT
// internal/derivatives, which owns persistence and therefore drags in database/sql. The
// point-in-time query that produces the history slice these functions consume belongs to the
// persistence layer; this package takes the resulting []Observation and computes.
//
// That direction matters for whoever adds the persistence-backed history: a DB-backed reader
// that returns institutional.Observation must live in a package that imports BOTH this one and
// internal/derivatives (the way internal/derivatives/acquire already imports both), never in
// internal/derivatives itself, which would close an import cycle.
//
// The consequence is that §6's status vocabulary is spelled here as strings rather than by
// importing derivatives.Status. That duplication is bounded and pinned: contract_test.go
// asserts, against the real constants, that every fetch status maps to exactly one MetricStatus
// and that no status was added on either side without the other noticing. Tests may import what
// production code may not.
//
// # The three semantics are three TYPES
//
//	FLOW     = TradingVolume(Net)   what was traded this session
//	POSITION = OpenInterest(Net)    exposure held at the close
//	CHANGE   = POSITION(D) − POSITION(previous valid observation)
//
// They are TradingFlowContracts, OpenInterestPositionContracts and PositionChangeContracts,
// and the compiler refuses to assign one to another. §10.4 lists the substitutions that each
// produce a plausible number — FLOW standing in for POSITION, CHANGE differenced from FLOW,
// falling back to FLOW when POSITION is absent — and a shared `float64` named `net` makes every
// one of them a one-character edit. 投信 on 2026-09-04 traded +1,785 lots while holding
// +76,174: a factor of 43 between two numbers that a single `net` field would have made
// indistinguishable.
package institutional

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FeatureVersion labels every value this package computes with the rule-set that produced it,
// so a stored figure stays interpretable after the rules move. Same discipline as
// derivatives.FeatureVersion and valuation's QualityRuleVersion; it is a separate constant
// because the M4 rules can move without the schema moving.
const FeatureVersion = "R15-M4-v1"

// SessionCombined is the ONLY session this package accepts.
//
// The institutional futures feed carries Date, ContractCode, Item and the two semantics'
// columns — there is no session dimension in it, so the session policy is COMBINED, fixed by
// the data contract (§10.4). It is never chosen by reading a clock, and NewObservation rejects
// any other value rather than defaulting to it silently.
const SessionCombined = "COMBINED"

// UnitLots is the unit of every value in this package. Lots, never contract value: §10.4
// forbids mixing the two, and a unit that is carried rather than assumed is what lets a
// renderer notice.
const UnitLots = "lots"

// ValueSemantics is §3's stored field, not a naming convention: a value that reaches a reader
// through any path still says what it is.
type ValueSemantics string

const (
	// SemanticsFlow — what was traded during the session, TradingVolume(Net).
	SemanticsFlow ValueSemantics = "FLOW"
	// SemanticsPosition — open exposure at the close, OpenInterest(Net).
	SemanticsPosition ValueSemantics = "POSITION"
	// SemanticsChange — POSITION(D) − POSITION(previous valid observation). Derived, and
	// therefore never stored on a raw observation (§3).
	SemanticsChange ValueSemantics = "CHANGE"
)

// MetricStatus is what a caller switches on. It carries §6's eight FETCH outcomes through to
// the metric unchanged, plus the two states that only a DERIVATION can be in.
//
// §6.1 is emphatic that vocabularies answering different questions must not be merged, and
// this type is the one place they legitimately meet: a CHANGE that cannot be computed because
// the exchange has not published today's file (NOT_PUBLISHED) and one that cannot be computed
// because only two prior observations exist (INSUFFICIENT_HISTORY) are both "no number here",
// and a reader has to be able to tell them apart in one switch. What the type does NOT do is
// let a derivation state be written into a data-health record: SourceStatus() converts one way
// only, and IsSourceStatus() is how a caller checks before writing back.
type MetricStatus string

const (
	// StatusAvailable — observed, and there is a number. Includes an observed ZERO.
	StatusAvailable MetricStatus = "AVAILABLE"
	// StatusPartial — the observation exists but this component of it does not (the feed's
	// net column was absent for this contract). Value absent; never 0.
	StatusPartial MetricStatus = "PARTIAL"
	// StatusMissing — not observed, and waiting will not help.
	StatusMissing MetricStatus = "MISSING"
	// StatusStale — observed, but older than this dataset expects.
	StatusStale MetricStatus = "STALE"
	// StatusError — the fetch or parse failed.
	StatusError MetricStatus = "ERROR"
	// StatusNotAvailable — no authorized source exists at all.
	StatusNotAvailable MetricStatus = "NOT_AVAILABLE"
	// StatusNoSession — the exchange did not trade.
	StatusNoSession MetricStatus = "NO_SESSION"
	// StatusNotPublished — the session happened; the file is not out yet. Come back later,
	// as opposed to MISSING's this-will-not-appear.
	StatusNotPublished MetricStatus = "NOT_PUBLISHED"

	// StatusInsufficientHistory — DERIVED. Fewer than N valid observations exist, so ChangeN
	// has no baseline. The value is absent and the horizon is NOT shortened to fit: a
	// 4-observation span wearing the CHANGE_10OBS label is the defect this state prevents.
	StatusInsufficientHistory MetricStatus = "INSUFFICIENT_HISTORY"
	// StatusStaleBaseline — DERIVED. A baseline was found, but further back than the
	// configured tolerance, so the difference is not published as a number. The baseline
	// date, the observation gap and the skipped dates still travel with it, because "we
	// looked and the last usable session was 23 days ago" is the useful part.
	StatusStaleBaseline MetricStatus = "STALE_BASELINE"
)

// SourceStatuses is §6's closed set, in §6's order. Exported so contract_test.go can walk it
// against derivatives.Statuses: a status added on either side without the other fails there
// rather than defaulting to whatever the last case in a switch happened to be.
var SourceStatuses = []MetricStatus{
	StatusAvailable, StatusPartial, StatusMissing, StatusStale,
	StatusError, StatusNotAvailable, StatusNoSession, StatusNotPublished,
}

// DerivedStatuses are the two states that describe a computation rather than a fetch.
var DerivedStatuses = []MetricStatus{StatusInsufficientHistory, StatusStaleBaseline}

// MetricStatuses is every status a metric can carry.
var MetricStatuses = append(append([]MetricStatus{}, SourceStatuses...), DerivedStatuses...)

// Valid reports whether s is one of the ten.
func (s MetricStatus) Valid() bool {
	for _, k := range MetricStatuses {
		if k == s {
			return true
		}
	}
	return false
}

// IsSourceStatus reports whether s is one of §6's eight fetch outcomes — i.e. whether it may
// be written back to a data-health record.
func (s MetricStatus) IsSourceStatus() bool {
	for _, k := range SourceStatuses {
		if k == s {
			return true
		}
	}
	return false
}

// IsDerived reports whether s describes a computation rather than a fetch.
func (s MetricStatus) IsDerived() bool { return !s.IsSourceStatus() && s.Valid() }

// Usable reports whether the SNAPSHOT behind this status carried real rows — which is a
// different question from whether this metric has a number. A PARTIAL snapshot is usable and
// a PARTIAL metric is not renderable; both are true at once, on the same day, for the same
// institution.
func (s MetricStatus) Usable() bool { return s == StatusAvailable || s == StatusPartial }

// Numeric reports whether a reader may render a metric in this state as a number.
//
// AVAILABLE only. PARTIAL is deliberately false here even though derivatives.Status.Numeric()
// allows it, and the difference is the level: a PARTIAL SNAPSHOT has usable rows in it, but a
// PARTIAL METRIC is the case where this particular component of it was absent — there is no
// number to render. R14 spent a milestone on INSUFFICIENT_DATA rendered as 0.
func (s MetricStatus) Numeric() bool { return s == StatusAvailable }

// SourceStatus converts §6's vocabulary, as a string, into a MetricStatus.
//
// It takes a string rather than a derivatives.Status so this package stays a leaf; the two
// spellings are pinned against each other in contract_test.go. An unknown token is an error,
// never a default: a new status silently becoming MISSING is how a fetch failure turns into
// "there was nothing there".
func SourceStatus(s string) (MetricStatus, error) {
	m := MetricStatus(strings.TrimSpace(s))
	if !m.IsSourceStatus() {
		return "", fmt.Errorf("institutional: %q is not one of the source statuses %v",
			s, SourceStatuses)
	}
	return m, nil
}

// Provenance is where a value came from. It travels with every metric because a figure whose
// snapshot and revision are not recorded cannot be re-derived, and §5's point-in-time contract
// is only meaningful if a reader can say which revision it read.
type Provenance struct {
	// AsOf is the trading session the value describes (YYYY-MM-DD, Taipei).
	AsOf string `json:"as_of"`
	// Session is always COMBINED for this feed (§10.4).
	Session string `json:"session"`
	// Source is the endpoint identifier, verbatim.
	Source string `json:"source"`
	// SnapshotID is the derivative_snapshots row the value was derived from. 0 when the
	// caller did not supply one (a fixture, a test); never invented.
	SnapshotID int64 `json:"snapshot_id,omitempty"`
	// Revision is that snapshot's revision_no — which correction the reader saw.
	Revision int `json:"revision"`
	// FetchedAt is when THIS repository retrieved the bytes, not when the exchange
	// published them (§6.3).
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// ObservedMetric is a lot count that might not exist, and says which.
//
// It is a sibling of healthcheck.Value, not a new invention: the same {Status, *float64,
// pre-rendered Display} shape, for the same reason — a template holding {Status, Value} will
// eventually print Value and forget Status, and "0" beside an institution whose position was
// never published is a lie that looks like data. Display is always safe to print and is never
// "0" or "-" for a value that does not exist.
//
// What it adds over healthcheck.Value is the part §3 requires: Semantics, so a value that
// reaches a reader through any path still says whether it is traded lots or held lots, and
// Provenance, so it says which snapshot and revision produced it.
//
// The invariant that matters: A CALLER MUST NEVER NEED value == 0 TO LEARN WHETHER THERE IS
// DATA. Observed answers that, Value is nil whenever Observed is false, and an observed zero
// has Observed == true with a non-nil zero behind it.
type ObservedMetric struct {
	Semantics ValueSemantics `json:"semantics"`
	// Observed is the single question "is there a reading here". True for an observed zero.
	Observed bool `json:"observed"`
	// Value is meaningful only when Observed. A pointer, so a genuine 0 — a dealer really
	// did end the day flat — stays distinct from "no reading".
	Value  *float64     `json:"value,omitempty"`
	Status MetricStatus `json:"status"`
	// Display is what a UI prints, always. Never empty, never "0" for an absent value.
	Display string `json:"display"`
	Unit    string `json:"unit"`
	// Reason explains a non-AVAILABLE status in one line.
	Reason string `json:"reason,omitempty"`
	Provenance
	FeatureVersion string `json:"feature_version"`
}

// Lots returns the number and whether there is one. The only supported way to get at a value.
func (m ObservedMetric) Lots() (float64, bool) {
	if !m.Observed || m.Value == nil {
		return 0, false
	}
	return *m.Value, true
}

// IsObservedZero distinguishes the reading that a `== 0` check destroys: a real, published
// zero, as opposed to every kind of absence.
func (m ObservedMetric) IsObservedZero() bool {
	v, ok := m.Lots()
	return ok && v == 0
}

// Absent is the complement of Observed, spelled so a caller can read it either way round.
func (m ObservedMetric) Absent() bool { return !m.Observed }

// newObserved builds a metric that has a number.
//
// A non-finite input is rejected rather than formatted. NaN can only arise from a bug here —
// every input is an integer lot count — and "NaN" renders enough like output to be believed.
func newObserved(sem ValueSemantics, lots float64, p Provenance) ObservedMetric {
	if math.IsNaN(lots) || math.IsInf(lots, 0) {
		return newAbsent(sem, StatusError, "computed value is not finite", p)
	}
	v := lots
	return ObservedMetric{
		Semantics:      sem,
		Observed:       true,
		Value:          &v,
		Status:         StatusAvailable,
		Display:        formatLots(lots),
		Unit:           UnitLots,
		Provenance:     p,
		FeatureVersion: FeatureVersion,
	}
}

// newAbsent builds a metric that has no number. status must not be AVAILABLE.
func newAbsent(sem ValueSemantics, status MetricStatus, reason string, p Provenance) ObservedMetric {
	if status == StatusAvailable || !status.Valid() {
		// A caller that reached here has a bug. Recording it as ERROR with the reason beats
		// emitting an AVAILABLE metric with no number behind it, which is the exact failure
		// this type exists to make impossible.
		reason = fmt.Sprintf("internal: absent metric built with status %q (%s)", status, reason)
		status = StatusError
	}
	return ObservedMetric{
		Semantics:      sem,
		Observed:       false,
		Status:         status,
		Display:        string(status),
		Unit:           UnitLots,
		Reason:         reason,
		Provenance:     p,
		FeatureVersion: FeatureVersion,
	}
}

// NewObservedMetric builds a metric that has a number, for a caller outside this package.
//
// Exported for R15 M5 (internal/derivatives/options), which measures different quantities —
// summed put and call lots — but needs the SAME absence discipline: Value nil whenever
// Observed is false, Display never "0" for a value that does not exist, a non-finite input
// rejected rather than formatted. A second implementation of ObservedMetric's constructor is
// how one of those rules would end up applying to institutional lots and not to option lots.
func NewObservedMetric(sem ValueSemantics, lots float64, p Provenance) ObservedMetric {
	return newObserved(sem, lots, p)
}

// NewAbsentMetric builds a metric that has no number. status must not be AVAILABLE.
//
// Exported alongside NewObservedMetric and for the same reason. A caller that passes
// AVAILABLE gets an ERROR metric with the mistake recorded, exactly as an internal caller
// would — the check is in newAbsent, not at the call sites.
func NewAbsentMetric(sem ValueSemantics, status MetricStatus, reason string, p Provenance) ObservedMetric {
	return newAbsent(sem, status, reason, p)
}

// TradingFlowContracts is FLOW: lots TRADED during the session, from TradingVolume(Net).
//
// A distinct type rather than a field, so `position = flow` does not compile. §10.4's forbidden
// list is almost entirely made of assignments that this type makes unwriteable.
type TradingFlowContracts struct{ ObservedMetric }

// OpenInterestPositionContracts is POSITION: lots HELD at the close, from OpenInterest(Net).
type OpenInterestPositionContracts struct{ ObservedMetric }

// PositionChangeContracts is CHANGE: POSITION(D) − POSITION(previous valid observation).
//
// Never POSITION differenced against FLOW, and never FLOW differenced against FLOW — the
// second is §10.4's named defect, and it produces a number that looks like a position change
// on every single day.
type PositionChangeContracts struct{ ObservedMetric }

func flowOf(m ObservedMetric) TradingFlowContracts {
	m.Semantics = SemanticsFlow
	return TradingFlowContracts{m}
}

func positionOf(m ObservedMetric) OpenInterestPositionContracts {
	m.Semantics = SemanticsPosition
	return OpenInterestPositionContracts{m}
}

func changeOf(m ObservedMetric) PositionChangeContracts {
	m.Semantics = SemanticsChange
	return PositionChangeContracts{m}
}

// Valid reports whether the wrapper and the stored semantics agree.
//
// The constructors guarantee it; this exists so a value that arrived by deserialisation — where
// the type is gone and only the field survives — can still be checked.
func (f TradingFlowContracts) Valid() error { return checkSemantics(f.Semantics, SemanticsFlow) }

// Valid reports whether the wrapper and the stored semantics agree.
func (p OpenInterestPositionContracts) Valid() error {
	return checkSemantics(p.Semantics, SemanticsPosition)
}

// Valid reports whether the wrapper and the stored semantics agree.
func (c PositionChangeContracts) Valid() error { return checkSemantics(c.Semantics, SemanticsChange) }

func checkSemantics(got, want ValueSemantics) error {
	if got != want {
		return fmt.Errorf("institutional: value carries semantics %q inside a %s container",
			got, want)
	}
	return nil
}

// formatLots renders a lot count with thousands separators and the unit.
//
// Local rather than borrowed from internal/healthcheck: that package imports internal/scanner,
// so reusing its formatter would pull the decision path into a leaf to spell a comma.
func formatLots(v float64) string {
	if v == 0 {
		// Catches negative zero, which %.0f renders as "-0". A dealer who ended the day
		// flat is not short.
		return "0 " + UnitLots
	}
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	out := b.String()
	if neg {
		out = "-" + out
	}
	return out + " " + UnitLots
}
