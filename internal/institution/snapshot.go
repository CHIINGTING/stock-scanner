package institution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrWouldDowngrade is returned by Save when a same-date rerun would replace an existing
// snapshot with a LESS complete one (fewer stock records). The good snapshot is kept
// (§19). Callers treat this as "skip write", not a fatal error.
var ErrWouldDowngrade = errors.New("institution: refusing to downgrade snapshot completeness")

const dateLayout = "2006-01-02"

// snapshotFile maps (market, date) to a filename stem: {twse|tpex}_YYYY-MM-DD.json.
func snapshotFile(market, asOfDate string) string {
	return strings.ToLower(market) + "_" + asOfDate + ".json"
}

// Validate reports the first structural problem with a snapshot, or nil.
func (s *Snapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("institution: nil snapshot")
	}
	if s.Market != MarketTWSE && s.Market != MarketTPEX {
		return fmt.Errorf("institution: invalid market %q", s.Market)
	}
	if _, err := time.Parse(dateLayout, s.AsOfDate); err != nil {
		return fmt.Errorf("institution: invalid as_of_date %q (want YYYY-MM-DD)", s.AsOfDate)
	}
	return nil
}

// Save writes a snapshot to <dir>/<market>_<date>.json (atomic temp+rename). It refuses
// to DOWNGRADE: if a snapshot already exists for the same (market, date) with MORE stock
// records than the new one, it returns ErrWouldDowngrade and leaves the old file intact
// (§19). Historical dates are effectively immutable; only same-day reruns hit this path.
func Save(dir string, s *Snapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("institution: create dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, snapshotFile(s.Market, s.AsOfDate))

	if existing, err := loadFile(path); err == nil && existing != nil {
		if len(existing.Stocks) > len(s.Stocks) {
			return fmt.Errorf("%w: existing=%d new=%d (%s %s)",
				ErrWouldDowngrade, len(existing.Stocks), len(s.Stocks), s.Market, s.AsOfDate)
		}
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("institution: marshal %s: %w", s.Market, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("institution: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("institution: replace %s: %w", path, err)
	}
	return nil
}

// Load reads the snapshot for (market, date) from dir. A missing file wraps os.ErrNotExist.
func Load(dir, market, asOfDate string) (*Snapshot, error) {
	path := filepath.Join(dir, snapshotFile(market, asOfDate))
	s, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("institution: %s: %w", path, err)
	}
	return s, nil
}

func loadFile(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("institution: parse %s: %w", path, err)
	}
	return &s, nil
}
