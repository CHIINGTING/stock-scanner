package acquire

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// The acquisition run: the one stage of §2.4's pipeline that may touch the network.
//
//	for each Dataset:
//	  transport.Get → structural validation → (split sessions) → parse
//	    → PutSnapshot per session → persist rows → record health
//
// Two properties of this loop are load-bearing and both are about NOT hiding things:
//
//   - A response that carries two trading sessions becomes TWO snapshots (§10.2b). Never one
//     COMBINED snapshot, never a sum, never whichever session happened to arrive: the session
//     lives on the SNAPSHOT because options_oi_by_strike is keyed without a session column, so
//     merging makes 126 of the fixture's 277 rows collide on the UNIQUE index — and the
//     official volume comes out at 214,161 against 356,219 if one session is dropped.
//   - Failure is PER DATASET AND PER SESSION, never a single pass/fail for the run. Nine feeds
//     publish at different times and fail independently; a run that reported one verdict would
//     have to choose between calling a normal afternoon broken and calling a real gap fine.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §2.2, §2.4, §3.1, §6, §6.1, §6.2, §10.2b.

// Runner performs one acquisition run.
type Runner struct {
	// Transport fetches. HTTPTransport live, FixtureTransport in tests.
	Transport Transport
	// Store is the R15 database, already migrated (store.Open with derivatives.Schema).
	Store *store.Store
	// ArchiveDir preserves the UNDIVIDED response beside the database (§5, §14). Empty
	// disables archiving — which is right for a test and wrong for production, so a caller
	// that wants it sets configs' derivatives.data_dir (derivatives.DefaultArchiveDir).
	//
	// It is not defaulted to the production directory precisely because it is not defaulted:
	// a test that forgot to set it would otherwise write into the repository's data/.
	ArchiveDir string
	// Datasets defaults to All. Narrowing it is how a caller re-fetches one feed; the health
	// records for the datasets NOT listed are left exactly as the last run wrote them, rather
	// than being invented.
	Datasets []Dataset
	// Now is the fetch clock. Zero → time.Now().UTC(). fetched_at means WHEN THIS REPOSITORY
	// RETRIEVED THE BYTES (§6.3), so it is stamped once per response and shared by every
	// snapshot that response produces.
	Now  func() time.Time
	Logf func(format string, args ...any)
}

// Outcome is one (dataset, session) result of a run.
type Outcome struct {
	Dataset Dataset
	Session string
	// Health is the record written for this outcome. Always populated.
	Health Record
	// Snapshot is what was stored. The zero value when nothing was — which is every status
	// except AVAILABLE and PARTIAL.
	Snapshot derivatives.Snapshot
	// ArchivePath is where the undivided response was preserved, "" when archiving is off.
	ArchivePath string
	// Err is the failure behind a non-usable status, kept typed so a caller can tell a rate
	// limit from a layout change without parsing Reason.
	Err error
}

// RunResult is the whole run, per dataset and per session.
type RunResult struct {
	TradingDate string
	// Session is the dailydata session state the run was for.
	Session  string
	Outcomes []Outcome
}

// Records returns every health record the run wrote, in outcome order.
func (r RunResult) Records() []Record {
	out := make([]Record, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		out = append(out, o.Health)
	}
	return out
}

// Find returns one outcome by dataset name and session.
func (r RunResult) Find(dataset, session string) (Outcome, bool) {
	for _, o := range r.Outcomes {
		if o.Dataset.Name == dataset && o.Session == session {
			return o, true
		}
	}
	return Outcome{}, false
}

// StatusOf is Find's status, or "" when the run produced no such outcome.
//
// "" rather than a default status: a dataset that was never attempted has no status, and
// returning one would be the same lie as a health record nobody wrote.
func (r RunResult) StatusOf(dataset, session string) Status {
	o, ok := r.Find(dataset, session)
	if !ok {
		return ""
	}
	return o.Health.Status
}

// Unusable returns the outcomes whose stored rows may not be read as evidence — everything
// that is not AVAILABLE or PARTIAL.
//
// This is what a caller checks instead of a run-level error. A run in which one of nine feeds
// failed SUCCEEDED at acquiring the other eight, and collapsing that into a returned error
// would make the caller choose between discarding eight good datasets and ignoring one bad
// one.
func (r RunResult) Unusable() []Outcome {
	var out []Outcome
	for _, o := range r.Outcomes {
		if !o.Health.Status.Usable() {
			out = append(out, o)
		}
	}
	return out
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r *Runner) datasets() []Dataset {
	if len(r.Datasets) > 0 {
		return r.Datasets
	}
	return All
}

