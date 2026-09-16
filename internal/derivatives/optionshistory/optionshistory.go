// Package optionshistory is R15 M5's POINT-IN-TIME reader: the persistence-backed half of the
// options structure contract.
//
// # Why it is its own package
//
// internal/derivatives/options is a pure domain layer — TestTheOptionsLayerStaysPure fails if
// it ever imports database/sql — and internal/derivatives owns persistence and therefore
// imports internal/store. A reader that returns options.Observation values out of SQLite needs
// BOTH, so it can live in neither: putting it in internal/derivatives closes an import cycle,
// and putting it in options puts a SQL driver behind every pure function there. It goes here,
// exactly as M4's reader goes in internal/derivatives/history.
//
// It is on internal/derivatives/architecture_test.go's forbidden list for the same reason
// every other R15 package is: the decision path may not reach the research layer.
//
// # What it guarantees
//
// The same rule M4's reader guarantees (§5, §7.3):
//
//	EACH TRADING DATE RESOLVES TO THE REVISION THAT WAS VISIBLE AT THE CUTOFF.
//
// with two things M4 does not have to deal with:
//
//  1. ONE SESSION DATE IS TWO SNAPSHOTS. §10.2b stores DailyMarketReportOpt as a DAY snapshot
//     and an AFTER_HOURS snapshot, because options_oi_by_strike is keyed without a session
//     column. Each is resolved independently against the cutoff — the night rows are published
//     later and can be corrected on their own — and the combined figure carries both
//     references.
//
//  2. THE SETTLING EXPIRY COMES FROM THE SNAPSHOT ARCHIVED FOR D, never from the live feed and
//     never from "today". FinalSettlementPriceIndexOptions carries no date of its own and
//     always answers with the most recent settlement event, so recomputing 2026-09-04 on or
//     after 2026-09-09 against the live response excludes nothing and lands on 223,014 against
//     an official 96,420 — a 2.31x error reached by rerunning a day that was correct when it
//     was first fetched. Reading it through derivatives.SnapshotForSession is what makes that
//     impossible rather than remembered.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §3.1, §5, §7.3, §10.7, §10.8.
package optionshistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// ErrLookAheadLeak is returned when a row that should have been excluded by the as-of bound
// reaches the assembled series anyway.
//
// A panic-grade condition expressed as an error, and deliberately redundant: the SQL is
// bounded, and this is the SECOND check, in Go, over the assembled result. Every bound in this
// file is one edited WHERE clause away from disappearing, and a look-ahead that returns a
// number is undetectable downstream.
var ErrLookAheadLeak = errors.New("optionshistory: a row later than the as-of cutoff reached the series")

// ErrNoObservations is returned when nothing at all was visible at the cutoff.
var ErrNoObservations = errors.New("optionshistory: no observation was visible at the as-of cutoff")

// DefaultMaxScanDates bounds how far back a walk may look. It is a SCAN bound, not a filter:
// when it is hit the result says so, rather than returning a short series that looks like a
// short history.
const DefaultMaxScanDates = 400

// Where a session's status came from.
const (
	// StatusFromHealth — a derivative_data_health record was VISIBLE at the cutoff.
	StatusFromHealth = "HEALTH_RECORD"
	// StatusFromSnapshot — no health record was visible, so the stored rows are the
	// observation. derivative_data_health is replace-on-write, so a record rewritten after
	// the cutoff is not evidence about the cutoff and is not used as any.
	StatusFromSnapshot = "SNAPSHOT_ONLY"
)

// SessionResolution is one (trading date, session) snapshot's point-in-time answer.
type SessionResolution struct {
	Session      string                     `json:"session"`
	SnapshotID   int64                      `json:"snapshot_id,omitempty"`
	Revision     int                        `json:"revision"`
	FetchedAt    time.Time                  `json:"fetched_at,omitempty"`
	Status       institutional.MetricStatus `json:"status"`
	StatusSource string                     `json:"status_source"`
	Rows         int                        `json:"rows"`
	Rejected     int                        `json:"rejected_rows"`
	Reason       string                     `json:"reason,omitempty"`
}

// Usable reports whether this session carries readable rows.
func (s SessionResolution) Usable() bool { return s.Status.Usable() }

