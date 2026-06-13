package etfflow

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Holdings ingestion (R8-1).
//
// First version is ManualCSVSource only: holdings are exported by hand to CSV and
// read from disk. NO crawler (official PCF / issuer / MoneyDJ / capitalfund) — that
// is a later, independent adapter and must not block the R8 main line. The
// HoldingsSource interface exists so such an adapter can be added without touching
// the calculator / signal / report layers. See SPEC §5.
// ──────────────────────────────────────────────────────────────────────────────

// RawHolding is one position as read from a source, before unit normalisation:
// Quantity is in RawUnit (股 / 張 / 千股).
type RawHolding struct {
	StockCode string
	StockName string
	Quantity  float64 // in RawUnit
	RawUnit   string  // "股" | "張" | "千股"
	Weight    float64 // percent
}

// HoldingsSource loads the raw holdings of one ETF for one as-of trading day. An
// implementation must not normalise units — that is centralised in NormalizeToShares.
type HoldingsSource interface {
	Load(etfCode, asOfDate string) ([]RawHolding, error)
}

// Unit → shares multipliers. 張 and 千股 are both 1,000 shares; 股 is 1.
const (
	unitShare    = "股"
	unitLot      = "張"  // 1 張 = 1,000 股
	unitThousand = "千股" // 1 千股 = 1,000 股
)

// NormalizeToShares converts a raw quantity in unit to whole shares (股). It is the
// single place the unit rule lives, so every source normalises identically. Unknown
// or empty units are rejected rather than silently assumed, to avoid 1000× errors.
func NormalizeToShares(qty float64, unit string) (int64, error) {
	switch strings.TrimSpace(unit) {
	case unitShare:
		return int64(qty), nil
	case unitLot, unitThousand:
		return int64(qty * 1000), nil
	default:
		return 0, fmt.Errorf("etfflow: unknown holding unit %q (want %q|%q|%q)",
			unit, unitShare, unitLot, unitThousand)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ManualCSVSource
//
// Reads <dir>/raw/<etf>_<asOfDate>.csv. Expected header (order-independent,
// case-insensitive, surrounding spaces trimmed):
//
//	stock_code, stock_name, holding_shares, holding_raw_unit, holding_weight
//
// holding_shares is the quantity in holding_raw_unit (NOT yet normalised). No sample
// CSV is committed to the repo; tests synthesise fixtures in t.TempDir().
// ──────────────────────────────────────────────────────────────────────────────

// ManualCSVSource reads hand-exported holdings CSVs rooted at Dir/raw.
type ManualCSVSource struct {
	Dir string // snapshot root; CSVs live under <Dir>/raw
}

// rawCSVPath is the on-disk location ManualCSVSource reads for (etf, asOfDate). It
// reuses snapshotFile's stem so the .json snapshot and its source .csv share a name.
func (m ManualCSVSource) rawCSVPath(etfCode, asOfDate string) string {
	stem := strings.TrimSuffix(snapshotFile(etfCode, asOfDate), ".json")
	return filepath.Join(m.Dir, "raw", stem+".csv")
}

// Load implements HoldingsSource.
func (m ManualCSVSource) Load(etfCode, asOfDate string) ([]RawHolding, error) {
	path := m.rawCSVPath(etfCode, asOfDate)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("etfflow: open %s: %w", path, err)
	}
	defer f.Close()
	out, err := parseHoldingsCSV(f)
	if err != nil {
		return nil, fmt.Errorf("etfflow: %s: %w", path, err)
	}
	return out, nil
}

// parseHoldingsCSV is the shared holdings-CSV parser behind ManualCSVSource and
// CSVFileSource (R8-5a DRY), so both parse identically and the R8-1 tests guard both.
// It reads a header + data rows and returns RawHolding (NOT yet normalised): unit
// normalisation stays centralised in Ingest via NormalizeToShares, per the
// HoldingsSource contract. Header is order-independent / case-insensitive; blank rows
// are skipped. Errors on missing required column, bad number, unknown unit, or when
// there are zero holding rows.
func parseHoldingsCSV(r io.Reader) ([]RawHolding, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // columns validated by header, not by a fixed count
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col, err := csvHeaderIndex(header)
	if err != nil {
		return nil, err
	}

	var out []RawHolding
	for line := 2; ; line++ { // line 1 is the header
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line %d: %w", line, err)
		}
		h, err := parseHoldingRow(rec, col)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if h == nil { // blank row → skip
			continue
		}
		out = append(out, *h)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no holding rows")
	}
	return out, nil
}