// Run acquires every dataset for one target session.
//
// The target comes from internal/dailydata and is decided BEFORE any request is made, which
// is what makes NO_SESSION a fact about the calendar rather than an inference from an empty
// response (§6). A non-trading day issues no requests at all and still writes a full set of
// records, because "we did not look, and here is why" is the answer the panel needs.
//
// The returned error is about the RUN — a bad target, an unwritable database, a cancelled
// context — never about a dataset. Dataset failures are in the result, per dataset and per
// session.
func (r *Runner) Run(ctx context.Context, target dailydata.SessionState) (RunResult, error) {
	if r.Transport == nil || r.Store == nil {
		return RunResult{}, fmt.Errorf("acquire: a run needs a Transport and a Store")
	}
	if _, err := time.Parse("2006-01-02", target.Date); err != nil {
		return RunResult{}, fmt.Errorf("acquire: target date must be YYYY-MM-DD, got %q", target.Date)
	}
	if target.Session == dailydata.SessionOpen {
		// Acquiring a session that has not finished archives a day in the middle of being
		// written down, and the snapshot is immutable once written. dailydata.Resolve never
		// produces this — it targets the previous weekday instead — so reaching it means a
		// caller built a SessionState by hand.
		return RunResult{}, fmt.Errorf("acquire: %s is still OPEN; a session is acquired after "+
			"it closes, never during", target.Date)
	}
	dss := r.datasets()
	for _, ds := range dss {
		if _, ok := parsers[ds.Name]; !ok {
			// Before any request: a dataset with no parser is a programming error, and half
			// a run of health records is a worse way to learn about it than a refusal.
			return RunResult{}, fmt.Errorf("acquire: dataset %q has no parser registered", ds.Name)
		}
	}

	res := RunResult{TradingDate: target.Date, Session: target.Session}

	if target.Session == dailydata.SessionNone {
		reason := target.Reason
		if reason == "" {
			reason = "the exchange did not trade on this date"
		}
		for _, ds := range dss {
			for _, session := range sessionsOf(ds) {
				h := NewRecord(ds, target.Date, session)
				h.Status = derivatives.StatusNoSession
				h.Reason = reason
				res.Outcomes = append(res.Outcomes, Outcome{Dataset: ds, Session: session, Health: h})
			}
		}
		return res, r.writeHealth(ctx, res)
	}

	for _, ds := range dss {
		if err := ctx.Err(); err != nil {
			// Stop, and write what actually happened. No records are invented for the
			// datasets that were never attempted: a fabricated MISSING is a claim about the
			// exchange, and the exchange was never asked.
			_ = r.writeHealth(ctx, res)
			return res, err
		}
		res.Outcomes = append(res.Outcomes, r.one(ctx, ds, target.Date)...)
	}
	return res, r.writeHealth(ctx, res)
}

func (r *Runner) writeHealth(ctx context.Context, res RunResult) error {
	return PutRecords(ctx, r.Store, res.Records()...)
}

// sessionsOf is the set of health/snapshot identities one dataset produces.
//
// Two for the split feeds and one for everything else, and it is the SAME list whatever
// happens to the response — including a fetch that never returned. A dataset that reports one
// session on a bad day and two on a good one leaves a panel unable to tell an absent session
// from an absent record.
func sessionsOf(ds Dataset) []string {
	if ds.SplitSessions {
		return []string{derivatives.SessionDay, derivatives.SessionAfterHours}
	}
	session := ds.Session
	if session == "" {
		session = derivatives.SessionCombined
	}
	return []string{session}
}

