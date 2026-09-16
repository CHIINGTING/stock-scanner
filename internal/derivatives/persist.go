package derivatives

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Writing parsed rows into the M2 tables.
//
// One rule governs every writer here and it is the one invariant 9 rests on: a row belongs to
// a SNAPSHOT, and a snapshot is immutable. So the idempotency question is not "does this row
// already exist" but "has this snapshot already been written", which is answerable in one
// query and cannot half-succeed.
//
// The consequence is deliberate: the INSERTs are plain INSERTs, never INSERT OR IGNORE. A key
// collision inside one snapshot means the caller built the wrong snapshot — the canonical
// case being both trading sessions of DailyMarketReportOpt merged into one COMBINED snapshot,
// where 126 of the fixture's 277 rows collide. OR IGNORE would swallow exactly those 126 rows
// and report success with the night session silently gone, which is the failure §10.2b exists
// to prevent. It has to be loud.

// StoredStrike is one options_oi_by_strike row as stored, read back for verification.
//
// OI / Volume / Settlement stay pointers on the way out for the same reason they are nullable
// on the way in: "-" (not carried) and "0" (a strike nobody holds) are different facts, and a
// reader that cannot see the difference cannot preserve it.
type StoredStrike struct {
	SnapshotID int64
	Product    string
	ExpiryCode string
	Strike     float64
	CallPut    string
	OI         *float64
	Volume     *float64
	Settlement *float64
	Source     string
}

