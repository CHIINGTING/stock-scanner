package news

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SnapshotPath is reports/news_YYYYMMDD_HHMMSS.json — one file PER RUN, keyed on the
// wall-clock observation time. Intraday reruns (08:30, 09:30, …) each get their own
// file so an earlier observed state is never overwritten (validation correctness).
func SnapshotPath(dir string, observedAt time.Time) string {
	return filepath.Join(dir, "news_"+observedAt.Format("20060102_150405")+".json")
}

// snapshotFile is the on-disk envelope. ObservedAt is the wall-clock fetch time; each
// signal also carries PublishedAt (source time) and ObservedAt so a later validator can
// join by code+date and only use a signal from ObservedAt onward (no look-ahead bias).
type snapshotFile struct {
	Date       string       `json:"date"`
	ObservedAt time.Time    `json:"observed_at"`
	Signals    []NewsSignal `json:"signals"`
}

// SaveSnapshot writes the run's signals to a per-run timestamped file and returns its
// path. If a file for the exact second already exists it appends a counter, so two runs
// in the same second still never clobber each other.
func SaveSnapshot(dir string, observedAt time.Time, signals []NewsSignal) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := uniquePath(SnapshotPath(dir, observedAt))
	env := snapshotFile{Date: observedAt.Format("2006-01-02"), ObservedAt: observedAt, Signals: signals}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	base := strings.TrimSuffix(p, ".json")
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s_%d.json", base, i)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// SnapshotRecord is one persisted run for a date.
type SnapshotRecord struct {
	Path       string
	ObservedAt time.Time
	Signals    []NewsSignal
}

// LoadSnapshotsForDate returns every run's snapshot for a date (sorted by observation
// time). A future historical validator consumes these — never re-fetching the web — and
// must gate each signal's usability by its ObservedAt.
func LoadSnapshotsForDate(dir string, date time.Time) ([]SnapshotRecord, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "news_"+date.Format("20060102")+"_*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []SnapshotRecord
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var env snapshotFile
		if err := json.Unmarshal(b, &env); err != nil {
			return nil, fmt.Errorf("decode %s: %w", p, err)
		}
		out = append(out, SnapshotRecord{Path: p, ObservedAt: env.ObservedAt, Signals: env.Signals})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.Before(out[j].ObservedAt) })
	return out, nil
}