// DateResolution is one trading date's answer across every session the query spans, plus the
// settlement read the open-interest half depends on.
type DateResolution struct {
	TradingDate string              `json:"trading_date"`
	Sessions    []SessionResolution `json:"sessions"`
	// Status is the date's combined status, and the rule that produces it is in
	// combineStatus: a half-present COMBINED figure is PARTIAL, which §6.2 then refuses to
	// publish as an aggregate. That is the intended outcome, not a side effect — a combined
	// volume figure missing the night session is a third of the activity.
	Status institutional.MetricStatus `json:"status"`
	Reason string                     `json:"reason,omitempty"`
	// SettlementRead says whether the settlement snapshot ARCHIVED FOR THIS DATE was read.
	SettlementRead     bool   `json:"settlement_read"`
	SettlingExpiry     string `json:"settling_expiry,omitempty"`
	SettlementSnapshot int64  `json:"settlement_snapshot_id,omitempty"`
	SettlementReason   string `json:"settlement_reason,omitempty"`
}

// Usable reports whether this date produced a readable observation.
func (d DateResolution) Usable() bool { return d.Status.Usable() }

// Query is one point-in-time history request.
type Query struct {
	// Dataset is the LOGICAL dataset name, which derivative_data_health is keyed on; Source
	// is the ENDPOINT, which derivative_snapshots is keyed on. Both, because the first
	// survives an endpoint rename and the second is what the bytes actually came from.
	Dataset string
	Source  string
	// SettlementDataset and SettlementSource identify the feed that names the expiry
	// settling on a session. Both may be empty, in which case no settlement read is
	// attempted and the open-interest half is MISSING for every date (§3.1) — never
	// silently computed over a settled expiry.
	SettlementDataset string
	SettlementSource  string

	Product string
	Family  options.ContractFamily
	// Session is DAY, AFTER_HOURS or their sum. Which metric may be PUBLISHED for it is
	// options.SessionPolicy's question, not this one's: the reader's job is to produce the
	// observation, and §10.8 requires the out-of-policy combination to answer
	// INSUFFICIENT_DATA rather than to be unaskable.
	Session string
	// AsOf is the point-in-time cutoff: a Taipei trading date.
	AsOf string
	// Count is how many VALID trading dates to return, newest first, INCLUDING the latest.
	Count int
	// MaxScanDates bounds the walk. Zero → DefaultMaxScanDates, widened to Count.
	MaxScanDates int
}

// RequiredObservations is how many valid observations a full M5 answer needs: the percentile
// window plus enough for the longest horizon.
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
			"optionshistory: a query needs both a dataset (health key) and a source "+
				"(snapshot key), got %q/%q", q.Dataset, q.Source)
	}
	key := options.ObservationKey{
		Product: q.Product, Family: q.Family, Session: q.Session,
		Policy: options.SelectProductFamilyExact,
	}
	if err := key.Valid(); err != nil {
		return Query{}, err
	}
	if (q.SettlementDataset == "") != (q.SettlementSource == "") {
		return Query{}, errors.New(
			"optionshistory: the settlement feed needs both a dataset and a source, or neither")
	}
	if _, err := time.Parse(institutional.DateLayout, q.AsOf); err != nil {
		return Query{}, fmt.Errorf("%w: as-of %q", options.ErrBadDate, q.AsOf)
	}
	if q.Count < 0 || q.MaxScanDates < 0 {
		return Query{}, fmt.Errorf("optionshistory: negative count %d / scan bound %d",
			q.Count, q.MaxScanDates)
	}
	if q.Count == 0 {
		q.Count = RequiredObservations(institutional.DefaultPolicy())
	}
	if q.MaxScanDates == 0 {
		// Only the DEFAULT is widened to fit the request. An explicit bound below Count is
		// a caller's decision — "look at most this far back" — and silently raising it
		// would turn a deliberate limit into a full scan.
		q.MaxScanDates = DefaultMaxScanDates
		if q.MaxScanDates < q.Count {
			q.MaxScanDates = q.Count
		}
	}
	return q, nil
}

