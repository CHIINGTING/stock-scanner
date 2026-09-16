package derivatives

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/store"
)

// Revision timestamps must not go backwards (§6.3).
//
// The readers order on fetched_at BEFORE revision_no, so a revision that went backwards in
// time would be permanently unreachable: an as-of read keeps returning revision 0 while a
// newer correction sits in the table, and nothing downstream can tell. A backfill replaying
// archived responses out of order is the realistic way to produce one.

// snapshotRows returns every snapshot row as a comparable string, so "the database did not
// change" is asserted against content rather than against a count — a rejected write that
// somehow replaced a payload would keep the count identical.
func snapshotRows(t *testing.T, st *store.Store) []string {
	t.Helper()
	var out []string
	err := st.WithTx(context.Background(), func(tx *sql.Tx) error {
		r, err := tx.Query(`SELECT source, trading_date, trading_session, revision_no,
			content_hash, payload, fetched_at FROM derivative_snapshots
			ORDER BY id`)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var src, date, sess, hash, payload, fetched string
			var rev int
			if err := r.Scan(&src, &date, &sess, &rev, &hash, &payload, &fetched); err != nil {
				return err
			}
			out = append(out, src+"|"+date+"|"+sess+"|"+strconv.Itoa(rev)+"|"+hash+"|"+
				payload+"|"+fetched)
		}
		return r.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRevisionsMustMoveForward(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()

	// 1. revision 0 writes.
	r0 := snap("Opt", "2026-09-04", SessionCombined, `v0`, at(t, "2026-09-04 15:30:00"))
	got, err := PutSnapshot(ctx, s, r0)
	if err != nil {
		t.Fatalf("revision 0: %v", err)
	}
	if got.RevisionNo != 0 {
		t.Fatalf("revision_no = %d, want 0", got.RevisionNo)
	}

	// 2. a later fetched_at writes as revision 1.
	r1 := r0
	r1.Payload, r1.ContentHash = `v1`, ""
	r1.FetchedAt = at(t, "2026-09-07 09:00:00")
	got, err = PutSnapshot(ctx, s, r1)
	if err != nil {
		t.Fatalf("revision 1: %v", err)
	}
	if got.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", got.RevisionNo)
	}

	before := snapshotRows(t, s)

	// 3. an EARLIER fetched_at is rejected, with a typed error.
	r2 := r0
	r2.Payload, r2.ContentHash = `v2`, ""
	r2.FetchedAt = at(t, "2026-09-05 09:00:00") // between r0 and r1
	_, err = PutSnapshot(ctx, s, r2)
	if err == nil {
		t.Fatal("a revision fetched BEFORE the latest one was accepted — the readers order " +
			"on fetched_at, so it would be permanently unreachable")
	}
	if !errors.Is(err, ErrRevisionOutOfOrder) {
		t.Fatalf("error is not ErrRevisionOutOfOrder: %v", err)
	}
	var ordErr *RevisionOrderError
	if !errors.As(err, &ordErr) {
		t.Fatalf("error does not carry the timestamps: %v", err)
	}
	if !ordErr.Offered.Equal(r2.FetchedAt) || !ordErr.LatestFetched.Equal(r1.FetchedAt) {
		t.Errorf("RevisionOrderError timestamps wrong: offered %v latest %v",
			ordErr.Offered, ordErr.LatestFetched)
	}

	// 4. the database is unchanged, content and all.
	after := snapshotRows(t, s)
	if len(before) != len(after) {
		t.Fatalf("row count %d → %d after a rejected write", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("row %d changed after a rejected write:\n before %s\n after  %s",
				i, before[i], after[i])
		}
	}

	// 5. the as-of reader never regresses.
	latest, err := SnapshotAsOf(ctx, s, "Opt", SessionCombined, "2026-09-10")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Payload != `v1` {
		t.Fatalf("as-of read returned %q, want v1 — a rejected out-of-order write left the "+
			"reader on stale content", latest.Payload)
	}
}

// An identical replay is idempotent WHATEVER its timestamp claims.
//
// The check lives on the INSERT path only, and that placement is the contract: a replay whose
// content matches inserts nothing, so there is no ordering to protect and rejecting it would
// break the idempotent rerun invariant 9 depends on. A backfill re-reading its own archive
// must not start failing because the archive's timestamps are older than the live fetch's.
func TestAnIdenticalReplayIsNotRejectedForBeingOld(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()

	first := snap("Opt", "2026-09-04", SessionCombined, `same`, at(t, "2026-09-07 09:00:00"))
	if _, err := PutSnapshot(ctx, s, first); err != nil {
		t.Fatal(err)
	}
	before := snapshotRows(t, s)

	replay := first
	replay.FetchedAt = at(t, "2026-09-04 15:30:00") // older, but identical content
	got, err := PutSnapshot(ctx, s, replay)
	if err != nil {
		t.Fatalf("an identical replay with an older timestamp was rejected: %v — the check "+
			"must guard ordering, not re-runs", err)
	}
	if got.RevisionNo != 0 {
		t.Fatalf("revision_no = %d, want the existing 0", got.RevisionNo)
	}
	after := snapshotRows(t, s)
	if len(before) != len(after) {
		t.Fatalf("an identical replay inserted a row: %d → %d", len(before), len(after))
	}
}

// Equal timestamps are allowed: two revisions can legitimately land in the same second, and
// revision_no is the tie-break the reader already applies.
func TestAnEqualTimestampIsAccepted(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	ts := at(t, "2026-09-04 15:30:00")

	a := snap("Opt", "2026-09-04", SessionCombined, `a`, ts)
	if _, err := PutSnapshot(ctx, s, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Payload, b.ContentHash = `b`, ""
	got, err := PutSnapshot(ctx, s, b)
	if err != nil {
		t.Fatalf("a revision at the same second was rejected: %v", err)
	}
	if got.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", got.RevisionNo)
	}
}
