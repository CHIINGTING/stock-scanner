package research

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// Config is the R13 research block of the scanner config. Default OFF: with Enabled false
// nothing is opened, nothing is written, and the scan is byte-identical to a build without
// this package.
type Config struct {
	Enabled bool         `yaml:"enabled"`
	Store   store.Config `yaml:"store"`
	// MarketSnapshotDir is where cmd/market-fetch already writes its snapshots. The caller
	// loads the snapshot and passes the regime in as MarketContext; this package never
	// imports internal/market. Empty → "data/market".
	MarketSnapshotDir string `yaml:"market_snapshot_dir"`
	// FXDir is where cmd/fx-fetch archives the USD/TWD series. Read-only from the scanner's
	// side: the scan never fetches a currency, it only reads what acquisition already stored.
	// Empty → "data/fx".
	FXDir string `yaml:"fx_dir"`
	// FundamentalDir is where cmd/fundamental-fetch archives dated MOPS snapshots. Read-only
	// from every consumer: a research run reads history, it never fetches it.
	// Empty → "data/fundamental".
	FundamentalDir string `yaml:"fundamental_dir"`
	// ValuationDir is where cmd/valuation-fetch archives the exchanges' published price
	// ratios. Read-only from every consumer, same as the others.
	// Empty → "data/valuation".
	ValuationDir string `yaml:"valuation_dir"`
	// MacroDir is where cmd/macro-fetch archives the point-in-time macro view.
	// Empty → "data/macro".
	MacroDir string `yaml:"macro_dir"`
}

// Defaulted fills zero values, matching the Defaulted() convention used across this repo.
func (c Config) Defaulted() Config {
	c.Store = c.Store.Defaulted()
	if c.MarketSnapshotDir == "" {
		c.MarketSnapshotDir = "data/market"
	}
	if c.FXDir == "" {
		c.FXDir = "data/fx"
	}
	if c.FundamentalDir == "" {
		c.FundamentalDir = "data/fundamental"
	}
	if c.ValuationDir == "" {
		c.ValuationDir = "data/valuation"
	}
	if c.MacroDir == "" {
		c.MacroDir = "data/macro"
	}
	return c
}

// RunMeta identifies one scan execution.
type RunMeta struct {
	// RunUID is the external id. Empty → derived from the trading date and start time.
	RunUID      string
	TradingDate string // YYYY-MM-DD, the ANALYSIS date, not the wall clock
	StartedAt   time.Time
	// ScannerVersion identifies the build. Empty → read from the embedded VCS stamp, or
	// left empty when the binary carries none. An unknown build is recorded as unknown.
	ScannerVersion string
	Note           string
}

// Input is the canonical in-memory scan result.
//
// These are THE results the report and the analysis-history sidecar are built from — the
// same values, passed by value from the same place in one scan. This package must never be
// fed by re-reading the sidecar JSON: that would make the store a second translation of a
// second source, and the two could then disagree about the same day.
type Input struct {
	Watchlist []scanner.WatchlistEntry
	Market    []scanner.StockAnalysis
	Portfolio []scanner.StockAnalysis
	// Rotation is the ranked sector panel; entries are matched to stocks by sector name.
	Rotation []scanner.SectorRotation
	// MarketCtx is the top-level regime read, empty when the market dashboard has not run.
	MarketCtx MarketContext
	// UniverseSize is how many stocks the market scan actually examined, for the run record.
	UniverseSize int
}

// Result reports what one RecordScan call persisted.
type Result struct {
	RunID     int64
	RunUID    string
	Snapshots int
	Evidence  int
	// Skipped counts stocks whose own write failed. They are logged and stepped over: one
	// unwritable row must not cost the whole scan's record.
	Skipped int
	// WatchlistSnapshots maps symbol → stock_snapshots.id for the watchlist tab.
	//
	// This is the handle R13-M3 takes: every agent in a run is pointed at these ids, so
	// "all agents in one run see the same snapshot" is a consequence of there being exactly
	// one id per stock rather than of each agent re-reading the scanner.
	WatchlistSnapshots map[string]int64
}

// Recorder writes scan results into the research store.
//
// Shadow-only in the strongest sense available: it takes the scan results by value, returns
// nothing the scanner reads, and has no method that could alter a score, an action, a
// ranking or a report. The dependency points one way — research imports scanner, never the
// reverse.
type Recorder struct {
	st   *store.Store
	logf func(string, ...any)
}

// NewRecorder wraps an open store. logf may be nil.
func NewRecorder(st *store.Store, logf func(string, ...any)) *Recorder {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Recorder{st: st, logf: logf}
}

// Open opens the store described by cfg and returns a recorder plus a close function.
//
// When cfg.Enabled is false it returns (nil, no-op close, nil): callers can therefore write
// the wiring once, unconditionally, and a disabled feature costs one nil check rather than a
// branch around every call site. A nil *Recorder's methods are safe no-ops.
func Open(cfg Config) (*Recorder, func() error, error) {
	noop := func() error { return nil }
	if !cfg.Enabled {
		return nil, noop, nil
	}
	cfg = cfg.Defaulted()
	st, err := store.Open(cfg.Store)
	if err != nil {
		return nil, noop, err
	}
	return NewRecorder(st, nil), st.Close, nil
}

// WithLogger returns the recorder with a log sink attached. Safe on nil.
func (r *Recorder) WithLogger(logf func(string, ...any)) *Recorder {
	if r == nil {
		return nil
	}
	if logf != nil {
		r.logf = logf
	}
	return r
}

// Store exposes the underlying store for the later work items (agents, judge, outcomes).
func (r *Recorder) Store() *store.Store {
	if r == nil {
		return nil
	}
	return r.st
}

// RecordScan persists one scan: the run, one snapshot per stock per tab, that stock's
// evidence, and the scanner's own decision.
//
// A nil Recorder returns a zero Result and no error, so a disabled feature needs no branch
// at the call site.
//
// Failure policy matches every other shadow layer in this repo: a per-stock failure is
// logged and skipped, and the caller is expected to log a returned error and carry on. The
// scan and the report must complete whatever this package does.
func (r *Recorder) RecordScan(ctx context.Context, meta RunMeta, in Input) (Result, error) {
	if r == nil || r.st == nil {
		return Result{}, nil
	}
	if meta.TradingDate == "" {
		return Result{}, errors.New("research: scan needs a trading date")
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	}
	if meta.RunUID == "" {
		meta.RunUID = fmt.Sprintf("scan-%s-%d", meta.TradingDate, meta.StartedAt.UTC().UnixNano())
	}
	if meta.ScannerVersion == "" {
		meta.ScannerVersion = BuildVersion()
	}

	run, err := r.st.Scans().StartRun(ctx, store.ScanRun{
		RunUID:         meta.RunUID,
		TradingDate:    meta.TradingDate,
		StartedAt:      meta.StartedAt,
		ScannerVersion: meta.ScannerVersion,
		MarketRegime:   in.MarketCtx.Regime,
		MarketScore:    in.MarketCtx.Score,
		UniverseSize:   in.UniverseSize,
		Note:           meta.Note,
	})
	if err != nil {
		return Result{}, err
	}

	res := Result{RunID: run.ID, RunUID: run.RunUID, WatchlistSnapshots: map[string]int64{}}
	rotIndex := indexRotation(in.Rotation)
	byCat := map[string]int{}

	// Watchlist first: it is the tab that carries every shadow layer, and the one the
	// multi-agent work items operate on.
	for _, e := range in.Watchlist {
		var c collector
		buildWatchlistEvidence(&c, e, rotIndex[e.Sector], in.MarketCtx)
		countByCategory(byCat, c.items)
		id, ok := r.persist(ctx, run, snapshotFrom(e, run.TradingDate), c.items, &res)
		if ok {
			res.WatchlistSnapshots[e.A.Symbol] = id
		}
	}
	for _, a := range in.Market {
		var c collector
		buildAnalysisEvidence(&c, a)
		buildMarketEvidence(&c, in.MarketCtx)
		countByCategory(byCat, c.items)
		r.persist(ctx, run, snapshotFromAnalysis(a, run.TradingDate, store.TabMarket), c.items, &res)
	}
	for _, a := range in.Portfolio {
		var c collector
		buildAnalysisEvidence(&c, a)
		buildMarketEvidence(&c, in.MarketCtx)
		countByCategory(byCat, c.items)
		r.persist(ctx, run, snapshotFromAnalysis(a, run.TradingDate, store.TabPositions), c.items, &res)
	}

	if err := r.st.Scans().FinishRun(ctx, run.ID, time.Now().UTC()); err != nil {
		// The rows are already written and correct; only the completion stamp is missing.
		// Reporting it loudly beats losing the run record over a bookkeeping failure.
		r.logf("research: run %s recorded but not stamped complete: %v", run.RunUID, err)
	}
	r.logf("research: run %s — %d snapshots, %d evidence rows, %d skipped (%s)",
		run.RunUID, res.Snapshots, res.Evidence, res.Skipped, describeCounts(byCat))
	return res, nil
}

