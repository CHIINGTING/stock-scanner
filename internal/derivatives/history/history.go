// Package history is R15 M4's POINT-IN-TIME history reader: the persistence-backed half of
// the institutional derivatives contract.
//
// # Why it is its own package
//
// internal/derivatives/institutional is a pure domain layer — it imports the decoder, the feed
// column names and the standard library, and TestThePureLayerStaysPure fails if it ever imports
// database/sql. internal/derivatives owns persistence and therefore imports internal/store. A
// reader that returns institutional.Observation values out of SQLite needs BOTH, so it can live
// in neither: putting it in internal/derivatives closes an import cycle
// (derivatives → institutional → derivatives), and putting it in institutional puts a SQL
// driver behind every pure function there.
//
// So it goes here, the way internal/derivatives/acquire already imports both. This package is
// on internal/derivatives/architecture_test.go's forbidden list for the same reason every other
// R15 package is: the decision path may not reach the research layer.
//
// # What it guarantees
//
// One rule, and every query in this file exists to enforce it (§5, §7.3):
//
//	EACH TRADING DATE RESOLVES TO THE REVISION THAT WAS VISIBLE AT THE CUTOFF.
//
// Not the latest revision of each date. Not the latest revision overall. The concrete failure
// is a correction to an EARLIER session archived LATER:
//
//	09-01 revision 0, fetched 09-01
//	09-04 revision 0, fetched 09-04
//	09-01 revision 1, fetched 09-05     ← a correction to 09-01, retrieved on 09-05
//
// A reading dated 09-04 must see 09-01 revision 0; a reading dated 09-06 must see revision 1;
// and in BOTH cases the latest trading date is 09-04, because a correction to an older session
// does not make that session current. An implementation that ordered on fetched_at before
// trading_date would answer "09-01" to "what is the most recent session" as of 09-06, and every
// number downstream would be a fortnight old under a current label.
//
// The forbidden shortcuts, each of which produces a plausible series: any LoadLatest-shaped
// call; taking each date's newest revision unconditionally; using ingestion order in place of
// the trading date.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §5, §6, §7.3, §10.4.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// ErrLookAheadLeak is returned when a row that should have been excluded by the as-of bound
// reaches the assembled series anyway.
//
// It is a panic-grade condition expressed as an error. The SQL is bounded; this is the SECOND
// check, in Go, over the assembled result, and it exists because the first one is one edited
// WHERE clause away from silently disappearing. A look-ahead that returns a number is
// undetectable downstream — the whole point of §5 — so the cheap redundancy is worth it.
var ErrLookAheadLeak = errors.New("history: a row later than the as-of cutoff reached the series")

// ErrNoObservations is returned when nothing at all was visible at the cutoff.
var ErrNoObservations = errors.New("history: no observation was visible at the as-of cutoff")

// DefaultMaxScanDates bounds how far back a walk may look for its N valid observations.
//
// It is a SCAN bound, not a filter: when it is hit the result says so (Truncated, with the
// reason), rather than returning a short series that looks like a short history. 400 trading
// dates is about eighteen months.
const DefaultMaxScanDates = 400

// Where a date's status came from. Recorded on every resolution because the two answers have
// different confidence and a reader must be able to tell them apart.
const (
	// StatusFromHealth — a derivative_data_health record was VISIBLE at the cutoff and its
	// status was used.
	StatusFromHealth = "HEALTH_RECORD"
	// StatusFromSnapshot — no health record was visible at the cutoff, so the stored rows
	// are the observation. derivative_data_health is replace-on-write (it is the CURRENT
	// answer to "what happened when we last tried"), so a record rewritten after the cutoff
	// is not evidence about the cutoff and is not used as any.
	StatusFromSnapshot = "SNAPSHOT_ONLY"
)

