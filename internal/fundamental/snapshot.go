package fundamental

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Snapshot persistence, following the layout the other market layers use.
//
//	data/fundamental/<observed-date>/<code>_<market>.json
//
// Dated directories are the point. The MOPS endpoints only ever serve the current period, so
// an archive that overwrote one file per stock would hold today's data and no history — and
// point-in-time research would be impossible by construction. One directory per observation
// date makes "what did we know on 2026-08-20" a directory listing.

const defaultDir = "data/fundamental"

// DefaultDir is the archive root.
func DefaultDir() string { return defaultDir }

// SnapshotPath is <dir>/<observed date>/<code>_<market>.json.
//
// Keyed by the canonical (code, market) pair, never by company name: names change, are not
// unique across markets, and cannot be typed reliably.
func SnapshotPath(dir, observedDate, code, market string) string {
	if dir == "" {
		dir = defaultDir
	}
	return filepath.Join(dir, observedDate, code+"_"+market+".json")
}

// SaveSnapshot writes one stock's snapshot atomically.
//
// Temp file plus rename, matching the other snapshot stores: a reader must never observe a
// half-written JSON, and a crash mid-write must leave the previous state intact rather than
// a file that parses as "no data".
func SaveSnapshot(dir string, s *Snapshot) (string, error) {
	if s == nil || s.Symbol == "" {
		return "", fmt.Errorf("fundamental: refusing to save a snapshot with no symbol")
	}
	code, market, err := fetcher.ParseYahooSymbol(s.Symbol)
	if err != nil {
		return "", fmt.Errorf("fundamental: %w", err)
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	path := SnapshotPath(dir, s.ObservedAt.Format("2006-01-02"), code, market)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// LoadSnapshot reads one stock's snapshot for a specific observation date.
func LoadSnapshot(dir, observedDate, symbol string) (*Snapshot, error) {
	code, market, err := fetcher.ParseYahooSymbol(symbol)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(SnapshotPath(dir, observedDate, code, market))
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		// A corrupt file is an ERROR, never an empty snapshot. Returning zeroed fundamentals
		// here would present "the file is damaged" as "the company earned nothing".
		return nil, fmt.Errorf("fundamental: %s snapshot for %s is unreadable: %w",
			observedDate, symbol, err)
	}
	if s.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("fundamental: %s snapshot for %s is schema v%d, this build "+
			"understands v%d", observedDate, symbol, s.SchemaVersion, SchemaVersion)
	}
	return &s, nil
}

// LoadAsOf returns the most recent snapshot observed ON OR BEFORE asOfDate.
//
// This is the point-in-time guarantee, and it is a deliberate choice of the LATEST PAST
// observation rather than the newest file on disk. A research run dated 2026-05-01 must see
// what was knowable on 2026-05-01 — reading a snapshot captured in August would hand it
// three months of hindsight, and every backtest built on that would be measuring the future.
//
// It only LOADS. A date with no archived snapshot yields (nil, nil): the caller reports the
// fundamentals unavailable rather than reaching for the network, because a live fetch during
// a historical run returns TODAY's figures and labels them with a past date.
func LoadAsOf(dir, asOfDate, symbol string) (*Snapshot, error) {
	if dir == "" {
		dir = defaultDir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Only well-formed observation dates, and only those at or before the cutoff.
		if _, err := time.Parse("2006-01-02", name); err != nil {
			continue
		}
		if name <= asOfDate {
			dates = append(dates, name)
		}
	}
	if len(dates) == 0 {
		return nil, nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	for _, d := range dates {
		s, err := LoadSnapshot(dir, d, symbol)
		if err != nil {
			if os.IsNotExist(err) {
				continue // this date has no file for this stock; try the one before it
			}
			return nil, err
		}
		return s, nil
	}
	return nil, nil
}

// VisibleAsOf reports whether a record had been PUBLISHED by asOfDate.
//
// The second half of the point-in-time contract. Choosing an old snapshot is not enough: a
// snapshot captured on 2026-08-31 contains a statement published on 2026-08-31, and a run
// dated 2026-08-20 must not see it even though the file is "old enough". Publication is the
// authority, and it comes from the source's own 出表日期 — never from when we fetched it.
func VisibleAsOf(publishedAt time.Time, asOfDate string) bool {
	if publishedAt.IsZero() {
		// No usable publication date means the record cannot be placed in time, so it is not
		// safe for point-in-time use. Guessing would silently reintroduce look-ahead.
		return false
	}
	return publishedAt.In(taipei()).Format("2006-01-02") <= strings.TrimSpace(asOfDate)
}
