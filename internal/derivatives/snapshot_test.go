package derivatives

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/store"
)

func openR15(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(store.Config{
		Path:   filepath.Join(t.TempDir(), "r15.db"),
		Schema: Schema,
	})
	if err != nil {
		t.Fatalf("open r15 store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// at builds a UTC instant from a Taipei wall-clock time, which is how every fetch timestamp in
// these tests is expressed — the boundary cases are only meaningful in Taipei.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, dailydata.Taipei)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

func snap(source, date, session, payload string, fetched time.Time) Snapshot {
	return Snapshot{
		Source: source, TradingDate: date, TradingSession: session,
		DateSource: DateObserved, Payload: payload, FetchedAt: fetched,
	}
}

func countSnapshots(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM derivative_snapshots`).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// ── migrations ────────────────────────────────────────────────────────────────────────

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r15.db")
	first, err := store.Open(store.Config{Path: path, Schema: Schema})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	v1, err := first.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := store.Open(store.Config{Path: path, Schema: Schema})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	v2, err := second.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v2 || v1 != Schema.Version() {
		t.Fatalf("versions %d then %d, want %d both times", v1, v2, Schema.Version())
	}
}

// R15 must not move the R13 ceiling — the property the separate schema exists for.
func TestR15DoesNotRaiseTheR13Ceiling(t *testing.T) {
	if store.SchemaVersion != 2 {
		t.Fatalf("store.SchemaVersion = %d, want 2 — R15 raised the shared ceiling and every "+
			"binary built without R15 would now refuse r13.db", store.SchemaVersion)
	}
	if Schema.Name == store.R13Schema.Name {
		t.Fatal("R15 is using the R13 schema identity")
	}
}

// ── append-only, and idempotent (invariant 9) ─────────────────────────────────────────

func TestIdenticalContentInsertsNothing(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	in := snap("PutCallRatio", "2026-09-04", SessionCombined, `[{"PutOI":"48162"}]`,
		at(t, "2026-09-04 15:30:00"))

	first, err := PutSnapshot(ctx, s, in)
	if err != nil {
		t.Fatal(err)
	}
	before := countSnapshots(t, s)

	// Re-fetched later the same day; the exchange said the same thing.
	again := in
	again.FetchedAt = at(t, "2026-09-04 18:00:00")
	second, err := PutSnapshot(ctx, s, again)
	if err != nil {
		t.Fatal(err)
	}

	if got := countSnapshots(t, s); got != before {
		t.Fatalf("row count %d → %d; an identical re-fetch inserted a row", before, got)
	}
	if second.ID != first.ID || second.RevisionNo != 0 {
		t.Fatalf("re-fetch returned id %d rev %d, want the existing id %d rev 0",
			second.ID, second.RevisionNo, first.ID)
	}
}

func TestChangedContentAppendsARevisionAndLeavesTheOriginal(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	orig := snap("PutCallRatio", "2026-09-04", SessionCombined, `{"PutOI":"48162"}`,
		at(t, "2026-09-04 15:30:00"))
	first, err := PutSnapshot(ctx, s, orig)
	if err != nil {
		t.Fatal(err)
	}

	corrected := orig
	corrected.Payload = `{"PutOI":"48170"}`
	corrected.ContentHash = ""
	corrected.FetchedAt = at(t, "2026-09-07 09:00:00")
	rev, err := PutSnapshot(ctx, s, corrected)
	if err != nil {
		t.Fatal(err)
	}
	if rev.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", rev.RevisionNo)
	}

	// Revision 0 must be byte-identical to what was written. An in-place correction would
	// erase the only record of what was knowable on 2026-09-04.
	got, err := SnapshotAsOf(ctx, s, "PutCallRatio", SessionCombined, "2026-09-04")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.Payload != orig.Payload {
		t.Fatalf("revision 0 changed: id %d payload %q", got.ID, got.Payload)
	}
}

// A revert must be recorded, not swallowed. Comparing the hash against ANY revision rather
// than the LATEST would find the original hash already present, insert nothing, and leave
// every reader permanently on the superseded content.
func TestARevertIsRecordedAsANewRevision(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	a := snap("PutCallRatio", "2026-09-04", SessionCombined, `{"v":1}`, at(t, "2026-09-04 15:00:00"))
	if _, err := PutSnapshot(ctx, s, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Payload, b.ContentHash, b.FetchedAt = `{"v":2}`, "", at(t, "2026-09-05 15:00:00")
	if _, err := PutSnapshot(ctx, s, b); err != nil {
		t.Fatal(err)
	}
	back := a
	back.ContentHash, back.FetchedAt = "", at(t, "2026-09-06 15:00:00")

	rev, err := PutSnapshot(ctx, s, back)
	if err != nil {
		t.Fatal(err)
	}
	if rev.RevisionNo != 2 {
		t.Fatalf("revision_no = %d, want 2 — a revert to earlier content was swallowed, and "+
			"readers would stay on the superseded revision forever", rev.RevisionNo)
	}
}

// ── point in time ─────────────────────────────────────────────────────────────────────

func TestACorrectionIsInvisibleToAnEarlierReading(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	orig := snap("Opt", "2026-09-04", SessionCombined, `original`, at(t, "2026-09-04 15:30:00"))
	if _, err := PutSnapshot(ctx, s, orig); err != nil {
		t.Fatal(err)
	}
	corrected := orig
	corrected.Payload, corrected.ContentHash = `corrected`, ""
	corrected.FetchedAt = at(t, "2026-09-07 09:00:00")
	if _, err := PutSnapshot(ctx, s, corrected); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ asOf, want string }{
		{"2026-09-04", "original"},  // before the correction existed
		{"2026-09-06", "original"},  // still before
		{"2026-09-07", "corrected"}, // the day it arrived
		{"2026-09-10", "corrected"},
	} {
		got, err := SnapshotAsOf(ctx, s, "Opt", SessionCombined, tc.asOf)
		if err != nil {
			t.Fatalf("as-of %s: %v", tc.asOf, err)
		}
		if got.Payload != tc.want {
			t.Errorf("as-of %s → %q, want %q — a correction fetched later reached an "+
				"earlier reading, which is what makes a backtest look better than the data was",
				tc.asOf, got.Payload, tc.want)
		}
	}
}

// A reading dated before any correction must resolve to revision 0, not to nothing. An earlier
// design kept revisions in a second table, where this case returned no row at all.
func TestAReadingBeforeAnyRevisionResolvesToRevisionZero(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	in := snap("Opt", "2026-09-04", SessionCombined, `only`, at(t, "2026-09-04 15:30:00"))
	if _, err := PutSnapshot(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotAsOf(ctx, s, "Opt", SessionCombined, "2026-09-04")
	if err != nil {
		t.Fatalf("as-of the same day found nothing: %v", err)
	}
	if got.RevisionNo != 0 || got.Payload != "only" {
		t.Fatalf("got rev %d %q", got.RevisionNo, got.Payload)
	}
}

// The Taipei day boundary. trading_date is a Taipei trading day and fetched_at is UTC, so a
// naive string comparison cuts the day eight hours early and hides an afternoon fetch from its
// own session.
func TestTheAsOfBoundIsTheEndOfTheTaipeiDay(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()

	late := snap("Opt", "2026-09-04", SessionCombined, `late`, at(t, "2026-09-04 23:59:00"))
	if _, err := PutSnapshot(ctx, s, late); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotAsOf(ctx, s, "Opt", SessionCombined, "2026-09-04")
	if err != nil || got.Payload != "late" {
		t.Fatalf("23:59 Taipei on the as-of day was not visible: %v %+v", err, got)
	}

	next := snap("Opt2", "2026-09-04", SessionCombined, `next`, at(t, "2026-09-05 00:01:00"))
	if _, err := PutSnapshot(ctx, s, next); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotAsOf(ctx, s, "Opt2", SessionCombined, "2026-09-04"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("00:01 Taipei the NEXT day was visible to a reading dated 2026-09-04: %v", err)
	}
}

// M5's settlement comparison must read the snapshot archived FOR the session, not the latest.
// The settlement feed carries no date of its own and always answers with the most recent
// event, so asking the live response "is today a settlement day" answers about a different day.
func TestSnapshotForSessionPinsTheExactSession(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	// Three sessions, deliberately straddling the target in BOTH directions. An earlier
	// fixture had only a later session, so `trading_date = ?` relaxed to `<= ?` still
	// excluded it and the mutation survived: the test guarded "do not read newer" and left
	// "do not read older" unguarded.
	// The last entry is the one with teeth: an OLDER session corrected AFTER the target was
	// fetched. Relaxing `trading_date = ?` to `<= ?` then orders by fetched_at and picks the
	// correction — the wrong session, with the newest timestamp. Without it, `<=` happens to
	// return the right answer and the mutation survives.
	for _, d := range []struct{ date, payload, fetched string }{
		{"2026-09-01", `settles=20260901`, "2026-09-01 15:30:00"},
		{"2026-09-04", `settles=20260904`, "2026-09-04 15:30:00"},
		{"2026-09-09", `settles=20260909`, "2026-09-09 15:30:00"},
		{"2026-09-01", `settles=20260901-corrected`, "2026-09-05 09:00:00"},
	} {
		if _, err := PutSnapshot(ctx, s, snap("FSP", d.date, SessionCombined, d.payload,
			at(t, d.fetched))); err != nil {
			t.Fatal(err)
		}
	}

	// A recompute run on 2026-09-10 that asks about 2026-09-04 must get 2026-09-04's answer.
	for _, tc := range []struct{ session, asOf, want string }{
		{"2026-09-04", "2026-09-10", `settles=20260904`},           // must not drift newer
		{"2026-09-01", "2026-09-10", `settles=20260901-corrected`}, // its own latest revision
		{"2026-09-09", "2026-09-10", `settles=20260909`},
	} {
		got, err := SnapshotForSession(ctx, s, "FSP", SessionCombined, tc.session, tc.asOf)
		if err != nil {
			t.Fatalf("session %s: %v", tc.session, err)
		}
		if got.Payload != tc.want {
			t.Errorf("session %s → %q, want %q — a rerun read a different session's "+
				"settlement event, which leaves the settled expiry in (or removes a live "+
				"expiry from) that session's open interest", tc.session, got.Payload, tc.want)
		}
	}
}

// SnapshotAsOf must order by SESSION first, then by fetch time.
//
// The twin of the SnapshotForSession hole, and it slipped through the same way: the fixture
// that exposes it already existed, and was simply never run through this function. Deleting
// `trading_date DESC` from the ORDER BY survives every other test in this file.
//
// The failing case is an older session corrected AFTER a newer session was fetched. Ordering on
// fetch time alone then returns the correction — an older session that won on recency — so a
// reading dated 2026-09-10 would answer with 2026-09-01's data while believing it had the
// latest. That is a wrong-session read, which is the same class as the one round 1 blocked on.
func TestSnapshotAsOfPrefersTheLatestSessionNotTheLatestFetch(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	for _, d := range []struct{ date, payload, fetched string }{
		{"2026-09-01", `settles=20260901`, "2026-09-01 15:30:00"},
		{"2026-09-04", `settles=20260904`, "2026-09-04 15:30:00"},
		// The correction that makes an OLDER session the most recently fetched row.
		{"2026-09-01", `settles=20260901-corrected`, "2026-09-05 09:00:00"},
	} {
		if _, err := PutSnapshot(ctx, s, snap("FSP", d.date, SessionCombined, d.payload,
			at(t, d.fetched))); err != nil {
			t.Fatal(err)
		}
	}

	got, err := SnapshotAsOf(ctx, s, "FSP", SessionCombined, "2026-09-10")
	if err != nil {
		t.Fatal(err)
	}
	if got.TradingDate != "2026-09-04" {
		t.Fatalf("as-of 2026-09-10 resolved to session %s (%q), want 2026-09-04 — ordering on "+
			"fetch time alone let a correction to an older session outrank a newer one",
			got.TradingDate, got.Payload)
	}
}

// ── the NULLABLE half of the same contract ────────────────────────────────────────────
//
// TestKeyColumnsAreNotNullable pins the columns that must never be NULL. This pins the ones
// that must be ABLE to be: the layer rests on "-" meaning ABSENT and "0" meaning an observed
// zero, and 1,295 TXO strikes carry a real 0 while 2,868 carry "-". A NOT NULL DEFAULT 0 on any
// of these silently merges the two, and every consumer downstream reads a fabricated zero as an
// observation.
func TestValueColumnsAreNullable(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()

	for _, tc := range []struct{ table, column string }{
		{"options_oi_by_strike", "oi"},
		{"options_oi_by_strike", "volume"},
		{"options_oi_by_strike", "settlement"},
		{"options_aggregate", "put_volume"},
		{"options_aggregate", "call_volume"},
		{"options_aggregate", "put_oi"},
		{"options_aggregate", "call_oi"},
		{"institutional_derivatives", "lots"},
		{"margin_rates", "clearing_margin"},
		{"margin_rates", "maintenance_margin"},
		{"margin_rates", "initial_margin"},
		{"iv_observations", "iv"},
		{"derivative_features", "value"},
		{"derivative_outcomes", "outcome_1d"},
		{"derivative_outcomes", "outcome_20d"},
		{"derivative_outcomes", "mfe"},
		{"derivative_outcomes", "mae"},
	} {
		var notNull int
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx,
				`SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`, tc.table, tc.column)
			if err != nil {
				return err
			}
			defer rows.Close()
			if !rows.Next() {
				return fmt.Errorf("%s.%s does not exist", tc.table, tc.column)
			}
			return rows.Scan(&notNull)
		})
		if err != nil {
			t.Fatalf("%s.%s: %v", tc.table, tc.column, err)
		}
		if notNull != 0 {
			t.Errorf("%s.%s is NOT NULL — an absent observation would have to be stored as a "+
				"number, and MISSING becomes ZERO at the storage layer where nothing "+
				"downstream can tell them apart", tc.table, tc.column)
		}
	}
}

// The behavioural half: a strike the exchange printed "-" for must be storable as absent.
func TestAnAbsentOpenInterestIsStorableAsNull(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	sn, err := PutSnapshot(ctx, s, snap("Opt", "2026-09-04", SessionAfterHours, `{}`,
		at(t, "2026-09-04 15:30:00")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO options_oi_by_strike
			  (schema_version, snapshot_id, product, expiry_code, strike, call_put,
			   oi, volume, settlement, source, fetched_at)
			VALUES (?,?,?,?,?,?,NULL,NULL,NULL,?,?)`,
			Schema.Version(), sn.ID, "TXO", "202609W2", 22000.0, "CALL",
			"test", "2026-09-04T07:30:00Z")
		return err
	}); err != nil {
		t.Fatalf("a '-' row could not be stored as absent: %v", err)
	}

	var oi sql.NullFloat64
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT oi FROM options_oi_by_strike`).Scan(&oi)
	}); err != nil {
		t.Fatal(err)
	}
	if oi.Valid {
		t.Fatalf("an absent open interest read back as %v", oi.Float64)
	}
}

// ── the key shape ─────────────────────────────────────────────────────────────────────

// FLOW and POSITION for the same institution and contract must coexist. Without
// value_semantics in the key they overwrite each other, and the distinction the layer exists
// for becomes unrecoverable: a net sale on a day the net long position rose is exactly the
// case that needs both numbers.
func TestFlowAndPositionCoexist(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	sn, err := PutSnapshot(ctx, s, snap("Inst", "2026-09-04", SessionCombined, `{}`,
		at(t, "2026-09-04 15:30:00")))
	if err != nil {
		t.Fatal(err)
	}

	insert := func(semantics string, lots float64) error {
		return s.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO institutional_derivatives
				  (schema_version, snapshot_id, institution, product, call_put,
				   value_semantics, side, lots, source, fetched_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				Schema.Version(), sn.ID, "外資", "臺股期貨", "", semantics, "NET", lots,
				"test", "2026-09-04T07:30:00Z")
			return err
		})
	}
	if err := insert("FLOW", -1856); err != nil {
		t.Fatalf("insert FLOW: %v", err)
	}
	if err := insert("POSITION", 90765); err != nil {
		t.Fatalf("insert POSITION: %v — value_semantics is not in the key, so the two "+
			"overwrite each other", err)
	}

	var n int
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM institutional_derivatives`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

// The ” sentinel, not NULL.
//
// An earlier version of this test inserted a literal ” and asserted the second insert was
// rejected. That passes whether or not the column is nullable — it proves a UNIQUE index
// exists on the tuple, which was never in doubt, and says nothing about the sentinel. Making
// call_put nullable SURVIVED it.
//
// So this asserts the two things that actually matter, at the level they hold: the column is
// declared NOT NULL, and a NULL is rejected by the database rather than opening an unconstrained
// parallel key space for exactly the futures rows.
func TestKeyColumnsAreNotNullable(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()

	for _, tc := range []struct{ table, column string }{
		{"institutional_derivatives", "call_put"},
		{"institutional_derivatives", "value_semantics"},
		{"institutional_derivatives", "side"},
		{"derivative_snapshots", "trading_session"},
		{"derivative_snapshots", "date_source"},
		{"options_oi_by_strike", "call_put"},
	} {
		var notNull int
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`,
				tc.table, tc.column)
			if err != nil {
				return err
			}
			defer rows.Close()
			if !rows.Next() {
				return fmt.Errorf("%s.%s does not exist", tc.table, tc.column)
			}
			return rows.Scan(&notNull)
		})
		if err != nil {
			t.Fatalf("%s.%s: %v", tc.table, tc.column, err)
		}
		if notNull != 1 {
			t.Errorf("%s.%s is nullable — SQLite treats every NULL as distinct in a UNIQUE "+
				"index, so idempotency would hold for the rows that fill it and silently "+
				"stop applying to the rows that do not", tc.table, tc.column)
		}
	}
}

// And the behavioural half: a NULL must be refused, not accepted twice.
func TestANullKeyColumnIsRejected(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	sn, err := PutSnapshot(ctx, s, snap("Inst", "2026-09-04", SessionCombined, `{}`,
		at(t, "2026-09-04 15:30:00")))
	if err != nil {
		t.Fatal(err)
	}
	insertNull := func() error {
		return s.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO institutional_derivatives
				  (schema_version, snapshot_id, institution, product, call_put,
				   value_semantics, side, lots, source, fetched_at)
				VALUES (?,?,?,?,NULL,?,?,?,?,?)`,
				Schema.Version(), sn.ID, "外資", "臺股期貨", "POSITION", "NET", 1.0,
				"test", "2026-09-04T07:30:00Z")
			return err
		})
	}
	if err := insertNull(); err == nil {
		// If this insert succeeds the constraint is already gone: two NULL rows would both
		// be accepted, because NULL never equals NULL in a UNIQUE index.
		_ = insertNull()
		var n int
		_ = s.WithTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT COUNT(*) FROM institutional_derivatives`).Scan(&n)
		})
		t.Fatalf("a NULL call_put was accepted (%d rows) — the '' sentinel is what keeps the "+
			"UNIQUE index applying to futures rows", n)
	}
}

// The ” sentinel itself still dedupes, which is the other half of the same contract.
func TestTheFuturesSentinelDedupes(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	sn, err := PutSnapshot(ctx, s, snap("Inst", "2026-09-04", SessionCombined, `{}`,
		at(t, "2026-09-04 15:30:00")))
	if err != nil {
		t.Fatal(err)
	}
	insert := func() error {
		return s.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO institutional_derivatives
				  (schema_version, snapshot_id, institution, product, call_put,
				   value_semantics, side, lots, source, fetched_at)
				VALUES (?,?,?,?,'',?,?,?,?,?)`,
				Schema.Version(), sn.ID, "外資", "臺股期貨", "POSITION", "NET", 1.0,
				"test", "2026-09-04T07:30:00Z")
			return err
		})
	}
	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(); err == nil {
		t.Fatal("a duplicate futures row was accepted")
	}
}