func (q Query) key() options.ObservationKey {
	return options.ObservationKey{
		Product: q.Product, Family: q.Family, Session: q.Session,
		Policy: options.SelectProductFamilyExact,
	}
}

// Result is one point-in-time read.
type Result struct {
	Query Query  `json:"-"`
	AsOf  string `json:"as_of"`
	// Cutoff is the UTC instant the as-of date ends at in Taipei — §7.3's fetched_at bound,
	// recorded so a stored result says what it was bounded by.
	Cutoff time.Time `json:"cutoff"`
	// Observations are EVERY date the walk produced, newest first — the usable readings AND
	// the days that were not. The unusable ones are deliberate: they are what lets the
	// baseline resolver name the dates it skipped rather than presenting a four-day
	// difference as a one-session one.
	Observations      []options.Observation `json:"observations"`
	Dates             []DateResolution      `json:"dates"`
	ValidObservations int                   `json:"valid_observations"`
	Requested         int                   `json:"requested"`
	ScannedDates      int                   `json:"scanned_dates"`
	Truncated         bool                  `json:"truncated"`
	TruncatedReason   string                `json:"truncated_reason,omitempty"`
}

// Latest is the newest USABLE observation — the reading a view is about.
//
// Newest by TRADING DATE, never by fetched_at: a correction to an older session is still a
// correction to an older session.
func (r Result) Latest() (options.Observation, bool) {
	for _, o := range r.Observations {
		if o.Usable() {
			return o, true
		}
	}
	return options.Observation{}, false
}

// Before returns every observation strictly earlier than date, newest first — the history
// slice options.ChangeOver, ResolveBaseline and Percentile all take.
func (r Result) Before(date string) []options.Observation {
	out := make([]options.Observation, 0, len(r.Observations))
	for _, o := range r.Observations {
		if o.TradingDate < date {
			out = append(out, o)
		}
	}
	return out
}

// LatestTradingDate is the newest trading date with a usable observation.
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

// Load walks trading dates backwards from the cutoff and returns one observation per date.
func Load(ctx context.Context, st *store.Store, q Query) (Result, error) {
	q, err := q.normalized()
	if err != nil {
		return Result{}, err
	}
	if st == nil {
		return Result{}, errors.New("optionshistory: a read needs a store")
	}
	cutoff, err := derivatives.AsOfCutoff(q.AsOf)
	if err != nil {
		return Result{}, err
	}
	sessions := sessionsOf(q.Session)

	dates, err := candidateDates(ctx, st, q, sessions)
	if err != nil {
		return Result{}, err
	}
	health, err := visibleHealth(ctx, st, q, cutoff)
	if err != nil {
		return Result{}, err
	}

	res := Result{Query: q, AsOf: q.AsOf, Cutoff: cutoff, Requested: q.Count}
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

		dr := DateResolution{TradingDate: date}
		var rows []provider.OptionStrikeRow
		var refs []options.SnapshotRef
		rejected := 0

		for _, session := range sessions {
			snap, hasSnap, err := visibleSnapshot(ctx, st, q.Source, session, date, q.AsOf)
			if err != nil {
				return Result{}, err
			}
			if hasSnap && snap.FetchedAt.After(cutoff) {
				return Result{}, fmt.Errorf("%w: snapshot %d for %s/%s was fetched at %s, "+
					"after the %s cutoff %s", ErrLookAheadLeak, snap.ID, date, session,
					snap.FetchedAt.UTC().Format(time.RFC3339), q.AsOf,
					cutoff.Format(time.RFC3339))
			}
			sr := resolveSession(session, health[healthKey{date, session}], snap, hasSnap)
			if sr.Usable() && hasSnap {
				stored, err := derivatives.OptionStrikesForSnapshot(ctx, st, snap.ID)
				if err != nil {
					return Result{}, err
				}
				sr.Rows = len(stored)
				rows = append(rows, toStrikeRows(stored, date, session)...)
			}
			rejected += sr.Rejected
			dr.Sessions = append(dr.Sessions, sr)
			refs = append(refs, options.SnapshotRef{
				Session: session, SnapshotID: sr.SnapshotID, Revision: sr.Revision,
				FetchedAt: sr.FetchedAt, Status: sr.Status, Rows: sr.Rows,
				Rejected: sr.Rejected,
			})
		}
		dr.Status, dr.Reason = combineStatus(dr.Sessions)

		if err := resolveSettlement(ctx, st, q, date, &dr); err != nil {
			return Result{}, err
		}

		if !dr.Usable() {
			// A status saying nothing was observed cannot travel with rows, so the rows go
			// with the status rather than the other way round.
			rows = nil
		}
		obs, err := options.NewObservation(options.ObservationRequest{
			TradingDate:     date,
			AsOf:            q.AsOf,
			Product:         q.Product,
			Family:          q.Family,
			Session:         q.Session,
			Source:          q.Source,
			SourceStatus:    dr.Status,
			Snapshots:       refs,
			RejectedRows:    rejected,
			SettlementRead:  dr.SettlementRead,
			SettlingExpiry:  dr.SettlingExpiry,
			SettlementDates: nil,
		}, rows)
		if err != nil {
			return Result{}, fmt.Errorf("optionshistory: %s %s: %w", date, q.key(), err)
		}

		res.Dates = append(res.Dates, dr)
		res.Observations = append(res.Observations, obs)
		if dr.Usable() {
			res.ValidObservations++
		}
	}

	if !res.Truncated && res.ValidObservations < q.Count && res.ScannedDates >= q.MaxScanDates {
		res.Truncated = true
		res.TruncatedReason = fmt.Sprintf(
			"the walk reached the %d-date scan bound with %d of the %d requested valid "+
				"observations", q.MaxScanDates, res.ValidObservations, q.Count)
	}
	if err := assertPointInTime(res, cutoff); err != nil {
		return Result{}, err
	}
	return res, nil
}

