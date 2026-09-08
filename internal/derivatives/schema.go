// Package derivatives is R15's TAIFEX derivatives risk layer.
//
// Default-off, shadow-only, and a leaf: nothing in internal/scanner or internal/candidate may
// import it, which `go list -deps ./internal/scanner` enforces rather than convention.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md. Every rule this file implements was written
// because a plausible alternative produced a wrong number against live exchange data, and the
// spec records the counterexample for each.
package derivatives

import "github.com/deep-huang/stock-scanner/internal/store"

// SchemaName is the R15 database's identity in migration errors.
const SchemaName = "r15_derivatives"

// FeatureVersion labels every computed value with the rule-set that produced it, so a stored
// figure stays interpretable after the rules move. Same discipline as valuation's
// QualityRuleVersion and SuitabilityRuleVersion.
const FeatureVersion = "R15-v1"

// Schema is R15's migration list.
//
// It is a SEPARATE schema on a SEPARATE file, not migration 3 on the R13 list, and the reason
// is a rollback property rather than tidiness: migrate() refuses a database whose recorded
// version exceeds its schema's, so raising the R13 ceiling would make every binary built
// without R15 refuse to open r13.db. A default-off research layer that can lock the existing
// research store is not default-off.
var Schema = store.Schema{Name: SchemaName, Migrations: []store.Migration{
	{
		Version: 1,
		Name:    "r15_foundation",
		Stmts: []string{
			// ── derivative_snapshots ────────────────────────────────────────────────
			//
			// APPEND-ONLY. Nothing in R15 ever UPDATEs a row here.
			//
			// A re-fetch whose content_hash matches inserts nothing; a re-fetch whose
			// content differs inserts revision_no+1 and leaves the earlier revision
			// exactly as written. That is what lets a point-in-time read answer "what did
			// we have on day A" — an in-place correction erases precisely that.
			//
			// content_hash is deliberately NOT part of the key. Putting it there would
			// insert a row whenever the response bytes differed in any way, including key
			// order, and re-running a fetch would grow the table forever.
			`CREATE TABLE derivative_snapshots (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version INTEGER NOT NULL,
				source         TEXT    NOT NULL,
				-- The Taipei TRADING date this observation describes.
				trading_date   TEXT    NOT NULL,
				-- DAY | AFTER_HOURS | COMBINED. 'COMBINED' for feeds with no session
				-- dimension. Never NULL: SQLite treats NULLs as distinct in a UNIQUE
				-- index, so a nullable key column silently disables the constraint for
				-- exactly the rows it covers.
				trading_session TEXT   NOT NULL,
				revision_no    INTEGER NOT NULL DEFAULT 0,
				-- OBSERVED when the feed carried its own trading date and it was
				-- validated; ASSIGNED when the feed carries none and the fetch's target
				-- session was used. Stored rather than inferred, because "we assumed this
				-- belonged to D" is exactly the claim that vanishes once it is only a
				-- convention.
				date_source    TEXT    NOT NULL,
				content_hash   TEXT    NOT NULL,
				payload        TEXT    NOT NULL,
				fetched_at     TEXT    NOT NULL,
				UNIQUE(source, trading_date, trading_session, revision_no)
			)`,
			`CREATE INDEX idx_deriv_snapshots_asof
				ON derivative_snapshots(source, trading_date, trading_session, fetched_at)`,

			// ── derivative_contracts ────────────────────────────────────────────────
			//
			// Where the exchange's three naming conventions are reconciled. The same TXO
			// weekly is TXO/202609F1 in the by-strike report, TXU/202609 with the suffix
			// in ContractName in the settlement feed, and a Chinese product name in the
			// institutional feed. A join keyed on (contract, expiry) silently matches
			// nothing across them.
			//
			// expiry_kind comes from the CODE, never from the settlement date: TAIFEX
			// defers a settlement to the next trading day when the third Wednesday is a
			// holiday, so 202602 settled on a Monday while remaining a monthly. The
			// settlement_date is stored as data and used for expiry-distance features.
			`CREATE TABLE derivative_contracts (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version  INTEGER NOT NULL,
				source          TEXT    NOT NULL,
				product         TEXT    NOT NULL,
				expiry_code     TEXT    NOT NULL,
				-- MONTHLY | WEEKLY | COMBINATION | UNRESOLVED
				expiry_kind     TEXT    NOT NULL,
				settlement_date TEXT,
				alt_product     TEXT    NOT NULL DEFAULT '',
				alt_expiry_code TEXT    NOT NULL DEFAULT '',
				contract_name   TEXT    NOT NULL DEFAULT '',
				fetched_at      TEXT    NOT NULL,
				UNIQUE(product, expiry_code)
			)`,

			// ── institutional_derivatives ───────────────────────────────────────────
			//
			// value_semantics is IN the key. One row cannot be both FLOW (what was traded)
			// and POSITION (what is held): 外資 can sell 3,000 lots on a day their net long
			// position RISES, because the sale closed shorts. Leaving the column out of the
			// key makes the two overwrite each other and the distinction unrecoverable.
			//
			// call_put is '' for futures rather than NULL, for the UNIQUE reason above.
			// side is LONG | SHORT | NET, and NET is taken from the feed's own net column
			// rather than derived from long - short, so a column-layout change surfaces as
			// a mismatch instead of being papered over by arithmetic.
			`CREATE TABLE institutional_derivatives (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version  INTEGER NOT NULL,
				snapshot_id     INTEGER NOT NULL REFERENCES derivative_snapshots(id) ON DELETE CASCADE,
				institution     TEXT    NOT NULL,
				product         TEXT    NOT NULL,
				call_put        TEXT    NOT NULL DEFAULT '',
				-- FLOW | POSITION
				value_semantics TEXT    NOT NULL,
				-- LONG | SHORT | NET
				side            TEXT    NOT NULL,
				lots            REAL,
				value_thousands REAL,
				source          TEXT    NOT NULL,
				fetched_at      TEXT    NOT NULL,
				UNIQUE(snapshot_id, institution, product, call_put, value_semantics, side)
			)`,

			// ── options_aggregate ───────────────────────────────────────────────────
			//
			// Volume and OI are aggregated by DIFFERENT rules, verified against the
			// official PutCallRatio: volume sums every TXO row across both sessions and
			// all expiries; OI sums every row EXCEPT the expiry settling on that session,
			// whose lots are still printed but are no longer live exposure.
			//
			// reconciled records whether the computed figures matched the exchange's own.
			// An unchecked number that happens to be right is indistinguishable from one
			// that happens to be wrong, so a failed or unavailable check is a status, not
			// a silence.
			`CREATE TABLE options_aggregate (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version  INTEGER NOT NULL,
				snapshot_id     INTEGER NOT NULL REFERENCES derivative_snapshots(id) ON DELETE CASCADE,
				product         TEXT    NOT NULL,
				-- ALL | WEEKLY | MONTHLY
				expiry_kind     TEXT    NOT NULL,
				put_volume      REAL,
				call_volume     REAL,
				put_oi          REAL,
				call_oi         REAL,
				-- AVAILABLE | PARTIAL | MISSING | STALE | ERROR | NOT_AVAILABLE | NO_SESSION
				status          TEXT    NOT NULL,
				reason          TEXT    NOT NULL DEFAULT '',
				-- MATCHED | MISMATCH | UNCHECKED
				reconciled      TEXT    NOT NULL DEFAULT 'UNCHECKED',
				source          TEXT    NOT NULL,
				fetched_at      TEXT    NOT NULL,
				UNIQUE(snapshot_id, product, expiry_kind)
			)`,

			// ── options_oi_by_strike ────────────────────────────────────────────────
			//
			// oi and volume are NULLABLE and that is the point: the feed sends "-" for a
			// value it does not carry and "0" for an observed zero, and 1,295 TXO rows
			// carry a real 0 while 2,868 carry "-". Storing both as 0 would erase the
			// distinction the whole layer rests on.
			`CREATE TABLE options_oi_by_strike (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version INTEGER NOT NULL,
				snapshot_id    INTEGER NOT NULL REFERENCES derivative_snapshots(id) ON DELETE CASCADE,
				product        TEXT    NOT NULL,
				expiry_code    TEXT    NOT NULL,
				strike         REAL    NOT NULL,
				-- CALL | PUT, normalised from 買權/賣權 and from the CALL/PUT the
				-- institutional call/put feed uses. An unrecognised value is a parse
				-- error, never a third category: a mis-mapped side inverts a PCR without
				-- changing its magnitude, which no reconciliation can see.
				call_put       TEXT    NOT NULL,
				oi             REAL,
				volume         REAL,
				settlement     REAL,
				source         TEXT    NOT NULL,
				fetched_at     TEXT    NOT NULL,
				UNIQUE(snapshot_id, product, expiry_code, strike, call_put)
			)`,

			// ── margin_rates ────────────────────────────────────────────────────────
			//
			// snapshot_id is required, not decorative: the carry-forward walks
			// effective_date INSIDE the snapshot archived for D, and never walks
			// trading_date across snapshots. A failed fetch for D therefore means no
			// snapshot for D, which means margin_source: NONE — the exchange's "applies
			// until changed" licenses reusing a value observed FOR D, not reusing an
			// observation never made.
			`CREATE TABLE margin_rates (
				id                 INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version     INTEGER NOT NULL,
				snapshot_id        INTEGER NOT NULL REFERENCES derivative_snapshots(id) ON DELETE CASCADE,
				product            TEXT    NOT NULL,
				effective_date     TEXT    NOT NULL,
				clearing_margin    REAL,
				maintenance_margin REAL,
				initial_margin     REAL,
				source             TEXT    NOT NULL,
				fetched_at         TEXT    NOT NULL,
				UNIQUE(snapshot_id, product, effective_date)
			)`,

			// ── iv_observations ─────────────────────────────────────────────────────
			//
			// The table exists while the data does not. TAIFEX's 135-endpoint catalogue
			// carries no implied-volatility dataset, so M8 reports NOT_AVAILABLE with that
			// reason and blocks nothing; a future provider slots in behind the interface
			// without a migration.
			`CREATE TABLE iv_observations (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version INTEGER NOT NULL,
				snapshot_id    INTEGER NOT NULL REFERENCES derivative_snapshots(id) ON DELETE CASCADE,
				product        TEXT    NOT NULL,
				expiry_code    TEXT    NOT NULL,
				atm_strike     REAL,
				iv             REAL,
				status         TEXT    NOT NULL,
				reason         TEXT    NOT NULL DEFAULT '',
				source         TEXT    NOT NULL,
				fetched_at     TEXT    NOT NULL,
				UNIQUE(snapshot_id, product, expiry_code)
			)`,

			// ── derivative_features ─────────────────────────────────────────────────
			//
			// No `source` or `fetched_at` here, and that is deliberate rather than an
			// omission (§7.2): these rows are COMPUTED, not fetched. An endpoint name would
			// be a lie about where the number came from and a retrieval time would record a
			// fetch that never happened. input_snapshot_ids plus feature_version are what
			// make a derived row re-derivable — the property source provides for an
			// observation.
			//
			// input_snapshot_ids records what a computed value was derived from, so a
			// figure can be re-derived rather than merely re-read. value is nullable for
			// the same reason as everywhere else: a feature that could not be computed is
			// absent with a status, not zero.
			`CREATE TABLE derivative_features (
				id                 INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version     INTEGER NOT NULL,
				trading_date       TEXT    NOT NULL,
				feature_version    TEXT    NOT NULL,
				name               TEXT    NOT NULL,
				product            TEXT    NOT NULL DEFAULT '',
				value              REAL,
				unit               TEXT    NOT NULL DEFAULT '',
				-- FLOW | POSITION | CHANGE | RATIO | LEVEL
				value_semantics    TEXT    NOT NULL DEFAULT '',
				status             TEXT    NOT NULL,
				reason             TEXT    NOT NULL DEFAULT '',
				input_snapshot_ids TEXT    NOT NULL DEFAULT '',
				computed_at        TEXT    NOT NULL,
				UNIQUE(trading_date, feature_version, name, product)
			)`,

			// ── derivative_data_health ──────────────────────────────────────────────
			`CREATE TABLE derivative_data_health (
				id                 INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version     INTEGER NOT NULL,
				trading_date       TEXT    NOT NULL,
				source             TEXT    NOT NULL,
				status             TEXT    NOT NULL,
				as_of              TEXT    NOT NULL DEFAULT '',
				fetched_at         TEXT    NOT NULL DEFAULT '',
				expected_freshness TEXT    NOT NULL DEFAULT '',
				reason             TEXT    NOT NULL DEFAULT '',
				recorded_at        TEXT    NOT NULL,
				UNIQUE(trading_date, source)
			)`,

			// ── strategy_evaluations ────────────────────────────────────────────────
			//
			// validation_status is constrained to the lifecycle, and ACTIVE is deliberately
			// absent from v1's reachable set — no R15 code path may set it. A retired
			// strategy keeps its row and its reason: a research store that quietly loses
			// its failures reports only survivors, which is what makes every strategy look
			// good.
			`CREATE TABLE strategy_evaluations (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version    INTEGER NOT NULL,
				strategy_id       TEXT    NOT NULL,
				strategy_version  TEXT    NOT NULL,
				feature_version   TEXT    NOT NULL,
				signal_time       TEXT    NOT NULL,
				as_of             TEXT    NOT NULL,
				expected_horizon  TEXT    NOT NULL DEFAULT '',
				entry_reference   REAL,
				prediction        TEXT    NOT NULL DEFAULT '',
				confidence        TEXT    NOT NULL DEFAULT '',
				sample_size       INTEGER NOT NULL DEFAULT 0,
				validation_status TEXT    NOT NULL,
				retirement_reason TEXT    NOT NULL DEFAULT '',
				created_at        TEXT    NOT NULL,
				UNIQUE(strategy_id, strategy_version, signal_time)
			)`,
			`CREATE INDEX idx_strategy_eval_status
				ON strategy_evaluations(validation_status, strategy_id)`,

			// ── derivative_outcomes ─────────────────────────────────────────────────
			//
			// Every return column is nullable. A horizon that has not elapsed is absent,
			// not zero — R14 spent a milestone on the equivalent confusion, and a 0 here
			// would enter an average as a real observation of "no move".
			`CREATE TABLE derivative_outcomes (
				id                    INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version        INTEGER NOT NULL,
				strategy_evaluation_id INTEGER NOT NULL UNIQUE
					REFERENCES strategy_evaluations(id) ON DELETE CASCADE,
				base_reference        REAL,
				outcome_1d            REAL,
				outcome_3d            REAL,
				outcome_5d            REAL,
				outcome_10d           REAL,
				outcome_20d           REAL,
				mfe                   REAL,
				mae                   REAL,
				bars_observed         INTEGER NOT NULL DEFAULT 0,
				first_computed_at     TEXT    NOT NULL,
				last_updated_at       TEXT    NOT NULL
			)`,
		},
	},
	{
		Version: 2,
		Name:    "data_health_acquisition_keys",
		Stmts: []string{
			// M3. derivative_data_health as M2 declared it is keyed (trading_date, source)
			// and cannot express what an acquisition run actually produces.
			//
			// Two facts force the change, and neither is a preference:
			//
			//   - DailyMarketReportOpt is stored as TWO snapshots for one session date
			//     (§10.2b), so its acquisition has two outcomes — the DAY rows can be
			//     complete while the AFTER_HOURS rows are rejected. Without `session` in
			//     the key the second overwrites the first and one of the two facts is
			//     lost, which is the same collision the split exists to prevent one table
			//     down.
			//   - §6.2 makes PARTIAL a COUNTED state: "valid rows are stored, rejected rows
			//     are COUNTED". A status with no counts beside it cannot tell an aggregate
			//     whether it may be published, so row_count / rejected_row_count are
			//     columns rather than prose inside `reason`.
			//
			// It is a NEW forward migration rather than an edit to migration 1. Migration
			// 1 is what an already-created r15_derivatives.db has applied; editing it in
			// place would leave that file claiming version 1 with a different shape than
			// this build believes version 1 to be — the exact drift the append-only
			// discipline exists to make impossible.
			//
			// SQLite cannot add a column to a UNIQUE constraint, so the table is rebuilt
			// and the existing rows are carried across: dataset defaults to the old
			// `source`, session to COMBINED (M2's own sentinel for "no session
			// dimension"), and the counts to 0 — which is honest, because no writer
			// existed to produce them.
			`ALTER TABLE derivative_data_health RENAME TO derivative_data_health_m2`,
			// expected_availability replaces M2's expected_freshness. They are different
			// questions: freshness asks "how old may this be before it is STALE", while
			// availability asks "by when should the exchange have published it" — which is
			// the only thing that separates NOT_PUBLISHED from MISSING (§6). A 15:00 run
			// legitimately sees the by-strike report and not the institutional positions.
			`CREATE TABLE derivative_data_health (
				id                    INTEGER PRIMARY KEY AUTOINCREMENT,
				schema_version        INTEGER NOT NULL,
				-- The logical dataset: options_by_strike, institutional_futures, margin.
				-- Stable across an endpoint rename, which the source column is not.
				dataset               TEXT    NOT NULL,
				trading_date          TEXT    NOT NULL,
				-- DAY | AFTER_HOURS | COMBINED, the same vocabulary and the same sentinel
				-- as derivative_snapshots. Never NULL, for the UNIQUE-index reason.
				session               TEXT    NOT NULL,
				-- The endpoint the bytes came from, verbatim.
				source                TEXT    NOT NULL,
				-- AVAILABLE | PARTIAL | MISSING | STALE | ERROR | NOT_AVAILABLE |
				-- NO_SESSION | NOT_PUBLISHED (§6).
				status                TEXT    NOT NULL,
				as_of                 TEXT    NOT NULL DEFAULT '',
				fetched_at            TEXT    NOT NULL DEFAULT '',
				expected_availability TEXT    NOT NULL DEFAULT '',
				-- Rows STORED and rows REJECTED (§6.2). Both are counts of a completed
				-- attempt, so 0/0 next to ERROR means "nothing was stored", not "an empty
				-- session".
				row_count             INTEGER NOT NULL DEFAULT 0,
				rejected_row_count    INTEGER NOT NULL DEFAULT 0,
				reason                TEXT    NOT NULL DEFAULT '',
				recorded_at           TEXT    NOT NULL,
				UNIQUE(dataset, trading_date, session)
			)`,
			`INSERT INTO derivative_data_health
				(schema_version, dataset, trading_date, session, source, status, as_of,
				 fetched_at, expected_availability, row_count, rejected_row_count, reason,
				 recorded_at)
			 SELECT schema_version, source, trading_date, 'COMBINED', source, status, as_of,
			        fetched_at, expected_freshness, 0, 0, reason, recorded_at
			 FROM derivative_data_health_m2`,
			`DROP TABLE derivative_data_health_m2`,
		},
	},
}}
