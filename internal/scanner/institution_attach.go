package scanner

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/institution"
)

// ──────────────────────────────────────────────────────────────────────────────
// Institutional Flow shadow layer (R10-1). Display/context only. AttachInstitution runs
// as a post-pass AFTER EnrichWatchlist, so computeRocket has already run and is never
// re-invoked — trivially provable that institutional data changes no Score / Action /
// RocketScore / WatchAction / sort / stop (mirrors AttachETFFlow). SPEC §15/§21.
//
// Universe membership (§24): a 4-digit code is NOT an ordinary-stock classification. The
// snapshot may retain any syntactically-valid code; authoritative membership happens
// HERE — we only attach to entries that already exist (they came from the scanner's
// universe), and only when we actually have institution records for the stock.
// ──────────────────────────────────────────────────────────────────────────────

// InstitutionConfig is the R10-1 institutional source/threshold settings (under scanner).
type InstitutionConfig struct {
	SnapshotDir    string `yaml:"snapshot_dir"`    // default data/institution
	HistoryDays    int    `yaml:"history_days"`    // lookback window (default 40)
	ContinuousDays int    `yaml:"continuous_days"` // CONTINUOUS_* threshold (default 3)
}

// Defaulted fills zero fields with defaults.
func (c InstitutionConfig) Defaulted() InstitutionConfig {
	if c.SnapshotDir == "" {
		c.SnapshotDir = "data/institution"
	}
	if c.HistoryDays <= 0 {
		c.HistoryDays = 40
	}
	if c.ContinuousDays <= 0 {
		c.ContinuousDays = 3
	}
	return c
}

// SignalThresholds returns the institution.SignalThresholds for this config.
func (c InstitutionConfig) SignalThresholds() institution.SignalThresholds {
	return institution.SignalThresholds{ContinuousDays: c.ContinuousDays}
}

// AttachInstitution attaches the interpreted per-stock chip view to each watchlist entry.
// It reads the price candles (the authoritative trading-day calendar, §9) to detect real
// gaps, and the loaded institution snapshots. Entries with no candles or no institution
// records are left untouched (Institution stays nil → report degrades gracefully).
func AttachInstitution(entries []WatchlistEntry, loaded *institution.Loaded, candlesByCode map[string][]fetcher.Candle, reportDate time.Time, cfg InstitutionConfig) {
	if loaded == nil {
		return
	}
	cfg = cfg.Defaulted()
	rd := reportDate.Format("2006-01-02")
	for i := range entries {
		code := entries[i].A.Symbol
		candles := candlesByCode[code]
		if len(candles) == 0 {
			continue
		}
		expected, volByDate := datesAndVolumes(candles, rd, cfg.HistoryDays)
		if len(expected) == 0 {
			continue
		}
		asOf := expected[len(expected)-1]

		records := make(map[string]institution.CategoryNets, len(expected))
		todayReconciled := false
		for _, d := range expected {
			chip, ok := loaded.Chip(code, d)
			if !ok {
				continue // real gap for this trading day
			}
			records[d] = chip.Nets()
			if d == asOf {
				todayReconciled = chip.Reconciled
			}
		}
		if len(records) == 0 {
			continue // no institution data for this stock at all → leave nil
		}
		view := institution.Compute(code, entries[i].A.Name, asOf, expected, records, volByDate[asOf], todayReconciled)
		entries[i].Institution = &view
	}
}

// datesAndVolumes returns the trading-day calendar (candle dates on/before reportDate,
// ascending, last = as-of date) capped to maxDays, plus a date→volume map for the ratio.
func datesAndVolumes(candles []fetcher.Candle, reportDate string, maxDays int) ([]string, map[string]int64) {
	dates := make([]string, 0, len(candles))
	vol := make(map[string]int64, len(candles))
	for _, c := range candles {
		d := c.Date.Format("2006-01-02")
		if d > reportDate {
			continue
		}
		dates = append(dates, d)
		vol[d] = c.Volume
	}
	if maxDays > 0 && len(dates) > maxDays {
		dates = dates[len(dates)-maxDays:]
	}
	return dates, vol
}