// Inconsistency is a disagreement between the health record and the snapshot table.
//
// Neither is corrected and neither is hidden. Both of these have happened for real reasons — a
// 15:00 AVAILABLE run followed by a 17:00 failure rewrites the health row while the 15:00
// snapshot stays exactly where it was — and a reader who cannot see the disagreement will read
// the resolution as a fact rather than as a choice.
type Inconsistency struct {
	TradingDate string `json:"trading_date"`
	// HealthStatus is what the visible health record said; Resolved is the status the walk
	// used.
	HealthStatus string                     `json:"health_status"`
	Resolved     institutional.MetricStatus `json:"resolved"`
	SnapshotID   int64                      `json:"snapshot_id,omitempty"`
	Reason       string                     `json:"reason"`
}

// DateResolution is one trading date's point-in-time answer: which revision the cutoff allowed,
// and what status the day carries.
type DateResolution struct {
	TradingDate string `json:"trading_date"`
	// SnapshotID and Revision identify the revision that was visible AT THE CUTOFF — not the
	// newest one in the table.
	SnapshotID int64     `json:"snapshot_id,omitempty"`
	Revision   int       `json:"revision"`
	FetchedAt  time.Time `json:"fetched_at,omitempty"`
	// Status is §6's vocabulary for this date.
	Status institutional.MetricStatus `json:"status"`
	// StatusSource is HEALTH_RECORD or SNAPSHOT_ONLY.
	StatusSource string `json:"status_source"`
	// Rows is how many institutional rows the visible revision carried, before scope
	// selection. 0 on a date with no usable snapshot.
	Rows   int    `json:"rows"`
	Reason string `json:"reason,omitempty"`
}

// Usable reports whether this date carries readable rows.
func (d DateResolution) Usable() bool { return d.Status.Usable() }

// Gap is one date the walk passed over without a usable observation, and why.
//
// §10.4: "Skipping is not silent." The gaps travel with the series so a CHANGE measured across
// four calendar days can be seen to have been measured across four calendar days.
type Gap struct {
	TradingDate string                     `json:"trading_date"`
	Status      institutional.MetricStatus `json:"status"`
	Reason      string                     `json:"reason"`
}

// Query is one point-in-time history request.
type Query struct {
	// Dataset is the LOGICAL dataset name (acquire.InstitutionalFutures.Name), which is what
	// derivative_data_health is keyed on. Source is the ENDPOINT, which is what
	// derivative_snapshots is keyed on. Both, because the first survives an endpoint rename
	// and the second is what the bytes actually came from.
	Dataset string
	Source  string
	// Session must be COMBINED: the institutional futures feed carries no session dimension,
	// so its session policy is fixed by the data contract and never read off a clock (§10.4).
	Session string
	Scope   institutional.InstrumentScope
	// Institutions are the investors to build series for. Read together from ONE snapshot
	// per date, because they come from one response and three separate walks could resolve
	// three different revision views of the same session.
	Institutions []string
	// AsOf is the point-in-time cutoff: a Taipei trading date. Every observation returned is
	// dated at or before it AND was fetched at or before the end of it (§7.3).
	AsOf string
	// Count is how many VALID trading dates to return, newest first, INCLUDING the latest.
	// Zero → RequiredObservations(DefaultPolicy), which is what the percentile window and the
	// longest horizon together need.
	Count int
	// MaxScanDates bounds the walk. Zero → DefaultMaxScanDates.
	MaxScanDates int
}

// RequiredObservations is how many valid observations a full M4 answer needs: the percentile
// lookback window, plus one baseline for the oldest window entry's CHANGE, plus enough for the
// longest horizon.
//
// Asking for exactly the lookback would leave the oldest entry in every CHANGE distribution
// without a baseline, which shows up as a distribution one shorter than the window and is
// tedious to diagnose from the outside.
func RequiredObservations(p institutional.Policy) int {
	np, err := p.Normalized()
	if err != nil {
		np = institutional.DefaultPolicy()
	}
	longest := 1
	for _, n := range np.Horizons {
		if n > longest {
			longest = n
		}
	}
	return np.PercentileLookbackObservations + longest
}

