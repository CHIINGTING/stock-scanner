package derivatives

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/store"
)

// Acquisition data health: one record per (dataset, trading_date, session).
//
// This is the layer's answer to "did we look, and what happened" — and it is written for
// EVERY dataset on every run, including the ones that produced nothing. A dataset that fails
// silently is indistinguishable from a dataset nobody asked for, and §14 makes a week of
// AVAILABLE here the precondition for turning the report section on, so the panel has to be
// able to say what it did not get.

// Status is §6's vocabulary, in full.
//
// It is a SUPERSET of internal/healthcheck's with a different domain (§6.1), not a rival:
// these describe a FETCH OUTCOME, while healthcheck.Status describes whether a computed block
// has something to show. The two are mapped explicitly below rather than assumed compatible.
type Status string

const (
	// StatusAvailable — observed, fresh, complete.
	StatusAvailable Status = "AVAILABLE"
	// StatusPartial — observed, some rows rejected (§6.2). The stored rows are usable; an
	// aggregate whose correctness depends on completeness is NOT.
	StatusPartial Status = "PARTIAL"
	// StatusMissing — not observed, and waiting does not help. A backfill might.
	StatusMissing Status = "MISSING"
	// StatusStale — observed, but older than this dataset expects. Not produced by
	// acquisition (see the note on Statuses), and never collapsed into either neighbour:
	// doing so is invariant 4's failure.
	StatusStale Status = "STALE"
	// StatusError — the fetch or the parse failed; the reason is recorded verbatim.
	StatusError Status = "ERROR"
	// StatusNotAvailable — no authorized source exists at all (IV today).
	StatusNotAvailable Status = "NOT_AVAILABLE"
	// StatusNoSession — the exchange did not trade. Decided before any request is made, from
	// internal/dailydata, and never inferred from an empty response.
	StatusNoSession Status = "NO_SESSION"
	// StatusNotPublished — the session happened; the exchange has not released this dataset
	// yet. Separated from MISSING because the two imply OPPOSITE actions: come back later
	// versus this will not appear by waiting. TAIFEX releases institutional positions well
	// after the by-strike report, so a 15:00 run legitimately sees one and not the other.
	StatusNotPublished Status = "NOT_PUBLISHED"
)

// Statuses is the closed set, in the order §6 lists them.
//
// Exported so a test can iterate it: a new status added without classifying it in Usable /
// Numeric / MapToHealthcheck fails immediately rather than defaulting to whatever the last
// case in a switch happened to be.
var Statuses = []Status{
	StatusAvailable, StatusPartial, StatusMissing, StatusStale,
	StatusError, StatusNotAvailable, StatusNoSession, StatusNotPublished,
}

// Valid reports whether s is one of the eight.
func (s Status) Valid() bool {
	for _, k := range Statuses {
		if k == s {
			return true
		}
	}
	return false
}

// Usable reports whether the observation behind this status may be read as evidence.
//
// PARTIAL is usable with a caveat the status cannot carry on its own: §6.2 forbids publishing
// an aggregate whose correctness depends on completeness from a PARTIAL snapshot. The counts
// on the health record are what a caller checks; this only says the stored rows are real.
func (s Status) Usable() bool { return s == StatusAvailable || s == StatusPartial }

// Numeric reports whether a reader may render this row as a number.
//
// False for everything that is not an observation, which is the whole point: R14 spent a
// milestone on INSUFFICIENT_DATA rendered as 0, and the rule that came out of it is that a
// value with nothing behind it must never look like one. STALE is false as well — a stale
// figure is a real observation, but presenting it as current is invariant 4's failure, so it
// renders with its own as_of rather than as a bare number.
func (s Status) Numeric() bool { return s == StatusAvailable || s == StatusPartial }

// MapToHealthcheck translates into the existing vocabulary for M10's shared panel, following
// §6.1's table exactly. It returns the healthcheck.Status VALUE as a string rather than the
// typed constant, so this package keeps no import of internal/healthcheck: that package
// depends on internal/scanner and internal/market/provider, and pulling a live-fetch subtree
// in here to spell one enum would undo §1.5's leaf property for a mapping table. A test in
// derivatives_test pins each string against the real constant, which is where the coupling
// belongs.
//
// It returns ok=false for STALE and NOT_PUBLISHED, and that is the interesting half. Neither
// has an equivalent, and both nearest neighbours are wrong in a way that loses the fact:
// STALE→AVAILABLE presents an old figure as current (invariant 4), STALE→UNAVAILABLE throws
// away a real observation, and NOT_PUBLISHED→UNAVAILABLE turns a normal afternoon into a
// fault. A caller that cannot render them must say so, not pick one.
func (s Status) MapToHealthcheck() (string, bool) {
	switch s {
	case StatusAvailable:
		return "AVAILABLE", true
	case StatusPartial:
		return "PARTIAL", true
	case StatusMissing, StatusError, StatusNoSession:
		// "not observed", "the attempt failed" and "the exchange did not trade" all reach a
		// consumer as UNAVAILABLE — "the source was consulted and had nothing". They stay
		// distinct HERE, on the health record, which is the panel that exists to tell them
		// apart; what collapses is only the coarse status a shared block renders.
		return "UNAVAILABLE", true
	case StatusNotAvailable:
		return "NOT_IMPLEMENTED", true
	case StatusStale, StatusNotPublished:
		return "", false
	}
	return "", false
}

