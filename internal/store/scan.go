package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ScanRepo persists scan runs and the stock snapshots belonging to them. It is the entry
// point of the R13 chain: nothing else can be written until a run exists to hang it off.
type ScanRepo struct{ s *Store }

// Scans returns the scan-run / snapshot repository.
func (s *Store) Scans() *ScanRepo { return &ScanRepo{s: s} }

// StartRun records the beginning of a scan and returns the row with its assigned ID.
//
// FinishedAt stays NULL until FinishRun is called, so a run that died half way through is
// permanently identifiable as incomplete rather than being averaged into later statistics.
func (r *ScanRepo) StartRun(ctx context.Context, run ScanRun) (ScanRun, error) {
	if run.RunUID == "" {
		return ScanRun{}, errors.New("store: scan run needs a run_uid")
	}
	if run.TradingDate == "" {
		return ScanRun{}, errors.New("store: scan run needs a trading_date")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = nowUTC()
	}

	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO scan_runs
			(run_uid, trading_date, started_at, scanner_version, market_regime, market_score, universe_size, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunUID, run.TradingDate, formatTime(run.StartedAt), run.ScannerVersion,
		run.MarketRegime, nullFloat(run.MarketScore), run.UniverseSize, run.Note)
	if err != nil {
		return ScanRun{}, fmt.Errorf("store: insert scan run %s: %w", run.RunUID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ScanRun{}, fmt.Errorf("store: scan run id: %w", err)
	}
	run.ID = id
	run.FinishedAt = nil
	return run, nil
}

// FinishRun stamps a run as complete. Calling it twice is an error: a run finishes once,
// and a second stamp would overwrite the real duration.
func (r *ScanRepo) FinishRun(ctx context.Context, runID int64, at time.Time) error {
	if at.IsZero() {
		at = nowUTC()
	}
	res, err := r.s.db.ExecContext(ctx,
		`UPDATE scan_runs SET finished_at = ? WHERE id = ? AND finished_at IS NULL`,
		formatTime(at), runID)
	if err != nil {
		return fmt.Errorf("store: finish run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: run %d is missing or already finished: %w", runID, ErrNotFound)
	}
	return nil
}

const scanRunCols = `id, run_uid, trading_date, started_at, finished_at,
	scanner_version, market_regime, market_score, universe_size, note`

// RunByUID looks up a run by its external id.
func (r *ScanRepo) RunByUID(ctx context.Context, uid string) (ScanRun, error) {
	row := r.s.db.QueryRowContext(ctx,
		`SELECT `+scanRunCols+` FROM scan_runs WHERE run_uid = ?`, uid)
	run, err := scanScanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanRun{}, fmt.Errorf("store: run %q: %w", uid, ErrNotFound)
	}
	return run, err
}