func (q Query) normalized() (Query, error) {
	if q.Dataset == "" || q.Source == "" {
		return Query{}, fmt.Errorf(
			"history: a query needs both a dataset (health key) and a source (snapshot key), got %q/%q",
			q.Dataset, q.Source)
	}
	if q.Session != institutional.SessionCombined {
		return Query{}, fmt.Errorf(
			"history: session must be %s — the institutional futures feed has no session "+
				"dimension, so its policy is fixed by the data contract (§10.4); got %q",
			institutional.SessionCombined, q.Session)
	}
	if err := q.Scope.Valid(); err != nil {
		return Query{}, err
	}
	if len(q.Institutions) == 0 {
		return Query{}, errors.New("history: a query needs at least one institution")
	}
	seen := map[string]bool{}
	for _, inst := range q.Institutions {
		if inst == "" {
			return Query{}, errors.New("history: an institution name may not be empty")
		}
		if seen[inst] {
			return Query{}, fmt.Errorf("%w: %q appears twice in the query",
				institutional.ErrInstitutionMismatch, inst)
		}
		seen[inst] = true
	}
	if _, err := time.Parse(institutional.DateLayout, q.AsOf); err != nil {
		return Query{}, fmt.Errorf("%w: as-of %q", institutional.ErrBadDate, q.AsOf)
	}
	if q.Count < 0 || q.MaxScanDates < 0 {
		return Query{}, fmt.Errorf("history: negative count %d / scan bound %d",
			q.Count, q.MaxScanDates)
	}
	if q.Count == 0 {
		q.Count = RequiredObservations(institutional.DefaultPolicy())
	}
	if q.MaxScanDates == 0 {
		// Only the DEFAULT is widened to fit the request. An explicit bound below Count is a
		// caller's decision — "look at most this far back" — and silently raising it would
		// turn a deliberate limit into a full scan. The result reports the truncation either
		// way, so a short answer is never mistaken for a short history.
		q.MaxScanDates = DefaultMaxScanDates
		if q.MaxScanDates < q.Count {
			q.MaxScanDates = q.Count
		}
	}
	return q, nil
}

// Series is one institution's point-in-time history, newest first.
type Series struct {
	Institution string `json:"institution"`
	// Observations are EVERY date the walk produced, newest first — the usable readings AND
	// the days that were not usable. The unusable ones are included deliberately: they are
	// what lets institutional.ResolveBaseline name the dates it skipped rather than
	// presenting a four-day difference as a one-session one.
	Observations []institutional.Observation `json:"observations"`
	// Gaps are the unusable dates, in the same order, with their reasons.
	Gaps []Gap `json:"gaps,omitempty"`
}

// Latest is the newest USABLE observation — the reading a view is about.
//
// It is the newest by TRADING DATE, never by fetched_at: a correction to an older session is
// still a correction to an older session (§7.3).
func (s Series) Latest() (institutional.Observation, bool) {
	for _, o := range s.Observations {
		if o.SourceStatus.Usable() {
			return o, true
		}
	}
	return institutional.Observation{}, false
}

// Before returns every observation strictly earlier than date, newest first — the history slice
// institutional.ChangeOver, ResolveBaseline and Percentiles all take.
func (s Series) Before(date string) []institutional.Observation {
	out := make([]institutional.Observation, 0, len(s.Observations))
	for _, o := range s.Observations {
		if o.TradingDate < date {
			out = append(out, o)
		}
	}
	return out
}

// Valid returns just the usable observations, newest first.
func (s Series) Valid() []institutional.Observation {
	out := make([]institutional.Observation, 0, len(s.Observations))
	for _, o := range s.Observations {
		if o.SourceStatus.Usable() {
			out = append(out, o)
		}
	}
	return out
}

// Result is one point-in-time read: the series, and everything needed to disbelieve them.
type Result struct {
	Query Query  `json:"-"`
	AsOf  string `json:"as_of"`
	// Cutoff is the UTC instant the as-of date ends at in Taipei — §7.3's fetched_at bound,
	// recorded so a stored result says exactly what it was bounded by.
	Cutoff time.Time `json:"cutoff"`
	Series []Series  `json:"series"`
	// Dates are the resolutions, newest first: which revision each date resolved to.
	Dates []DateResolution `json:"dates"`
	// ValidObservations is how many usable dates the walk found; Requested is how many were
	// asked for.
	ValidObservations int `json:"valid_observations"`
	Requested         int `json:"requested"`
	ScannedDates      int `json:"scanned_dates"`
	// Truncated is true when the scan bound stopped the walk before it had Requested valid
	// observations. A short series and a bounded scan are different facts.
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
	// Inconsistencies are health/snapshot disagreements, recorded rather than resolved
	// silently.
	Inconsistencies []Inconsistency `json:"inconsistencies,omitempty"`
}

