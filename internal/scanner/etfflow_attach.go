package scanner

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

// ──────────────────────────────────────────────────────────────────────────────
// ETF Flow attach (R8-4) — display/context only.
//
// AttachETFFlow runs AFTER EnrichWatchlist as an independent post-pass, so the
// scoring enrichment path is never touched and it is trivially provable that ETF
// flow changes no score / action / probability / order: computeRocket has already
// run and is never re-invoked here. ETF flow is delayed-disclosure CONTEXT, never an
// intraday signal or trade instruction. See SPEC §11 + R8-4 plan.
// ──────────────────────────────────────────────────────────────────────────────

// ETFWatch is one watched ETF (code + active/passive class) from config.
type ETFWatch struct {
	Code string `yaml:"code"`
	Type string `yaml:"type"` // etfflow.TypeActive | etfflow.TypePassive
}

// ETFFlowStrengthConfig optionally overrides the strength thresholds. Any all-zero
// config falls back to etfflow.DefaultStrengthThresholds() (handled in Classify).
type ETFFlowStrengthConfig struct {
	PressureLow        float64 `yaml:"pressure_low"`
	PressureMedium     float64 `yaml:"pressure_medium"`
	PressureHigh       float64 `yaml:"pressure_high"`
	ConsecutiveMin     int     `yaml:"consecutive_min"`
	ConsecutiveExtreme int     `yaml:"consecutive_extreme"`
}

// ETFFlowConfig is the R8 ETF-flow source/threshold settings (nested under scanner).
type ETFFlowConfig struct {
	SnapshotDir string                `yaml:"snapshot_dir"`
	HistoryDays int                   `yaml:"history_days"`
	WatchETFs   []ETFWatch            `yaml:"watch_etfs"`
	Strength    ETFFlowStrengthConfig `yaml:"strength"`
}

// Thresholds maps the config overrides to etfflow.StrengthThresholds. A zero config
// yields the zero value, which Classify treats as "use defaults".
func (c ETFFlowStrengthConfig) Thresholds() etfflow.StrengthThresholds {
	return etfflow.StrengthThresholds{
		PressureLow:        c.PressureLow,
		PressureMedium:     c.PressureMedium,
		PressureHigh:       c.PressureHigh,
		ConsecutiveMin:     c.ConsecutiveMin,
		ConsecutiveExtreme: c.ConsecutiveExtreme,
	}
}

// AttachETFFlow classifies each watchlist entry's ETF holdings flow and stores it on
// WatchlistEntry.ETFFlow. It reads only the entry's symbol + 20-day average volume,
// mutates nothing else, and leaves entries with no flow untouched (ETFFlow stays nil).
func AttachETFFlow(entries []WatchlistEntry, flows map[string]*etfflow.StockFlow, reportDate time.Time, th etfflow.StrengthThresholds) {
	if len(flows) == 0 {
		return
	}
	for i := range entries {
		code := entries[i].A.Symbol
		flow, ok := flows[code]
		if !ok || flow == nil {
			continue
		}
		sig := etfflow.Classify(etfflow.ClassifyInput{
			StockCode:   code,
			Flow:        flow,
			AvgVolume20: float64(entries[i].A.AvgVolume20),
			ReportDate:  reportDate,
			AsOfDate:    conservativeAsOf(flow),
			Thresholds:  th,
		})
		entries[i].ETFFlow = &sig
	}
}

// conservativeAsOf returns the OLDEST as-of date across a stock's ETF legs, i.e. the
// largest data lag, so the report never understates how stale the disclosure is when
// watched ETFs disclose on different days. Returns "" when there are no dated legs.
func conservativeAsOf(flow *etfflow.StockFlow) string {
	asOf := ""
	for _, l := range flow.Legs {
		if l.AsOfDate == "" {
			continue
		}
		if asOf == "" || l.AsOfDate < asOf {
			asOf = l.AsOfDate
		}
	}
	return asOf
}
