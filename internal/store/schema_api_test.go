package store

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The Schema API exists so one binary can run two databases with independent migration
// sequences (R15 spec §7.1). These tests pin the two properties that make that safe.
//
// The alternative — adding R15's tables to R13Schema — raises the R13 ceiling, and
// migrate() then refuses r13.db from any binary built without R15. A default-off research
// layer able to lock the existing research store on rollback is not default-off at all.

// ── the R13 database must be untouched by the refactor ────────────────────────────────

// objectNames returns every table and index sqlite knows about, sorted.
//
// Names rather than full DDL text: re-indenting a CREATE statement changes sqlite_master's
// text without changing the database, so comparing text would fail on a whitespace edit while
// a dropped index would look identical. A missing or extra object is the real regression.
func objectNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT type || ' ' || name FROM sqlite_master
		 WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// The exact set of objects migration 1+2 produce. Written out rather than computed, so a
// migration that silently stops creating something fails here instead of agreeing with itself.
var r13Objects = []string{
	"index idx_agent_analysis_agent",
	"index idx_agent_analysis_run",
	"index idx_analysis_runs_snapshot",
	"index idx_analysis_runs_uid",
	"index idx_analysis_runs_versions",
	"index idx_decisions_judge_once",
	"index idx_decisions_scanner_once",
	"index idx_decisions_snapshot",
	"index idx_decisions_source",
	"index idx_evidence_lookup",
	"index idx_evidence_snapshot",
	"index idx_scan_runs_trading_date",
	"index idx_stock_snapshots_run",
	"index idx_stock_snapshots_symbol_date",
	"table agent_analysis",
	"table analysis_runs",
	"table decisions",
	"table evidence",
	"table outcomes",
	"table scan_runs",
	"table schema_migrations",
	"table stock_snapshots",
}

func TestR13SchemaIsUnchangedByTheSchemaAPI(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	got := objectNames(t, s)
	if strings.Join(got, "\n") != strings.Join(r13Objects, "\n") {
		t.Fatalf("the R13 database changed shape.\n got: %v\nwant: %v", got, r13Objects)
	}
}

// The zero Config must still mean R13 — every call site written before Schema existed passes
// one, and none of them were changed.
func TestZeroConfigStillSelectsR13(t *testing.T) {
	got := Config{}.Defaulted().Schema
	if got.Name != R13Schema.Name || got.Version() != R13Schema.Version() {
		t.Fatalf("zero Config resolved to schema %q v%d, want %q v%d",
			got.Name, got.Version(), R13Schema.Name, R13Schema.Version())
	}
}

// SchemaVersion is derived from the list now. If someone appends a migration without
// intending to move the ceiling, that is exactly what they have done.
func TestSchemaVersionTracksTheList(t *testing.T) {
	if SchemaVersion != R13Schema.Version() {
		t.Fatalf("SchemaVersion = %d, R13Schema.Version() = %d — they must not drift",
			SchemaVersion, R13Schema.Version())
	}
	if SchemaVersion != 2 {
		t.Fatalf("SchemaVersion = %d, want 2 — R15 must not raise the R13 ceiling", SchemaVersion)
	}
}

// Version is a MAX, not len() or the last element: an out-of-order or duplicated entry must
// not quietly lower the ceiling and let a migrated database be re-opened as if it were older.
func TestSchemaVersionIsAMax(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Schema
		want int
	}{
		{"ordered", Schema{Migrations: []Migration{{Version: 1}, {Version: 2}}}, 2},
		{"out of order", Schema{Migrations: []Migration{{Version: 3}, {Version: 1}}}, 3},
		{"duplicated", Schema{Migrations: []Migration{{Version: 1}, {Version: 1}}}, 1},
		{"empty", Schema{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Version(); got != tc.want {
				t.Fatalf("Version() = %d, want %d", got, tc.want)
			}
		})
	}
}

// ── the ceiling is per-schema, which is the whole point ───────────────────────────────

// A second schema with its own sequence migrates its own file, and doing so leaves the R13
// ceiling exactly where it was. Before this API the ceiling came from a package constant, so
// this could not be expressed at all.
func TestTwoSchemasMigrateIndependently(t *testing.T) {
	other := Schema{Name: "r15_test", Migrations: []Migration{
		{Version: 1, Name: "one", Stmts: []string{`CREATE TABLE alpha (id INTEGER PRIMARY KEY)`}},
		{Version: 2, Name: "two", Stmts: []string{`CREATE TABLE beta (id INTEGER PRIMARY KEY)`}},
		{Version: 3, Name: "three", Stmts: []string{`CREATE TABLE gamma (id INTEGER PRIMARY KEY)`}},
	}}

	dir := t.TempDir()
	o, err := Open(Config{Path: filepath.Join(dir, "other.db"), Schema: other})
	if err != nil {
		t.Fatalf("open other schema: %v", err)
	}
	defer o.Close()

	v, err := o.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 3 {
		t.Fatalf("other schema migrated to %d, want 3", v)
	}
	if got := objectNames(t, o); len(got) != 4 { // alpha, beta, gamma, schema_migrations
		t.Fatalf("other database has %v", got)
	}

	// And the R13 database is still at 2 — the property the whole design exists for.
	r13, err := Open(Config{Path: filepath.Join(dir, "r13.db")})
	if err != nil {
		t.Fatalf("open r13: %v", err)
	}
	defer r13.Close()
	rv, err := r13.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rv != 2 {
		t.Fatalf("r13 migrated to %d, want 2 — a second schema moved the R13 ceiling", rv)
	}
}

// The downgrade guard must judge a database against ITS OWN schema. Judging every database by
// the R13 constant is the bug this replaced: the day R15's list reached version 3, R15's own
// code would have refused R15's own file.
func TestDowngradeGuardIsPerSchema(t *testing.T) {
	full := Schema{Name: "full", Migrations: []Migration{
		{Version: 1, Name: "one", Stmts: []string{`CREATE TABLE alpha (id INTEGER PRIMARY KEY)`}},
		{Version: 2, Name: "two", Stmts: []string{`CREATE TABLE beta (id INTEGER PRIMARY KEY)`}},
	}}
	older := Schema{Name: "older", Migrations: full.Migrations[:1]}

	path := filepath.Join(t.TempDir(), "x.db")
	s, err := Open(Config{Path: path, Schema: full})
	if err != nil {
		t.Fatalf("open full: %v", err)
	}
	_ = s.Close()

	if _, err := Open(Config{Path: path, Schema: older}); err == nil {
		t.Fatal("a database migrated past its schema's version was opened anyway")
	}
}
