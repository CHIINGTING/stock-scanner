// Package service orchestrates the Market Dashboard: it pulls each feed from the Provider,
// hands the data to the pure analyzer functions, assembles the Dashboard, and (optionally)
// persists it. Provider failures are ISOLATED — a single down source becomes an unavailable
// module and the dashboard still builds (mirrors internal/news.Service.Collect).
package service

import (
	"context"
	"log"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/analyzer"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// topN is how many sectors surface as Top Opportunities / Top Risks.
const topN = 3

// Service builds dashboards from a Provider using a MarketConfig. It never touches scanner
// scoring — it only produces the top-level market context + a historical snapshot.
type Service struct {
	p    provider.Provider
	cfg  model.MarketConfig
	logf func(format string, args ...any)
	now  func() time.Time
}

// NewService builds a Service. A nil logger defaults to the standard logger.
func NewService(p provider.Provider, cfg model.MarketConfig, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = log.Printf
	}
	return &Service{p: p, cfg: cfg.Defaulted(), logf: logf, now: time.Now}
}

// Build assembles the dashboard for date (YYYY-MM-DD). It never returns an error: any
// provider that fails is logged and its module marked unavailable, so a partial-data day
// still yields a (lower-confidence) dashboard.
func (s *Service) Build(ctx context.Context, date string) *model.Dashboard {
	w := s.cfg.Weights
	var mods []model.ModuleScore

	if d, err := s.p.Futures(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleForeignFutures, err))
	} else {
		mods = append(mods, analyzer.AnalyzeFutures(d, w[model.ModuleForeignFutures], s.cfg.Futures))
	}

	if d, err := s.p.ForeignBuySell(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleForeignCash, err))
	} else {
		mods = append(mods, analyzer.AnalyzeCash(d, w[model.ModuleForeignCash]))
	}

	if d, err := s.p.Margin(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleMargin, err))
	} else {
		mods = append(mods, analyzer.AnalyzeMargin(d, w[model.ModuleMargin]))
	}

	if d, err := s.p.Options(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleOptions, err))
	} else {
		mods = append(mods, analyzer.AnalyzeOptions(d, w[model.ModuleOptions]))
	}

	if d, err := s.p.Currency(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleCurrency, err))
	} else {
		mods = append(mods, analyzer.AnalyzeCurrency(d, w[model.ModuleCurrency]))
	}

	if d, err := s.p.VIX(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleVIX, err))
	} else {
		mods = append(mods, analyzer.AnalyzeVIX(d, w[model.ModuleVIX]))
	}

	var sectors []model.SectorScore
	if ss, err := s.p.Sectors(ctx, date); err != nil {
		mods = append(mods, s.unavailable(model.ModuleSectorCycle, err))
	} else {
		sectors = ss
		mods = append(mods, analyzer.AnalyzeSectorCycle(sectors, w[model.ModuleSectorCycle]))
	}

	ms := analyzer.Aggregate(mods)
	regime := analyzer.Classify(ms, s.cfg.Regime, s.cfg.ConfidenceMinModules)

	s.logf("market date=%s score=%.0f regime=%s confidence=%s modules=%d/%d",
		date, ms.Total, regime.Regime, regime.Confidence, ms.AvailableCount, len(mods))

	return &model.Dashboard{
		Date:             date,
		GeneratedAt:      s.now(),
		MarketScore:      ms,
		Regime:           regime,
		Sectors:          analyzer.RankSectors(sectors),
		TopOpportunities: analyzer.TopOpportunities(sectors, topN),
		TopRisks:         analyzer.TopRisks(sectors, topN),
	}
}

// unavailable logs the isolated failure and returns a placeholder module (excluded from the
// aggregate by analyzer.Aggregate).
func (s *Service) unavailable(name string, err error) model.ModuleScore {
	s.logf("market module=%s unavailable err=%q → skipped", name, err.Error())
	return model.ModuleScore{Name: name, Weight: s.cfg.Weights[name], State: "n/a", Available: false}
}
