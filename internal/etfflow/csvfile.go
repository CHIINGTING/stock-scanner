package etfflow

import (
	"fmt"
	"os"
)

// CSVFileSource reads a holdings CSV from an explicit file path (any location),
// unlike ManualCSVSource which derives its path from <Dir>/raw/<etf>_<date>.csv. It
// is the ingest CLI's source: --input points straight at the file. Both sources share
// parseHoldingsCSV, so they parse identically.
type CSVFileSource struct {
	Path string
}

// Load implements HoldingsSource. etfCode/asOfDate are unused — the file is addressed
// directly by Path; they stay in the signature to satisfy HoldingsSource so Ingest can
// stamp meta from its own SnapshotMeta. Returns RawHolding (un-normalised); Ingest
// normalises via NormalizeToShares.
func (s CSVFileSource) Load(_, _ string) ([]RawHolding, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, fmt.Errorf("etfflow: open %s: %w", s.Path, err)
	}
	defer f.Close()
	out, err := parseHoldingsCSV(f)
	if err != nil {
		return nil, fmt.Errorf("etfflow: %s: %w", s.Path, err)
	}
	return out, nil
}
