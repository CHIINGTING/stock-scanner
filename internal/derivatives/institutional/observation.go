package institutional

import (
	"errors"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/taifexfeed"
)

// One institution's reading for one trading date, one session, one instrument scope.
//
// This is the unit the whole package is built on: the point-in-time query in the persistence
// layer produces a series of these, and everything below — CHANGE, the horizons, alignment,
// the three-institution total — is a pure function of that series.

// The three institutions the feed reports, re-exported from the decoder so a caller building a
// view does not have to reach into provider for a label. 外資及陸資 is the exchange's own
// name; R15 never renames it, and never calls the non-institutional remainder 散戶 (§11).
const (
	InstitutionDealer  = provider.InstitutionDealer
	InstitutionTrust   = provider.InstitutionTrust
	InstitutionForeign = provider.InstitutionForeign
)

// RequiredInstitutions is the closed set a three-institution total needs, in the exchange's own
// order.
var RequiredInstitutions = []string{InstitutionDealer, InstitutionTrust, InstitutionForeign}

// DateLayout is the only date format this package accepts or emits: a Taipei calendar date.
// No clock is ever read — every date arrives as data.
const DateLayout = "2006-01-02"

// Errors that mean a caller combined things that do not combine. Each is a distinct sentinel
// because the caller's fix is different for each, and "invalid input" is not an instruction.
var (
	// ErrSessionMismatch — two observations from different sessions. Also returned for a
	// session other than COMBINED, which this feed does not have (§10.4).
	ErrSessionMismatch = errors.New("institutional: trading sessions differ")
	// ErrDatasetMismatch — two observations from different endpoints.
	ErrDatasetMismatch = errors.New("institutional: datasets differ")
	// ErrDuplicateObservation — two observations for the same trading date in one series, or
	// two feed rows for the same (product, institution, semantics, side). Either way the
	// value is ambiguous, and summing them is the failure mode.
	ErrDuplicateObservation = errors.New("institutional: duplicate observation")
	// ErrLookAhead — a history entry dated on or after the observation being computed. §5:
	// every query is bounded by as_of, and a baseline resolver that quietly tolerates a
	// future row would make a look-ahead invisible.
	ErrLookAhead = errors.New("institutional: history contains an observation at or after the current date")
	// ErrBadDate — a date that is not a YYYY-MM-DD calendar date.
	ErrBadDate = errors.New("institutional: date must be YYYY-MM-DD")
)

// ObservationRequest is everything about a reading that does NOT come from the rows: which
// institution and scope were asked for, and what the snapshot's own fetch status was.
type ObservationRequest struct {
	TradingDate string
	// Session must be COMBINED. Supplied rather than defaulted so a caller that has a
	// session value from somewhere else cannot pass it through unnoticed.
	Session     string
	Dataset     string
	Institution string
	Scope       InstrumentScope
	// SourceStatus is §6's status of the SNAPSHOT these rows came from. It is an input, not
	// something this package can work out: NOT_PUBLISHED and NO_SESSION both arrive as zero
	// rows, and only the acquisition layer knows which one happened.
	SourceStatus MetricStatus
	SnapshotID   int64
	Revision     int
	FetchedAt    time.Time
}

// Observation is one institution's FLOW and POSITION for one date/session/scope, plus the
// provenance and coverage that make it re-derivable.
//
// The two lot counts are pointers and are the feed's own NET columns. They are never derived
// from long − short: §3 and the existing internal/market/provider both take NET from the
// official column so that a column-layout change surfaces as a mismatch instead of being
// papered over by arithmetic.
type Observation struct {
	TradingDate string          `json:"trading_date"`
	Session     string          `json:"session"`
	Dataset     string          `json:"dataset"`
	Institution string          `json:"institution"`
	Scope       InstrumentScope `json:"scope"`
	// SourceStatus is the snapshot's fetch outcome, carried so a reader can tell a day the
	// exchange did not trade from a day nobody fetched.
	SourceStatus MetricStatus `json:"source_status"`
	// FlowNetLots is TradingVolume(Net). nil means the column was absent — not zero.
	FlowNetLots *float64 `json:"flow_net_lots,omitempty"`
	// PositionNetLots is OpenInterest(Net). nil means absent — not zero, and never filled
	// from FlowNetLots (§10.4's "falling back to FLOW when POSITION is absent").
	PositionNetLots *float64      `json:"position_net_lots,omitempty"`
	SnapshotID      int64         `json:"snapshot_id,omitempty"`
	Revision        int           `json:"revision"`
	FetchedAt       time.Time     `json:"fetched_at,omitempty"`
	Coverage        ScopeCoverage `json:"coverage"`
}