// one acquires a single dataset.
//
// Two phases, and the boundary between them is §6.2's. PREPARE decodes and validates without
// writing anything; COMMIT archives, snapshots and stores. Nothing is written until every
// session of the response has passed, because "the whole response is rejected" has to mean
// the whole response: a by-strike report that fails to decode must not leave the day session
// ERROR and the night session NOT_PUBLISHED, which would be a claim about what the exchange
// has published made from bytes we could not read.
func (r *Runner) one(ctx context.Context, ds Dataset, date string) []Outcome {
	sessions := sessionsOf(ds)

	resp, err := r.Transport.Get(ctx, ds)
	if err != nil {
		status, reason := transportFailure(err, resp)
		r.logf("acquire: %s: %s: %v", ds.Name, status, err)
		return failed(ds, date, sessions, status, reason, err)
	}
	fetchedAt := r.now()

	var (
		ready      []prepared
		unassigned []provider.RejectedRow
	)
	if ds.SplitSessions {
		// §10.2b. Partition BEFORE anything else looks at the bytes: each session is parsed,
		// hashed and stored as an independent observation, and the partition — not a
		// re-serialisation of it — is what gets hashed, so content_hash stays a property of
		// what the exchange said rather than of Go's marshaller.
		parts, rejects, err := provider.SplitBySession(ds.Endpoint, resp.Body)
		if err != nil {
			r.logf("acquire: %s: structural failure: %v", ds.Name, err)
			return failed(ds, date, sessions, derivatives.StatusError,
				"structural failure: "+err.Error(), err)
		}
		unassigned = rejects
		byS := map[string]provider.SessionPart{}
		for _, part := range parts {
			byS[part.Session] = part
		}
		for _, session := range sessions {
			part, ok := byS[session]
			if !ok {
				ready = append(ready, absentPartition(session, parts, rejects))
				continue
			}
			ready = append(ready, r.prepare(ds, date, session, part.Body))
		}
	} else {
		ready = append(ready, r.prepare(ds, date, sessions[0], resp.Body))
	}

	// A whole-response rejection reaching ANY session rejects the response. It is not
	// per-session evidence: an undecodable envelope and a date that contradicts the requested
	// session are both statements about the bytes, and the partitions were cut from the same
	// bytes.
	for _, pr := range ready {
		if pr.whole {
			r.logf("acquire: %s: whole-response rejection: %s", ds.Name, pr.reason)
			return failedWith(ds, date, sessions, pr.bad, pr.reason, pr.err, fetchedAt, unassigned)
		}
	}

	var out []Outcome
	for _, pr := range ready {
		out = append(out, r.commit(ctx, ds, date, resp.Body, pr, unassigned, fetchedAt))
	}
	return out
}

// absentPartition decides what an ABSENT session partition means (§6).
//
// Two different facts reach this point looking identical — the partition is not there — and §6
// forbids collapsing them in either direction:
//
//	the exchange has not released this session yet   → NOT_PUBLISHED. Come back later. The
//	                                                   night rows are published after the day
//	                                                   rows, so a 15:00 run legitimately sees
//	                                                   one and not the other.
//	every row that could have been this session was
//	unassignable                                     → ERROR. Waiting never fixes it.
//
// The second is what a relabelled TradingSession looks like: if the exchange renamed 一般 to
// 早盤, all 277 by-strike rows are rejected by NormalizeSession, BOTH partitions come back
// empty, and reporting NOT_PUBLISHED twice would describe a layout change as a normal
// afternoon — for as long as the label stays renamed, which is forever. §14 makes "a week of
// AVAILABLE in the data-health panel" the gate for turning `show` on, so a permanent
// NOT_PUBLISHED is a gate that never fires and never says why.
//
// An unassignable row cannot be attributed to a session — that column is what decides which
// snapshot it belongs to — so ANY rejected row makes an absent partition suspect: the missing
// session may be exactly the one those rows were. That is the same reasoning rejectionReason
// applies when it counts an unassignable row on every session of a response, and it errs in
// the same direction: never present as merely-unpublished a session that might have been
// thrown away.
//
// This is NOT a whole-response rejection. The rows that DID assign are a real observation of
// their own session and are stored; only the absent partition is an ERROR.
func absentPartition(session string, parts []provider.SessionPart,
	rejected []provider.RejectedRow) prepared {

	out := prepared{session: session}
	total := rowsIn(parts) + len(rejected)
	if len(rejected) > 0 {
		out.reason = fmt.Sprintf("the response carried no %s rows, and %d of its %d row(s) "+
			"carried an unrecognised TradingSession (first: %s) — every row that could have "+
			"been this session was unassignable, which a later fetch does not fix (%s)",
			session, len(rejected), total, rejected[0], presentSessions(parts))
		out.bad = derivatives.StatusError
		out.err = errors.New(out.reason)
		return out
	}
	// NOT an empty snapshot. The night rows are published later than the day rows, so a 15:00
	// run legitimately sees one session and not the other; storing a zero-row snapshot for the
	// other would make "the exchange has not released it" indistinguishable from "the exchange
	// released nothing", and the first is a normal afternoon while the second is a fault.
	out.bad = derivatives.StatusNotPublished
	out.reason = fmt.Sprintf("the response carried no %s rows (%d row(s) in all; %s)",
		session, total, presentSessions(parts))
	return out
}

