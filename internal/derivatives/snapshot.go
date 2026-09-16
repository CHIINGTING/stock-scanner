package derivatives

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// Snapshot storage: append-only writes, and reads bounded by an as-of date.
//
// These two are one file because they are one contract. The writer never updates a row, and
// the reader resolves an as-of date to the newest revision it was ALLOWED to see. Either half
// alone is useless: append-only storage that readers scan without a bound still lets a later
// correction reach a past reading, and a bounded reader over mutable rows has nothing to bound.

// Session values. Sentinels rather than NULL — SQLite treats every NULL as distinct in a
// UNIQUE index, so a nullable key column silently disables the constraint for exactly the rows
// it covers.
const (
	SessionDay        = "DAY"
	SessionAfterHours = "AFTER_HOURS"
	// SessionCombined is used by every feed with no session dimension: PutCallRatio, margin,
	// settlement, delta, settled positions.
	SessionCombined = "COMBINED"
)

// Where a snapshot's trading_date came from.
const (
	// DateObserved — the feed carried its own trading date and it matched the target session.
	DateObserved = "OBSERVED"
	// DateAssigned — the feed carries no trading date (or carries a date that is not one:
	// a settlement day, a future contract settlement, a margin effective date), so the
	// fetch's target session was used. Stored rather than inferred: "we assumed this
	// belonged to D" is exactly the claim that disappears once it is only a convention.
	DateAssigned = "ASSIGNED"
)

// ErrRevisionOutOfOrder rejects a revision whose fetched_at precedes the latest revision's.
//
// The readers order on fetched_at BEFORE revision_no (§7.3), so an out-of-order revision
// would be permanently unreachable: an as-of read would keep returning revision 0 while a
// newer correction sat one row below it, and nothing would say so.
//
// This is a natural invariant rather than a constraint fought against. fetched_at means WHEN
// THIS REPOSITORY RETRIEVED THE BYTES, not when the exchange published them, so a backfill
// that recovers a 2025 session today carries today's timestamp and is monotonic by
// construction. The rejection fires only on a caller asserting a retrieval order that did not
// happen — which is exactly what should fail.
//
// Nothing here silently adjusts a timestamp to make the write fit. Overwriting fetched_at
// would destroy the only ordering the point-in-time reader has, and it would do it invisibly.
var ErrRevisionOutOfOrder = errors.New("derivatives: revision fetched_at goes backwards")

// RevisionOrderError carries the two timestamps, because "out of order" without them cannot
// be acted on: the caller needs to know whether it is replaying an archive in the wrong
// order or has a clock problem.
type RevisionOrderError struct {
	Source         string
	TradingDate    string
	TradingSession string
	LatestRevision int
	LatestFetched  time.Time
	Offered        time.Time
}

func (e *RevisionOrderError) Error() string {
	return fmt.Sprintf("derivatives: %s %s/%s: revision %d was fetched at %s; the offered "+
		"revision claims %s, which is earlier — the as-of reader orders on fetched_at, so it "+
		"would never be seen",
		e.Source, e.TradingDate, e.TradingSession, e.LatestRevision,
		e.LatestFetched.UTC().Format(time.RFC3339), e.Offered.UTC().Format(time.RFC3339))
}

func (e *RevisionOrderError) Is(target error) bool { return target == ErrRevisionOutOfOrder }

// Snapshot is one immutable raw observation.
type Snapshot struct {
	ID             int64
	Source         string
	TradingDate    string // YYYY-MM-DD, Asia/Taipei trading day
	TradingSession string
	RevisionNo     int
	DateSource     string
	ContentHash    string
	Payload        string
	FetchedAt      time.Time
}

// HashPayload is the content identity of a response.
//
// Over the payload only — not the fetch time, not the revision — so an unchanged response
// re-fetched tomorrow hashes the same and inserts nothing.
func HashPayload(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// PutSnapshot stores one observation, and is the only way rows enter derivative_snapshots.
//
// Three outcomes, and the middle one is the whole design:
//
//	no row for this key yet          → insert revision 0
//	latest revision has this hash    → insert NOTHING, return the existing row
//	latest revision has another hash → insert revision_no+1, leave the earlier row untouched
//
// The hash is compared against the LATEST revision, not against any revision. Under "any", an
// exchange that reverts a correction would find the original hash already present, insert
// nothing, and leave every reader permanently on the superseded content.
//
// Nothing here updates a row. A correction that edited the original in place would erase the
// only record of what was knowable on the earlier day, which is precisely what the
// point-in-time contract is for.
func PutSnapshot(ctx context.Context, s *store.Store, snap Snapshot) (Snapshot, error) {
	if snap.Source == "" || snap.TradingDate == "" || snap.TradingSession == "" {
		return Snapshot{}, fmt.Errorf("derivatives: snapshot needs source, trading_date and session")
	}
	if snap.DateSource != DateObserved && snap.DateSource != DateAssigned {
		return Snapshot{}, fmt.Errorf("derivatives: date_source must be OBSERVED or ASSIGNED, got %q",
			snap.DateSource)
	}
	switch snap.TradingSession {
	case SessionDay, SessionAfterHours, SessionCombined:
	default:
		// A typo opens a parallel key space rather than failing: "COMBINE" would never
		// collide with "COMBINED", so every re-fetch would insert instead of dedupe and the
		// as-of reader would never find the row it was looking for.
		return Snapshot{}, fmt.Errorf("derivatives: trading_session must be DAY, AFTER_HOURS or "+
			"COMBINED, got %q", snap.TradingSession)
	}
	// The date format is load-bearing, not cosmetic. trading_date is compared as a BARE STRING
	// (`trading_date <= ?`) against an as-of date parsed as 2006-01-02, so a decoder writing the
	// feed's native "20260904" would compare wrong against "2026-09-04" and silently return the
	// wrong session — exactly the class of failure the point-in-time contract exists to prevent.
	if _, err := time.Parse("2006-01-02", snap.TradingDate); err != nil {
		return Snapshot{}, fmt.Errorf("derivatives: trading_date must be YYYY-MM-DD, got %q",
			snap.TradingDate)
	}
	if snap.ContentHash == "" {
		snap.ContentHash = HashPayload(snap.Payload)
	}
	if snap.FetchedAt.IsZero() {
		snap.FetchedAt = time.Now().UTC()
	}

	var out Snapshot
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		latest, err := latestRevision(ctx, tx, snap.Source, snap.TradingDate, snap.TradingSession)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			snap.RevisionNo = 0
		case err != nil:
			return err
		case latest.ContentHash == snap.ContentHash:
			out = latest // identical content: nothing to record
			return nil
		default:
			// §6.3. Checked on the INSERT path only. An identical replay inserts nothing
			// whatever its timestamp claims, so there is no ordering to protect and
			// rejecting it would break the idempotent rerun invariant 9 depends on.
			if snap.FetchedAt.Before(latest.FetchedAt) {
				return &RevisionOrderError{
					Source: snap.Source, TradingDate: snap.TradingDate,
					TradingSession: snap.TradingSession,
					LatestRevision: latest.RevisionNo,
					LatestFetched:  latest.FetchedAt,
					Offered:        snap.FetchedAt,
				}
			}
			snap.RevisionNo = latest.RevisionNo + 1
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO derivative_snapshots
			  (schema_version, source, trading_date, trading_session, revision_no,
			   date_source, content_hash, payload, fetched_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			Schema.Version(), snap.Source, snap.TradingDate, snap.TradingSession,
			snap.RevisionNo, snap.DateSource, snap.ContentHash, snap.Payload,
			snap.FetchedAt.UTC().Format(time.RFC3339))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		snap.ID = id
		out = snap
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("derivatives: put snapshot: %w", err)
	}
	return out, nil
}

func latestRevision(ctx context.Context, tx *sql.Tx, source, date, session string) (Snapshot, error) {
	var s Snapshot
	var fetched string
	err := tx.QueryRowContext(ctx, `
		SELECT id, source, trading_date, trading_session, revision_no,
		       date_source, content_hash, payload, fetched_at
		FROM derivative_snapshots
		WHERE source = ? AND trading_date = ? AND trading_session = ?
		ORDER BY revision_no DESC LIMIT 1`, source, date, session).
		Scan(&s.ID, &s.Source, &s.TradingDate, &s.TradingSession, &s.RevisionNo,
			&s.DateSource, &s.ContentHash, &s.Payload, &fetched)
	if err != nil {
		return Snapshot{}, err
	}
	s.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
	return s, nil
}

// ── the point-in-time read ────────────────────────────────────────────────────────────

