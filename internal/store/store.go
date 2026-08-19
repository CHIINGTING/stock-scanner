package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"; see Open for why
)

// timeLayout is how every timestamp is stored: RFC3339 with a numeric zone, always UTC.
// Text keeps the file readable with the sqlite3 CLI, which matters for an audit store.
const timeLayout = time.RFC3339

// DefaultPath is where the R13 database lives when the config does not say otherwise. It
// sits under data/ next to the other derived data products, not under reports/ (which stays
// presentation-only) and not under .cache/ (which is disposable — this is not).
const DefaultPath = "data/research/r13.db"

// ErrNotFound is returned by lookups that found no row. Callers distinguish "no such run"
// from a real failure without string-matching driver errors.
var ErrNotFound = errors.New("store: not found")

// nowUTC is the clock, indirected so tests can pin time without touching every call site.
var nowUTC = func() time.Time { return time.Now().UTC() }

// Config is the R13 store block of the scanner config.
type Config struct {
	// Path is the SQLite file. Empty → DefaultPath. ":memory:" is accepted for tests.
	Path string `yaml:"path"`
	// BusyTimeoutMs bounds how long a statement waits for a lock. Zero → 5000.
	BusyTimeoutMs int `yaml:"busy_timeout_ms"`
}

// Defaulted fills zero values, matching the Defaulted() convention used by the ai, market
// and institution configs in this repo.
func (c Config) Defaulted() Config {
	if c.Path == "" {
		c.Path = DefaultPath
	}
	if c.BusyTimeoutMs <= 0 {
		c.BusyTimeoutMs = 5000
	}
	return c
}

// Store is the handle every R13 repository hangs off.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database and migrates it to SchemaVersion.
//
// The driver is modernc.org/sqlite: pure Go, so `go build` needs no C toolchain and
// CGO_ENABLED=0 still works. That matters more here than raw speed — this repo has never
// required cgo, and an audit store is not on any hot path.
//
// MaxOpenConns is pinned to 1 deliberately. The scanner is a batch process, and a single
// connection substantially avoids INTRA-process write contention at no real cost. It is not
// a guarantee against SQLITE_BUSY: another process — or another Store instance in this one —
// opening the same file can still take a lock, which is what busy_timeout and WAL are for.
//
// The one rule this imposes on callers: never start a second query while rows from a first
// are still open on the same goroutine. Every read method in this package therefore
// materialises its rows fully before returning.
func Open(cfg Config) (*Store, error) {
	return OpenContext(context.Background(), cfg)
}

// OpenContext is Open with a caller-supplied context for the migration step.
func OpenContext(ctx context.Context, cfg Config) (*Store, error) {
	cfg = cfg.Defaulted()

	if !isMemoryPath(cfg.Path) {
		if dir := filepath.Dir(cfg.Path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: create %s: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", dsn(cfg))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", cfg.Path, err)
	}
	// See the doc comment: one connection, by design.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", cfg.Path, err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: cfg.Path}, nil
}

// dsn builds the connection string. The pragmas are set through the DSN rather than with
// PRAGMA statements so they apply to every connection the pool ever opens — a pragma run
// once on one connection is a classic source of foreign keys silently being off.
//
//   - foreign_keys(1): the ON DELETE CASCADE relationships are load-bearing here; SQLite
//     ignores them unless this is on.
//   - journal_mode(WAL): readers do not block the writer, so a report can read the store
//     while a scan is still writing to it.
//   - busy_timeout: wait rather than fail if something else holds the file.
//   - synchronous(NORMAL): the safe pairing with WAL. This is derived data — a machine
//     crash losing the last transaction is acceptable; corruption is not.
func dsn(cfg Config) string {
	if isMemoryPath(cfg.Path) {
		return cfg.Path
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", cfg.Defaulted().BusyTimeoutMs))
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + cfg.Path + "?" + q.Encode()
}

// isMemoryPath reports whether the path asks for an in-memory database. Those get no
// directory creation and no WAL (there is no file to journal).
func isMemoryPath(p string) bool {
	return p == ":memory:" || p == "file::memory:"
}

// Close releases the database. It is safe on a nil Store so callers can defer it next to a
// failed Open without a nil check.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path is the file this store is backed by, for logs and reports.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// SchemaVersion reports the migration level of the open database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return schemaVersion(ctx, s.db)
}

// WithTx runs fn inside a transaction, committing on success and rolling back on any error
// or panic. Callers MUST use the supplied *sql.Tx for every statement inside fn: with a
// single pooled connection, reaching for the Store instead would wait forever on a
// connection the transaction is already holding.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ── scanning helpers ──────────────────────────────────────────────────────────────────
//
// Optional numbers cross the SQL boundary as sql.Null* and come back as pointers, so a NULL
// column stays nil instead of collapsing into 0.

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// parseTime reads a stored timestamp. A malformed value is an error rather than a zero
// time: in an audit store, a silently zeroed timestamp is worse than a loud failure.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func parseTimePtr(n sql.NullString) (*time.Time, error) {
	if !n.Valid || n.String == "" {
		return nil, nil
	}
	t, err := parseTime(n.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// formatTime renders a timestamp for storage, always in UTC.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }
