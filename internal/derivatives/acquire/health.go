package acquire

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// The acquisition data-health record: what M10's panel reads, and the only place an
// acquisition failure survives as a fact rather than as a log line.
//
// One record per (dataset, trading_date, session). Written for EVERY dataset on every run,
// including the ones that produced nothing — a dataset that fails silently is
// indistinguishable from one nobody asked for, and §14 makes a week of AVAILABLE here the
// precondition for turning the report section on, so the panel has to be able to say what it
// did not get.
//
// The vocabulary and the table are M2's (internal/derivatives/health.go, migration 2). This
// file does NOT redefine either. It is the acquisition-shaped view of them: how a fetch
// outcome becomes a record, what a record may not be written without, and how it reads out.
// A second Status enum here would be the FU-10 failure in miniature — two implementations of
// one vocabulary, disagreeing about whether a given day was healthy depending on which one
// the caller happened to reach.

// Status is §6's vocabulary. A type ALIAS, not a new type: acquire and derivatives must be
// the same enum, so a value crossing the boundary needs no conversion and no conversion can
// lose the distinction between two of them.
type Status = derivatives.Status

// The eight values are deliberately NOT re-exported as acquire.StatusX constants, and the
// reason is a collision worth respecting rather than working around: this package already
// declares StatusError, and it is the HTTP failure type carrying "the exchange answered 503".
// A health status spelled identically, one identifier away from it, is precisely the
// ambiguity that produces a switch matching the wrong thing. Acquisition code therefore
// writes derivatives.StatusError when it means the health status — every time, with no
// shorthand that could be mistaken for the other.

// Statuses is the closed set, in §6's order.
var Statuses = derivatives.Statuses

// MustExplain lists the four statuses that a record may not carry without a reason.
//
// Each of them makes a CLAIM ABOUT THE WORLD that the status word alone cannot support, and
// each has a nearest neighbour that means the opposite thing to whoever reads the panel:
//
//	MISSING       "it will not appear by waiting"  ↔ NOT_PUBLISHED "come back later"
//	NOT_PUBLISHED "the exchange has not released it yet" — a normal afternoon, not a fault
//	NO_SESSION    "the exchange did not trade" — decided from the calendar, never inferred
//	                from an empty response
//	STALE         "this is real but old" — presenting it as current is invariant 4's failure
//
// An unexplained one of these is already half-collapsed: a reader who cannot see WHY has no
// way to act differently than they would on any other quiet row, which is exactly the state
// §6 separates them to prevent. So the write is refused rather than the reason being
// defaulted to something neutral.
var MustExplain = []Status{
	derivatives.StatusMissing, derivatives.StatusNotPublished,
	derivatives.StatusNoSession, derivatives.StatusStale,
}

func mustExplain(s Status) bool {
	for _, k := range MustExplain {
		if k == s {
			return true
		}
	}
	return false
}

// Record is one acquisition outcome, in the columns §6/§6.1 name.
//
// Status alone does not distinguish "we stored 270 rows and rejected 2" from "we stored 2 and
// rejected 270", and both are PARTIAL — so the counts are fields, not prose inside Reason.
type Record struct {
	// Dataset is the stable logical name (options_by_strike); Source is the endpoint the
	// bytes came from, verbatim. Both, because the first survives a rename and the second is
	// what every stored row says about its own provenance.
	Dataset     string
	TradingDate string
	// Session is DAY / AFTER_HOURS / COMBINED — the snapshot's vocabulary, because one
	// response can produce two outcomes (§10.2b) and one status per date would hide one of
	// them. COMBINED is a sentinel for "this feed has no session dimension", never NULL.
	Session string
	// ExpectedAvailability is when this dataset should exist for a session, in the words the
	// panel shows a reader. It is what makes NOT_PUBLISHED actionable — "come back after
	// 15:00" rather than "come back" — and it is carried on the record rather than looked up
	// later, so a record stays readable after the catalogue moves.
	ExpectedAvailability string
	// FetchedAt is when this repository retrieved the bytes. Zero when nothing was retrieved,
	// which is a different claim from "retrieved at the zero time" and is stored as an empty
	// string rather than 0001-01-01.
	FetchedAt time.Time
	// RowCount is FEED rows accepted into the snapshot — not database rows written. One
	// institutional feed row becomes six institutional_derivatives rows, and a count that
	// mixed the two would make the same response look six times larger on one dataset than
	// on another.
	//
	// It is taken from the PARSE, never from the writer's return value: an idempotent re-run
	// writes 0 new rows over an unchanged snapshot (invariant 9), and recording that would
	// rewrite a healthy record as an empty one on every second run.
	RowCount int
	// RejectedRowCount is §6.2's counted rejects. PARTIAL without a count cannot tell an
	// aggregate whether it may be published.
	RejectedRowCount int
	Source           string
	Status           Status
	Reason           string
	// RecordedAt is when this record was written. Zero → now, at persist time.
	RecordedAt time.Time
}

