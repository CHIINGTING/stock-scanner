package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EvidenceRepo persists the facts the scanner already computed, and hands them back to the
// agents. It never derives anything: if a number is not here, the scanner did not produce
// it, and no layer above may invent one.
type EvidenceRepo struct{ s *Store }

// Evidence returns the evidence repository.
func (s *Store) Evidence() *EvidenceRepo { return &EvidenceRepo{s: s} }

// Num builds a numeric evidence row.
func Num(category, key string, v float64, unit, source string) Evidence {
	return Evidence{Category: category, Key: key, NumValue: &v, Unit: unit, Source: source}
}

// Text builds a labelled evidence row (a stage, a regime, a pattern name).
func Text(category, key, v, source string) Evidence {
	return Evidence{Category: category, Key: key, TextValue: &v, Source: source}
}

// SaveMany writes a batch of evidence for one snapshot in a single transaction.
//
// Rows with neither value are DROPPED rather than stored as zero — a fact the scanner could
// not compute must leave no trace, so an agent reading the store sees absence, not a
// confident 0. The count returned is how many actually landed.
func (r *EvidenceRepo) SaveMany(ctx context.Context, snapshotID int64, items []Evidence) (int, error) {
	if snapshotID == 0 {
		return 0, errors.New("store: evidence needs a stock_snapshot_id")
	}
	now := nowUTC()

	var written int
	err := r.s.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO evidence
				(stock_snapshot_id, category, key, num_value, text_value, unit, source, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("store: prepare evidence insert: %w", err)
		}
		defer stmt.Close()

		for _, e := range items {
			if e.NumValue == nil && e.TextValue == nil {
				continue // absent stays absent
			}
			if e.Category == "" || e.Key == "" {
				return fmt.Errorf("store: evidence needs category and key (got %q/%q)", e.Category, e.Key)
			}
			createdAt := e.CreatedAt
			if createdAt.IsZero() {
				createdAt = now
			}
			var text any
			if e.TextValue != nil {
				text = *e.TextValue
			}
			if _, err := stmt.ExecContext(ctx, snapshotID, e.Category, e.Key,
				nullFloat(e.NumValue), text, e.Unit, e.Source, formatTime(createdAt)); err != nil {
				return fmt.Errorf("store: insert evidence %s/%s: %w", e.Category, e.Key, err)
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

const evidenceCols = `id, stock_snapshot_id, category, key, num_value, text_value, unit, source, created_at`

// BySnapshot returns all evidence for a snapshot, ordered for stable output.
func (r *EvidenceRepo) BySnapshot(ctx context.Context, snapshotID int64) ([]Evidence, error) {
	return r.query(ctx,
		`SELECT `+evidenceCols+` FROM evidence WHERE stock_snapshot_id = ? ORDER BY category, key`,
		snapshotID)
}

// ByCategory returns one domain's evidence for a snapshot.
//
// This is how R13-M3 keeps each specialist agent inside its own lane: the technical agent is
// handed the technical rows and never sees the news rows, so "only analyse your domain" is
// enforced by what it is given rather than by asking a model to behave.
func (r *EvidenceRepo) ByCategory(ctx context.Context, snapshotID int64, category string) ([]Evidence, error) {
	return r.query(ctx,
		`SELECT `+evidenceCols+` FROM evidence WHERE stock_snapshot_id = ? AND category = ? ORDER BY key`,
		snapshotID, category)
}

func (r *EvidenceRepo) query(ctx context.Context, q string, args ...any) ([]Evidence, error) {
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query evidence: %w", err)
	}
	defer rows.Close()

	var out []Evidence
	for rows.Next() {
		var (
			e         Evidence
			num       sql.NullFloat64
			text      sql.NullString
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.StockSnapshotID, &e.Category, &e.Key, &num, &text,
			&e.Unit, &e.Source, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan evidence: %w", err)
		}
		if e.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		e.NumValue = floatPtr(num)
		if text.Valid {
			v := text.String
			e.TextValue = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