// Series returns one institution's series.
func (r Result) SeriesFor(institution string) (Series, bool) {
	for _, s := range r.Series {
		if s.Institution == institution {
			return s, true
		}
	}
	return Series{}, false
}

// LatestTradingDate is the newest trading date with a usable observation, "" when there is
// none.
//
// Newest by TRADING DATE. This is the value the 09-01-corrected-on-09-05 case is about: it must
// be 09-04 whether the cutoff is 09-04 or 09-06.
func (r Result) LatestTradingDate() string {
	for _, d := range r.Dates {
		if d.Usable() {
			return d.TradingDate
		}
	}
	return ""
}

// Resolution returns one date's resolution.
func (r Result) Resolution(date string) (DateResolution, bool) {
	for _, d := range r.Dates {
		if d.TradingDate == date {
			return d, true
		}
	}
	return DateResolution{}, false
}

// Load walks trading dates backwards from the cutoff and returns each institution's series.
//
// The walk, in order, and every step of it is bounded by the cutoff:
//
//  1. the candidate dates — the union of the snapshot dates and the health dates that were
//     VISIBLE at the cutoff, ordered by TRADING DATE descending
//  2. per date, the revision visible at the cutoff, via derivatives.SnapshotForSession, which
//     is §7.3's query and the only place that ordering lives
//  3. per date, the status: the health record if one was visible, otherwise the snapshot
//  4. per usable date, the rows of THAT revision, turned into one observation per institution
//
// Nothing here takes "the latest" of anything without a bound, and step 1's ORDER BY is on
// trading_date first — which is what stops a correction to an older session from presenting
// itself as the current session.
func Load(ctx context.Context, st *store.Store, q Query) (Result, error) {
	q, err := q.normalized()
	if err != nil {
		return Result{}, err
	}
	if st == nil {
		return Result{}, errors.New("history: a read needs a store")
	}
	cutoff, err := derivatives.AsOfCutoff(q.AsOf)
	if err != nil {
		return Result{}, err
	}

	dates, err := candidateDates(ctx, st, q)
	if err != nil {
		return Result{}, err
	}
	health, err := visibleHealth(ctx, st, q, cutoff)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		Query:     q,
		AsOf:      q.AsOf,
		Cutoff:    cutoff,
		Requested: q.Count,
	}
	byInstitution := map[string]*Series{}
	for _, inst := range q.Institutions {
		byInstitution[inst] = &Series{Institution: inst}
	}

	for _, date := range dates {
		if res.ValidObservations >= q.Count {
			break
		}
		if res.ScannedDates >= q.MaxScanDates {
			res.Truncated = true
			res.TruncatedReason = fmt.Sprintf(
				"the walk stopped after %d trading dates with %d of the %d requested valid "+
					"observations; a scan bound is not a short history",
				res.ScannedDates, res.ValidObservations, q.Count)
			break
		}
		res.ScannedDates++

		snap, hasSnap, err := visibleSnapshot(ctx, st, q, date)
		if err != nil {
			return Result{}, err
		}
		if hasSnap && snap.FetchedAt.After(cutoff) {
			return Result{}, fmt.Errorf("%w: snapshot %d for %s was fetched at %s, after the "+
				"%s cutoff %s", ErrLookAheadLeak, snap.ID, date,
				snap.FetchedAt.UTC().Format(time.RFC3339), q.AsOf,
				cutoff.Format(time.RFC3339))
		}

		resolution, inc := resolveStatus(date, health[date], snap, hasSnap)
		if inc != nil {
			res.Inconsistencies = append(res.Inconsistencies, *inc)
		}

		var rows []provider.InstitutionalRow
		if resolution.Usable() && hasSnap {
			rows, err = institutionalRows(ctx, st, snap.ID, snap.TradingDate)
			if err != nil {
				return Result{}, err
			}
			resolution.Rows = len(rows)
		}
		res.Dates = append(res.Dates, resolution)

		for _, inst := range q.Institutions {
			obs, err := institutional.NewObservation(institutional.ObservationRequest{
				TradingDate:  date,
				Session:      q.Session,
				Dataset:      q.Source,
				Institution:  inst,
				Scope:        q.Scope,
				SourceStatus: resolution.Status,
				SnapshotID:   resolution.SnapshotID,
				Revision:     resolution.Revision,
				FetchedAt:    resolution.FetchedAt,
			}, rows)
			if err != nil {
				return Result{}, fmt.Errorf("history: %s %s %s: %w", date, inst, q.Scope, err)
			}
			s := byInstitution[inst]
			s.Observations = append(s.Observations, obs)
			if !resolution.Usable() {
				s.Gaps = append(s.Gaps, Gap{
					TradingDate: date,
					Status:      resolution.Status,
					Reason:      resolution.Reason,
				})
			}
		}
		if resolution.Usable() {
			res.ValidObservations++
		}
	}

	if !res.Truncated && res.ValidObservations < q.Count && res.ScannedDates >= q.MaxScanDates {
		res.Truncated = true
		res.TruncatedReason = fmt.Sprintf(
			"the walk reached the %d-date scan bound with %d of the %d requested valid "+
				"observations", q.MaxScanDates, res.ValidObservations, q.Count)
	}

	// In the caller's order, so two runs over the same store serialise identically.
	for _, inst := range q.Institutions {
		res.Series = append(res.Series, *byInstitution[inst])
	}
	if err := assertPointInTime(res, cutoff); err != nil {
		return Result{}, err
	}
	return res, nil
}

