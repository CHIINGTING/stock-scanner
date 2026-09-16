package recon

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// Context says which per-session context is rebuilt.
//
// FullFidelity is the only value a reported result may use. EmptyContext exists to MEASURE
// what dropping the context would have cost (EP-9 Phase 2a deliverable 2) and nothing else:
// with no sector rotation, analyzeConsolidation receives inflow=false for every stock, so the
// plans are plans of a different scanner.
type Context string

const (
	// FullFidelity rebuilds sector rotation, the sector member panel and the full-market RS
	// table from the same truncated candles.
	FullFidelity Context = "FULL"
	// EmptyContext passes no sector map, no rotation, no members and no RS table.
	EmptyContext Context = "EMPTY"
)

// SessionPlan is one reconstructed stock-session: the production watchlist entry, with the
// production plan attached.
type SessionPlan struct {
	Entry scanner.WatchlistEntry
}

// SessionResult is one reconstructed session under one arm and one context.
type SessionResult struct {
	Date    string
	Arm     Arm
	Context Context

	// Universe is how many symbols traded on the session with enough history; Entries is how
	// many survived EnrichWatchlist's own 30-bar gate. The two differ and the funnel needs
	// both.
	Universe int
	Entries  []scanner.WatchlistEntry

	// Elapsed is wall time for the reconstruction only, excluding cache load.
	Elapsed time.Duration
	// Stages is where that time went. Measured per stage so "what dominates" is a
	// measurement rather than an assumption; the four sum to Elapsed minus the truncation
	// and the map build, which are the two cheap steps.
	Stages StageTimes
}

// StageTimes is the per-stage wall time of one reconstruction.
type StageTimes struct {
	Truncate time.Duration `json:"truncate"`
	Rotation time.Duration `json:"rotation"`
	RSTable  time.Duration `json:"rs_table"`
	Enrich   time.Duration `json:"enrich"`
	// AttachPlan is named AttachPlan and NOT EntryPlan on purpose. It is a DURATION, but the
	// repo-wide guard in internal/scanner/entryplan_symbol_guard_test.go attributes every
	// selector literally named EntryPlan to its enclosing declaration — its own doc says it
	// over-approximates that way — so a timing field with that name would make an innocent
	// timing read look like a plan read and would have to be allowlisted. Renaming the field
	// is cheaper and more honest than widening the allowlist for something that is not a plan.
	AttachPlan time.Duration `json:"attach_plan"`
}

// SectorPanel is the sector membership the rotation is computed over. It is the parsed
// configs/sectors.yaml, held once and re-sliced per session.
type SectorPanel struct {
	// Order is the sector order from the file.
	Order []string
	// Members maps sector name → member codes, in file order.
	Members map[string][]string
}

// NewSectorPanel flattens a fetcher.SectorList. Nil is legal and means "no sector file",
// which is what EmptyContext uses.
func NewSectorPanel(sl *fetcher.SectorList) *SectorPanel {
	if sl == nil {
		return nil
	}
	p := &SectorPanel{Members: map[string][]string{}}
	for _, sec := range sl.Sectors {
		p.Order = append(p.Order, sec.Name)
		for _, st := range sec.Stocks {
			p.Members[sec.Name] = append(p.Members[sec.Name], st.Code)
		}
	}
	return p
}

// Group distributes the session's truncated stocks back into their sectors, mirroring
// cmd/scanner/main.go:926-946 groupBySector. A stock may appear in several sectors; one that
// did not trade on the session is simply absent from its sector, which is the same thing
// production sees when a fetch returns nothing for it.
func (p *SectorPanel) Group(stocks []fetcher.StockData) ([]string, map[string][]fetcher.StockData) {
	byCode := make(map[string]fetcher.StockData, len(stocks))
	for _, d := range stocks {
		byCode[d.Symbol] = d
	}
	grouped := make(map[string][]fetcher.StockData, len(p.Order))
	for _, name := range p.Order {
		for _, code := range p.Members[name] {
			if d, ok := byCode[code]; ok {
				grouped[name] = append(grouped[name], d)
			}
		}
	}
	return p.Order, grouped
}

// SectorOf mirrors cmd/scanner/main.go:896-922 buildSectorOf: ranked order wins, then any
// sector the rotation skipped. Transcribed rather than imported because it lives in package
// main; the two are compared by TestSectorOfMatchesTheProductionRule.
func (p *SectorPanel) SectorOf(ranked []scanner.SectorRotation) map[string]string {
	out := map[string]string{}
	if p == nil {
		return out
	}
	for _, r := range ranked {
		for _, code := range p.Members[r.Name] {
			if _, ok := out[code]; !ok {
				out[code] = r.Name
			}
		}
	}
	for _, name := range p.Order {
		for _, code := range p.Members[name] {
			if _, ok := out[code]; !ok {
				out[code] = name
			}
		}
	}
	return out
}

