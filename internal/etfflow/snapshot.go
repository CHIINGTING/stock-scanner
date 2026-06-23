// Package etfflow ingests and models daily ETF holdings, so the scanner can show
// ETF / active-rotation *context* (R8). It is a delayed-disclosure data layer:
// nothing here is an intraday signal, a trading instruction, or an order. R8-1
// covers only the snapshot schema + persistence and the holdings ingestion source.
// Flow calculation (R8-2), signal classification (R8-3) and report/scanner wiring
// (R8-4/R8-5) live in later commits and are NOT part of this file.
package etfflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ETF type tags. ETFs are classified by config, never inferred — see SPEC §8.
const (
	TypeActive  = "active"  // 主動式 ETF（經理人主動換股）
	TypePassive = "passive" // 高股息 / 規則型 ETF（規則換股 / 再平衡）
)

// Coverage tags describe how COMPLETE a snapshot's source is. Only full daily
// holdings (or an empty tag, for R8-1..R8-5 manual snapshots written before tagging
// existed) may feed R8 flow (CalculateFlows / delta_shares). Everything else is
// context-only and must be skipped by LoadHistory, so a partial source can never
// fabricate a sell-pressure signal. See docs/SPEC_R8_6B_SOURCE_DISCOVERY.md.
const (
	CoverageFullHoldings         = "full_holdings"         // 逐日、完整成分、含持有股數 → 可進 flow
	CoverageTopNOnly             = "top_n_only"            // 僅前 N 大（如 MoneyDJ top-10）→ context only
	CoverageQuarterlyComposition = "quarterly_composition" // 季度成分比例（無股數）→ context only
	CoverageUnknown              = "unknown"               // 無法判定 → 保守跳過
)

// Holding is one stock position inside an ETF on a given as-of date. HoldingShares
// is always normalised to 股 (shares); the original unit is kept in HoldingRawUnit
// so the normalisation is auditable.
type Holding struct {
	StockCode      string  `json:"stock_code"`
	StockName      string  `json:"stock_name"`
	HoldingShares  int64   `json:"holding_shares"`   // normalised to 股
	HoldingRawUnit string  `json:"holding_raw_unit"` // "股" | "張" | "千股"
	HoldingWeight  float64 `json:"holding_weight"`   // percent
}

// Snapshot is one ETF's full holdings for one trading day. AsOfDate is the trading
// day the holdings correspond to (disclosure is delayed: the data is fetched later,
// recorded in FetchedAt). data_lag_days is NOT stored here — it is derived against
// the scan's trading date at signal time (R8-3).
type Snapshot struct {
	ETFCode          string    `json:"etf_code"`
	ETFName          string    `json:"etf_name"`
	ETFType          string    `json:"etf_type"` // TypeActive | TypePassive
	AsOfDate         string    `json:"as_of_date"`
	UnitsOutstanding int64     `json:"etf_units_outstanding,omitempty"`
	NAVOrAUM         float64   `json:"nav_or_aum,omitempty"`
	Holdings         []Holding `json:"holdings"`
	SourceURL        string    `json:"source_url"`
	FetchedAt        time.Time `json:"fetched_at"`

	// Coverage metadata (R8-6). Optional: an empty CoverageType means a legacy /
	// manual full snapshot (treated as full_holdings). A non-full tag marks the
	// snapshot as context-only — IsFlowEligible returns false and LoadHistory skips
	// it, so partial / quarterly data never reaches CalculateFlows.
	CoverageType string `json:"coverage_type,omitempty"` // full_holdings | top_n_only | quarterly_composition | unknown
	TopN         int    `json:"top_n,omitempty"`         // set when CoverageType == top_n_only
	CoverageNote string `json:"coverage_note,omitempty"` // human-readable provenance caveat
}

// IsFlowEligible reports whether this snapshot's coverage is complete enough to feed
// R8 flow. Only full daily holdings qualify; an empty CoverageType is treated as
// full_holdings for backward-compatibility with R8-1..R8-5 manual snapshots written
// before coverage tagging existed. Everything else (top_n_only / quarterly /
// unknown / any unrecognised value) fails closed — it is context-only.
func (s *Snapshot) IsFlowEligible() bool {
	if s == nil {
		return false
	}
	switch strings.TrimSpace(s.CoverageType) {
	case "", CoverageFullHoldings:
		return true
	default:
		return false
	}
}

// dateLayout is the canonical AsOfDate format used throughout R8.
const dateLayout = "2006-01-02"

// Validate reports the first structural problem with a snapshot, or nil. It does not
// judge the *content* of holdings (that is the calculator's job); it only checks the
// fields the persistence + ingestion layers rely on.
func (s *Snapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("etfflow: nil snapshot")
	}
	if strings.TrimSpace(s.ETFCode) == "" {
		return fmt.Errorf("etfflow: snapshot missing etf_code")
	}
	if s.ETFType != TypeActive && s.ETFType != TypePassive {
		return fmt.Errorf("etfflow: snapshot %s has invalid etf_type %q (want %q|%q)",
			s.ETFCode, s.ETFType, TypeActive, TypePassive)
	}
	if _, err := time.Parse(dateLayout, s.AsOfDate); err != nil {
		return fmt.Errorf("etfflow: snapshot %s has invalid as_of_date %q (want YYYY-MM-DD)",
			s.ETFCode, s.AsOfDate)
	}
	return nil
}

// snapshotFile maps (etf, asOfDate) to a safe filename stem. ETF codes are dot-free
// in practice ("00981A"), but we sanitise defensively, mirroring fetcher.cacheKey.
func snapshotFile(etfCode, asOfDate string) string {
	safe := func(s string) string {
		s = strings.ReplaceAll(s, ".", "_")
		return strings.ReplaceAll(s, string(os.PathSeparator), "_")
	}
	return safe(etfCode) + "_" + safe(asOfDate) + ".json"
}

// Save writes a snapshot to <dir>/<etf>_<asOfDate>.json (pretty JSON), creating dir
// if needed. It validates first so a malformed snapshot is never persisted. The
// write is atomic (temp file + rename) so a partial file is never left behind.
func Save(dir string, s *Snapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("etfflow: create dir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("etfflow: marshal snapshot %s: %w", s.ETFCode, err)
	}
	path := filepath.Join(dir, snapshotFile(s.ETFCode, s.AsOfDate))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("etfflow: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("etfflow: replace %s: %w", path, err)
	}
	return nil
}

// Load reads the snapshot for (etfCode, asOfDate) from dir. A missing file returns
// an error wrapping os.ErrNotExist, so callers can branch with errors.Is.
func Load(dir, etfCode, asOfDate string) (*Snapshot, error) {
	path := filepath.Join(dir, snapshotFile(etfCode, asOfDate))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("etfflow: read %s: %w", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("etfflow: parse %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("etfflow: %s: %w", path, err)
	}
	return &s, nil
}
