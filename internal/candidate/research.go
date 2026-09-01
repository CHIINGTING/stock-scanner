package candidate

import (
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/macro"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// Candidate research: the same evidence the scanner already computes, assembled for a
// human-chosen universe.
//
// # The reuse rule, and why it is absolute
//
// Nothing in this file computes an indicator. Every number comes from scanner.EnrichWatchlist
// and the existing Attach* passes — the same functions, on the same inputs, producing the
// same values. A second implementation would be a fork: it would agree today, drift in six
// months, and the two answers would both look authoritative. So the candidate path calls the
// scanner's own code and re-orders the result; that is the entire trick.
//
// # What this does NOT inherit
//
// EnrichWatchlist is built for the scan, and does two things a research universe must not:
//
//	it SORTS by RocketScore — but candidate order is a human's priority statement
//	it SKIPS stocks with <30 bars — a candidate must not silently vanish
//
// Both are handled here, at the boundary, rather than by changing the scanner.

// Status is one candidate's data availability. Price 0 is a price; it is never a status.
type Status string

const (
	// StatusOK means the enrichment ran and produced an entry.
	StatusOK Status = "OK"
	// StatusDataUnavailable means the stock was fetched but could not be analysed — most
	// often too little history. Distinct from a fetch failure: the data source answered.
	StatusDataUnavailable Status = "DATA_UNAVAILABLE"
	// StatusFetchError means the price data could not be obtained at all.
	StatusFetchError Status = "FETCH_ERROR"
)

// Availability is the three-state answer every evidence block must be able to give.
//
// DISABLED and UNAVAILABLE are deliberately separate: the first is a decision (the feature is
// switched off), the second is a fact about the data (it was switched on and nothing arrived).
// Collapsing them would make a config change indistinguishable from a broken feed.
type Availability string

const (
	Available   Availability = "AVAILABLE"
	Unavailable Availability = "UNAVAILABLE"
	Disabled    Availability = "DISABLED"
	NotWired    Availability = "NOT_WIRED"
	// Partial is for a layer with more than one half — fundamentals carry monthly revenue
	// and quarterly statements, published on different schedules — where one arrived and the
	// other did not. Without it, a company that has reported revenue but not yet filed its
	// statement would show as having no financial data at all.
	Partial Availability = "PARTIAL"
)

// MarketContext is the market read shared by every candidate in one run.
type MarketContext struct {
	Status Availability
	Regime string
	// Score is nil when unavailable. A market with no snapshot has no score, and 0 would be
	// a legitimate reading of a real one.
	Score    *float64
	AsOfDate string
}

// FXContext is the currency read, shared by every candidate in one run.
type FXContext struct {
	Status Availability
	// USDTWD and DXY are nil when their archive is absent or does not reach this date.
	USDTWD *fx.Metrics
	DXY    *fx.Metrics
}

// Evidence is everything known about one candidate.
//
// Entry is the SCANNER's own WatchlistEntry, referenced rather than copied. Every indicator,
// stage, score and shadow layer hangs off it exactly as the scan produced them — so
// "candidate technical == scanner technical" is true by construction, not by a mapping that
// could be written wrong.
type Evidence struct {
	Candidate Candidate
	Status    Status
	// Reason explains a non-OK status in one line.
	Reason string

	// Entry is nil unless Status is OK. Its fields — Action, RocketScore, ExplosionProb,
	// Stage — are the scanner's, and are READ-ONLY here.
	Entry *scanner.WatchlistEntry

	Market MarketContext
	FX     FXContext

	// InstitutionStatus distinguishes the three cases the numbers alone cannot: the feature
	// being off, the snapshot being absent, and a real reading that happens to be zero.
	InstitutionStatus Availability
	NewsStatus        Availability

	// Fundamental is the ACTUAL reported financial data, already derived. It comes from the
	// fundamental service's point-in-time view — the research layer never parses a source
	// response and never computes a margin of its own.
	Fundamental *fundamental.View

	// Valuation is the price-relative picture: the exchange's own trailing P/E plus its
	// distribution over the archived history. Forward-looking measures are absent because no
	// authorized forecast source exists — the valuation reports that as a status, not as a
	// gap the reader has to notice.
	Valuation *valuation.Valuation

	// Macro is the market-wide monetary, rate, inflation, labour and volatility picture. It
	// is a POINTER SHARED by every candidate in a run: the Fed's rate is the same fact for
	// every stock, and loading it per candidate would be a hundred reads of one file.
	Macro *macro.Research
}

// Config drives one research run. It carries the SCANNER's config so every feature flag is
// honoured identically — a candidate must never see an indicator the scan has switched off.
type Config struct {
	Scanner scanner.Config
	// ReportDate is the analysis date; snapshots are looked up for it.
	ReportDate time.Time
	// MarketSnapshotDir, FXDir and FundamentalDir point at the existing archives. Empty →
	// the usual defaults.
	MarketSnapshotDir string
	FXDir             string
	FundamentalDir    string
	ValuationDir      string
	MacroDir          string
	// EnableFundamental gates the layer. False → DISABLED, which is a different answer from
	// "enabled but no data": one is a decision, the other is a fact about the archive.
	EnableFundamental bool
	EnableValuation   bool
	EnableMacro       bool
}

// Resolver turns candidates into evidence.
//
// It holds the existing fetcher and scanner rather than any client of its own: there is one
// price cache in this repo and one set of analyzers, and a research layer that built its own
// would be answering a different question than the scan.
type Resolver struct {
	Fetcher *fetcher.Fetcher
	Scanner *scanner.Scanner
	Cfg     Config
	// Logf is optional progress output.
	Logf func(string, ...any)
}

// NewResolver builds a resolver from an existing fetcher and scanner.
func NewResolver(f *fetcher.Fetcher, s *scanner.Scanner, cfg Config, logf func(string, ...any)) *Resolver {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Resolver{Fetcher: f, Scanner: s, Cfg: cfg, Logf: logf}
}

// Resolve assembles evidence for every candidate, IN THE ORDER GIVEN.
//
// Every candidate gets exactly one Evidence, whatever happened to it. A stock that could not
// be fetched, or has too little history to analyse, appears with a non-OK status rather than
// disappearing — the human asked about it, so the answer is "we could not", never silence.
func (r *Resolver) Resolve(list *List, market MarketContext, fxCtx FXContext) []Evidence {
	return r.ResolveAsOf(list, market, fxCtx, r.Cfg.ReportDate.Format("2006-01-02"))
}

// ResolveAsOf is Resolve with an explicit point-in-time date for the fundamental archive.
func (r *Resolver) ResolveAsOf(list *List, market MarketContext, fxCtx FXContext,
	asOfDate string) []Evidence {
	if list == nil || len(list.Candidates) == 0 {
		return nil
	}

	out := make([]Evidence, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		out = append(out, Evidence{
			Candidate: c, Status: StatusFetchError, Reason: "not fetched",
			Market: market, FX: fxCtx,
			InstitutionStatus: Unavailable, NewsStatus: NotWired,
		})
	}

	// One fetch for the whole universe, through the existing entry point for "fetch exactly
	// these symbols". It writes to the same .cache the scanner reads, so a stock already
	// fetched today costs nothing and no second cache is created.
	infos := make([]fetcher.StockInfo, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		infos = append(infos, fetcher.StockInfo{Symbol: c.Code, Market: c.Market})
	}
	stocks, err := r.Fetcher.FetchSectorStocks(infos)
	if err != nil {
		// A whole-batch failure still leaves every candidate present, each carrying the
		// reason — the caller can print a complete list and the human sees what happened.
		r.Logf("candidate: fetch failed: %v", err)
		for i := range out {
			out[i].Reason = err.Error()
		}
		return out
	}

	byCode := make(map[string]fetcher.StockData, len(stocks))
	for _, s := range stocks {
		byCode[s.Symbol] = s
	}

	// Enrich through the SCANNER's own function. Candidates with no data are left out of the
	// call (it would skip them anyway) and keep their FETCH_ERROR status.
	var toEnrich []fetcher.StockData
	for _, c := range list.Candidates {
		if sd, ok := byCode[c.Code]; ok && len(sd.Candles) > 0 {
			toEnrich = append(toEnrich, sd)
		}
	}

	// sectorOf / rot / members are empty: a research universe of a handful of stocks cannot
	// produce a meaningful sector rotation panel, and re-deriving one from three stocks would
	// be a second, weaker answer to a question the daily scan already answers properly.
	// Sector linkage arrives when a caller passes the scan's own rotation in (Part 3+).
	entries := r.Scanner.EnrichWatchlist(toEnrich, nil, nil, nil, nil)

	byEntry := make(map[string]*scanner.WatchlistEntry, len(entries))
	for i := range entries {
		byEntry[entries[i].A.Symbol] = &entries[i]
	}

	// Institution, honouring the scanner's flag exactly. A research view must never see a
	// layer the operator switched off.
	instStatus := r.attachInstitution(entries, toEnrich)

	for i, c := range list.Candidates {
		sd, fetched := byCode[c.Code]
		switch {
		case !fetched || len(sd.Candles) == 0:
			out[i].Status = StatusFetchError
			out[i].Reason = fmt.Sprintf("no price data for %s", c.CanonicalSymbol())
		default:
			e, ok := byEntry[c.Code]
			if !ok {
				// EnrichWatchlist dropped it — it needs 30 bars. Saying so beats vanishing.
				out[i].Status = StatusDataUnavailable
				out[i].Reason = fmt.Sprintf("only %d bars; the analyzers need at least 30",
					len(sd.Candles))
				break
			}
			out[i].Status = StatusOK
			out[i].Reason = ""
			out[i].Entry = e
			out[i].InstitutionStatus = instStatus
			if instStatus == Available && e.Institution == nil {
				// The layer ran and had no row for THIS stock — different from the layer
				// being off, and different from a zero net.
				out[i].InstitutionStatus = Unavailable
			}
		}
	}

	// Fundamentals are attached to EVERY candidate, including ones whose price data failed:
	// the financial statements come from a different source on a different schedule, and a
	// Yahoo outage says nothing about whether a company filed its quarterly report.
	r.attachFundamentals(out, asOfDate)
	r.attachValuation(out, asOfDate)
	r.attachMacro(out, asOfDate)
	return out
}

// attachFundamentals reads the archived fundamental view for each candidate.
//
// It only READS. cmd/fundamental-fetch produces the archive; a research run never fetches,
// because a live fetch during a historical run returns TODAY's figures and would label them
// with a past date — the exact look-ahead the point-in-time archive exists to prevent.
func (r *Resolver) attachFundamentals(ev []Evidence, asOfDate string) {
	if !r.Cfg.EnableFundamental {
		return // Fundamental stays nil; the view reports DISABLED.
	}
	svc := fundamental.NewService(nil, r.Cfg.FundamentalDir, r.Logf)
	for i := range ev {
		v, err := svc.LoadView(asOfDate, ev[i].Candidate.CanonicalSymbol())
		if err != nil {
			r.Logf("candidate: fundamentals for %s: %v", ev[i].Candidate.CanonicalSymbol(), err)
		}
		vc := v
		ev[i].Fundamental = &vc
	}
}

// attachValuation reads the archived price ratios for each candidate.
//
// Read-only, like the fundamentals: cmd/valuation-fetch produces the archive. A live fetch
// during a historical run would return today's P/E and file it under a past date, which is
// the same look-ahead the dated archive exists to prevent.
func (r *Resolver) attachValuation(ev []Evidence, asOfDate string) {
	if !r.Cfg.EnableValuation {
		return // Valuation stays nil; the view reports DISABLED.
	}
	svc := valuation.NewService(nil, r.Cfg.ValuationDir, r.Logf)
	for i := range ev {
		v, err := svc.LoadValuation(asOfDate, ev[i].Candidate.CanonicalSymbol())
		if err != nil {
			r.Logf("candidate: valuation for %s: %v", ev[i].Candidate.CanonicalSymbol(), err)
		}
		vc := v
		ev[i].Valuation = &vc
	}
}

// attachMacro loads the market-wide macro view ONCE and shares it.
//
// Read-only, like the other archives. A historical run reads what was archived on or before
// its date; it never fetches, because a live call returns today's Fed rate and would file it
// under a past date.
func (r *Resolver) attachMacro(ev []Evidence, asOfDate string) {
	if !r.Cfg.EnableMacro {
		return // Macro stays nil; the view reports DISABLED.
	}
	snap, err := macro.LoadAsOf(r.Cfg.MacroDir, asOfDate)
	if err != nil {
		r.Logf("candidate: macro: %v", err)
	}
	// Interpreted once. Every candidate then points at the SAME Research — one read, one
	// interpretation, and no way for two candidates in one run to disagree about the Fed.
	res := macro.Interpret(snap, asOfDate)
	for i := range ev {
		ev[i].Macro = &res
	}
}

// attachInstitution runs the existing institutional pass and reports what it could do.
func (r *Resolver) attachInstitution(entries []scanner.WatchlistEntry, stocks []fetcher.StockData) Availability {
	if !r.Cfg.Scanner.EnableInstitution {
		return Disabled
	}
	cfg := r.Cfg.Scanner.Institution.Defaulted()
	loaded, err := institution.LoadHistory(cfg.SnapshotDir,
		r.Cfg.ReportDate.Format("2006-01-02"), cfg.HistoryDays)
	if err != nil || loaded == nil {
		r.Logf("candidate: institution snapshots unavailable: %v", err)
		return Unavailable
	}
	// LoadHistory returns an EMPTY Loaded (not an error) when the directory is missing or
	// holds no snapshots — the loader's own graceful-degradation contract. Reporting that as
	// AVAILABLE would be the exact confusion this status exists to prevent: every stock would
	// then show "available, no institutional activity" when nothing was ever loaded.
	if len(loaded.Dates()) == 0 {
		r.Logf("candidate: institution enabled but no snapshots under %s", cfg.SnapshotDir)
		return Unavailable
	}
	candles := make(map[string][]fetcher.Candle, len(stocks))
	for _, s := range stocks {
		candles[s.Symbol] = s.Candles
	}
	scanner.AttachInstitution(entries, loaded, candles, r.Cfg.ReportDate, cfg)
	return Available
}

// LoadMarketContext reads the market dashboard snapshot for the run's date.
//
// A missing snapshot is UNAVAILABLE with no regime and no score — never a neutral default.
// This mirrors the contract R13-M3 established for the AI prompt: "we could not look" and
// "the market is unremarkable" are different facts.
func LoadMarketContext(regime string, score *float64, asOf string) MarketContext {
	if regime == "" {
		return MarketContext{Status: Unavailable}
	}
	return MarketContext{Status: Available, Regime: regime, Score: score, AsOfDate: asOf}
}

// LoadFXContext reads the currency archives for the run's date.
func LoadFXContext(dir, taiwanDate string) FXContext {
	out := FXContext{Status: Unavailable}
	cfg := fx.DefaultConfig()
	if s, err := fx.LoadSeries(dir, fx.SpecUSDTWD); err == nil {
		if m := s.MetricsAsOf(taiwanDate, cfg); m.Status.OK() {
			out.USDTWD = &m
		}
	}
	if s, err := fx.LoadSeries(dir, fx.SpecDXY); err == nil {
		if m := s.MetricsAsOf(taiwanDate, cfg); m.Status.OK() {
			out.DXY = &m
		}
	}
	if out.USDTWD != nil || out.DXY != nil {
		out.Status = Available
	}
	return out
}
