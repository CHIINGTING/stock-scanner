package macro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Point-in-time archive, following the same shape as the fundamental and valuation layers.
//
//	data/macro/<archive-date>/macro.json
//
// One file per capture, never overwritten across days. The sources do publish history, so a
// backfill is technically possible — but a snapshot dated 2026-05-01 that was assembled today
// would assert that today's revised CPI was knowable in May, which is precisely the
// look-ahead the dated directory exists to prevent.

const defaultDir = "data/macro"

// DefaultDir is the archive root.
func DefaultDir() string { return defaultDir }

// SnapshotPath is <dir>/<archive date>/macro.json.
func SnapshotPath(dir, archiveDate string) string {
	if dir == "" {
		dir = defaultDir
	}
	return filepath.Join(dir, archiveDate, "macro.json")
}

// SaveSnapshot writes one capture atomically: temp file then rename, so a reader never sees
// a half-written JSON and a crash leaves the previous state intact.
func SaveSnapshot(dir string, s *Snapshot) (string, error) {
	if s == nil || s.ArchiveDate == "" {
		return "", fmt.Errorf("macro: refusing to save a snapshot with no archive date")
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	path := SnapshotPath(dir, s.ArchiveDate)
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

// LoadAsOf returns the most recent snapshot archived ON OR BEFORE asOfDate.
//
// The point-in-time guarantee. It only LOADS — a historical run must never fetch, because a
// live call returns today's readings and would file them under a past date. A date with
// nothing archived yields (nil, nil), and the caller reports the macro context unavailable.
func LoadAsOf(dir, asOfDate string) (*Snapshot, error) {
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
		if _, err := time.Parse("2006-01-02", e.Name()); err != nil {
			continue
		}
		if asOfDate == "" || e.Name() <= asOfDate {
			dates = append(dates, e.Name())
		}
	}
	if len(dates) == 0 {
		return nil, nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	for _, d := range dates {
		b, err := os.ReadFile(SnapshotPath(dir, d))
		if err != nil {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			// A corrupt file is an ERROR, never an empty snapshot: zeroed macro data would
			// report a zero Fed funds rate, which is a policy statement rather than a gap.
			return nil, fmt.Errorf("macro: %s snapshot is unreadable: %w", d, err)
		}
		return &s, nil
	}
	return nil, nil
}

// Service orchestrates the providers and the archive.
type Service struct {
	Provider *Provider
	Dir      string
	Logf     func(string, ...any)
}

// NewService wires a provider to an archive directory.
func NewService(p *Provider, dir string, logf func(string, ...any)) *Service {
	if p == nil {
		p = NewProvider(logf)
	}
	if dir == "" {
		dir = defaultDir
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{Provider: p, Dir: dir, Logf: logf}
}

// FetchAndStore assembles one snapshot from all four sources and archives it.
//
// A failure in one source does not cost the others. They are independent agencies publishing
// unrelated series on different schedules; losing the Fed's rate because CBOE timed out would
// be a self-inflicted outage. The resulting snapshot is PARTIAL, and says which half is
// missing.
func (s *Service) FetchAndStore(ctx context.Context, now time.Time) (*Snapshot, error) {
	snap := &Snapshot{
		SchemaVersion: SchemaVersion,
		ArchiveDate:   now.Format("2006-01-02"),
		ObservedAt:    now,
		// Investigated and unusable — see the package doc.
		PCEStatus:          NotImplemented,
		FOMCCalendarStatus: NotImplemented,
	}

	// A year and a bit of daily readings. The direction of the last policy move is only
	// visible if the window reaches back past it, and the Committee can hold a range for
	// many months — ninety days was not enough to see the previous one.
	if fed, err := s.Provider.FetchFed(ctx, 400); err != nil {
		s.Logf("macro: Fed: %v", err)
		snap.Fed = fed
	} else {
		snap.Fed = fed
	}

	if t, err := s.Provider.FetchTreasury(ctx, now.Year()); err != nil {
		s.Logf("macro: Treasury: %v", err)
		snap.Treasury = t
	} else {
		snap.Treasury = t
	}

	// One request for four series; see FetchBLS. Two years of history so a year-on-year
	// comparison has a base to reach for.
	series, err := s.Provider.FetchBLS(ctx, now.Year()-2, now.Year())
	if err != nil {
		s.Logf("macro: BLS: %v", err)
		snap.Inflation = InflationSnapshot{Status: Unavailable, Reason: err.Error()}
		snap.Labor = LaborSnapshot{Status: Unavailable, Reason: err.Error()}
	} else {
		snap.Inflation = BuildInflation(series[seriesCPI], series[seriesCoreCPI])
		snap.Labor = BuildLabor(series[seriesUnemployment], series[seriesPayrolls])
	}

	if v, err := s.Provider.FetchVIX(ctx); err != nil {
		s.Logf("macro: VIX: %v", err)
		snap.Volatility = v
	} else {
		snap.Volatility = v
	}

	snap.Status = AggregateStatus(snap.Fed.Status, snap.Treasury.Status,
		snap.Inflation.Status, snap.Labor.Status, snap.Volatility.Status)

	if _, err := SaveSnapshot(s.Dir, snap); err != nil {
		return snap, fmt.Errorf("macro: archive: %w", err)
	}
	return snap, nil
}