// persist writes one snapshot plus its evidence and the scanner's decision, counting the
// result. A failure is logged and reported as a skip rather than aborting the run.
func (r *Recorder) persist(ctx context.Context, run store.ScanRun, snap store.StockSnapshot,
	items []store.Evidence, res *Result) (int64, bool) {

	snap.ScanRunID = run.ID
	saved, err := r.st.Scans().SaveSnapshot(ctx, snap)
	if err != nil {
		r.logf("research: skipping %s/%s: %v", snap.Symbol, snap.SourceTab, err)
		res.Skipped++
		return 0, false
	}
	res.Snapshots++

	n, err := r.st.Evidence().SaveMany(ctx, saved.ID, items)
	if err != nil {
		// The snapshot stands; only its evidence is short. Saying so is more useful than
		// pretending the stock was never scanned.
		r.logf("research: %s evidence incomplete: %v", snap.Symbol, err)
	}
	res.Evidence += n

	// The scanner's own verdict, mirrored verbatim. This is what every later accuracy
	// comparison measures the AI against, so it is recorded even when the AI never runs.
	if snap.ScannerAction != "" {
		if _, err := r.st.Analyses().SaveScannerDecision(ctx, saved.ID, snap.ScannerAction); err != nil {
			r.logf("research: %s scanner decision not recorded: %v", snap.Symbol, err)
		}
	}
	return saved.ID, true
}

// snapshotFrom projects a watchlist entry onto its snapshot row.
func snapshotFrom(e scanner.WatchlistEntry, tradingDate string) store.StockSnapshot {
	snap := snapshotFromAnalysis(e.A, tradingDate, store.TabWatchlist)
	rocket := e.RocketScore
	snap.RocketScore = &rocket
	snap.RocketStage = string(e.RocketStage)
	snap.WatchAction = string(e.WatchAction)
	snap.Sector = e.Sector
	return snap
}

// snapshotFromAnalysis projects the fields every scanned stock has.
func snapshotFromAnalysis(a scanner.StockAnalysis, tradingDate, tab string) store.StockSnapshot {
	snap := store.StockSnapshot{
		Symbol:        a.Symbol,
		Name:          a.Name,
		TradingDate:   tradingDate,
		SourceTab:     tab,
		ScannerAction: string(a.Action),
	}
	// Close is the basis every future outcome is measured from. A zero price is not a price:
	// it stays nil so no outcome can ever be computed against it.
	if a.Close > 0 {
		px := a.Close
		snap.Close = &px
	}
	score := float64(a.Score)
	snap.ScannerScore = &score
	return snap
}

// indexRotation keys the ranked sector panel by name for per-stock lookup.
func indexRotation(rows []scanner.SectorRotation) map[string]*scanner.SectorRotation {
	out := make(map[string]*scanner.SectorRotation, len(rows))
	for i := range rows {
		out[rows[i].Name] = &rows[i]
	}
	return out
}

// BuildVersion returns the VCS revision Go embeds in the binary, suffixed with "-dirty" when
// the tree was modified. It returns "" when the binary carries no stamp (as `go test`
// binaries and -buildvcs=false builds do), because an unknown build must be recorded as
// unknown rather than guessed.
func BuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		return rev + "-dirty"
	}
	return rev
}
