package etfflow

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// LoadHistory returns up to maxDays of an ETF's most-recent snapshots from dir,
// ordered ascending by as-of date (oldest first). It is a read-only helper over the
// R8-1 schema: it never writes and never panics.
//
// Degrade rules (so a live scan is never interrupted by ETF data gaps):
//   - dir does not exist           → (nil, nil): ETF simply not seeded yet.
//   - dir unreadable for other reasons → (nil, err): caller may log + skip the ETF.
//   - a malformed / foreign file   → skipped silently (not fatal).
//
// Coverage guardrail (R8-6): only flow-eligible snapshots (full_holdings or an empty
// legacy tag) are returned. Partial (top_n_only), quarterly_composition and unknown
// snapshots are skipped here, so they can never reach CalculateFlows and fabricate a
// sell-pressure signal. This is the single chokepoint feeding R8 flow.
//
// maxDays <= 0 means "all available".
func LoadHistory(dir, etfCode string, maxDays int) ([]*Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("etfflow: read dir %s: %w", dir, err)
	}

	// snapshotFile(code, "") yields "<safeEtf>_.json"; trimming ".json" leaves the
	// "<safeEtf>_" filename prefix every snapshot for this ETF shares.
	prefix := strings.TrimSuffix(snapshotFile(etfCode, ""), ".json")

	var snaps []*Snapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		asOf := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
		s, err := Load(dir, etfCode, asOf)
		if err != nil {
			continue // malformed or unrelated file → skip, never fatal
		}
		if !s.IsFlowEligible() {
			continue // partial / quarterly / unknown coverage must never feed R8 flow
		}
		snaps = append(snaps, s)
	}

	sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].AsOfDate < snaps[j].AsOfDate })
	if maxDays > 0 && len(snaps) > maxDays {
		snaps = snaps[len(snaps)-maxDays:]
	}
	return snaps, nil
}