// ── every read path fills FetchedAt ────────────────────────────────────────────────────
//
// It did not. Both point-in-time readers scanned fetched_at into a local and dropped it, while
// latestRevision parsed it — one struct, one field, three read paths, one of them correct.
// `go vet` cannot see it because the variable IS used, as a Scan destination.
//
// Silent, and consequential: §6's STALE rule and §10.3's data-health panel both read this
// field, so every source would have reported a fetch time of 0001-01-01 and been graded stale.
func TestEveryReadPathFillsFetchedAt(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	want := at(t, "2026-09-04 15:30:00")
	if _, err := PutSnapshot(ctx, s, snap("Opt", "2026-09-04", SessionCombined, `x`, want)); err != nil {
		t.Fatal(err)
	}

	got, err := SnapshotAsOf(ctx, s, "Opt", SessionCombined, "2026-09-04")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FetchedAt.Equal(want) {
		t.Errorf("SnapshotAsOf FetchedAt = %v, want %v", got.FetchedAt, want)
	}

	got, err = SnapshotForSession(ctx, s, "Opt", SessionCombined, "2026-09-04", "2026-09-04")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FetchedAt.Equal(want) {
		t.Errorf("SnapshotForSession FetchedAt = %v, want %v", got.FetchedAt, want)
	}
}

// ── the writer refuses malformed keys ──────────────────────────────────────────────────

func TestPutSnapshotRejectsMalformedKeys(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	base := snap("Opt", "2026-09-04", SessionCombined, `x`, at(t, "2026-09-04 15:30:00"))

	bad := base
	bad.TradingSession = "COMBINE" // a typo, not a session
	if _, err := PutSnapshot(ctx, s, bad); err == nil {
		t.Error("a misspelt session was accepted — it would open a parallel key space in " +
			"which every re-fetch inserts and no as-of read ever finds the row")
	}

	bad = base
	bad.TradingDate = "20260904" // the feed's native format
	if _, err := PutSnapshot(ctx, s, bad); err == nil {
		t.Error("a non-ISO trading_date was accepted — it is compared as a bare string " +
			"against an as-of date, so it would silently resolve to the wrong session")
	}
}
