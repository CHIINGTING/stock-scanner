package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// OutcomeRepo persists what actually happened after a snapshot.
//
// The whole design of this file exists to serve one rule: a prediction is never rewritten by
// hindsight. Outcomes are a SEPARATE table from decisions, they are filled forward one
// horizon at a time as the future becomes known, and a horizon that already has a value is
// never overwritten — not by a rerun, not by better data, not by a later build.
type OutcomeRepo struct{ s *Store }

// Outcomes returns the outcome repository.
func (s *Store) Outcomes() *OutcomeRepo { return &OutcomeRepo{s: s} }

// Horizon names the forward windows R13 tracks. They are the columns of the outcomes table,
// not free-form, so a typo cannot silently create a new metric nobody aggregates.
type Horizon int

// The tracked horizons, in trading days.
const (
	H1D  Horizon = 1
	H3D  Horizon = 3
	H5D  Horizon = 5
	H10D Horizon = 10
	H20D Horizon = 20
)

// Horizons is every tracked horizon in order, for callers that iterate.
var Horizons = []Horizon{H1D, H3D, H5D, H10D, H20D}

// column maps a horizon to its outcomes column. An unknown horizon returns "", which every
// caller turns into an error rather than a silent no-op.
func (h Horizon) column() string {
	switch h {
	case H1D:
		return "return_1d"
	case H3D:
		return "return_3d"
	case H5D:
		return "return_5d"
	case H10D:
		return "return_10d"
	case H20D:
		return "return_20d"
	default:
		return ""
	}
}

// Days is the horizon length in trading days.
func (h Horizon) Days() int { return int(h) }

// Ensure creates the outcome row for a snapshot if it does not exist yet, and returns it.
//
// baseClose is the price every return is measured from and is fixed at creation. A second
// call with a different base does NOT move it — the basis of a measurement is part of the
// record, and letting it drift would silently redefine every return already stored.
func (r *OutcomeRepo) Ensure(ctx context.Context, snapshotID int64, baseClose float64) (Outcome, error) {
	if snapshotID == 0 {
		return Outcome{}, errors.New("store: outcome needs a stock_snapshot_id")
	}
	if baseClose <= 0 {
		return Outcome{}, fmt.Errorf("store: outcome base close must be positive, got %v", baseClose)
	}
	now := nowUTC()

	// INSERT ... ON CONFLICT DO NOTHING keeps this idempotent without a read-then-write
	// race, and without touching an existing row's base price.
	if _, err := r.s.db.ExecContext(ctx, `
		INSERT INTO outcomes (stock_snapshot_id, base_close, bars_observed, first_computed_at, last_updated_at)
		VALUES (?, ?, 0, ?, ?)
		ON CONFLICT(stock_snapshot_id) DO NOTHING`,
		snapshotID, baseClose, formatTime(now), formatTime(now)); err != nil {
		return Outcome{}, fmt.Errorf("store: ensure outcome for snapshot %d: %w", snapshotID, err)
	}
	return r.BySnapshot(ctx, snapshotID)
}

