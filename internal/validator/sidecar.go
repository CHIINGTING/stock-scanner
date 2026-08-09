package validator

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/deep-huang/stock-scanner/internal/analysishistory"
)

// SidecarStore reads the structured history sidecars (data/analysis_history/<date>.json)
// written by internal/analysishistory. It deliberately does NOT define its own JSON
// structs — it unmarshals into the single authoritative analysishistory types, so a
// report-template change can never break structured validation (SPEC §17/§18).
//
// It mirrors PriceStore: read-only, offline, cached, degrades to "absent" on any problem.
type SidecarStore struct {
	dir    string
	loaded map[string]*analysishistory.Snapshot // date(YYYY-MM-DD) → snapshot (nil = confirmed absent)
}

// NewSidecarStore builds a store over dir (default data/analysis_history).
func NewSidecarStore(dir string) *SidecarStore {
	if dir == "" {
		dir = analysishistory.DefaultDir
	}
	return &SidecarStore{dir: dir, loaded: make(map[string]*analysishistory.Snapshot)}
}

// Load returns the sidecar for a date, or ok=false when absent/unreadable. Schema
// handling: a newer SchemaVersion is read best-effort (fields are additive, §20); an
// unreadable/corrupt file is treated as absent (never fatal).
func (s *SidecarStore) Load(date time.Time) (*analysishistory.Snapshot, bool) {
	key := date.UTC().Format("2006-01-02")
	if snap, seen := s.loaded[key]; seen {
		return snap, snap != nil
	}
	path := filepath.Join(s.dir, key+".json")
	snap := loadSidecarFile(path)
	s.loaded[key] = snap
	return snap, snap != nil
}

// Record returns one stock's structured record for a date, or ok=false.
func (s *SidecarStore) Record(date time.Time, code string) (analysishistory.StockData, bool) {
	snap, ok := s.Load(date)
	if !ok {
		return analysishistory.StockData{}, false
	}
	for _, st := range snap.Stocks {
		if st.Code == code {
			return st, true
		}
	}
	return analysishistory.StockData{}, false
}

func loadSidecarFile(path string) *analysishistory.Snapshot {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // absent
	}
	var snap analysishistory.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		log.Printf("sidecar: parse %s: %v (skipping)", path, err)
		return nil
	}
	if snap.SchemaVersion > analysishistory.SchemaVersion {
		log.Printf("sidecar: %s schema v%d newer than supported v%d — reading additive fields best-effort",
			path, snap.SchemaVersion, analysishistory.SchemaVersion)
	}
	return &snap
}