// DataHealth is one acquisition outcome.
//
// Every field is here because a panel that cannot answer the question it is on cannot be
// acted on. Status alone does not distinguish "we stored 270 rows and rejected 2" from "we
// stored 2 and rejected 270", and both are PARTIAL.
type DataHealth struct {
	// Dataset is the stable logical name (options_by_strike), which survives an endpoint
	// rename; Source is the endpoint the bytes came from, verbatim.
	Dataset     string
	TradingDate string
	// Session is DAY / AFTER_HOURS / COMBINED — the same vocabulary as the snapshot, because
	// one response can produce two outcomes (§10.2b) and one status per date would hide one.
	Session string
	Source  string
	Status  Status
	// AsOf is the session this record describes. Equal to TradingDate for an observed
	// dataset; for margin it is deliberately still the trading date and NOT the effective
	// date, which would light up a STALE rule on a margin that is simply unchanged (§3.1).
	AsOf string
	// FetchedAt is when this repository retrieved the bytes. Zero when nothing was retrieved.
	FetchedAt time.Time
	// ExpectedAvailability is the exchange's declared publication cut-off for this dataset,
	// as a Taipei wall clock. It is what makes NOT_PUBLISHED actionable: "come back after
	// 15:00" rather than "come back".
	ExpectedAvailability string
	RowCount             int
	RejectedRowCount     int
	Reason               string
	RecordedAt           time.Time
}

// PutDataHealth records one outcome, replacing any earlier record for the same key.
//
// Replacing rather than appending is right HERE and wrong for a snapshot, and the difference
// is what the row means. A snapshot is an observation and an observation is never edited; a
// health record is the CURRENT answer to "what happened when we last tried", and a run at
// 15:00 that finds NOT_PUBLISHED followed by one at 17:00 that finds AVAILABLE has one
// answer, not two. The evidence of the earlier attempt survives in the snapshot table, which
// is where evidence belongs.
func PutDataHealth(ctx context.Context, s *store.Store, h DataHealth) error {
	if h.Dataset == "" || h.TradingDate == "" || h.Session == "" {
		return fmt.Errorf("derivatives: data health needs dataset, trading_date and session")
	}
	switch h.Session {
	case SessionDay, SessionAfterHours, SessionCombined:
	default:
		return fmt.Errorf("derivatives: data health session must be DAY, AFTER_HOURS or "+
			"COMBINED, got %q", h.Session)
	}
	if !h.Status.Valid() {
		return fmt.Errorf("derivatives: data health status %q is not one of %v", h.Status, Statuses)
	}
	if _, err := time.Parse("2006-01-02", h.TradingDate); err != nil {
		return fmt.Errorf("derivatives: data health trading_date must be YYYY-MM-DD, got %q",
			h.TradingDate)
	}
	if h.RecordedAt.IsZero() {
		h.RecordedAt = time.Now().UTC()
	}
	var fetched string
	if !h.FetchedAt.IsZero() {
		fetched = h.FetchedAt.UTC().Format(time.RFC3339)
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO derivative_data_health
			  (schema_version, dataset, trading_date, session, source, status, as_of,
			   fetched_at, expected_availability, row_count, rejected_row_count, reason,
			   recorded_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(dataset, trading_date, session) DO UPDATE SET
			  source                = excluded.source,
			  status                = excluded.status,
			  as_of                 = excluded.as_of,
			  fetched_at            = excluded.fetched_at,
			  expected_availability = excluded.expected_availability,
			  row_count             = excluded.row_count,
			  rejected_row_count    = excluded.rejected_row_count,
			  reason                = excluded.reason,
			  recorded_at           = excluded.recorded_at`,
			Schema.Version(), h.Dataset, h.TradingDate, h.Session, h.Source, string(h.Status),
			h.AsOf, fetched, h.ExpectedAvailability, h.RowCount, h.RejectedRowCount,
			h.Reason, h.RecordedAt.UTC().Format(time.RFC3339))
		return err
	})
}

// DataHealthFor returns every record for one trading date, ordered by dataset then session so
// a panel and a test see the same list.
func DataHealthFor(ctx context.Context, s *store.Store, tradingDate string) ([]DataHealth, error) {
	var out []DataHealth
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT dataset, trading_date, session, source, status, as_of, fetched_at,
			       expected_availability, row_count, rejected_row_count, reason, recorded_at
			FROM derivative_data_health
			WHERE trading_date = ?`, tradingDate)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h DataHealth
			var status, fetched, recorded string
			if err := rows.Scan(&h.Dataset, &h.TradingDate, &h.Session, &h.Source, &status,
				&h.AsOf, &fetched, &h.ExpectedAvailability, &h.RowCount, &h.RejectedRowCount,
				&h.Reason, &recorded); err != nil {
				return err
			}
			h.Status = Status(status)
			if fetched != "" {
				h.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
			}
			h.RecordedAt, _ = time.Parse(time.RFC3339, recorded)
			out = append(out, h)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("derivatives: read data health: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dataset != out[j].Dataset {
			return out[i].Dataset < out[j].Dataset
		}
		return out[i].Session < out[j].Session
	})
	return out, nil
}