// Reconstructor holds everything loaded once and reused across sessions.
type Reconstructor struct {
	Scanner *scanner.Scanner
	Cache   *Cache
	Sectors *SectorPanel
	Replay  *ReplayArchive

	// MinBars is the per-symbol history required at the signal session. See EligibilityBars.
	MinBars int
}

// EligibilityBars is the per-symbol minimum history this study requires at the signal
// session, and every term in it is read off production code rather than chosen:
//
//	30   EnrichWatchlist skips a stock outright below this (watchlist.go:187), and
//	     computeRocket returns WAIT below 30 candles.
//	61   analyzeConsolidation's PivotHigh reads the last min(60, n-1)+1 bars
//	     (consolidation.go:85 and :100-101, both branches), so 61 bars is where that
//	     reference stops being truncated by the start of the series.
//	100  Scanner.BuildRSTable's rs_min_history_days default in configs/config.yaml.
//
// 61 is the binding one for the PLAN, because the breakout pivot is an entryplan level. RS is
// shadow context and a stock below 100 bars simply has no RS row, which is a legal state.
//
// It is NOT 60 and NOT 20: MA20 and ATR(14) are satisfied well below it, and the repo's ATR
// is Wilder-smoothed and reads the whole series, so no bar count describes it at all
// (internal/fetcher/adjustment.go's WindowIsAdjustmentClean doc says exactly this and warns
// against inventing an "ATR_N reads N+1" rule).
const EligibilityBars = 61

// Reconstruct rebuilds one session end to end and returns the production watchlist entries
// with production plans attached.
//
// The call sequence is cmd/scanner's, in order: ScanRotation → buildSectorOf → BuildRSTable →
// EnrichWatchlist → AttachEntryPlan. Nothing is skipped and nothing is substituted.
//
// The one thing it does NOT reproduce is production's watchlist SELECTION. Production runs
// EnrichWatchlist over stocks.yaml's watchlist, which is machine-rebuilt from each scan; that
// file's current contents were chosen with knowledge of 2026 and using it as a historical
// universe would be survivorship selection of the worst kind. This runs the whole cached
// universe instead, the same choice EP-6F made.
func (r *Reconstructor) Reconstruct(date string, arm Arm, ctx Context) (SessionResult, error) {
	start := time.Now()
	res := SessionResult{Date: date, Arm: arm, Context: ctx}

	tt := time.Now()
	stocks := r.Cache.TruncateAt(date, r.minBars())
	res.Stages.Truncate = time.Since(tt)
	res.Universe = len(stocks)
	if len(stocks) == 0 {
		res.Elapsed = time.Since(start)
		return res, nil
	}

	market, _, err := r.Replay.Market(arm, date)
	if err != nil {
		return res, err
	}

	var sectorOf map[string]string
	var rotMap map[string]*scanner.SectorRotation
	var grouped map[string][]fetcher.StockData
	var rsTable map[string]scanner.RSResult

	if ctx == FullFidelity && r.Sectors != nil {
		t := time.Now()
		order, g := r.Sectors.Group(stocks)
		grouped = g
		rot := r.Scanner.ScanRotation(order, grouped)
		rotMap = make(map[string]*scanner.SectorRotation, len(rot))
		for i := range rot {
			rotMap[rot[i].Name] = &rot[i]
		}
		sectorOf = r.Sectors.SectorOf(rot)
		res.Stages.Rotation = time.Since(t)

		t = time.Now()
		rsTable = r.Scanner.BuildRSTable(stocks)
		res.Stages.RSTable = time.Since(t)
	}

	t := time.Now()
	entries := r.Scanner.EnrichWatchlist(stocks, sectorOf, rotMap, grouped, rsTable)
	res.Stages.Enrich = time.Since(t)

	candlesByCode := make(map[string][]fetcher.Candle, len(stocks))
	for _, s := range stocks {
		candlesByCode[s.Symbol] = s.Candles
	}
	// Valuation is deliberately NOT overlaid. data/valuation holds three dated directories
	// (2026-09-01/05/06) and valuation.LoadHistory filters on the archive directory name
	// (provider.go:296), so for every session this study can grade there is no archive dated
	// on or before it. Passing no overlay is the honest encoding of that, and it is what the
	// production bridge already does when no view matches (entryplan_attach.go:372).
	t = time.Now()
	scanner.AttachEntryPlan(entries, candlesByCode, market, true)
	res.Stages.AttachPlan = time.Since(t)

	res.Entries = entries
	res.Elapsed = time.Since(start)
	return res, nil
}

func (r *Reconstructor) minBars() int {
	if r.MinBars > 0 {
		return r.MinBars
	}
	return EligibilityBars
}

// Codes is the set of symbols the sector file lists. It is the denominator a fidelity
// measurement needs: only a listed symbol can receive a non-neutral sector flow direction, so
// only a listed symbol's consolidation can be moved by dropping the rotation.
func (p *SectorPanel) Codes() map[string]bool {
	out := map[string]bool{}
	if p == nil {
		return out
	}
	for _, codes := range p.Members {
		for _, c := range codes {
			out[c] = true
		}
	}
	return out
}