// NewRecord starts a record for one dataset and session, with everything that is known before
// the request is made.
//
// Status is deliberately left empty — the zero Status is not AVAILABLE and does not Validate,
// so a code path that forgets to set one fails at the write instead of publishing a default.
func NewRecord(ds Dataset, tradingDate, session string) Record {
	return Record{
		Dataset:              ds.Name,
		TradingDate:          tradingDate,
		Session:              session,
		ExpectedAvailability: ds.ExpectedFreshness,
		Source:               ds.Endpoint,
	}
}

// Validate reports whether this record may be written.
func (r Record) Validate() error {
	if r.Dataset == "" || r.TradingDate == "" || r.Session == "" {
		return fmt.Errorf("acquire: health record needs dataset, trading_date and session")
	}
	switch r.Session {
	case derivatives.SessionDay, derivatives.SessionAfterHours, derivatives.SessionCombined:
	default:
		return fmt.Errorf("acquire: health session must be DAY, AFTER_HOURS or COMBINED, got %q",
			r.Session)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("acquire: health status %q is not one of %v", r.Status, Statuses)
	}
	if mustExplain(r.Status) && strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("acquire: %s %s/%s is %s with no reason — %v each mean something a "+
			"reader has to act on differently, and an unexplained one reads as any other quiet row",
			r.Dataset, r.TradingDate, r.Session, r.Status, MustExplain)
	}
	if r.Status == derivatives.StatusPartial && r.RejectedRowCount == 0 {
		return fmt.Errorf("acquire: %s %s/%s is PARTIAL with 0 rejected rows — §6.2 makes "+
			"PARTIAL a COUNTED state, and an uncounted one cannot tell an aggregate whether "+
			"it may be published", r.Dataset, r.TradingDate, r.Session)
	}
	return nil
}

// DataHealth converts to the stored shape.
//
// AsOf is the TRADING DATE, for every dataset including margin. Margin's own effective date
// would be the intuitive choice and is the wrong one: it changes only when the exchange
// revises margins, so showing it would light up §6's STALE rule on a figure that is current
// and simply has not changed (§3.1's presentation note for M10).
func (r Record) DataHealth() derivatives.DataHealth {
	return derivatives.DataHealth{
		Dataset:              r.Dataset,
		TradingDate:          r.TradingDate,
		Session:              r.Session,
		Source:               r.Source,
		Status:               r.Status,
		AsOf:                 r.TradingDate,
		FetchedAt:            r.FetchedAt,
		ExpectedAvailability: r.ExpectedAvailability,
		RowCount:             r.RowCount,
		RejectedRowCount:     r.RejectedRowCount,
		Reason:               r.Reason,
		RecordedAt:           r.RecordedAt,
	}
}

// PutRecords writes records to derivative_data_health.
//
// Every record is validated BEFORE any of them is written, so a run cannot leave half its
// health behind and abort on the record that would have explained why.
func PutRecords(ctx context.Context, s *store.Store, records ...Record) error {
	for _, r := range records {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	for _, r := range records {
		if err := derivatives.PutDataHealth(ctx, s, r.DataHealth()); err != nil {
			return fmt.Errorf("acquire: record health for %s/%s: %w", r.Dataset, r.Session, err)
		}
	}
	return nil
}

// HealthFor reads one trading date's records back, in the stored order.
func HealthFor(ctx context.Context, s *store.Store, tradingDate string) ([]Record, error) {
	rows, err := derivatives.DataHealthFor(ctx, s, tradingDate)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, h := range rows {
		out = append(out, Record{
			Dataset:              h.Dataset,
			TradingDate:          h.TradingDate,
			Session:              h.Session,
			ExpectedAvailability: h.ExpectedAvailability,
			FetchedAt:            h.FetchedAt,
			RowCount:             h.RowCount,
			RejectedRowCount:     h.RejectedRowCount,
			Source:               h.Source,
			Status:               h.Status,
			Reason:               h.Reason,
			RecordedAt:           h.RecordedAt,
		})
	}
	return out, nil
}

// Explain renders one record as the line a data-health panel shows.
//
// It always names the status verbatim and never substitutes a neighbour for it. That is the
// whole job: the collapse §6 forbids does not usually happen in storage, it happens at the
// last step, where NOT_PUBLISHED becomes a blank cell that reads as fine and MISSING becomes
// a 0 that reads as an observation. R14 spent a milestone on the second one.
//
// The counts are shown for every status, including the ones where they are 0/0 — "nothing was
// stored" is a fact worth printing next to ERROR, and hiding it there would make an ERROR row
// look like a row nobody had got to yet.
func (r Record) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s/%s: %s (%d rows, %d rejected)",
		r.Dataset, r.TradingDate, r.Session, r.Status, r.RowCount, r.RejectedRowCount)
	if r.Reason != "" {
		fmt.Fprintf(&b, " — %s", r.Reason)
	}
	if r.Status == derivatives.StatusNotPublished && r.ExpectedAvailability != "" {
		fmt.Fprintf(&b, " [expected: %s]", r.ExpectedAvailability)
	}
	return b.String()
}