// assertPointInTime re-checks, in Go and over the ASSEMBLED result, the two bounds the SQL
// already applied.
//
// Redundant on purpose. Every bound in this file is one edited WHERE clause away from
// disappearing, and a look-ahead that returns a number cannot be detected by anything
// downstream — §5's whole subject. It also pins the ORDERING, which no single query proves:
// the dates must come out strictly descending by TRADING DATE.
func assertPointInTime(r Result, cutoff time.Time) error {
	prev := ""
	for _, d := range r.Dates {
		if d.TradingDate > r.AsOf {
			return fmt.Errorf("%w: %s is later than the as-of date %s",
				ErrLookAheadLeak, d.TradingDate, r.AsOf)
		}
		if !d.FetchedAt.IsZero() && d.FetchedAt.After(cutoff) {
			return fmt.Errorf("%w: %s resolved to revision %d fetched at %s, after the cutoff %s",
				ErrLookAheadLeak, d.TradingDate, d.Revision,
				d.FetchedAt.UTC().Format(time.RFC3339), cutoff.Format(time.RFC3339))
		}
		if prev != "" && d.TradingDate >= prev {
			return fmt.Errorf(
				"history: trading dates came back out of order (%s after %s) — the walk must be "+
					"ordered by trading date descending, or a correction to an older session "+
					"becomes the current one", d.TradingDate, prev)
		}
		prev = d.TradingDate
	}
	for _, s := range r.Series {
		for _, o := range s.Observations {
			if o.TradingDate > r.AsOf {
				return fmt.Errorf("%w: %s %s is later than the as-of date %s",
					ErrLookAheadLeak, s.Institution, o.TradingDate, r.AsOf)
			}
			if !o.FetchedAt.IsZero() && o.FetchedAt.After(cutoff) {
				return fmt.Errorf("%w: %s %s carries fetched_at %s, after the cutoff %s",
					ErrLookAheadLeak, s.Institution, o.TradingDate,
					o.FetchedAt.UTC().Format(time.RFC3339), cutoff.Format(time.RFC3339))
			}
		}
	}
	return nil
}

