package acquire

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// Acquisition tests. Every one of them replays a committed fixture through FixtureTransport;
// nothing here opens a socket, which is the property http.go's package comment promises and
// §2.4 makes structural.

func openR15(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(store.Config{
		Path:   filepath.Join(t.TempDir(), "r15.db"),
		Schema: derivatives.Schema,
	})
	if err != nil {
		t.Fatalf("open r15 store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// closed is the ordinary target: the fixtures' session, already finished.
func closed() dailydata.SessionState {
	return dailydata.SessionState{Date: FixtureDate, Session: dailydata.SessionClosed}
}

func runner(t *testing.T, tr Transport, s *store.Store) *Runner {
	t.Helper()
	return &Runner{
		Transport: tr,
		Store:     s,
		// t.TempDir, never the production data/derivatives: an archive written by a test is
		// indistinguishable on disk from one written by a real run.
		ArchiveDir: filepath.Join(t.TempDir(), "archive"),
		Now:        func() time.Time { return time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC) },
	}
}

func mustRun(t *testing.T, r *Runner, target dailydata.SessionState) RunResult {
	t.Helper()
	res, err := r.Run(context.Background(), target)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func health(t *testing.T, res RunResult, dataset, session string) Record {
	t.Helper()
	o, ok := res.Find(dataset, session)
	if !ok {
		t.Fatalf("no outcome for %s/%s; got %v", dataset, session, summary(res))
	}
	return o.Health
}

func summary(res RunResult) []string {
	var out []string
	for _, o := range res.Outcomes {
		out = append(out, fmt.Sprintf("%s/%s=%s", o.Dataset.Name, o.Session, o.Health.Status))
	}
	sort.Strings(out)
	return out
}

func countRows(t *testing.T, s *store.Store, table string) int {
	t.Helper()
	var n int
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// sessionBody re-serialises a by-strike response keeping only the named feed sessions, so a
// test can build "the night rows are not out yet" without committing a second fixture.
func sessionBody(t *testing.T, file string, keep ...string) []byte {
	t.Helper()
	raw, err := ReadFixture("", file)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for _, r := range rows {
		for _, k := range keep {
			if r["TradingSession"] == k {
				out = append(out, r)
				break
			}
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── the session split (§10.2b) ────────────────────────────────────────────────────────

// TestBothSessionsAreStoredAndReadBackIndependently is the mutation guard for "store one
// COMBINED snapshot instead".
//
// It asserts on a key that exists in BOTH sessions with DIFFERENT values — TXO 202609F1
// 43000 賣權 traded 0 lots in 一般 and 37 in 盤後 — because that is the only shape that can
// tell a split from a merge. A merge cannot store both: they collide on
// UNIQUE(snapshot_id, product, expiry_code, strike, call_put), so one session's volume is
// either lost or overwrites the other's.
func TestBothSessionsAreStoredAndReadBackIndependently(t *testing.T) {
	s := openR15(t)
	r := runner(t, &FixtureTransport{}, s)
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	day, ok := res.Find(OptionsByStrike.Name, derivatives.SessionDay)
	if !ok {
		t.Fatalf("no DAY outcome: %v", summary(res))
	}
	night, ok := res.Find(OptionsByStrike.Name, derivatives.SessionAfterHours)
	if !ok {
		t.Fatalf("no AFTER_HOURS outcome: %v", summary(res))
	}
	for _, o := range []Outcome{day, night} {
		if o.Health.Status != derivatives.StatusAvailable {
			t.Fatalf("%s: %s — %s", o.Session, o.Health.Status, o.Health.Reason)
		}
	}

	// Two snapshots, one trading date, distinct content.
	if n := countRows(t, s, "derivative_snapshots"); n != 2 {
		t.Fatalf("want 2 snapshots (DAY and AFTER_HOURS), got %d", n)
	}
	if day.Snapshot.TradingDate != FixtureDate || night.Snapshot.TradingDate != FixtureDate {
		t.Fatalf("both sessions describe %s; got %s and %s",
			FixtureDate, day.Snapshot.TradingDate, night.Snapshot.TradingDate)
	}
	if day.Snapshot.ContentHash == night.Snapshot.ContentHash {
		t.Fatal("the two sessions hashed identically — the response was not partitioned")
	}
	if day.Snapshot.ID == night.Snapshot.ID {
		t.Fatal("one snapshot serving both sessions")
	}

	// The fixture's row counts, per session, as captured.
	if day.Health.RowCount != 151 || night.Health.RowCount != 126 {
		t.Fatalf("row counts: DAY %d (want 151), AFTER_HOURS %d (want 126)",
			day.Health.RowCount, night.Health.RowCount)
	}

	// The same key reads back differently from each session.
	ctx := context.Background()
	dayRow := findStrike(t, ctx, s, day.Snapshot.ID, "TXO", "202609F1", 43000, "PUT")
	nightRow := findStrike(t, ctx, s, night.Snapshot.ID, "TXO", "202609F1", 43000, "PUT")
	if dayRow.Volume == nil || *dayRow.Volume != 0 {
		t.Fatalf("DAY volume: want an observed 0, got %v", deref(dayRow.Volume))
	}
	if nightRow.Volume == nil || *nightRow.Volume != 37 {
		t.Fatalf("AFTER_HOURS volume: want 37, got %v — the sessions are not independent",
			deref(nightRow.Volume))
	}
	if dayRow.OI == nil || *dayRow.OI != 162 {
		t.Fatalf("DAY open interest: want 162, got %v", dayRow.OI)
	}
	// Mutation guard: the night row carries "-", which is NOT zero.
	if nightRow.OI != nil {
		t.Fatalf("AFTER_HOURS open interest was %v; the feed sent \"-\", which is an absence, "+
			"not an observed 0", *nightRow.OI)
	}
	if nightRow.Settlement != nil {
		t.Fatalf("AFTER_HOURS settlement was %v; the feed sent \"-\"", *nightRow.Settlement)
	}
	// …and the day row's "0" values ARE zero, so the guard above is not just "everything is
	// nil". 1,295 live TXO rows carry a real 0 next to 2,868 carrying "-".
	if dayRow.Settlement == nil || *dayRow.Settlement != 0 {
		t.Fatalf("DAY settlement: want an observed 0, got %v", dayRow.Settlement)
	}
}

func findStrike(t *testing.T, ctx context.Context, s *store.Store, snapshotID int64,
	product, expiry string, strike float64, callPut string) derivatives.StoredStrike {
	t.Helper()
	rows, err := derivatives.OptionStrikesForSnapshot(ctx, s, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Product == product && r.ExpiryCode == expiry && r.Strike == strike && r.CallPut == callPut {
			return r
		}
	}
	t.Fatalf("snapshot %d has no %s/%s/%v/%s (%d rows)", snapshotID, product, expiry,
		strike, callPut, len(rows))
	return derivatives.StoredStrike{}
}

func deref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// TestASessionAbsentFromTheResponseIsNotPublished — the night rows are published later, so a
// response carrying only 一般 is a normal afternoon, not an empty night session.
func TestASessionAbsentFromTheResponseIsNotPublished(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{Bodies: map[string][]byte{
		OptionsByStrike.Name: sessionBody(t, "options_by_strike_20260904.json", "一般"),
	}}
	r := runner(t, tr, s)
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	if got := health(t, res, OptionsByStrike.Name, derivatives.SessionDay).Status; got != derivatives.StatusAvailable {
		t.Fatalf("DAY: want AVAILABLE, got %s", got)
	}
	night := health(t, res, OptionsByStrike.Name, derivatives.SessionAfterHours)
	if night.Status != derivatives.StatusNotPublished {
		t.Fatalf("AFTER_HOURS: want NOT_PUBLISHED, got %s — an absent session is not an empty "+
			"one", night.Status)
	}
	if night.RowCount != 0 {
		t.Fatalf("NOT_PUBLISHED carries %d rows", night.RowCount)
	}
	if night.Reason == "" {
		t.Fatal("NOT_PUBLISHED with no reason reads as any other quiet row")
	}
	// No snapshot for the session that was not published. One, for the one that was.
	if n := countRows(t, s, "derivative_snapshots"); n != 1 {
		t.Fatalf("want exactly 1 snapshot, got %d — an absent session must not become an "+
			"empty snapshot", n)
	}
	// And it survives the round trip through the database, which is where a collapse would
	// actually reach a reader.
	stored := storedHealth(t, s, OptionsByStrike.Name, derivatives.SessionAfterHours)
	if stored.Status != derivatives.StatusNotPublished {
		t.Fatalf("stored status: want NOT_PUBLISHED, got %s", stored.Status)
	}
}

func storedHealth(t *testing.T, s *store.Store, dataset, session string) Record {
	t.Helper()
	rows, err := HealthFor(context.Background(), s, FixtureDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Dataset == dataset && r.Session == session {
			return r
		}
	}
	t.Fatalf("no stored health for %s/%s", dataset, session)
	return Record{}
}

// ── §6.2: whole-response versus per-row ───────────────────────────────────────────────

// TestAStructuralFailureStoresNothing covers the whole-response rejection, one malformed
// fixture at a time. Each is a separate §6.2 bullet and each must reach the same place:
// ERROR, and an untouched database.
func TestAStructuralFailureStoresNothing(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{"html error page", "malformed/html_error_page.html"},
		{"no rows", "malformed/no_rows.json"},
		{"undecodable", "malformed/undecodable.json"},
		{"required column renamed away", "malformed/missing_openinterest_column.json"},
		{"two trading dates", "malformed/options_by_strike_mixed_dates.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := ReadFixture("", tc.file)
			if err != nil {
				t.Fatal(err)
			}
			s := openR15(t)
			// Served as application/json on purpose: the transport's content-type check must
			// not be what catches this, or the parser's structural rejection is untested.
			tr := &FixtureTransport{Bodies: map[string][]byte{OptionsByStrike.Name: body}}
			r := runner(t, tr, s)
			r.Datasets = []Dataset{OptionsByStrike}
			res := mustRun(t, r, closed())

			for _, session := range []string{derivatives.SessionDay, derivatives.SessionAfterHours} {
				h := health(t, res, OptionsByStrike.Name, session)
				if h.Status != derivatives.StatusError {
					t.Fatalf("%s: want ERROR, got %s (%s)", session, h.Status, h.Reason)
				}
				if h.RowCount != 0 {
					t.Fatalf("%s: ERROR carries %d rows", session, h.RowCount)
				}
			}
			if n := countRows(t, s, "derivative_snapshots"); n != 0 {
				t.Fatalf("a structural failure stored %d snapshot(s); §6.2 stores nothing", n)
			}
			if n := countRows(t, s, "options_oi_by_strike"); n != 0 {
				t.Fatalf("a structural failure stored %d row(s)", n)
			}
			// The health record itself is written — that is the one thing that must exist.
			if n := countRows(t, s, "derivative_data_health"); n != 2 {
				t.Fatalf("want 2 health records (one per session), got %d", n)
			}
		})
	}
}

// TestPerRowFailureIsPartialWithACount is §6.2's other half: the response is well formed, some
// rows are not, and the good ones are stored with the bad ones counted.
func TestPerRowFailureIsPartialWithACount(t *testing.T) {
	body, err := ReadFixture("", "malformed/options_by_strike_bad_rows.json")
	if err != nil {
		t.Fatal(err)
	}
	s := openR15(t)
	tr := &FixtureTransport{Bodies: map[string][]byte{OptionsByStrike.Name: body}}
	r := runner(t, tr, s)
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	// The fixture: 5 rows. One good 一般 row; one 一般 row with a non-numeric StrikePrice (a
	// NOT NULL key); one 盤後 row whose Volume token is unknown (nullable → ABSENT, stored);
	// one 一般 row with an unrecognised CallPut (in the key); one row whose TradingSession is
	// unrecognised, which belongs to no session at all.
	day := health(t, res, OptionsByStrike.Name, derivatives.SessionDay)
	if day.Status != derivatives.StatusPartial {
		t.Fatalf("DAY: want PARTIAL, got %s (%s)", day.Status, day.Reason)
	}
	if day.RowCount != 1 {
		t.Fatalf("DAY row count: want 1 stored, got %d", day.RowCount)
	}
	// 2 rejected in this session (strike, call/put) + 1 unassignable.
	if day.RejectedRowCount != 3 {
		t.Fatalf("DAY rejected count: want 3, got %d", day.RejectedRowCount)
	}

	night := health(t, res, OptionsByStrike.Name, derivatives.SessionAfterHours)
	if night.Status != derivatives.StatusPartial {
		t.Fatalf("AFTER_HOURS: want PARTIAL, got %s (%s)", night.Status, night.Reason)
	}
	if night.RowCount != 1 {
		t.Fatalf("AFTER_HOURS row count: want 1, got %d", night.RowCount)
	}
	// The unassignable row is counted here too: its TradingSession is what decides which
	// snapshot it belongs to, so neither session may be presented as complete.
	if night.RejectedRowCount != 1 {
		t.Fatalf("AFTER_HOURS rejected count: want 1 (the unassignable row), got %d",
			night.RejectedRowCount)
	}

	// The valid rows really are stored, and the "-"/"NULL"/"" tokens in them are absences.
	if n := countRows(t, s, "options_oi_by_strike"); n != 2 {
		t.Fatalf("want the 2 valid rows stored, got %d", n)
	}
	nightSnap, _ := res.Find(OptionsByStrike.Name, derivatives.SessionAfterHours)
	row := findStrike(t, context.Background(), s, nightSnap.Snapshot.ID, "TXO", "202609", 46200, "CALL")
	if row.Volume != nil {
		t.Fatalf("an unknown Volume token stored as %v; §3.1 makes it ABSENT", *row.Volume)
	}
	if row.OI != nil {
		t.Fatalf(`OpenInterest "-" stored as %v`, *row.OI)
	}
	if row.Settlement != nil {
		t.Fatalf(`SettlementPrice "NULL" stored as %v`, *row.Settlement)
	}
}

// TestPartialIsAlwaysCounted — a PARTIAL with no count cannot tell an aggregate whether it may
// be published (§6.2), so the record is refused rather than written uncounted.
func TestPartialIsAlwaysCounted(t *testing.T) {
	r := Record{
		Dataset: "options_by_strike", TradingDate: FixtureDate,
		Session: derivatives.SessionDay, Status: derivatives.StatusPartial,
		RowCount: 10, RejectedRowCount: 0,
	}
	if err := r.Validate(); err == nil {
		t.Fatal("an uncounted PARTIAL was accepted")
	}
}

// ── §2.2 / §3.1: which date, and where it came from ───────────────────────────────────

// TestDateSourceIsObservedOrAssignedPerDataset.
//
// Dropping this distinction is invisible in every other test: both kinds of snapshot store a
// trading date, and the only thing that says whether the exchange asserted it or we assumed it
// is this column.
func TestDateSourceIsObservedOrAssignedPerDataset(t *testing.T) {
	s := openR15(t)
	r := runner(t, &FixtureTransport{}, s)
	res := mustRun(t, r, closed())

	want := map[string]string{
		OptionsByStrike.Name:        derivatives.DateObserved,
		FuturesDaily.Name:           derivatives.DateObserved,
		InstitutionalFutures.Name:   derivatives.DateObserved,
		InstitutionalCallsPuts.Name: derivatives.DateObserved,
		PutCallRatio.Name:           derivatives.DateObserved,
		// These four carry a date that is not a trading day: a settlement day, a per-contract
		// future settlement, a margin EFFECTIVE date. Validating any of them against the
		// session would fail on an ordinary day.
		Margin.Name:           derivatives.DateAssigned,
		FinalSettlement.Name:  derivatives.DateAssigned,
		SettledPositions.Name: derivatives.DateAssigned,
		OptionsDelta.Name:     derivatives.DateAssigned,
	}
	seen := map[string]bool{}
	for _, o := range res.Outcomes {
		if o.Snapshot.ID == 0 {
			t.Fatalf("%s/%s stored nothing: %s (%s)",
				o.Dataset.Name, o.Session, o.Health.Status, o.Health.Reason)
		}
		w, ok := want[o.Dataset.Name]
		if !ok {
			t.Fatalf("unexpected dataset %s", o.Dataset.Name)
		}
		if o.Snapshot.DateSource != w {
			t.Fatalf("%s: date_source %q, want %q", o.Dataset.Name, o.Snapshot.DateSource, w)
		}
		if o.Snapshot.TradingDate != FixtureDate {
			t.Fatalf("%s: trading_date %q, want %q", o.Dataset.Name, o.Snapshot.TradingDate,
				FixtureDate)
		}
		seen[o.Dataset.Name] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("covered %d datasets of %d", len(seen), len(want))
	}
	// And it is the STORED column, not just the in-memory value.
	assertStoredDateSources(t, s, want)
}

func assertStoredDateSources(t *testing.T, s *store.Store, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT source, date_source FROM derivative_snapshots`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var source, ds string
			if err := rows.Scan(&source, &ds); err != nil {
				return err
			}
			got[source] = ds
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ds := range All {
		if got[ds.Endpoint] != want[ds.Name] {
			t.Fatalf("stored date_source for %s: %q, want %q", ds.Name, got[ds.Endpoint],
				want[ds.Name])
		}
	}
}

// TestADateMismatchIsAnErrorAndStoresNothing — §6.2: the response's own date contradicts the
// requested session. Today's data must never wear yesterday's label.
func TestADateMismatchIsAnErrorAndStoresNothing(t *testing.T) {
	s := openR15(t)
	r := runner(t, &FixtureTransport{}, s)
	r.Datasets = []Dataset{InstitutionalFutures}
	target := dailydata.SessionState{Date: "2026-09-07", Session: dailydata.SessionClosed}
	res := mustRun(t, r, target)

	h := health(t, res, InstitutionalFutures.Name, derivatives.SessionCombined)
	if h.Status != derivatives.StatusError {
		t.Fatalf("want ERROR, got %s (%s)", h.Status, h.Reason)
	}
	if !strings.Contains(h.Reason, "2026-09-04") || !strings.Contains(h.Reason, "2026-09-07") {
		t.Fatalf("the reason must name both dates, got %q", h.Reason)
	}
	if n := countRows(t, s, "derivative_snapshots"); n != 0 {
		t.Fatalf("a mismatched date stored %d snapshot(s)", n)
	}
	if n := countRows(t, s, "institutional_derivatives"); n != 0 {
		t.Fatalf("a mismatched date stored %d row(s)", n)
	}
}

// TestAHistoryFeedIsNotPublishedOrMissingRatherThanMismatched.
//
// PutCallRatio answers with ~23 sessions where every other feed answers with one, so "the
// target is not in the response" has two very different meanings and neither is a mismatch:
// after the newest session the exchange has simply not published it yet; before the oldest it
// has fallen out of the window and no amount of waiting brings it back.
func TestAHistoryFeedIsNotPublishedOrMissingRatherThanMismatched(t *testing.T) {
	cases := []struct {
		date string
		want Status
	}{
		{"2026-09-07", derivatives.StatusNotPublished}, // after the newest (2026-09-04)
		{"2026-07-01", derivatives.StatusMissing},      // before the oldest (2026-08-05)
	}
	for _, tc := range cases {
		t.Run(tc.date, func(t *testing.T) {
			s := openR15(t)
			r := runner(t, &FixtureTransport{}, s)
			r.Datasets = []Dataset{PutCallRatio}
			res := mustRun(t, r, dailydata.SessionState{
				Date: tc.date, Session: dailydata.SessionClosed})

			h := health(t, res, PutCallRatio.Name, derivatives.SessionCombined)
			if h.Status != tc.want {
				t.Fatalf("want %s, got %s (%s)", tc.want, h.Status, h.Reason)
			}
			if h.Reason == "" {
				t.Fatal("no reason")
			}
			if n := countRows(t, s, "derivative_snapshots"); n != 0 {
				t.Fatalf("stored %d snapshot(s) for a session the response does not carry", n)
			}
		})
	}
}

// ── the run as a whole ────────────────────────────────────────────────────────────────

// TestAFullRunIsIdempotent is invariant 9: re-running produces no duplicate rows.
func TestAFullRunIsIdempotent(t *testing.T) {
	s := openR15(t)
	archive := t.TempDir()
	tables := []string{
		"derivative_snapshots", "options_oi_by_strike", "institutional_derivatives",
		"margin_rates", "derivative_data_health",
	}

	first := &Runner{Transport: &FixtureTransport{}, Store: s, ArchiveDir: archive,
		Now: func() time.Time { return time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC) }}
	mustRun(t, first, closed())

	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = countRows(t, s, tb)
		if before[tb] == 0 && tb != "margin_rates" {
			t.Fatalf("%s is empty after the first run", tb)
		}
	}
	dump := snapshotDump(t, s)

	// The second run happens LATER, which is the realistic case: a scheduler firing twice.
	// A later fetched_at over identical content must still insert nothing — PutSnapshot
	// compares the hash, and only a changed response earns a revision.
	second := &Runner{Transport: &FixtureTransport{}, Store: s, ArchiveDir: archive,
		Now: func() time.Time { return time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC) }}
	res := mustRun(t, second, closed())
	if u := res.Unusable(); len(u) != 0 {
		t.Fatalf("second run degraded: %v", summary(res))
	}

	for _, tb := range tables {
		if got := countRows(t, s, tb); got != before[tb] {
			t.Fatalf("%s: %d rows after one run, %d after two", tb, before[tb], got)
		}
	}
	if got := snapshotDump(t, s); !equalStrings(got, dump) {
		t.Fatalf("the snapshots changed on a re-run:\nbefore %v\nafter  %v", dump, got)
	}
}

func snapshotDump(t *testing.T, s *store.Store) []string {
	t.Helper()
	var out []string
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT source, trading_date, trading_session, revision_no,
			date_source, content_hash, fetched_at FROM derivative_snapshots ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c, d, e, f string
			var rev int
			if err := rows.Scan(&a, &b, &c, &rev, &d, &e, &f); err != nil {
				return err
			}
			out = append(out, fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s", a, b, c, rev, d, e, f))
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestOneDatasetFailingDoesNotMarkTheOthersFailed — §6/§10: partial failure across datasets is
// visible per dataset, never as one pass/fail for the run. Nine feeds publish at different
// times and fail independently.
func TestOneDatasetFailingDoesNotMarkTheOthersFailed(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{Faults: map[string]Fault{
		InstitutionalFutures.Name: {Status: 500},
	}}
	r := runner(t, tr, s)
	res := mustRun(t, r, closed())

	if got := res.StatusOf(InstitutionalFutures.Name, derivatives.SessionCombined); got != derivatives.StatusError {
		t.Fatalf("the failing dataset: want ERROR, got %s", got)
	}
	for _, o := range res.Outcomes {
		if o.Dataset.Name == InstitutionalFutures.Name {
			continue
		}
		if o.Health.Status != derivatives.StatusAvailable {
			t.Fatalf("%s/%s was dragged down to %s (%s)", o.Dataset.Name, o.Session,
				o.Health.Status, o.Health.Reason)
		}
		if o.Snapshot.ID == 0 {
			t.Fatalf("%s/%s stored nothing", o.Dataset.Name, o.Session)
		}
	}
	if n := len(res.Unusable()); n != 1 {
		t.Fatalf("want exactly 1 unusable outcome, got %d: %v", n, summary(res))
	}
	// And the failure is stored, not merely returned: a run that logged it and moved on
	// leaves a panel unable to say the layer is incomplete.
	if got := storedHealth(t, s, InstitutionalFutures.Name, derivatives.SessionCombined).Status; got != derivatives.StatusError {
		t.Fatalf("stored status: want ERROR, got %s", got)
	}
}

// TestTransportFailuresKeepTheirIdentity — a 5xx, a rate limit and a 404 are three different
// facts, and only the last one means "there is nothing here".
func TestTransportFailuresKeepTheirIdentity(t *testing.T) {
	cases := []struct {
		name  string
		fault Fault
		want  Status
	}{
		{"gateway error", Fault{Status: 502}, derivatives.StatusError},
		{"rate limited", Fault{Status: 429}, derivatives.StatusError},
		{"maintenance page", Fault{ContentType: "text/html; charset=utf-8"}, derivatives.StatusError},
		{"empty body", Fault{Empty: true}, derivatives.StatusError},
		{"not found", Fault{Status: 404}, derivatives.StatusMissing},
		{"connection refused", Fault{Err: errors.New("dial tcp: connection refused")},
			derivatives.StatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openR15(t)
			tr := &FixtureTransport{Faults: map[string]Fault{Margin.Name: tc.fault}}
			r := runner(t, tr, s)
			r.Datasets = []Dataset{Margin}
			res := mustRun(t, r, closed())

			h := health(t, res, Margin.Name, derivatives.SessionCombined)
			if h.Status != tc.want {
				t.Fatalf("want %s, got %s (%s)", tc.want, h.Status, h.Reason)
			}
			if n := countRows(t, s, "derivative_snapshots"); n != 0 {
				t.Fatalf("a failed fetch stored %d snapshot(s)", n)
			}
			if n := countRows(t, s, "margin_rates"); n != 0 {
				t.Fatalf("a failed fetch stored %d margin row(s); §2.3 makes that "+
					"margin_source: NONE", n)
			}
		})
	}
}

// TestNoSessionIsDecidedBeforeAnyRequest — §6: NO_SESSION comes from the calendar, and is
// never inferred from an empty response. The only way to prove that is to prove no request
// happened.
func TestNoSessionIsDecidedBeforeAnyRequest(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{}
	r := runner(t, tr, s)
	res := mustRun(t, r, dailydata.SessionState{
		Date: "2026-09-05", Session: dailydata.SessionNone, Reason: "週末，交易所無交易"})

	if tr.TotalCalls() != 0 {
		t.Fatalf("%d request(s) issued on a non-trading day", tr.TotalCalls())
	}
	if len(res.Outcomes) != 11 {
		// nine datasets, two of which produce two sessions each
		t.Fatalf("want 11 outcomes (9 datasets, 2 of them split), got %d: %v",
			len(res.Outcomes), summary(res))
	}
	for _, o := range res.Outcomes {
		if o.Health.Status != derivatives.StatusNoSession {
			t.Fatalf("%s/%s: %s", o.Dataset.Name, o.Session, o.Health.Status)
		}
		if o.Health.Reason == "" {
			t.Fatal("NO_SESSION with no reason")
		}
	}
	if n := countRows(t, s, "derivative_data_health"); n != 11 {
		t.Fatalf("want 11 health records, got %d — a day nobody looked at still has to say so", n)
	}
	if n := countRows(t, s, "derivative_snapshots"); n != 0 {
		t.Fatalf("a non-trading day stored %d snapshot(s)", n)
	}
}

// TestAnOpenSessionIsRefused — a session still trading is not final, and a snapshot is
// immutable once written.
func TestAnOpenSessionIsRefused(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{}
	r := runner(t, tr, s)
	if _, err := r.Run(context.Background(), dailydata.SessionState{
		Date: FixtureDate, Session: dailydata.SessionOpen}); err == nil {
		t.Fatal("an OPEN session was acquired")
	}
	if tr.TotalCalls() != 0 {
		t.Fatalf("%d request(s) issued for an unfinished session", tr.TotalCalls())
	}
}

// TestARunRefusesADatasetWithNoParserBeforeFetching — half a run of health records is a worse
// way to learn about a missing parser than a refusal.
func TestARunRefusesADatasetWithNoParserBeforeFetching(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{}
	r := runner(t, tr, s)
	r.Datasets = []Dataset{Margin, {Name: "invented", Endpoint: "Invented",
		Session: derivatives.SessionCombined}}
	if _, err := r.Run(context.Background(), closed()); err == nil {
		t.Fatal("a dataset with no parser was accepted")
	}
	if tr.TotalCalls() != 0 {
		t.Fatalf("%d request(s) issued before the catalogue was checked", tr.TotalCalls())
	}
	if n := countRows(t, s, "derivative_data_health"); n != 0 {
		t.Fatalf("%d health record(s) written by a refused run", n)
	}
}

// TestEveryDatasetEndpointMatchesItsProviderSource.
//
// The endpoint is what is stored as `source` on every snapshot and every row, and it is the
// key an as-of read resolves against. A catalogue entry that disagreed with the parser's own
// constant would archive rows under a name no reader looks for.
func TestEveryDatasetEndpointMatchesItsProviderSource(t *testing.T) {
	want := map[string]string{
		OptionsByStrike.Name:        "DailyMarketReportOpt",
		FuturesDaily.Name:           "DailyMarketReportFut",
		InstitutionalFutures.Name:   "MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate",
		InstitutionalCallsPuts.Name: "MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate",
		PutCallRatio.Name:           "PutCallRatio",
		Margin.Name:                 "IndexFuturesAndOptionsMargining",
		FinalSettlement.Name:        "FinalSettlementPriceIndexOptions",
		SettledPositions.Name:       "SettledPositionsIndexOptions",
		OptionsDelta.Name:           "DailyOptionsDelta",
	}
	for _, ds := range All {
		if want[ds.Name] != ds.Endpoint {
			t.Fatalf("%s: endpoint %q, want %q", ds.Name, ds.Endpoint, want[ds.Name])
		}
		if _, ok := FixtureFiles[ds.Name]; !ok {
			t.Fatalf("%s has no fixture, so no test in this package can cover it without a "+
				"network call", ds.Name)
		}
	}
}

// TestTheArchiveKeepsTheUndividedResponse — the database holds the session PARTITIONS a reader
// queries; the archive holds the response they were derived from. Without it, "why does the
// DAY snapshot say that" is unanswerable a month later.
func TestTheArchiveKeepsTheUndividedResponse(t *testing.T) {
	s := openR15(t)
	dir := t.TempDir()
	r := &Runner{Transport: &FixtureTransport{}, Store: s, ArchiveDir: dir,
		Now: func() time.Time { return time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC) }}
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	names, err := derivatives.ArchivedSources(dir, FixtureDate)
	if err != nil {
		t.Fatal(err)
	}
	// One file: both snapshots came from one response, and the archive preserves the response.
	if len(names) != 1 {
		t.Fatalf("want 1 archived response, got %d: %v", len(names), names)
	}
	raw, err := ReadFixture("", "options_by_strike_20260904.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := derivatives.ReadArchived(dir, FixtureDate, OptionsByStrike.Endpoint,
		derivatives.HashPayload(string(raw)))
	if err != nil {
		t.Fatalf("the archived response is not the response that was fetched: %v", err)
	}
	if len(got) != len(raw) {
		t.Fatalf("archived %d bytes, fetched %d", len(got), len(raw))
	}
	// And the snapshots hold the partitions, which are each SMALLER than the response.
	for _, o := range res.Outcomes {
		if len(o.Snapshot.Payload) >= len(raw) {
			t.Fatalf("%s payload is %d bytes against a %d-byte response — it was not "+
				"partitioned", o.Session, len(o.Snapshot.Payload), len(raw))
		}
	}
}

// TestAStructuralFailureArchivesNothing — §6.2's "nothing is stored" includes the archive: a
// preserved response is a claim that an observation was made.
func TestAStructuralFailureArchivesNothing(t *testing.T) {
	body, err := ReadFixture("", "malformed/undecodable.json")
	if err != nil {
		t.Fatal(err)
	}
	s := openR15(t)
	dir := t.TempDir()
	r := &Runner{Transport: &FixtureTransport{Bodies: map[string][]byte{Margin.Name: body}},
		Store: s, ArchiveDir: dir}
	r.Datasets = []Dataset{Margin}
	mustRun(t, r, closed())

	names, err := derivatives.ArchivedSources(dir, FixtureDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("a rejected response was archived: %v", names)
	}
}