func rowsIn(parts []provider.SessionPart) int {
	var n int
	for _, part := range parts {
		n += part.Rows
	}
	return n
}

func presentSessions(parts []provider.SessionPart) string {
	if len(parts) == 0 {
		return "none"
	}
	var s string
	for i, part := range parts {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s×%d", part.Session, part.Rows)
	}
	return s
}

// prepared is one session's decoded, validated, NOT YET STORED response.
type prepared struct {
	session string
	body    []byte
	p       parsed
	// dateSource is OBSERVED or ASSIGNED, decided by §2.2/§3.1.
	dateSource string
	// bad is non-empty when this session may not be stored, with reason and err beside it.
	bad    Status
	reason string
	err    error
	// whole marks a bad that condemns the ENTIRE response rather than this session: a
	// structural failure or a date that contradicts the requested session. A session that
	// merely lost every row to §6.2's per-row test is not whole — the other session's rows
	// are still a real observation.
	whole bool
}

// prepare decodes one body and decides whether it may be stored. It writes nothing.
func (r *Runner) prepare(ds Dataset, date, session string, body []byte) prepared {
	out := prepared{session: session, body: body}

	p, err := parsers[ds.Name](body)
	if err != nil {
		// §6.2: structural, so nothing is stored — not a snapshot, not a row, not an archive
		// entry pretending the response was usable.
		out.bad, out.reason, out.err, out.whole =
			derivatives.StatusError, "structural failure: "+err.Error(), err, true
		return out
	}
	out.p = p
	if p.rowsAccepted == 0 {
		// Every row was rejected. rawRows already refused an empty payload, so this is a
		// response whose rows all failed §6.2's per-row test — storing it would create a
		// snapshot that reads as an observed session containing nothing.
		out.err = fmt.Errorf("acquire: %s/%s: no row survived (%d rejected)",
			ds.Name, session, len(p.rejected))
		out.bad, out.reason = derivatives.StatusError, out.err.Error()
		return out
	}

	dateSource, bad, reason := resolveDate(ds, date, p.dates)
	if bad != "" {
		out.bad, out.reason, out.err = bad, reason, errors.New(reason)
		// A date that contradicts the requested session is §6.2's whole-response rejection.
		// NOT_PUBLISHED and MISSING are not: they say the response is fine and does not cover
		// the session asked for, which is the same fact for every partition of it — but they
		// are reached only by the history feed, which is never split.
		out.whole = bad == derivatives.StatusError
		return out
	}
	out.dateSource = dateSource
	return out
}