// AsOfCutoff converts a Taipei trading date to the UTC instant that ends it.
//
// The two clocks have to be reconciled explicitly rather than compared as strings.
// trading_date is a Taipei trading day; fetched_at is UTC RFC3339 at second granularity. Taipei
// is UTC+8, so 23:59:59 on day A in Taipei is 15:59:59 UTC on the SAME day — comparing the
// as-of date against a UTC timestamp lexicographically would cut the day eight hours early and
// silently hide the afternoon's fetches from their own session.
//
// Exported because it is the ONE definition of §7.3's bound, and every reader outside this
// package — the point-in-time history reader in internal/derivatives/history, and whatever M5
// and M6 add — has to use the same instant. A second copy of "end of the Taipei day, in UTC"
// is the FU-10 failure in miniature: two readers that agree on every day except the ones near
// the boundary, which are exactly the days a look-ahead hides in.
func AsOfCutoff(asOf string) (time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", asOf, dailydata.Taipei)
	if err != nil {
		return time.Time{}, fmt.Errorf("derivatives: as-of %q is not a date: %w", asOf, err)
	}
	return d.Add(24*time.Hour - time.Second).UTC(), nil
}

// asOfBound is AsOfCutoff in the exact text form the fetched_at column stores, so the SQL
// comparison is between two identically formatted RFC3339 strings.
func asOfBound(asOf string) (string, error) {
	end, err := AsOfCutoff(asOf)
	if err != nil {
		return "", err
	}
	return end.Format(time.RFC3339), nil
}

// SnapshotAsOf returns the revision of one observation that a reading dated asOf may see.
//
//	WHERE trading_date <= asOf AND fetched_at <= end-of-asOf
//	ORDER BY fetched_at DESC, revision_no DESC LIMIT 1
//
// Both bounds are load-bearing and they are not the same bound. The first says the session had
// happened; the second says we had already fetched it. A correction fetched after asOf is
// invisible to a reading dated asOf, which is what stops a later revision from improving a past
// evaluation — the failure mode that makes a backtest look better than the data ever was.
//
// revision_no DESC is the tie-break: fetched_at has second granularity, and two revisions
// landing in the same second would otherwise make the query non-deterministic. A
// non-deterministic point-in-time read is a look-ahead bug that only appears under load.
//
// Returns sql.ErrNoRows when nothing was visible on that date.
func SnapshotAsOf(ctx context.Context, s *store.Store, source, session, asOf string) (Snapshot, error) {
	bound, err := asOfBound(asOf)
	if err != nil {
		return Snapshot{}, err
	}
	var out Snapshot
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var fetched string
		if err := tx.QueryRowContext(ctx, `
			SELECT id, source, trading_date, trading_session, revision_no,
			       date_source, content_hash, payload, fetched_at
			FROM derivative_snapshots
			WHERE source = ? AND trading_session = ?
			  AND trading_date <= ? AND fetched_at <= ?
			ORDER BY trading_date DESC, fetched_at DESC, revision_no DESC
			LIMIT 1`, source, session, asOf, bound).
			Scan(&out.ID, &out.Source, &out.TradingDate, &out.TradingSession, &out.RevisionNo,
				&out.DateSource, &out.ContentHash, &out.Payload, &fetched); err != nil {
			return err
		}
		out.FetchedAt, err = time.Parse(time.RFC3339, fetched)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

// SnapshotForSession is SnapshotAsOf pinned to one exact session rather than the latest at or
// before asOf.
//
// M5's settlement comparison needs this: it must read the settlement snapshot archived FOR the
// session being aggregated. Reading the latest one instead re-introduces a 2.31x open-interest
// error when an earlier day is recomputed — the feed carries no date of its own and always
// answers with the most recent settlement event, so "is today a settlement day" asked of the
// live response is answered about a different day entirely.
func SnapshotForSession(ctx context.Context, s *store.Store,
	source, session, tradingDate, asOf string) (Snapshot, error) {
	bound, err := asOfBound(asOf)
	if err != nil {
		return Snapshot{}, err
	}
	var out Snapshot
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var fetched string
		if err := tx.QueryRowContext(ctx, `
			SELECT id, source, trading_date, trading_session, revision_no,
			       date_source, content_hash, payload, fetched_at
			FROM derivative_snapshots
			WHERE source = ? AND trading_session = ? AND trading_date = ?
			  AND fetched_at <= ?
			ORDER BY fetched_at DESC, revision_no DESC
			LIMIT 1`, source, session, tradingDate, bound).
			Scan(&out.ID, &out.Source, &out.TradingDate, &out.TradingSession, &out.RevisionNo,
				&out.DateSource, &out.ContentHash, &out.Payload, &fetched); err != nil {
			return err
		}
		out.FetchedAt, err = time.Parse(time.RFC3339, fetched)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return out, nil
}