// NewObservation reads ONE institution's net FLOW and net POSITION out of a decoded response.
//
// The rows are M3's output: six per feed row, FLOW x {LONG, SHORT, NET} and POSITION x the
// same. This picks the two NET rows for the requested scope and institution and ignores the
// rest — it does not add LONG and SHORT, and it does not fall back to long − short when NET is
// absent. An absent NET stays absent.
func NewObservation(req ObservationRequest, rows []provider.InstitutionalRow) (Observation, error) {
	if _, err := time.Parse(DateLayout, req.TradingDate); err != nil {
		return Observation{}, fmt.Errorf("%w: trading date %q", ErrBadDate, req.TradingDate)
	}
	if req.Session != SessionCombined {
		return Observation{}, fmt.Errorf(
			"%w: this feed has no session dimension, so its session policy is %s, not %q",
			ErrSessionMismatch, SessionCombined, req.Session)
	}
	if req.Institution == "" {
		return Observation{}, errors.New("institutional: an observation needs an institution")
	}
	if req.Dataset == "" {
		return Observation{}, errors.New("institutional: an observation needs a dataset")
	}
	if err := req.Scope.Valid(); err != nil {
		return Observation{}, err
	}
	if !req.SourceStatus.IsSourceStatus() {
		return Observation{}, fmt.Errorf(
			"institutional: source status %q is not one of %v — a derived status describes a "+
				"computation and cannot be a snapshot's fetch outcome (§6.1)",
			req.SourceStatus, SourceStatuses)
	}

	sel := selectScope(rows, req.Scope, req.Institution, req.TradingDate)

	obs := Observation{
		TradingDate:  req.TradingDate,
		Session:      req.Session,
		Dataset:      req.Dataset,
		Institution:  req.Institution,
		Scope:        req.Scope,
		SourceStatus: req.SourceStatus,
		SnapshotID:   req.SnapshotID,
		Revision:     req.Revision,
		FetchedAt:    req.FetchedAt,
		Coverage:     sel.coverage,
	}

	// A status that says nothing was observed, over rows that plainly were, is a
	// contradiction rather than a preference. Silently trusting either half would produce a
	// NO_SESSION day carrying a position, or an AVAILABLE day carrying nothing.
	if !req.SourceStatus.Usable() && len(sel.rows) > 0 {
		return Observation{}, fmt.Errorf(
			"institutional: source status %s carries %d in-scope rows for %s %s %s — a status "+
				"saying nothing was observed cannot travel with an observation",
			req.SourceStatus, len(sel.rows), req.Institution, req.Scope, req.TradingDate)
	}

	seen := map[string]bool{}
	for _, r := range sel.rows {
		if r.Side != provider.SideNet {
			continue
		}
		key := string(r.Semantics) + "|" + r.Side
		if seen[key] {
			return Observation{}, fmt.Errorf(
				"%w: %s %s %s carries two %s NET rows; summing them would double-count and "+
					"choosing one would be arbitrary",
				ErrDuplicateObservation, req.Institution, req.Scope, req.TradingDate, r.Semantics)
		}
		seen[key] = true
		switch r.Semantics {
		case provider.SemanticsFlow:
			obs.FlowNetLots = copyLots(r.Lots)
		case provider.SemanticsPosition:
			obs.PositionNetLots = copyLots(r.Lots)
		}
	}
	return obs, nil
}