// resolveStatus decides one date's §6 status from the two things that can know it.
//
// The rule, fixed here rather than left to a call site:
//
//	visible health, usable, snapshot visible     → the health status (AVAILABLE or PARTIAL)
//	visible health, usable, NO snapshot visible  → MISSING, and the disagreement is recorded
//	visible health, NOT usable                   → that status, rows NOT read even if a
//	                                               snapshot is visible
//	no visible health, snapshot visible          → AVAILABLE, source SNAPSHOT_ONLY
//
// The third line is the one worth arguing about, and it errs deliberately towards the shorter
// history. STALE is the case that settles it: §10.4 says a stale reading is skipped and that
// this is not configurable, because its freshness contract already failed and using it would
// launder that failure into every CHANGE and every percentile computed from it. A rule that
// preferred the rows whenever rows existed would do exactly that laundering — so the health
// record wins, the rows are left unread, and the fact that rows existed is recorded as an
// Inconsistency rather than dropped.
func resolveStatus(date string, h healthRow, snap derivatives.Snapshot, hasSnap bool) (
	DateResolution, *Inconsistency) {

	d := DateResolution{TradingDate: date}

	if h.found {
		status, err := institutional.SourceStatus(h.status)
		if err != nil {
			// An unrecognised status is not defaulted to anything. ERROR is the honest
			// answer: something wrote a status this build does not know, and treating it
			// as MISSING or AVAILABLE would both be claims nobody made.
			d.Status = institutional.StatusError
			d.StatusSource = StatusFromHealth
			d.Reason = fmt.Sprintf("health record for %s carries status %q, which is not one "+
				"of §6's: %v", date, h.status, err)
			return d, &Inconsistency{
				TradingDate: date, HealthStatus: h.status, Resolved: d.Status,
				Reason: d.Reason,
			}
		}
		d.StatusSource = StatusFromHealth
		if !status.Usable() {
			d.Status = status
			d.Reason = h.reason
			if d.Reason == "" {
				d.Reason = fmt.Sprintf("the %s health record for %s is %s", h.dataset, date, status)
			}
			if hasSnap {
				return d, &Inconsistency{
					TradingDate: date, HealthStatus: h.status, Resolved: status,
					SnapshotID: snap.ID,
					Reason: fmt.Sprintf(
						"snapshot %d (revision %d) is visible for %s while the health record "+
							"says %s; the health record wins and the rows are left unread, "+
							"because a status whose freshness contract failed must not be "+
							"laundered into a usable observation",
						snap.ID, snap.RevisionNo, date, status),
				}
			}
			return d, nil
		}
		if !hasSnap {
			d.Status = institutional.StatusMissing
			d.Reason = fmt.Sprintf(
				"the %s health record for %s says %s, and no revision was visible at the "+
					"cutoff — nothing can be read for this date", h.dataset, date, status)
			return d, &Inconsistency{
				TradingDate: date, HealthStatus: h.status, Resolved: d.Status, Reason: d.Reason,
			}
		}
		d.Status = status
		d.SnapshotID = snap.ID
		d.Revision = snap.RevisionNo
		d.FetchedAt = snap.FetchedAt
		if status == institutional.StatusPartial {
			d.Reason = h.reason
		}
		return d, nil
	}

	if hasSnap {
		// No health record was visible at the cutoff. The rows are, and rows only exist
		// because acquisition stored them — it stores nothing on any status but AVAILABLE
		// or PARTIAL. AVAILABLE is recorded with StatusSource SNAPSHOT_ONLY so a reader can
		// see that the completeness half of the claim has no record behind it.
		d.Status = institutional.StatusAvailable
		d.StatusSource = StatusFromSnapshot
		d.SnapshotID = snap.ID
		d.Revision = snap.RevisionNo
		d.FetchedAt = snap.FetchedAt
		d.Reason = "no health record was visible at the cutoff; the stored rows are the observation"
		return d, nil
	}

	// Unreachable through Load — a candidate date came from one of the two tables — but a
	// status is returned rather than a panic, because the alternative to an honest MISSING
	// here would be a zero-valued DateResolution that reads as AVAILABLE.
	d.Status = institutional.StatusMissing
	d.StatusSource = StatusFromSnapshot
	d.Reason = fmt.Sprintf("neither a snapshot nor a health record was visible for %s", date)
	return d, nil
}