// LoadView reads the series and assembles M5's answer for the newest usable trading date.
//
// The reading and its history come from ONE Load, under ONE cutoff, which is the property that
// makes the view point-in-time: a change resolved under one cutoff and a percentile under
// another would be two answers wearing one label.
//
// It is a view/interface only. There is no report wiring, no config flag and no rendering here.
func LoadView(ctx context.Context, st *store.Store, q Query, m options.MetricType,
	p institutional.Policy) (options.MetricView, Result, error) {

	np, err := p.Normalized()
	if err != nil {
		return options.MetricView{}, Result{}, err
	}
	if q.Count == 0 {
		q.Count = RequiredObservations(np)
	}
	res, err := Load(ctx, st, q)
	if err != nil {
		return options.MetricView{}, Result{}, err
	}
	current, ok := res.Latest()
	if !ok {
		return options.MetricView{}, res, fmt.Errorf("%w: %s %s as of %s",
			ErrNoObservations, q.Dataset, q.key(), q.AsOf)
	}
	view, err := options.BuildMetricView(m, current, res.Before(current.TradingDate), np)
	if err != nil {
		return options.MetricView{}, res, err
	}
	return view, res, nil
}

// ── the walk's parts ──────────────────────────────────────────────────────────────────

func sessionsOf(session string) []string {
	if session == provider.SessionCombined {
		return []string{provider.SessionDay, provider.SessionAfterHours}
	}
	return []string{session}
}