// commit stores one prepared session: the archive, the snapshot, then the typed rows.
//
// raw is always the UNDIVIDED response, which is what the archive preserves: the database
// holds the partitions a reader queries, and the archive holds the response those partitions
// were derived from.
//
// unassigned carries the rows a split could not place in any session. See rejectionReason for
// why they are counted on every session of the response.
func (r *Runner) commit(ctx context.Context, ds Dataset, date string, raw []byte,
	pr prepared, unassigned []provider.RejectedRow, fetchedAt time.Time) Outcome {

	h := NewRecord(ds, date, pr.session)
	h.FetchedAt = fetchedAt
	h.RejectedRowCount = len(pr.p.rejected) + len(unassigned)

	if pr.bad != "" {
		h.Status, h.Reason = pr.bad, pr.reason
		if pr.bad == derivatives.StatusNotPublished {
			// Nothing was fetched FOR this session; the fetch time belongs to the response
			// that did not carry it, and the counts belong to the rows that did.
			h.RejectedRowCount = len(unassigned)
		}
		return Outcome{Dataset: ds, Session: pr.session, Health: h, Err: pr.err}
	}

	fail := func(reason string, err error) Outcome {
		h.Status, h.Reason = derivatives.StatusError, reason
		return Outcome{Dataset: ds, Session: pr.session, Health: h, Err: err}
	}

	archived := ""
	if r.ArchiveDir != "" {
		entry, err := derivatives.ArchiveRaw(r.ArchiveDir, date, ds.Endpoint, raw)
		if err != nil {
			return fail("archive: "+err.Error(), err)
		}
		archived = entry.Path
	}

	snap, err := derivatives.PutSnapshot(ctx, r.Store, derivatives.Snapshot{
		Source:         ds.Endpoint,
		TradingDate:    date,
		TradingSession: pr.session,
		DateSource:     pr.dateSource,
		Payload:        string(pr.body),
		FetchedAt:      fetchedAt,
	})
	if err != nil {
		return fail("snapshot: "+err.Error(), err)
	}

	if pr.p.write != nil {
		if _, err := pr.p.write(ctx, r.Store, snap.ID, ds.Endpoint, fetchedAt); err != nil {
			// A row write that fails after the snapshot landed is a key collision or a
			// database fault, never ordinary data: the writers use plain INSERTs precisely so
			// the §10.2b collision is loud. Reported as ERROR with the snapshot it belongs to.
			r.logf("acquire: %s/%s: persist rows: %v", ds.Name, pr.session, err)
			out := fail("persist rows: "+err.Error(), err)
			out.Snapshot, out.ArchivePath = snap, archived
			return out
		}
	}

	h.RowCount = pr.p.rowsAccepted
	if h.RejectedRowCount > 0 {
		h.Status = derivatives.StatusPartial
		h.Reason = rejectionReason(pr.p.rejected, unassigned)
	} else {
		h.Status = derivatives.StatusAvailable
	}
	return Outcome{Dataset: ds, Session: pr.session, Health: h, Snapshot: snap,
		ArchivePath: archived}
}

// rejectionReason explains a PARTIAL, including the awkward half.
//
// A row whose TradingSession is unrecognised belongs to NO session — that column is what
// decides which snapshot it would have gone into — so it cannot honestly be attributed to
// either one. It is therefore counted on BOTH, and said so: neither session may be presented
// as complete, because either could be the one missing a row. Counting it once, on whichever
// session happened to be written first, would leave the other reading AVAILABLE while a row
// that might have been its own was thrown away.
//
// The consequence is that rejected counts across the two sessions of one response must not be
// summed. That is a smaller price than a session that looks complete and is not.
func rejectionReason(rejected, unassigned []provider.RejectedRow) string {
	switch {
	case len(rejected) > 0 && len(unassigned) > 0:
		return fmt.Sprintf("%d row(s) rejected in this session (first: %s); %d row(s) carried "+
			"an unrecognised TradingSession and belong to no session, so they are counted on "+
			"every session of this response (first: %s)",
			len(rejected), rejected[0], len(unassigned), unassigned[0])
	case len(unassigned) > 0:
		return fmt.Sprintf("%d row(s) carried an unrecognised TradingSession and belong to no "+
			"session, so they are counted on every session of this response (first: %s)",
			len(unassigned), unassigned[0])
	case len(rejected) > 0:
		return fmt.Sprintf("%d row(s) rejected (first: %s)", len(rejected), rejected[0])
	}
	return ""
}

// failed builds the same non-usable outcome for every session of a dataset — the shape a
// fetch that never produced a body takes, and the shape a whole-response rejection takes.
func failed(ds Dataset, date string, sessions []string, status Status, reason string,
	err error) []Outcome {

	return failedWith(ds, date, sessions, status, reason, err, time.Time{}, nil)
}

// failedWith is failed with the retrieval that did happen recorded on it.
//
// fetchedAt is zero when nothing was retrieved and set when bytes arrived and were rejected —
// the two are different facts, and a panel that cannot tell them apart cannot say whether the
// exchange was reachable.
func failedWith(ds Dataset, date string, sessions []string, status Status, reason string,
	err error, fetchedAt time.Time, unassigned []provider.RejectedRow) []Outcome {

	out := make([]Outcome, 0, len(sessions))
	for _, session := range sessions {
		h := NewRecord(ds, date, session)
		h.Status = status
		h.Reason = reason
		h.FetchedAt = fetchedAt
		h.RejectedRowCount = len(unassigned)
		out = append(out, Outcome{Dataset: ds, Session: session, Health: h, Err: err})
	}
	return out
}