func copyLots(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// Provenance describes where this observation came from.
func (o Observation) Prov() Provenance {
	return Provenance{
		AsOf:       o.TradingDate,
		Session:    o.Session,
		Source:     o.Dataset,
		SnapshotID: o.SnapshotID,
		Revision:   o.Revision,
		FetchedAt:  o.FetchedAt,
	}
}

// Flow is what this institution TRADED during the session.
func (o Observation) Flow() TradingFlowContracts {
	return flowOf(o.metric(SemanticsFlow, o.FlowNetLots, taifexfeed.ColFlowNet))
}

// Position is what this institution HELD at the close.
//
// Never FLOW, on any path. §10.4 forbids the fallback explicitly, and the reason is in the
// 2026-09-04 numbers: 投信 traded +1,785 while holding +76,174, so a fallback would have
// under-reported the position by a factor of 43 and looked entirely plausible doing it.
func (o Observation) Position() OpenInterestPositionContracts {
	return positionOf(o.metric(SemanticsPosition, o.PositionNetLots, taifexfeed.ColPosNet))
}

// HasPosition is the predicate the baseline resolver uses. An observation without POSITION
// cannot anchor a CHANGE, whatever its FLOW says.
func (o Observation) HasPosition() bool { return o.PositionNetLots != nil }

// metric turns one nullable lot count into an ObservedMetric, deciding the absence REASON from
// the snapshot's status and the coverage — which is the whole job. "No number" has six different
// causes here and they imply different actions.
//
// The column name in the reason comes from internal/taifexfeed, which owns the ONE spelling of
// these headers (§10.2/FU-10). Even a message that only ever reaches a human goes through the
// constant: internal/taifexfeed/one_decoder_test.go treats a literal here as a second decoder
// waiting to happen, and it is right to — the first FU-10 failure started as two places that
// each knew what the column was called.
func (o Observation) metric(sem ValueSemantics, lots *float64, column string) ObservedMetric {
	p := o.Prov()
	switch {
	case !o.SourceStatus.Usable():
		// NO_SESSION / NOT_PUBLISHED / MISSING / ERROR / STALE / NOT_AVAILABLE travel
		// through unchanged. Collapsing them here is exactly what §6 forbids: a normal
		// afternoon before the file is out would become indistinguishable from a gap.
		return newAbsent(sem, o.SourceStatus, "snapshot status "+string(o.SourceStatus), p)
	case lots != nil:
		return newObserved(sem, *lots, p)
	case !o.Coverage.FoundScopeProduct():
		// The fetch succeeded and 21 other products are present; this one is not in the
		// response at all. That is a missing observation, not a partial one.
		return newAbsent(sem, StatusMissing,
			fmt.Sprintf("%s did not appear in the response for %s", o.Scope.Product, o.TradingDate), p)
	default:
		// The contract is in the response and this column is not a number in it: §3.1's
		// ABSENT, an observation of absence. PARTIAL, value absent, never 0.
		return newAbsent(sem, StatusPartial,
			fmt.Sprintf("%s is absent for %s %s", column, o.Institution, o.Scope), p)
	}
}

// sameSeries reports whether another observation belongs to the same series as o: same
// institution, scope, session and dataset. Anything else is a different measurement wearing
// the same shape.
func (o Observation) sameSeries(x Observation) error {
	if o.Institution != x.Institution {
		return fmt.Errorf("%w: %q and %q", ErrInstitutionMismatch, o.Institution, x.Institution)
	}
	if !o.Scope.Same(x.Scope) {
		return fmt.Errorf("%w: %s and %s — 1 TX lot is not 1 MTX lot, so these series do not "+
			"difference against each other", ErrScopeMismatch, o.Scope, x.Scope)
	}
	if o.Session != x.Session {
		return fmt.Errorf("%w: %q and %q", ErrSessionMismatch, o.Session, x.Session)
	}
	if o.Dataset != x.Dataset {
		return fmt.Errorf("%w: %q and %q", ErrDatasetMismatch, o.Dataset, x.Dataset)
	}
	return nil
}

// parseDate is the only date parse in the package.
func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrBadDate, s)
	}
	return t, nil
}

// calendarDays is the plain difference between two Taipei calendar dates. It is reported
// ALONGSIDE the observation gap, never instead of it: §10.4's whole point is that a reader can
// see that a "previous session" difference spans four days.
func calendarDays(from, to time.Time) int {
	return int(to.Sub(from).Hours() / 24)
}