// candidateDates is every trading date VISIBLE at the cutoff, newest first.
//
// The union of two tables, because the two hold different halves of the history: a date the
// exchange did not publish has a health record and no snapshot, and dropping it would make a
// four-day change indistinguishable from a one-session one. Both halves are bounded on the
// cutoff — the snapshots on fetched_at, the health records on recorded_at.
//
// The ORDER BY and the Go sort below are NOT redundant, and §10.7 is the contract:
//
//	SQL ORDER BY    decides WHICH dates survive the LIMIT
//	Go sort.Reverse decides the final arrangement of what came back
//
// A Go re-sort cannot recover a date the SQL LIMIT already truncated away, so a mutation of
// the SQL ordering alone produces a correctly-sorted list of the WRONG days — and is invisible
// whenever MaxScanDates exceeds the available history, which is the shape of every small
// fixture. TestTheScanLimitKeepsTheMostRecentOptionDates makes the history LONGER than the
// limit for exactly that reason.
func candidateDates(ctx context.Context, st *store.Store, q Query, sessions []string) ([]string, error) {
	bound, err := derivatives.AsOfCutoff(q.AsOf)
	if err != nil {
		return nil, err
	}
	at := bound.Format(time.RFC3339)

	seen := map[string]bool{}
	var dates []string
	collect := func(query string, args ...any) error {
		rows, err := queryDates(ctx, st, query, args...)
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

	for _, session := range sessions {
		if err := collect(`
			SELECT DISTINCT trading_date FROM derivative_snapshots
			WHERE source = ? AND trading_session = ?
			  AND trading_date <= ? AND fetched_at <= ?
			ORDER BY trading_date DESC
			LIMIT ?`, q.Source, session, q.AsOf, at, q.MaxScanDates); err != nil {
			return nil, err
		}
		if err := collect(`
			SELECT DISTINCT trading_date FROM derivative_data_health
			WHERE dataset = ? AND session = ?
			  AND trading_date <= ? AND recorded_at <= ?
			ORDER BY trading_date DESC
			LIMIT ?`, q.Dataset, session, q.AsOf, at, q.MaxScanDates); err != nil {
			return nil, err
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	if len(dates) > q.MaxScanDates {
		dates = dates[:q.MaxScanDates]
	}
	return dates, nil
}

func queryDates(ctx context.Context, st *store.Store, query string, args ...any) ([]string, error) {
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
		return nil, fmt.Errorf("optionshistory: read trading dates: %w", err)
	}
	return out, nil
}

type healthKey struct{ date, session string }

type healthRow struct {
	found    bool
	dataset  string
	status   string
	reason   string
	rows     int
	rejected int
}

// visibleHealth reads every health record for this dataset and these sessions that was
// RECORDED at or before the cutoff.
//
// The recorded_at bound is load-bearing and easy to leave out. derivative_data_health is
// replace-on-write by design — it is "the current answer to what happened when we last tried"
// — so the row for an old date can have been rewritten yesterday, and reading it unbounded
// would let a status established after the cutoff decide what a reading dated before the
// cutoff saw.
func visibleHealth(ctx context.Context, st *store.Store, q Query, cutoff time.Time) (
	map[healthKey]healthRow, error) {

	out := map[healthKey]healthRow{}
	at := cutoff.Format(time.RFC3339)
	err := st.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT trading_date, session, dataset, status, reason, row_count, rejected_row_count
			FROM derivative_data_health
			WHERE dataset = ? AND trading_date <= ? AND recorded_at <= ?
			ORDER BY trading_date DESC`, q.Dataset, q.AsOf, at)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var date, session string
			var h healthRow
			if err := rows.Scan(&date, &session, &h.dataset, &h.status, &h.reason,
				&h.rows, &h.rejected); err != nil {
				return err
			}
			h.found = true
			out[healthKey{date, session}] = h
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("optionshistory: read data health: %w", err)
	}
	return out, nil
}

// visibleSnapshot is §7.3's per-date read, delegated to internal/derivatives so there is
// exactly ONE implementation of "which revision may a reading dated A see".
func visibleSnapshot(ctx context.Context, st *store.Store, source, session, date, asOf string) (
	derivatives.Snapshot, bool, error) {

	snap, err := derivatives.SnapshotForSession(ctx, st, source, session, date, asOf)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return derivatives.Snapshot{}, false, nil
	case err != nil:
		return derivatives.Snapshot{}, false,
			fmt.Errorf("optionshistory: snapshot for %s/%s: %w", date, session, err)
	}
	return snap, true, nil
}

// resolveSession decides one (date, session)'s §6 status from the two things that can know it.
//
// The same rule M4's reader uses, and the third line is the one worth arguing about: a visible
// health record that is NOT usable wins over visible rows, and the rows are left unread. STALE
// settles it — a reading whose freshness contract already failed must not be laundered into a
// usable observation by the fact that rows exist.
func resolveSession(session string, h healthRow, snap derivatives.Snapshot, hasSnap bool) SessionResolution {
	sr := SessionResolution{Session: session}
	if h.found {
		status, err := institutional.SourceStatus(h.status)
		if err != nil {
			sr.Status = institutional.StatusError
			sr.StatusSource = StatusFromHealth
			sr.Reason = fmt.Sprintf("health record carries status %q, which is not one of "+
				"§6's: %v", h.status, err)
			return sr
		}
		sr.StatusSource = StatusFromHealth
		sr.Rejected = h.rejected
		if !status.Usable() {
			sr.Status = status
			sr.Reason = h.reason
			if sr.Reason == "" {
				sr.Reason = fmt.Sprintf("the %s health record for %s is %s", h.dataset, session, status)
			}
			return sr
		}
		if !hasSnap {
			sr.Status = institutional.StatusMissing
			sr.Reason = fmt.Sprintf("the %s health record says %s and no revision was visible "+
				"at the cutoff — nothing can be read for this session", h.dataset, status)
			return sr
		}
		sr.Status = status
		sr.SnapshotID, sr.Revision, sr.FetchedAt = snap.ID, snap.RevisionNo, snap.FetchedAt
		if status == institutional.StatusPartial {
			sr.Reason = h.reason
		}
		return sr
	}
	if hasSnap {
		// No health record was visible. The rows are, and rows only exist because
		// acquisition stored them, which it does on AVAILABLE and PARTIAL only. AVAILABLE
		// is recorded with StatusSource SNAPSHOT_ONLY so a reader can see that the
		// completeness half of the claim has no record behind it.
		sr.Status = institutional.StatusAvailable
		sr.StatusSource = StatusFromSnapshot
		sr.SnapshotID, sr.Revision, sr.FetchedAt = snap.ID, snap.RevisionNo, snap.FetchedAt
		sr.Reason = "no health record was visible at the cutoff; the stored rows are the observation"
		return sr
	}
	sr.Status = institutional.StatusMissing
	sr.StatusSource = StatusFromSnapshot
	sr.Reason = "neither a snapshot nor a health record was visible for this session"
	return sr
}

// combineStatus turns the per-session statuses into the date's own.
//
// One session: its status, unchanged. Two:
//
//	both usable                  → PARTIAL if either is PARTIAL, else AVAILABLE
//	one usable, one not          → PARTIAL, naming the absent session
//	neither usable, agreeing     → that status
//	neither usable, disagreeing  → MISSING, naming both
//
// The second line is the one that matters and its consequence is intended: §6.2 then refuses
// to publish the aggregate, because a COMBINED volume figure missing the night session is a
// third of the activity and would reconcile against nothing.
func combineStatus(sessions []SessionResolution) (institutional.MetricStatus, string) {
	if len(sessions) == 0 {
		return institutional.StatusMissing, "no session was resolved"
	}
	if len(sessions) == 1 {
		return sessions[0].Status, sessions[0].Reason
	}
	usable, unusable := 0, []SessionResolution{}
	partial := false
	for _, s := range sessions {
		if s.Usable() {
			usable++
			if s.Status == institutional.StatusPartial {
				partial = true
			}
			continue
		}
		unusable = append(unusable, s)
	}
	switch {
	case usable == len(sessions) && !partial:
		return institutional.StatusAvailable, ""
	case usable == len(sessions):
		return institutional.StatusPartial, "a session's snapshot is PARTIAL"
	case usable > 0:
		names := make([]string, 0, len(unusable))
		for _, s := range unusable {
			names = append(names, fmt.Sprintf("%s is %s", s.Session, s.Status))
		}
		return institutional.StatusPartial, fmt.Sprintf(
			"%v, so a figure spanning both sessions would span one of them", names)
	}
	first := unusable[0].Status
	for _, s := range unusable[1:] {
		if s.Status != first {
			return institutional.StatusMissing, fmt.Sprintf(
				"the sessions disagree (%s and %s) and neither carries rows",
				first, s.Status)
		}
	}
	return first, unusable[0].Reason
}

// resolveSettlement reads the settlement snapshot ARCHIVED FOR THIS DATE and asks it which
// expiry stopped being live exposure at this session's close.
//
// Against the trading date being aggregated, never against "today" and never against the live
// feed. A failed read is not an optional input: §3.1 makes the open-interest half MISSING
// without it, because the alternative is a 2.31x error with an AVAILABLE status.
func resolveSettlement(ctx context.Context, st *store.Store, q Query, date string,
	dr *DateResolution) error {

	if q.SettlementSource == "" {
		dr.SettlementReason = "no settlement feed was configured for this query, so the " +
			"open-interest half has no way to know whether an expiry settled"
		return nil
	}
	snap, hasSnap, err := visibleSnapshot(ctx, st, q.SettlementSource,
		provider.SessionCombined, date, q.AsOf)
	if err != nil {
		return err
	}
	if !hasSnap {
		dr.SettlementReason = fmt.Sprintf(
			"no %s snapshot was visible for %s at the cutoff", q.SettlementSource, date)
		return nil
	}
	decoded, err := provider.ParseFinalSettlement([]byte(snap.Payload))
	if err != nil {
		dr.SettlementReason = fmt.Sprintf("the %s snapshot for %s did not decode: %v",
			q.SettlementSource, date, err)
		return nil
	}
	dr.SettlementRead = true
	dr.SettlementSnapshot = snap.ID
	dr.SettlingExpiry = provider.SettlingExpiry(decoded.Rows, q.Product, date)
	if dr.SettlingExpiry == "" {
		dr.SettlementReason = fmt.Sprintf(
			"%s is not a settlement day for %s; the feed's latest event is for another day",
			date, q.Product)
	}
	return nil
}

// toStrikeRows turns stored rows back into the decoder's shape.
//
// The session and the trading date come from the SNAPSHOT, which is where they are recorded:
// options_oi_by_strike has neither column, and the session is on the snapshot precisely
// because the by-strike key has no room for it (§10.2b).
//
// ExpiryKind is deliberately left EMPTY. The store does not carry a classification, and
// filling one in here would be a second classifier — options.classify derives it from the
// CODE, through M3's ClassifyExpiry, which is the only rule §3.1 allows.
func toStrikeRows(stored []derivatives.StoredStrike, date, session string) []provider.OptionStrikeRow {
	out := make([]provider.OptionStrikeRow, 0, len(stored))
	for _, s := range stored {
		out = append(out, provider.OptionStrikeRow{
			TradingDate: date,
			Product:     s.Product,
			ExpiryCode:  s.ExpiryCode,
			Strike:      s.Strike,
			CallPut:     s.CallPut,
			Session:     session,
			OI:          s.OI,
			Volume:      s.Volume,
			Settlement:  s.Settlement,
		})
	}
	return out
}

// assertPointInTime re-checks, in Go and over the ASSEMBLED result, the bounds the SQL already
// applied — and pins the ORDERING, which no single query proves.
func assertPointInTime(r Result, cutoff time.Time) error {
	prev := ""
	for _, d := range r.Dates {
		if d.TradingDate > r.AsOf {
			return fmt.Errorf("%w: %s is later than the as-of date %s",
				ErrLookAheadLeak, d.TradingDate, r.AsOf)
		}
		for _, s := range d.Sessions {
			if !s.FetchedAt.IsZero() && s.FetchedAt.After(cutoff) {
				return fmt.Errorf("%w: %s/%s resolved to revision %d fetched at %s, after "+
					"the cutoff %s", ErrLookAheadLeak, d.TradingDate, s.Session, s.Revision,
					s.FetchedAt.UTC().Format(time.RFC3339), cutoff.Format(time.RFC3339))
			}
		}
		if prev != "" && d.TradingDate >= prev {
			return fmt.Errorf(
				"optionshistory: trading dates came back out of order (%s after %s) — the "+
					"walk must be ordered by trading date descending, or a correction to an "+
					"older session becomes the current one", d.TradingDate, prev)
		}
		prev = d.TradingDate
	}
	for _, o := range r.Observations {
		if o.TradingDate > r.AsOf {
			return fmt.Errorf("%w: %s is later than the as-of date %s",
				ErrLookAheadLeak, o.TradingDate, r.AsOf)
		}
		if at := o.Provenance.FetchedAt(); !at.IsZero() && at.After(cutoff) {
			return fmt.Errorf("%w: %s carries fetched_at %s, after the cutoff %s",
				ErrLookAheadLeak, o.TradingDate, at.UTC().Format(time.RFC3339),
				cutoff.Format(time.RFC3339))
		}
	}
	return nil
}