// candidateDates is every trading date that was VISIBLE at the cutoff, newest first.
//
// The union of two tables, because the two hold different halves of the history: a date the
// exchange did not publish has a health record and no snapshot, and dropping it would make a
// four-day CHANGE indistinguishable from a one-session one. Both halves are bounded on the
// cutoff — the snapshots on fetched_at, the health records on recorded_at.
//
// ORDER BY trading_date DESC, and that is the line mutation (d) is about: order these on
// fetched_at instead and a correction to 09-01 archived on 09-05 becomes the most recent
// session.
func candidateDates(ctx context.Context, st *store.Store, q Query) ([]string, error) {
	bound, err := derivatives.AsOfCutoff(q.AsOf)
	if err != nil {
		return nil, err
	}
	at := bound.Format(time.RFC3339)

	seen := map[string]bool{}
	var dates []string
	collect := func(query string, args ...any) error {
		rows, err := queryRows(ctx, st, query, args...)
		if err != nil {
			return err
		}
		for _, d := range rows {
			if !seen[d] {
				seen[d] = true
				dates = append(dates, d)
			}
		}
		return nil
	}

	if err := collect(`
		SELECT DISTINCT trading_date FROM derivative_snapshots
		WHERE source = ? AND trading_session = ?
		  AND trading_date <= ? AND fetched_at <= ?
		ORDER BY trading_date DESC
		LIMIT ?`, q.Source, q.Session, q.AsOf, at, q.MaxScanDates); err != nil {
		return nil, err
	}
	if err := collect(`
		SELECT DISTINCT trading_date FROM derivative_data_health
		WHERE dataset = ? AND session = ?
		  AND trading_date <= ? AND recorded_at <= ?
		ORDER BY trading_date DESC
		LIMIT ?`, q.Dataset, q.Session, q.AsOf, at, q.MaxScanDates); err != nil {
		return nil, err
	}

	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	if len(dates) > q.MaxScanDates {
		dates = dates[:q.MaxScanDates]
	}
	return dates, nil
}

func queryRows(ctx context.Context, st *store.Store, query string, args ...any) ([]string, error) {
	var out []string
	err := st.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("history: read trading dates: %w", err)
	}
	return out, nil
}

// healthRow is the part of a derivative_data_health record this reader uses.
type healthRow struct {
	found   bool
	dataset string
	status  string
	reason  string
}

