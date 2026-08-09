package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Snapshot persistence. This is the archive every future validation run reads: "when the
// score was above 70, what did the benchmark do over the next 5/10/20 sessions?" is
// answered by replaying these files, never by re-fetching TWSE/TAIFEX.
//
// Two properties matter more than convenience:
//
//   - ATOMIC. Written to a temp file in the same directory and renamed into place, so a
//     crash mid-write leaves the previous day's file intact instead of a truncated one.
//     (Same approach as internal/institution/snapshot.go.)
//   - VALIDATED. Validate() runs before the bytes are written. A malformed snapshot must
//     never enter the archive, because the archive's only value is that old entries stay
//     trustworthy.

// SnapshotPath is <dir>/market_<date>.json. The prefix mirrors the institution package's
// {market}_{date}.json convention and keeps these files distinguishable from the legacy
// Dashboard files (<date>.json) during the transition.
func SnapshotPath(dir, date string) string {
	return filepath.Join(dir, "market_"+date+".json")
}

// SaveSnapshot validates then atomically writes s to <dir>/market_<date>.json, returning
// the path. One file per trading day: a re-run for the same date replaces it.
func SaveSnapshot(dir string, s *model.Snapshot) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode snapshot %s: %w", s.Date, err)
	}
	b = append(b, '\n')

	path := SnapshotPath(dir, s.Date)
	tmp, err := os.CreateTemp(dir, "market_*.json.tmp")
	if err != nil {
		return "", fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("rename into %s: %w", path, err)
	}
	return path, nil
}

// LoadSnapshot reads back one day. It validates on read as well: a file that has been
// hand-edited into an undefined state should fail loudly rather than feed a validation run
// a regime nobody defined.
func LoadSnapshot(dir, date string) (*model.Snapshot, error) {
	path := SnapshotPath(dir, date)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s model.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// HasSnapshot reports whether a snapshot exists for date, without decoding it.
func HasSnapshot(dir, date string) bool {
	st, err := os.Stat(SnapshotPath(dir, date))
	return err == nil && !st.IsDir()
}