// transportFailure classifies a fetch that never produced a body.
//
// The split is between "the attempt failed" and "the exchange answered, and answered that
// there is nothing here":
//
//	4xx other than the rate-limit family → MISSING. The endpoint answered and had nothing to
//	                                       give; waiting does not help, a fix or a backfill
//	                                       might. That is exactly §6's MISSING.
//	everything else                      → ERROR. A 5xx, a timeout, a refused connection or a
//	                                       maintenance page is an attempt that failed, and the
//	                                       next attempt may well succeed.
//
// Rate limits stay ERROR despite being 4xx: 429 is the exchange declining to answer, not an
// answer. §6 does not adjudicate 4xx-versus-5xx directly; this is the reading that keeps
// MISSING and ERROR meaning what §6 says they mean.
func transportFailure(err error, resp Response) (Status, string) {
	attempts := resp.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	suffix := fmt.Sprintf(" (after %d attempt(s))", attempts)
	var se *StatusError
	if errors.As(err, &se) && se.Status >= 400 && se.Status < 500 && !IsRateLimited(err) {
		return derivatives.StatusMissing, "the endpoint answered HTTP " + fmt.Sprint(se.Status) +
			", so there is nothing to observe here: " + err.Error() + suffix
	}
	return derivatives.StatusError, "fetch failed: " + err.Error() + suffix
}