// requiredCSVColumns lists the headers ManualCSVSource needs.
var requiredCSVColumns = []string{
	"stock_code", "stock_name", "holding_shares", "holding_raw_unit", "holding_weight",
}

// csvHeaderIndex maps each required column name to its position, case-insensitively.
func csvHeaderIndex(header []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	col := make(map[string]int, len(requiredCSVColumns))
	for _, name := range requiredCSVColumns {
		i, ok := idx[name]
		if !ok {
			return nil, fmt.Errorf("missing required column %q", name)
		}
		col[name] = i
	}
	return col, nil
}

// parseHoldingRow turns one CSV record into a RawHolding. A fully blank row returns
// (nil, nil) so trailing blank lines are tolerated.
func parseHoldingRow(rec []string, col map[string]int) (*RawHolding, error) {
	get := func(name string) string {
		i := col[name]
		if i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	code := get("stock_code")
	if code == "" && get("stock_name") == "" && get("holding_shares") == "" {
		return nil, nil // blank row
	}
	if code == "" {
		return nil, fmt.Errorf("missing stock_code")
	}

	qtyStr := strings.ReplaceAll(get("holding_shares"), ",", "")
	qty, err := strconv.ParseFloat(qtyStr, 64)
	if err != nil {
		return nil, fmt.Errorf("bad holding_shares %q: %w", get("holding_shares"), err)
	}

	unit := get("holding_raw_unit")
	if _, err := NormalizeToShares(qty, unit); err != nil { // fail fast on bad unit
		return nil, err
	}

	var weight float64
	if w := strings.TrimSuffix(get("holding_weight"), "%"); w != "" {
		weight, err = strconv.ParseFloat(w, 64)
		if err != nil {
			return nil, fmt.Errorf("bad holding_weight %q: %w", get("holding_weight"), err)
		}
	}

	return &RawHolding{
		StockCode: code,
		StockName: get("stock_name"),
		Quantity:  qty,
		RawUnit:   unit,
		Weight:    weight,
	}, nil
}

// SnapshotMeta is the per-ETF metadata Ingest stamps onto a snapshot; it is supplied
// by config (R8-5), never inferred from the holdings data.
type SnapshotMeta struct {
	ETFCode   string
	ETFName   string
	ETFType   string // TypeActive | TypePassive
	AsOfDate  string // YYYY-MM-DD trading day the holdings correspond to
	SourceURL string // provenance note; for ManualCSVSource a free-text description
}

// Ingest loads raw holdings via src, normalises units to 股, and assembles a
// validated Snapshot stamped with meta and FetchedAt=now. It does not persist; the
// caller decides whether to Save. now is injected for deterministic tests.
func Ingest(src HoldingsSource, meta SnapshotMeta, now time.Time) (*Snapshot, error) {
	if meta.ETFType != TypeActive && meta.ETFType != TypePassive {
		return nil, fmt.Errorf("etfflow: ingest %s: invalid etf_type %q", meta.ETFCode, meta.ETFType)
	}
	raws, err := src.Load(meta.ETFCode, meta.AsOfDate)
	if err != nil {
		return nil, err
	}
	holdings := make([]Holding, 0, len(raws))
	for _, rh := range raws {
		shares, err := NormalizeToShares(rh.Quantity, rh.RawUnit)
		if err != nil {
			return nil, fmt.Errorf("etfflow: ingest %s %s: %w", meta.ETFCode, rh.StockCode, err)
		}
		holdings = append(holdings, Holding{
			StockCode:      rh.StockCode,
			StockName:      rh.StockName,
			HoldingShares:  shares,
			HoldingRawUnit: rh.RawUnit,
			HoldingWeight:  rh.Weight,
		})
	}
	s := &Snapshot{
		ETFCode:   meta.ETFCode,
		ETFName:   meta.ETFName,
		ETFType:   meta.ETFType,
		AsOfDate:  meta.AsOfDate,
		Holdings:  holdings,
		SourceURL: meta.SourceURL,
		FetchedAt: now,
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