// PutOptionStrikes writes one session's by-strike rows under one snapshot.
//
// It returns the number of rows inserted; a re-run over an unchanged response returns 0,
// because PutSnapshot handed back the existing snapshot and the rows under it are already
// there. Nothing is updated, ever.
func PutOptionStrikes(ctx context.Context, s storer, snapshotID int64, source string,
	fetchedAt time.Time, rows []provider.OptionStrikeRow) (int, error) {

	var n int
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		already, err := snapshotHasRows(ctx, tx, "options_oi_by_strike", snapshotID)
		if err != nil || already {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO options_oi_by_strike
			  (schema_version, snapshot_id, product, expiry_code, strike, call_put,
			   oi, volume, settlement, source, fetched_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		at := fetchedAt.UTC().Format(time.RFC3339)
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, Schema.Version(), snapshotID, r.Product,
				r.ExpiryCode, r.Strike, r.CallPut, r.OI, r.Volume, r.Settlement,
				source, at); err != nil {
				return fmt.Errorf("strike %s/%s/%v/%s: %w",
					r.Product, r.ExpiryCode, r.Strike, r.CallPut, err)
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("derivatives: put option strikes: %w", err)
	}
	return n, nil
}

// OptionStrikesForSnapshot reads one snapshot's rows back, ordered so a test and a reader see
// the same list.
func OptionStrikesForSnapshot(ctx context.Context, s storer, snapshotID int64) ([]StoredStrike, error) {
	var out []StoredStrike
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT snapshot_id, product, expiry_code, strike, call_put, oi, volume,
			       settlement, source
			FROM options_oi_by_strike WHERE snapshot_id = ?
			ORDER BY product, expiry_code, strike, call_put`, snapshotID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r StoredStrike
			if err := rows.Scan(&r.SnapshotID, &r.Product, &r.ExpiryCode, &r.Strike,
				&r.CallPut, &r.OI, &r.Volume, &r.Settlement, &r.Source); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("derivatives: read option strikes: %w", err)
	}
	return out, nil
}

// PutInstitutional writes the FLOW and POSITION rows of one institutional response.
//
// value_semantics is in the key, so one feed row's six observations — {FLOW, POSITION} ×
// {LONG, SHORT, NET} — are six rows rather than one row overwritten five times.
func PutInstitutional(ctx context.Context, s storer, snapshotID int64, source string,
	fetchedAt time.Time, rows []provider.InstitutionalRow) (int, error) {

	var n int
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		already, err := snapshotHasRows(ctx, tx, "institutional_derivatives", snapshotID)
		if err != nil || already {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO institutional_derivatives
			  (schema_version, snapshot_id, institution, product, call_put,
			   value_semantics, side, lots, value_thousands, source, fetched_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		at := fetchedAt.UTC().Format(time.RFC3339)
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, Schema.Version(), snapshotID, r.Institution,
				r.Product, r.CallPut, r.Semantics, r.Side, r.Lots, r.ValueThousands,
				source, at); err != nil {
				return fmt.Errorf("%s/%s/%s/%s/%s: %w",
					r.Institution, r.Product, r.CallPut, r.Semantics, r.Side, err)
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("derivatives: put institutional: %w", err)
	}
	return n, nil
}

// PutMarginRates writes one margin snapshot's rows.
//
// The rows are stored exactly as published, with the exchange's own effective date. The
// carry-forward M7 needs — the most recent effective_date <= D — walks INSIDE this snapshot
// and never across snapshots, which is why snapshot_id is part of the key: a failed fetch for
// D leaves no snapshot for D and therefore no margin at all, which is margin_source: NONE.
func PutMarginRates(ctx context.Context, s storer, snapshotID int64, source string,
	fetchedAt time.Time, rows []provider.MarginRow) (int, error) {

	var n int
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		already, err := snapshotHasRows(ctx, tx, "margin_rates", snapshotID)
		if err != nil || already {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO margin_rates
			  (schema_version, snapshot_id, product, effective_date, clearing_margin,
			   maintenance_margin, initial_margin, source, fetched_at)
			VALUES (?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		at := fetchedAt.UTC().Format(time.RFC3339)
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, Schema.Version(), snapshotID, r.Product,
				r.EffectiveDate, r.ClearingMargin, r.MaintenanceMargin, r.InitialMargin,
				source, at); err != nil {
				return fmt.Errorf("margin %s/%s: %w", r.Product, r.EffectiveDate, err)
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("derivatives: put margin rates: %w", err)
	}
	return n, nil
}

// Contract is one row of derivative_contracts: a series under every name the exchange gives
// it, plus the kind its CODE says it is.
type Contract struct {
	Product        string
	ExpiryCode     string
	ExpiryKind     string
	SettlementDate string // may be empty: only the settlement feed knows it
	AltProduct     string // TXU, the settlement feed's name for TXO
	AltExpiryCode  string // 202609, a delivery month rather than a series code
	ContractName   string // 臺指選擇權F1, where the series suffix actually lives
	Source         string
}

// ErrExpiryKindConflict fires when two feeds classify the same series differently.
//
// It is a hard error rather than a last-write-wins, because that disagreement has exactly one
// cause worth having: a classifier was fed a column that is not a series code. §3.1 names the
// three feeds that may classify, and SettledPositionsIndexOptions is emphatically not one —
// its ContractDeliveryMonth is a bare 202609 for the F1 WEEKLY, so a rule applied there calls
// MONTHLY the same contract DailyMarketReportOpt calls WEEKLY. Silently keeping whichever
// arrived last would make M6's weekly/monthly split depend on fetch order.
var ErrExpiryKindConflict = fmt.Errorf("derivatives: one series classified two ways")

// PutContracts merges contract identities.
//
// derivative_contracts is keyed (product, expiry_code) and is NOT snapshot-scoped: it is the
// reconciliation table, and the whole point is that a row accumulates what several feeds each
// know about one series. So this one merges rather than skipping — but it only ever FILLS
// empty fields. A populated alias is never overwritten by an empty one, which is what would
// happen if the by-strike feed (which knows no aliases) ran after the settlement feed.
//
// Returns how many rows were newly inserted. A re-run inserts 0, which is invariant 9.
func PutContracts(ctx context.Context, s storer, fetchedAt time.Time, cs []Contract) (int, error) {
	var inserted int
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		at := fetchedAt.UTC().Format(time.RFC3339)
		for _, c := range cs {
			if c.Product == "" || c.ExpiryCode == "" {
				return fmt.Errorf("contract needs product and expiry_code, got %q/%q",
					c.Product, c.ExpiryCode)
			}
			var (
				existingKind string
				existingAltP string
				existingAltE string
				existingName string
				existingSett sql.NullString
			)
			err := tx.QueryRowContext(ctx, `
				SELECT expiry_kind, alt_product, alt_expiry_code, contract_name, settlement_date
				FROM derivative_contracts WHERE product = ? AND expiry_code = ?`,
				c.Product, c.ExpiryCode).
				Scan(&existingKind, &existingAltP, &existingAltE, &existingName, &existingSett)
			switch {
			case err == sql.ErrNoRows:
				var sett any
				if c.SettlementDate != "" {
					sett = c.SettlementDate
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO derivative_contracts
					  (schema_version, source, product, expiry_code, expiry_kind,
					   settlement_date, alt_product, alt_expiry_code, contract_name, fetched_at)
					VALUES (?,?,?,?,?,?,?,?,?,?)`,
					Schema.Version(), c.Source, c.Product, c.ExpiryCode, c.ExpiryKind,
					sett, c.AltProduct, c.AltExpiryCode, c.ContractName, at); err != nil {
					return err
				}
				inserted++
				continue
			case err != nil:
				return err
			}
			if existingKind != c.ExpiryKind {
				return fmt.Errorf("%w: %s/%s is %s here and %s in %s",
					ErrExpiryKindConflict, c.Product, c.ExpiryCode, existingKind,
					c.ExpiryKind, c.Source)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE derivative_contracts SET
				  alt_product     = CASE WHEN alt_product     = '' THEN ? ELSE alt_product END,
				  alt_expiry_code = CASE WHEN alt_expiry_code = '' THEN ? ELSE alt_expiry_code END,
				  contract_name   = CASE WHEN contract_name   = '' THEN ? ELSE contract_name END,
				  settlement_date = COALESCE(settlement_date, ?)
				WHERE product = ? AND expiry_code = ?`,
				c.AltProduct, c.AltExpiryCode, c.ContractName,
				nullIfEmpty(c.SettlementDate), c.Product, c.ExpiryCode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("derivatives: put contracts: %w", err)
	}
	return inserted, nil
}

// ContractByCode reads one reconciled series back.
func ContractByCode(ctx context.Context, s storer, product, expiryCode string) (Contract, error) {
	var c Contract
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var sett sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT product, expiry_code, expiry_kind, settlement_date, alt_product,
			       alt_expiry_code, contract_name, source
			FROM derivative_contracts WHERE product = ? AND expiry_code = ?`,
			product, expiryCode).
			Scan(&c.Product, &c.ExpiryCode, &c.ExpiryKind, &sett, &c.AltProduct,
				&c.AltExpiryCode, &c.ContractName, &c.Source); err != nil {
			return err
		}
		c.SettlementDate = sett.String
		return nil
	})
	if err != nil {
		return Contract{}, err
	}
	return c, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// snapshotHasRows answers the only idempotency question these writers need.
//
// Per snapshot rather than per row: a snapshot is written once, whole, inside one
// transaction, so it is either all there or none of it is. Checking row by row would make a
// half-written snapshot look complete on the next run.
func snapshotHasRows(ctx context.Context, tx *sql.Tx, table string, snapshotID int64) (bool, error) {
	// table is never caller-supplied — the three call sites above pass literals — so the
	// concatenation cannot carry input. Placeholders are not allowed for identifiers.
	var n int
	err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE snapshot_id = ?", snapshotID).Scan(&n)
	return n > 0, err
}

// storer is the slice of *store.Store these writers use.
//
// WithTx is the only exported way into R15's tables, reads included (§7.1), so this is the
// whole surface. It is an interface so a caller can wrap it — not so it can be faked: the
// tests run against a real SQLite file, because half of what is being asserted here is what
// the UNIQUE indexes do.
type storer interface {
	WithTx(ctx context.Context, fn func(*sql.Tx) error) error
}