// resolveDate applies §2.2 and §3.1 to the dates a response carried.
//
// Returns the date_source to store, or a non-empty status meaning nothing may be stored.
//
//	DateFromRequest → ASSIGNED, unconditionally. These feeds carry a date that is not a
//	                  trading day — a settlement day, a per-contract future settlement, a
//	                  margin EFFECTIVE date — and validating it would raise a mismatch on
//	                  every ordinary day and leave M7 permanently at margin_source: NONE.
//	DateFromFeed    → the target must be among the dates the response actually carried.
//
// The last rule has three failures, and they are three different facts:
//
//	one date, and it is not the target   → ERROR (§6.2: the response's own date contradicts
//	                                       the requested session — today's data wearing
//	                                       yesterday's label is the thing this prevents)
//	many dates, target after the newest  → NOT_PUBLISHED. A history feed (PutCallRatio carries
//	                                       ~23 sessions) that simply does not have D yet.
//	many dates, target before the oldest → MISSING. D fell out of the history window; waiting
//	                                       will not bring it back, a backfill would.
func resolveDate(ds Dataset, target string, dates []string) (dateSource string, bad Status, reason string) {
	if ds.Dates == DateFromRequest {
		return derivatives.DateAssigned, "", ""
	}
	if len(dates) == 0 {
		return "", derivatives.StatusError, "the response carried no observation date, and " + ds.Name +
			" is a feed whose date must be validated against the requested session"
	}
	uniq := map[string]bool{}
	var sorted []string
	for _, d := range dates {
		if d == "" || uniq[d] {
			continue
		}
		uniq[d] = true
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	if uniq[target] {
		return derivatives.DateObserved, "", ""
	}
	if len(sorted) == 1 {
		return "", derivatives.StatusError, fmt.Sprintf("date mismatch: the response describes %s, the "+
			"requested session is %s", sorted[0], target)
	}
	oldest, newest := sorted[0], sorted[len(sorted)-1]
	if target < oldest {
		return "", derivatives.StatusMissing, fmt.Sprintf("the response carries %d sessions from %s to %s; "+
			"%s is older than the window, so waiting will not produce it",
			len(sorted), oldest, newest, target)
	}
	return "", derivatives.StatusNotPublished, fmt.Sprintf("the response carries %d sessions from %s to %s "+
		"and does not yet include %s", len(sorted), oldest, newest, target)
}

// ── parsers ───────────────────────────────────────────────────────────────────────────

// parsed is one decoded response, reduced to what acquisition needs to decide with.
type parsed struct {
	// dates is every OBSERVATION date the accepted rows carried. Empty for a feed that
	// carries none. A slice rather than one string because PutCallRatio answers with ~23
	// sessions where every other R15 feed answers with one.
	dates []string
	// rowsAccepted is FEED rows, which is what the health record counts.
	rowsAccepted int
	rejected     []provider.RejectedRow
	// write persists the typed rows under the snapshot, and is nil for a dataset whose
	// storage IS the snapshot payload. Nil is not "nothing was stored": the response is in
	// derivative_snapshots either way, and M4–M8 decode from there. A typed table exists only
	// where a reader needs to query rows rather than a document.
	write func(ctx context.Context, s *store.Store, snapshotID int64, source string,
		fetchedAt time.Time) (int, error)
}

type parserFunc func(body []byte) (parsed, error)

// parsers dispatches on Dataset.Name.
//
// On the NAME rather than the endpoint, so a renamed endpoint does not silently lose its
// parser; and checked before any request is made, so a dataset added to the catalogue without
// one fails the run rather than producing eight good records and one hole.
var parsers = map[string]parserFunc{
	OptionsByStrike.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseOptionsByStrike(body)
		if err != nil {
			return parsed{}, err
		}
		return parsed{
			dates:        dateOf(v.TradingDate),
			rowsAccepted: v.Report.RowsAccepted,
			rejected:     v.Report.Rejected,
			write: func(ctx context.Context, s *store.Store, id int64, source string,
				at time.Time) (int, error) {
				return derivatives.PutOptionStrikes(ctx, s, id, source, at, v.Rows)
			},
		}, nil
	},
	FuturesDaily.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseFuturesDaily(body)
		if err != nil {
			return parsed{}, err
		}
		// No typed writer: M2 has no futures_daily table, so the snapshot payload is the
		// storage. Inventing a table here would put a schema decision in the fetcher.
		return parsed{
			dates:        dateOf(v.TradingDate),
			rowsAccepted: v.Report.RowsAccepted,
			rejected:     v.Report.Rejected,
		}, nil
	},
	InstitutionalFutures.Name:   institutionalParser(provider.ParseInstitutionalFutures),
	InstitutionalCallsPuts.Name: institutionalParser(provider.ParseInstitutionalCallsPuts),
	PutCallRatio.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParsePutCallRatio(body)
		if err != nil {
			return parsed{}, err
		}
		var dates []string
		for _, row := range v.Rows {
			dates = append(dates, row.TradingDate)
		}
		return parsed{dates: dates, rowsAccepted: v.Report.RowsAccepted,
			rejected: v.Report.Rejected}, nil
	},
	Margin.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseMargin(body)
		if err != nil {
			return parsed{}, err
		}
		return parsed{
			rowsAccepted: v.Report.RowsAccepted,
			rejected:     v.Report.Rejected,
			write: func(ctx context.Context, s *store.Store, id int64, source string,
				at time.Time) (int, error) {
				return derivatives.PutMarginRates(ctx, s, id, source, at, v.Rows)
			},
		}, nil
	},
	FinalSettlement.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseFinalSettlement(body)
		if err != nil {
			return parsed{}, err
		}
		// TheFinalSettlementDay is a SETTLEMENT day: it equals the trading date only on a
		// settlement day, so it is deliberately not offered as an observation date.
		return parsed{rowsAccepted: v.Report.RowsAccepted, rejected: v.Report.Rejected}, nil
	},
	SettledPositions.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseSettledPositions(body)
		if err != nil {
			return parsed{}, err
		}
		return parsed{rowsAccepted: v.Report.RowsAccepted, rejected: v.Report.Rejected}, nil
	},
	OptionsDelta.Name: func(body []byte) (parsed, error) {
		v, err := provider.ParseOptionsDelta(body)
		if err != nil {
			return parsed{}, err
		}
		// ContractSettlementDay is a FUTURE date, per row. Never an observation date.
		return parsed{rowsAccepted: v.Report.RowsAccepted, rejected: v.Report.Rejected}, nil
	},
}

func institutionalParser(parse func([]byte) (*provider.Institutional, error)) parserFunc {
	return func(body []byte) (parsed, error) {
		v, err := parse(body)
		if err != nil {
			return parsed{}, err
		}
		return parsed{
			dates:        dateOf(v.TradingDate),
			rowsAccepted: v.Report.RowsAccepted,
			rejected:     v.Report.Rejected,
			write: func(ctx context.Context, s *store.Store, id int64, source string,
				at time.Time) (int, error) {
				return derivatives.PutInstitutional(ctx, s, id, source, at, v.Rows)
			},
		}, nil
	}
}

// dateOf wraps a single observation date, and yields nothing for an empty one — which is what
// a response whose every row was rejected leaves behind, and is not a date.
func dateOf(d string) []string {
	if d == "" {
		return nil
	}
	return []string{d}
}