// SetReturn fills in one horizon, exactly once.
//
// It returns false when the horizon was ALREADY recorded — the value stays as first written
// and this is not an error, because a rerun over a date whose returns are already known is a
// normal, expected event. It is only an error if the outcome row does not exist at all.
func (r *OutcomeRepo) SetReturn(ctx context.Context, snapshotID int64, h Horizon, pct float64) (bool, error) {
	col := h.column()
	if col == "" {
		return false, fmt.Errorf("store: unknown horizon %dd", int(h))
	}
	// The WHERE clause is what enforces write-once: an already-populated column matches
	// nothing, so hindsight has no path to the row.
	res, err := r.s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE outcomes SET %s = ?, last_updated_at = ?
		WHERE stock_snapshot_id = ? AND %s IS NULL`, col, col),
		pct, formatTime(nowUTC()), snapshotID)
	if err != nil {
		return false, fmt.Errorf("store: set %s for snapshot %d: %w", col, snapshotID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: set %s for snapshot %d: %w", col, snapshotID, err)
	}
	if n > 0 {
		return true, nil
	}
	// Nothing changed: either the value was already there, or there is no row at all. Those
	// mean very different things, so tell them apart rather than reporting a bland false.
	if _, err := r.BySnapshot(ctx, snapshotID); err != nil {
		return false, err
	}
	return false, nil
}

// SetExtremes records the 20-day best and worst excursions, write-once like the returns.
//
// Both are set together because they describe the same path: a caller that knew one but not
// the other would be reporting half a picture.
func (r *OutcomeRepo) SetExtremes(ctx context.Context, snapshotID int64, maxGain, maxDrawdown float64) (bool, error) {
	res, err := r.s.db.ExecContext(ctx, `
		UPDATE outcomes SET max_gain_20d = ?, max_drawdown_20d = ?, last_updated_at = ?
		WHERE stock_snapshot_id = ? AND max_gain_20d IS NULL AND max_drawdown_20d IS NULL`,
		maxGain, maxDrawdown, formatTime(nowUTC()), snapshotID)
	if err != nil {
		return false, fmt.Errorf("store: set extremes for snapshot %d: %w", snapshotID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: set extremes for snapshot %d: %w", snapshotID, err)
	}
	if n > 0 {
		return true, nil
	}
	if _, err := r.BySnapshot(ctx, snapshotID); err != nil {
		return false, err
	}
	return false, nil
}

// SetBarsObserved records how many forward trading days have been seen.
//
// Unlike the returns this DOES move — it is a progress counter, not a measurement — but only
// forwards. A smaller count is ignored, so a partial re-scan cannot make a fully measured
// snapshot look unmeasured.
func (r *OutcomeRepo) SetBarsObserved(ctx context.Context, snapshotID int64, bars int) error {
	if bars < 0 {
		return fmt.Errorf("store: bars observed cannot be negative, got %d", bars)
	}
	res, err := r.s.db.ExecContext(ctx, `
		UPDATE outcomes SET bars_observed = ?, last_updated_at = ?
		WHERE stock_snapshot_id = ? AND bars_observed < ?`,
		bars, formatTime(nowUTC()), snapshotID, bars)
	if err != nil {
		return fmt.Errorf("store: set bars for snapshot %d: %w", snapshotID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		// Confirm the row exists; a missing row is a real failure, a stale count is not.
		if _, err := r.BySnapshot(ctx, snapshotID); err != nil {
			return err
		}
	}
	return nil
}

const outcomeCols = `id, stock_snapshot_id, base_close, return_1d, return_3d, return_5d,
	return_10d, return_20d, max_gain_20d, max_drawdown_20d, bars_observed,
	first_computed_at, last_updated_at`

// BySnapshot fetches a snapshot's outcome row.
func (r *OutcomeRepo) BySnapshot(ctx context.Context, snapshotID int64) (Outcome, error) {
	row := r.s.db.QueryRowContext(ctx,
		`SELECT `+outcomeCols+` FROM outcomes WHERE stock_snapshot_id = ?`, snapshotID)
	o, err := scanOutcome(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, fmt.Errorf("store: outcome for snapshot %d: %w", snapshotID, ErrNotFound)
	}
	return o, err
}

// PendingHorizon lists snapshots on or before a trading date whose given horizon is still
// unmeasured — the work queue R13-M6 drains. Ordered oldest first so the longest-waiting
// snapshots are settled before the newest ones.
func (r *OutcomeRepo) PendingHorizon(ctx context.Context, h Horizon, onOrBefore string, limit int) ([]StockSnapshot, error) {
	col := h.column()
	if col == "" {
		return nil, fmt.Errorf("store: unknown horizon %dd", int(h))
	}
	q := fmt.Sprintf(`
		SELECT s.id, s.scan_run_id, s.symbol, s.name, s.trading_date, s.source_tab, s.close,
		       s.scanner_action, s.scanner_score, s.rocket_score, s.rocket_stage,
		       s.watch_action, s.sector, s.created_at
		FROM stock_snapshots s
		JOIN outcomes o ON o.stock_snapshot_id = s.id
		WHERE o.%s IS NULL AND s.trading_date <= ?
		ORDER BY s.trading_date, s.symbol`, col)
	args := []any{onOrBefore}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: pending %s: %w", col, err)
	}
	defer rows.Close()

	var out []StockSnapshot
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func scanOutcome(sc rowScanner) (Outcome, error) {
	var (
		o                          Outcome
		r1, r3, r5, r10, r20       sql.NullFloat64
		maxGain, maxDD             sql.NullFloat64
		firstComputed, lastUpdated string
	)
	if err := sc.Scan(&o.ID, &o.StockSnapshotID, &o.BaseClose, &r1, &r3, &r5, &r10, &r20,
		&maxGain, &maxDD, &o.BarsObserved, &firstComputed, &lastUpdated); err != nil {
		return Outcome{}, err
	}
	var err error
	if o.FirstComputedAt, err = parseTime(firstComputed); err != nil {
		return Outcome{}, err
	}
	if o.LastUpdatedAt, err = parseTime(lastUpdated); err != nil {
		return Outcome{}, err
	}
	o.Return1D, o.Return3D, o.Return5D = floatPtr(r1), floatPtr(r3), floatPtr(r5)
	o.Return10D, o.Return20D = floatPtr(r10), floatPtr(r20)
	o.MaxGain20D, o.MaxDrawdown20D = floatPtr(maxGain), floatPtr(maxDD)
	return o, nil
}