// RunsByDate returns every run for a trading date, oldest first. Reruns of the same day are
// kept as separate runs rather than replacing each other — which one was in force is a
// question the data should be able to answer.
func (r *ScanRepo) RunsByDate(ctx context.Context, tradingDate string) ([]ScanRun, error) {
	rows, err := r.s.db.QueryContext(ctx,
		`SELECT `+scanRunCols+` FROM scan_runs WHERE trading_date = ? ORDER BY started_at, id`,
		tradingDate)
	if err != nil {
		return nil, fmt.Errorf("store: runs for %s: %w", tradingDate, err)
	}
	defer rows.Close()

	var out []ScanRun
	for rows.Next() {
		run, err := scanScanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan helper serves the
// single-row and multi-row paths without duplicating the column list.
type rowScanner interface{ Scan(dest ...any) error }

func scanScanRun(sc rowScanner) (ScanRun, error) {
	var (
		run        ScanRun
		startedAt  string
		finishedAt sql.NullString
		score      sql.NullFloat64
	)
	if err := sc.Scan(&run.ID, &run.RunUID, &run.TradingDate, &startedAt, &finishedAt,
		&run.ScannerVersion, &run.MarketRegime, &score, &run.UniverseSize, &run.Note); err != nil {
		return ScanRun{}, err
	}
	var err error
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return ScanRun{}, err
	}
	if run.FinishedAt, err = parseTimePtr(finishedAt); err != nil {
		return ScanRun{}, err
	}
	run.MarketScore = floatPtr(score)
	return run, nil
}

// SaveSnapshot writes one stock's snapshot for a run.
//
// It is an INSERT, never an upsert: re-recording the same (run, symbol, tab) means the
// caller has a bug, and an audit store should say so rather than quietly replace evidence
// that other rows already point at.
func (r *ScanRepo) SaveSnapshot(ctx context.Context, snap StockSnapshot) (StockSnapshot, error) {
	if snap.ScanRunID == 0 {
		return StockSnapshot{}, errors.New("store: snapshot needs a scan_run_id")
	}
	if snap.Symbol == "" {
		return StockSnapshot{}, errors.New("store: snapshot needs a symbol")
	}
	if snap.SourceTab == "" {
		return StockSnapshot{}, errors.New("store: snapshot needs a source_tab")
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = nowUTC()
	}

	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO stock_snapshots
			(scan_run_id, symbol, name, trading_date, source_tab, close,
			 scanner_action, scanner_score, rocket_score, rocket_stage, watch_action, sector, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ScanRunID, snap.Symbol, snap.Name, snap.TradingDate, snap.SourceTab,
		nullFloat(snap.Close), snap.ScannerAction, nullFloat(snap.ScannerScore),
		nullInt(snap.RocketScore), snap.RocketStage, snap.WatchAction, snap.Sector,
		formatTime(snap.CreatedAt))
	if err != nil {
		return StockSnapshot{}, fmt.Errorf("store: insert snapshot %s/%s: %w", snap.Symbol, snap.SourceTab, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return StockSnapshot{}, fmt.Errorf("store: snapshot id: %w", err)
	}
	snap.ID = id
	return snap, nil
}

const snapshotCols = `id, scan_run_id, symbol, name, trading_date, source_tab, close,
	scanner_action, scanner_score, rocket_score, rocket_stage, watch_action, sector, created_at`

// SnapshotsByRun returns every snapshot of a run, ordered by symbol for stable output.
func (r *ScanRepo) SnapshotsByRun(ctx context.Context, runID int64) ([]StockSnapshot, error) {
	rows, err := r.s.db.QueryContext(ctx,
		`SELECT `+snapshotCols+` FROM stock_snapshots WHERE scan_run_id = ? ORDER BY symbol, source_tab`,
		runID)
	if err != nil {
		return nil, fmt.Errorf("store: snapshots for run %d: %w", runID, err)
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

// Snapshot fetches one snapshot by id.
func (r *ScanRepo) Snapshot(ctx context.Context, id int64) (StockSnapshot, error) {
	row := r.s.db.QueryRowContext(ctx, `SELECT `+snapshotCols+` FROM stock_snapshots WHERE id = ?`, id)
	snap, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StockSnapshot{}, fmt.Errorf("store: snapshot %d: %w", id, ErrNotFound)
	}
	return snap, err
}

func scanSnapshot(sc rowScanner) (StockSnapshot, error) {
	var (
		snap      StockSnapshot
		closePx   sql.NullFloat64
		score     sql.NullFloat64
		rocket    sql.NullInt64
		createdAt string
	)
	if err := sc.Scan(&snap.ID, &snap.ScanRunID, &snap.Symbol, &snap.Name, &snap.TradingDate,
		&snap.SourceTab, &closePx, &snap.ScannerAction, &score, &rocket, &snap.RocketStage,
		&snap.WatchAction, &snap.Sector, &createdAt); err != nil {
		return StockSnapshot{}, err
	}
	var err error
	if snap.CreatedAt, err = parseTime(createdAt); err != nil {
		return StockSnapshot{}, err
	}
	snap.Close = floatPtr(closePx)
	snap.ScannerScore = floatPtr(score)
	snap.RocketScore = intPtr(rocket)
	return snap, nil
}
