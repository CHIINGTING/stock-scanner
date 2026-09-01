package store

import (
	"context"
	"database/sql"
	"fmt"
)

// SchemaVersion is the highest migration this build knows how to apply. A database
// migrated PAST this version is refused rather than used: an older binary silently
// writing into a newer schema is how audit stores quietly lose columns.
const SchemaVersion = 2

// migration is one forward-only schema step. There is deliberately no Down: this is an
// audit store, and the answer to a bad migration is a new forward migration, not an
// automated rollback that destroys recorded evidence.
type migration struct {
	version int
	name    string
	stmts   []string
}

// migrations must stay append-only and ordered. Editing a RELEASED migration would leave
// existing databases permanently inconsistent with new ones; once version 1 has shipped,
// every change is a new version. (Version 1 itself was still amended in place during R13-M1
// review, while no database built from it existed anywhere.)
var migrations = []migration{
	{
		version: 1,
		name:    "r13_foundation",
		stmts: []string{
			// ── scan_runs ────────────────────────────────────────────────────────────
			// One row per scanner execution. Everything else in R13 is reachable from
			// here, which is what makes "trace this back to one scan" a schema property
			// rather than a convention.
			`CREATE TABLE scan_runs (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				run_uid         TEXT    NOT NULL UNIQUE,
				trading_date    TEXT    NOT NULL,
				started_at      TEXT    NOT NULL,
				finished_at     TEXT,
				scanner_version TEXT    NOT NULL DEFAULT '',
				market_regime   TEXT    NOT NULL DEFAULT '',
				market_score    REAL,
				universe_size   INTEGER NOT NULL DEFAULT 0,
				note            TEXT    NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX idx_scan_runs_trading_date ON scan_runs(trading_date)`,

			// ── stock_snapshots ──────────────────────────────────────────────────────
			// The anchor row. UNIQUE(scan_run_id, symbol, source_tab) allows the same
			// symbol to appear once per tab of a run — a watchlist stock is usually also
			// in the market scan and the two carry different scanner decisions — while
			// still making a re-insert of the same (run, symbol, tab) an error instead of
			// a duplicate that would double-count in every later statistic.
			`CREATE TABLE stock_snapshots (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				scan_run_id    INTEGER NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
				symbol         TEXT    NOT NULL,
				name           TEXT    NOT NULL DEFAULT '',
				trading_date   TEXT    NOT NULL,
				source_tab     TEXT    NOT NULL,
				close          REAL,
				scanner_action TEXT    NOT NULL DEFAULT '',
				scanner_score  REAL,
				rocket_score   INTEGER,
				rocket_stage   TEXT    NOT NULL DEFAULT '',
				watch_action   TEXT    NOT NULL DEFAULT '',
				sector         TEXT    NOT NULL DEFAULT '',
				created_at     TEXT    NOT NULL,
				UNIQUE(scan_run_id, symbol, source_tab)
			)`,
			`CREATE INDEX idx_stock_snapshots_symbol_date ON stock_snapshots(symbol, trading_date)`,
			`CREATE INDEX idx_stock_snapshots_run ON stock_snapshots(scan_run_id)`,

			// ── evidence ─────────────────────────────────────────────────────────────
			// Key/value by design: a fact the scanner did not produce has NO ROW, which is
			// how MISSING stays distinguishable from a real 0. The CHECK enforces that a
			// row without any payload cannot exist.
			`CREATE TABLE evidence (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				stock_snapshot_id INTEGER NOT NULL REFERENCES stock_snapshots(id) ON DELETE CASCADE,
				category          TEXT    NOT NULL,
				key               TEXT    NOT NULL,
				num_value         REAL,
				text_value        TEXT,
				unit              TEXT    NOT NULL DEFAULT '',
				source            TEXT    NOT NULL DEFAULT '',
				created_at        TEXT    NOT NULL,
				UNIQUE(stock_snapshot_id, category, key),
				CHECK (num_value IS NOT NULL OR text_value IS NOT NULL)
			)`,
			`CREATE INDEX idx_evidence_snapshot ON evidence(stock_snapshot_id)`,
			`CREATE INDEX idx_evidence_lookup ON evidence(category, key)`,

			// ── analysis_runs ────────────────────────────────────────────────────────
			// One multi-agent EXECUTION over one snapshot. This layer is what separates
			// "an agent may not revise its verdict" (kept) from "a snapshot may only ever
			// be analysed once" (rejected) — replay, prompt A/B and research backtests all
			// need the second to be allowed.
			//
			// run_uid is shared by every snapshot of one execution, so uniqueness is per
			// (run_uid, stock_snapshot_id): that is what makes "which execution produced
			// this panel" answerable across stocks while still rejecting a duplicate row.
			`CREATE TABLE analysis_runs (
				id                   INTEGER PRIMARY KEY AUTOINCREMENT,
				stock_snapshot_id    INTEGER NOT NULL REFERENCES stock_snapshots(id) ON DELETE CASCADE,
				run_uid              TEXT    NOT NULL,
				mode                 TEXT    NOT NULL,
				orchestrator_version TEXT    NOT NULL DEFAULT '',
				prompt_set_version   TEXT    NOT NULL DEFAULT '',
				status               TEXT    NOT NULL,
				started_at           TEXT    NOT NULL,
				finished_at          TEXT,
				created_at           TEXT    NOT NULL,
				UNIQUE(run_uid, stock_snapshot_id)
			)`,
			`CREATE INDEX idx_analysis_runs_snapshot ON analysis_runs(stock_snapshot_id, started_at)`,
			`CREATE INDEX idx_analysis_runs_uid ON analysis_runs(run_uid)`,
			`CREATE INDEX idx_analysis_runs_versions ON analysis_runs(prompt_set_version, mode)`,

			// ── agent_analysis ───────────────────────────────────────────────────────
			// UNIQUE(analysis_run_id, agent) is the structural ban on agent debate: within
			// one execution an agent gets one verdict and has nowhere to record a revision
			// after reading a peer's conclusion. A NEW analysis run may legitimately reach
			// a different answer — that is replay, not revision, and it lands in its own
			// immutable row.
			`CREATE TABLE agent_analysis (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				analysis_run_id INTEGER NOT NULL REFERENCES analysis_runs(id) ON DELETE CASCADE,
				agent           TEXT    NOT NULL,
				verdict         TEXT    NOT NULL DEFAULT '',
				score           REAL,
				confidence      REAL,
				reasoning       TEXT    NOT NULL DEFAULT '',
				model           TEXT    NOT NULL DEFAULT '',
				status          TEXT    NOT NULL,
				latency_ms      INTEGER,
				tokens          INTEGER,
				created_at      TEXT    NOT NULL,
				UNIQUE(analysis_run_id, agent)
			)`,
			`CREATE INDEX idx_agent_analysis_run ON agent_analysis(analysis_run_id)`,
			`CREATE INDEX idx_agent_analysis_agent ON agent_analysis(agent, verdict)`,

			// ── decisions ────────────────────────────────────────────────────────────
			// The two sources sit at different levels and the schema says so:
			//
			//   SCANNER   snapshot-level, analysis_run_id NULL, at most one per snapshot
			//   AI_JUDGE  run-level,      analysis_run_id set,  at most one per analysis run
			//
			// The CHECK makes a mislevelled row impossible to insert; the two PARTIAL unique
			// indexes give each source its own cardinality. So the scanner's BUY, run #1's
			// WATCH and run #2's BUY all coexist, and no AI replay has any path to the
			// scanner's row.
			//
			// The source literals below are mirrored by the Go constants in model.go;
			// TestDecisionSourceConstantsMatchSchema fails if they drift.
			`CREATE TABLE decisions (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				stock_snapshot_id INTEGER NOT NULL REFERENCES stock_snapshots(id) ON DELETE CASCADE,
				analysis_run_id   INTEGER REFERENCES analysis_runs(id) ON DELETE CASCADE,
				source            TEXT    NOT NULL,
				decision          TEXT    NOT NULL,
				confidence        REAL,
				reasoning         TEXT    NOT NULL DEFAULT '',
				supporting        TEXT    NOT NULL DEFAULT '',
				opposing          TEXT    NOT NULL DEFAULT '',
				model             TEXT    NOT NULL DEFAULT '',
				created_at        TEXT    NOT NULL,
				CHECK (
					(source = 'SCANNER'  AND analysis_run_id IS NULL) OR
					(source = 'AI_JUDGE' AND analysis_run_id IS NOT NULL)
				)
			)`,
			`CREATE UNIQUE INDEX idx_decisions_scanner_once
				ON decisions(stock_snapshot_id) WHERE source = 'SCANNER'`,
			`CREATE UNIQUE INDEX idx_decisions_judge_once
				ON decisions(analysis_run_id) WHERE source = 'AI_JUDGE'`,
			`CREATE INDEX idx_decisions_snapshot ON decisions(stock_snapshot_id)`,
			`CREATE INDEX idx_decisions_source ON decisions(source, decision)`,

			// ── outcomes ─────────────────────────────────────────────────────────────
			// One row per SNAPSHOT, not per decision: the forward return of a stock is the
			// same number whoever held an opinion about it, so storing it once removes any
			// way for two copies to drift. Joining through stock_snapshots gives every
			// decision and every agent verdict its outcome.
			`CREATE TABLE outcomes (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				stock_snapshot_id INTEGER NOT NULL UNIQUE REFERENCES stock_snapshots(id) ON DELETE CASCADE,
				base_close        REAL    NOT NULL,
				return_1d         REAL,
				return_3d         REAL,
				return_5d         REAL,
				return_10d        REAL,
				return_20d        REAL,
				max_gain_20d      REAL,
				max_drawdown_20d  REAL,
				bars_observed     INTEGER NOT NULL DEFAULT 0,
				first_computed_at TEXT    NOT NULL,
				last_updated_at   TEXT    NOT NULL
			)`,
		},
	},
	{
		version: 2,
		name:    "agent_output_json",
		stmts: []string{
			// R13-M3. `reasoning` holds one agent's prose; the R11 analyst also produces
			// three DISTINCT LISTS (bull case, bear case, risk flags) plus a summary.
			// Packing four structured values into the prose column would (a) void that
			// column's documented meaning, (b) leave them unreadable without a parsing
			// convention the schema does not state, and (c) make an agent writing genuine
			// prose indistinguishable from one writing packed JSON. So the structure gets
			// its own column.
			//
			// NOT NULL DEFAULT '' keeps every existing row valid and every existing INSERT
			// (which does not name this column) working unchanged — the migration is
			// backward-compatible by construction, and '' means "this agent produced no
			// structured output", which is different from "{}".
			`ALTER TABLE agent_analysis ADD COLUMN output_json TEXT NOT NULL DEFAULT ''`,
		},
	},
}

// migrate brings an open database up to SchemaVersion.
//
// Each migration runs in its own transaction together with the row that records it, so a
// half-applied migration is impossible: either the DDL and the version bump both land, or
// neither does.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	current, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > SchemaVersion {
		return fmt.Errorf("store: database schema is version %d but this build only knows %d "+
			"— refusing to write with an older binary", current, SchemaVersion)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migration %d (%s) statement %d: %w", m.version, m.name, i+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, nowUTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %d: %w", m.version, err)
	}
	return nil
}

// schemaVersion returns the highest applied migration version (0 for a fresh database).
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}