// visibleHealth reads every health record for this dataset and session that was RECORDED at or
// before the cutoff.
//
// The recorded_at bound is load-bearing and is easy to leave out. derivative_data_health is
// replace-on-write by design — it is "the current answer to what happened when we last tried"
// (internal/derivatives/health.go) — so the row for an old date can have been rewritten
// yesterday. Reading it unbounded would let a status established after the cutoff decide what a
// reading dated before the cutoff saw, which is the same look-ahead as an unbounded snapshot
// read wearing different clothes.
func visibleHealth(ctx context.Context, st *store.Store, q Query, cutoff time.Time) (
	map[string]healthRow, error) {

	out := map[string]healthRow{}
	at := cutoff.Format(time.RFC3339)
	err := st.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT trading_date, dataset, status, reason
			FROM derivative_data_health
			WHERE dataset = ? AND session = ?
			  AND trading_date <= ? AND recorded_at <= ?
			ORDER BY trading_date DESC
			LIMIT ?`, q.Dataset, q.Session, q.AsOf, at, q.MaxScanDates)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var date string
			var h healthRow
			if err := rows.Scan(&date, &h.dataset, &h.status, &h.reason); err != nil {
				return err
			}
			h.found = true
			out[date] = h
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("history: read data health: %w", err)
	}
	return out, nil
}

// visibleSnapshot is §7.3's per-date read, delegated to internal/derivatives so there is
// exactly one implementation of "which revision may a reading dated A see".
//
// derivatives.SnapshotForSession is pinned to ONE trading date and bounded on fetched_at, which
// is precisely the per-date rule: the greatest fetched_at ≤ the end of A, tie-broken on
// revision_no. Rewriting the query here would be a second point-in-time reader, and the two
// would eventually disagree about a boundary second.
func visibleSnapshot(ctx context.Context, st *store.Store, q Query, date string) (
	derivatives.Snapshot, bool, error) {

	snap, err := derivatives.SnapshotForSession(ctx, st, q.Source, q.Session, date, q.AsOf)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return derivatives.Snapshot{}, false, nil
	case err != nil:
		return derivatives.Snapshot{}, false, fmt.Errorf("history: snapshot for %s: %w", date, err)
	}
	return snap, true, nil
}

// institutionalRows reads one revision's stored rows back into the decoder's shape.
//
// The trading date comes from the SNAPSHOT, which is where it is recorded: institutional_
// derivatives has no date column of its own, and inventing one from the row's fetched_at would
// be §7.3's forbidden "ingestion order in place of the trading date".
func institutionalRows(ctx context.Context, st *store.Store, snapshotID int64, tradingDate string) (
	[]provider.InstitutionalRow, error) {

	var out []provider.InstitutionalRow
	err := st.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT institution, product, call_put, value_semantics, side, lots, value_thousands
			FROM institutional_derivatives
			WHERE snapshot_id = ?
			ORDER BY institution, product, call_put, value_semantics, side`, snapshotID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r := provider.InstitutionalRow{TradingDate: tradingDate}
			if err := rows.Scan(&r.Institution, &r.Product, &r.CallPut, &r.Semantics,
				&r.Side, &r.Lots, &r.ValueThousands); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("history: read institutional rows for snapshot %d: %w",
			snapshotID, err)
	}
	return out, nil
}

// LoadView reads the series and assembles M4's whole answer for the newest usable trading date.
//
// The reading and its history come from ONE Load, under ONE cutoff, which is the property that
// makes the view point-in-time: a CHANGE resolved under one cutoff and a percentile under
// another would be two answers wearing one label.
//
// It is a view/interface only. There is no report wiring, no config flag and no rendering here
// — M10 owns integration, and R15 is default-off until §14's gate is met.
func LoadView(ctx context.Context, st *store.Store, q Query, p institutional.Policy) (
	institutional.InstitutionalDerivativesView, Result, error) {

	np, err := p.Normalized()
	if err != nil {
		return institutional.InstitutionalDerivativesView{}, Result{}, err
	}
	if q.Count == 0 {
		q.Count = RequiredObservations(np)
	}
	res, err := Load(ctx, st, q)
	if err != nil {
		return institutional.InstitutionalDerivativesView{}, Result{}, err
	}

	// One trading date for the whole view: the newest date with a usable observation. Built
	// per institution it could differ between them, and §10.4 makes a mixed identity an
	// ERROR rather than a status — "no number would have been correct".
	date := res.LatestTradingDate()
	if date == "" {
		return institutional.InstitutionalDerivativesView{}, res, fmt.Errorf(
			"%w: %s %s %s as of %s", ErrNoObservations, q.Dataset, q.Scope, q.Session, q.AsOf)
	}

	var series []institutional.InstitutionSeries
	for _, s := range res.Series {
		var current institutional.Observation
		found := false
		for _, o := range s.Observations {
			if o.TradingDate == date {
				current = o
				found = true
				break
			}
		}
		if !found {
			// The institution has no row for the view's date. That is a MISSING
			// institution, which BuildInstitutionalDerivativesView reports as PARTIAL with
			// the absentee named — not something to substitute another date for.
			continue
		}
		series = append(series, institutional.InstitutionSeries{
			Current: current,
			History: s.Before(date),
		})
	}
	if len(series) == 0 {
		return institutional.InstitutionalDerivativesView{}, res, fmt.Errorf(
			"%w: no institution had an observation for %s", ErrNoObservations, date)
	}

	view, err := institutional.BuildInstitutionalDerivativesView(q.AsOf, series, np)
	if err != nil {
		return institutional.InstitutionalDerivativesView{}, res, err
	}
	return view, res, nil
}
