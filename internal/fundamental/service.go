package fundamental

import (
	"context"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// The service: the one place that turns provider responses into snapshots.
//
// Consumers (candidate research, the CLI, any future dashboard) talk to this, never to the
// provider. That boundary is what keeps a renderer from parsing MOPS JSON, and what makes
// "where did this number come from" a question with one answer.

// Service builds and reads fundamental snapshots.
type Service struct {
	Provider *Provider
	Dir      string
	Logf     func(string, ...any)
}

// NewService wires a provider to an archive directory.
func NewService(p *Provider, dir string, logf func(string, ...any)) *Service {
	if p == nil {
		p = NewProvider(logf)
	}
	if dir == "" {
		dir = defaultDir
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{Provider: p, Dir: dir, Logf: logf}
}

// FetchAndStore downloads the datasets each market needs and archives one snapshot per symbol.
//
// Symbols are grouped by market first, so a hundred candidates cost at most FOUR requests —
// two datasets for each of the two markets. Fetching per symbol would issue a hundred
// identical downloads to answer questions one download already answers.
func (s *Service) FetchAndStore(ctx context.Context, symbols []string) ([]Snapshot, error) {
	byMarket := map[string][]string{}
	var order []string
	for _, sym := range symbols {
		code, market, err := fetcher.ParseYahooSymbol(sym)
		if err != nil {
			s.Logf("fundamental: skipping %s: %v", sym, err)
			continue
		}
		byMarket[market] = append(byMarket[market], code)
		order = append(order, sym)
	}

	datasets := map[string]*Dataset{}
	for market := range byMarket {
		ds, err := s.Provider.FetchMarket(ctx, market)
		if err != nil {
			// One market failing must not cost the other. A TWSE outage should not blank
			// every OTC candidate.
			s.Logf("fundamental: %s datasets unavailable: %v", market, err)
		}
		datasets[market] = ds
	}

	out := make([]Snapshot, 0, len(order))
	for _, sym := range order {
		code, market, _ := fetcher.ParseYahooSymbol(sym)
		snap := buildSnapshot(sym, code, datasets[market])
		if _, err := SaveSnapshot(s.Dir, &snap); err != nil {
			// The snapshot still stands in memory; only the archive write failed.
			s.Logf("fundamental: %s not archived: %v", sym, err)
		}
		out = append(out, snap)
	}
	return out, nil
}

// buildSnapshot assembles one stock's snapshot from the market datasets.
//
// A stock absent from a dataset is UNAVAILABLE for that half and available for the other —
// the two halves come from different pipelines with different schedules, and a company that
// has reported revenue but not yet its statements is a normal, common state.
func buildSnapshot(symbol, code string, ds *Dataset) Snapshot {
	snap := Snapshot{
		SchemaVersion:   SchemaVersion,
		Symbol:          symbol,
		Source:          SourceName,
		ObservedAt:      time.Now(),
		RevenueStatus:   Unavailable,
		FinancialStatus: Unavailable,
	}
	if ds == nil {
		snap.Status = Unavailable
		snap.Reason = "no dataset for this market"
		return snap
	}
	snap.ObservedAt = ds.ObservedAt

	if r, ok := ds.Revenue[code]; ok {
		rc := r
		rc.Symbol = symbol
		snap.Revenue = &rc
		snap.RevenueStatus = Available
	} else if ds.RevenueErr != nil {
		snap.Reason = "revenue dataset: " + ds.RevenueErr.Error()
	}

	if f, ok := ds.Financials[code]; ok {
		fc := f
		fc.Symbol = symbol
		snap.Financials = &fc
		snap.FinancialStatus = Available
	} else if ds.FinancialErr != nil && snap.Reason == "" {
		snap.Reason = "financial dataset: " + ds.FinancialErr.Error()
	}

	snap.Status = aggregate(snap.RevenueStatus, snap.FinancialStatus)
	if snap.Status == Unavailable && snap.Reason == "" {
		snap.Reason = fmt.Sprintf("%s appears in neither dataset", code)
	}
	return snap
}

// aggregate combines the two halves.
//
// PARTIAL exists so that one half being absent does not discard the other. Without it, a
// company that has published revenue but not yet its quarterly statement would show as having
// no fundamentals at all.
func aggregate(revenue, financial Availability) Availability {
	switch {
	case revenue == Available && financial == Available:
		return Available
	case revenue == Available || financial == Available:
		return Partial
	default:
		return Unavailable
	}
}

// View is the read side: everything a consumer needs about one stock, already derived.
type View struct {
	Symbol string
	Status Availability
	Reason string
	Source string

	ObservedAt time.Time

	Revenue        *MonthlyRevenue
	RevenueMetrics RevenueMetrics
	RevenueStatus  Availability

	Financials       *Financials
	FinancialMetrics FinancialMetrics
	FinancialStatus  Availability
}

// LoadView reads the archived snapshot visible at asOfDate and derives its metrics.
//
// Both halves of the point-in-time contract are applied here: the SNAPSHOT must have been
// observed on or before the date, and each RECORD inside it must have been published on or
// before it. A statement published the same afternoon a snapshot was captured is still
// invisible to a run dated that morning.
func (s *Service) LoadView(asOfDate, symbol string) (View, error) {
	v := View{Symbol: symbol, Status: Unavailable, RevenueStatus: Unavailable,
		FinancialStatus: Unavailable}

	snap, err := LoadAsOf(s.Dir, asOfDate, symbol)
	if err != nil {
		v.Reason = err.Error()
		return v, err
	}
	if snap == nil {
		v.Reason = "no fundamental snapshot archived on or before " + asOfDate
		return v, nil
	}

	v.Source, v.ObservedAt, v.Reason = snap.Source, snap.ObservedAt, snap.Reason

	if snap.Revenue != nil && VisibleAsOf(snap.Revenue.PublishedAt, asOfDate) {
		v.Revenue = snap.Revenue
		v.RevenueStatus = Available
	} else if snap.Revenue != nil {
		v.Reason = fmt.Sprintf("monthly revenue was published %s, after %s",
			snap.Revenue.PublishedAt.Format("2006-01-02"), asOfDate)
	}
	if snap.Financials != nil && VisibleAsOf(snap.Financials.PublishedAt, asOfDate) {
		v.Financials = snap.Financials
		v.FinancialStatus = Available
	}

	v.RevenueMetrics = DeriveRevenue(v.Revenue)
	v.FinancialMetrics = DeriveFinancials(v.Financials)
	v.Status = aggregate(v.RevenueStatus, v.FinancialStatus)
	return v, nil
}
