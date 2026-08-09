package service

import (
	"context"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/analyzer"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// The daily Market Dashboard pipeline (M8).
//
//	.cache (benchmark + universe)  +  TWSE  +  TAIFEX
//	                    ↓
//	              RawSnapshot
//	                    ↓
//	               Analyzers  →  Evidence
//	                    ↓
//	   StructureView → DecideRegime        Score / Confidence
//	                    ↓
//	                 Snapshot  →  data/market/market_<date>.json
//
// SHADOW-ONLY: nothing here touches stock scoring, BUY/WATCH/SELL, stage analysis,
// candlestick, or any ranking. The market package still imports no scanner code at all, and
// a test in the provider package enforces the one-way data flow.

// NetProvider is the part of the pipeline that talks to an exchange. Both TWSE and TAIFEX
// satisfy it; a test double satisfies it too, which is how the pipeline is tested without a
// network.
type NetProvider interface {
	Name() string
	Fetch(ctx context.Context, date string) (*model.RawSnapshot, error)
}

// Pipeline assembles one trading day.
type Pipeline struct {
	Cfg model.MarketConfig
	// Feed supplies the two local dimensions. Required — price and breadth are what the
	// regime is built on, and without them the day can only ever be UNKNOWN.
	Feed provider.CacheFeed
	// Net are the exchange providers. Any may be absent; each missing one simply leaves its
	// evidence unavailable.
	Net []NetProvider
	// HistoryDays is how far back to read stored snapshots for the institutional context
	// windows. Zero → 60, enough for the futures crowding percentile's own floor.
	HistoryDays int

	logf func(string, ...any)
	now  func() time.Time
}

// NewPipeline builds a Pipeline with defaults applied.
func NewPipeline(cfg model.MarketConfig, feed provider.CacheFeed, net ...NetProvider) *Pipeline {
	return &Pipeline{
		Cfg: cfg.Defaulted(), Feed: feed, Net: net,
		logf: func(string, ...any) {}, now: time.Now,
	}
}

// WithLogger attaches a logger; nil disables logging.
func (p *Pipeline) WithLogger(logf func(string, ...any)) *Pipeline {
	if logf != nil {
		p.logf = logf
	}
	return p
}

// Run builds the snapshot for date (YYYY-MM-DD) WITHOUT writing it.
//
// It never returns an error for a partial day: a dead exchange endpoint leaves its evidence
// unavailable, lowers confidence and is recorded in the snapshot. An error means the day
// itself could not be assembled at all — the local price cache is unusable, which is the one
// case where there is nothing worth persisting.
func (p *Pipeline) Run(ctx context.Context, date string) (*model.Snapshot, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("market: bad date %q: %w", date, err)
	}
	snap := model.NewSnapshot(date, p.now())

	// ── 1. local: benchmark + universe, as-of the requested date ─────────────────
	benchmark := p.Cfg.Benchmark
	if benchmark == "" {
		benchmark = model.Benchmark0050
	}
	bars, err := p.Feed.Benchmark(string(benchmark), date)
	if err != nil {
		return nil, fmt.Errorf("market: benchmark %s unavailable: %w", benchmark, err)
	}
	uni, err := p.Feed.Universe(date)
	if err != nil {
		return nil, fmt.Errorf("market: universe unavailable: %w", err)
	}
	snap.Raw.Sources = append(snap.Raw.Sources, model.SourceMeta{
		Name: "cache/" + string(benchmark), Status: model.SourceOK,
		FetchedAt: p.now(), AsOfDate: bars[len(bars)-1].Date,
	})

	// ── 2. exchanges, each isolated ──────────────────────────────────────────────
	snap.Raw.Date, snap.Raw.FetchedAt = date, p.now()
	for _, np := range p.Net {
		part, err := np.Fetch(ctx, date)
		if err != nil {
			p.logf("market: %s fetch failed: %v", np.Name(), err)
			snap.Raw.Sources = append(snap.Raw.Sources, model.SourceMeta{
				Name: np.Name(), Status: model.SourceError, Err: err.Error(), FetchedAt: p.now(),
			})
			continue
		}
		snap.Raw.Merge(part)
	}

	// ── 3. price + breadth evidence, and the structural layers ───────────────────
	th := p.Cfg.Structure
	priceEv, pview := analyzer.AnalyzePrice(benchmark, bars, th)
	panel := analyzer.BuildBreadthPanel(uni.Dates, uni.Closes)
	nearHigh := pview.Sufficient && pview.DDHigh <= th.Defaulted().NearHighDDPct
	breadthEv, bview := analyzer.AnalyzeBreadth(panel, indexOfDate(panel.Dates, date), nearHigh, th)

	// ── 4. institutional evidence, with whatever history the archive holds ───────
	hist := p.loadHistory(date, bars)
	futEv := analyzer.AnalyzeForeignFutures(snap.Raw.Futures, hist, p.Cfg.Futures)
	cashEv := analyzer.AnalyzeForeignCash(snap.Raw.Cash, hist)
	marginEv := analyzer.AnalyzeMarginBalance(snap.Raw.Margin, hist)
	posture := analyzer.CombinePosture(futEv, cashEv, marginEv)

	snap.Evidence = []model.Evidence{priceEv, breadthEv, cashEv, futEv, marginEv}

	// ── 5. structure → regime (score is NOT an input) ────────────────────────────
	view := analyzer.BuildStructureView(pview, bview, posture, th)
	snap.ApplyDecision(analyzer.DecideRegime(view, th))
	snap.Flags = analyzer.Flags(pview, bview, th)

	// ── 6. score + confidence (independent of the regime) ────────────────────────
	sc := analyzer.Score(snap.Evidence, p.Cfg.Weights)
	cf := analyzer.Confidence(snap.Evidence, p.Cfg.Weights)
	snap.Score, snap.Confidence = sc.Score, cf.Confidence

	// ── 7. everything needed to reconstruct the day ──────────────────────────────
	snap.Reasons = append(snap.Reasons,
		fmt.Sprintf("Score %.0f（可用權重 %.0f/%.0f）", sc.Score, sc.UsedWeight, sc.TotalWeight),
		fmt.Sprintf("Confidence %.2f = 完整度 %.2f × 一致性 %.2f × 強度 %.2f",
			cf.Confidence, cf.Completeness, cf.Agreement, cf.Conviction))
	for _, c := range sc.Contributions {
		snap.Reasons = append(snap.Reasons,
			fmt.Sprintf("  %s 貢獻 %+.0f（子分 %.0f × 有效權重 %.1f）", c.Source, c.Pull, c.SubScore, c.Effective))
	}
	snap.Caveats = append(snap.Caveats, cf.Caveats...)
	for _, m := range analyzer.MissingSources(snap.Evidence) {
		snap.Caveats = append(snap.Caveats, "缺漏證據 "+m)
	}
	for _, c := range analyzer.ConflictingSources(snap.Evidence, p.Cfg.Weights) {
		snap.Caveats = append(snap.Caveats, "反向證據 "+c)
	}
	if len(uni.DroppedDates) > 0 {
		snap.Caveats = append(snap.Caveats,
			fmt.Sprintf("universe 有 %d 個低覆蓋日期被排除在日期軸外", len(uni.DroppedDates)))
	}
	return snap, nil
}

// RunAndSave builds the day and persists it. Validation happens inside SaveSnapshot, so a
// malformed snapshot never reaches the archive.
func (p *Pipeline) RunAndSave(ctx context.Context, date string) (*model.Snapshot, string, error) {
	snap, err := p.Run(ctx, date)
	if err != nil {
		return nil, "", err
	}
	path, err := SaveSnapshot(p.Cfg.StorageDir, snap)
	if err != nil {
		return snap, "", err
	}
	return snap, path, nil
}

// loadHistory reads the stored archive backwards from date to assemble the trailing series
// the institutional analyzers use for context.
//
// Only what is actually on disk is returned: an archive that starts today yields empty
// windows, the analyzers degrade to lower confidence, and no value is invented. Benchmark
// returns come from the price cache instead, which always has depth.
func (p *Pipeline) loadHistory(date string, bars []model.Bar) analyzer.History {
	days := p.HistoryDays
	if days <= 0 {
		days = 60
	}
	var h analyzer.History

	// Benchmark daily returns, oldest-first, ending on the session before `date`.
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Close > 0 {
			h.BenchmarkRet = append(h.BenchmarkRet, (bars[i].Close/bars[i-1].Close-1)*100)
		}
	}
	if n := len(h.BenchmarkRet); n > days {
		h.BenchmarkRet = h.BenchmarkRet[n-days:]
	}

	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return h
	}
	// Walk back over calendar days, collecting the snapshots that exist. Ascending order is
	// restored at the end because the analyzers expect oldest-first.
	var futs, cash, margin []float64
	for back := 1; back <= days*2 && len(futs) < days; back++ {
		prev := d.AddDate(0, 0, -back).Format("2006-01-02")
		s, err := LoadSnapshot(p.Cfg.StorageDir, prev)
		if err != nil {
			continue
		}
		if s.Raw.Futures != nil {
			futs = append(futs, s.Raw.Futures.NetOI)
		}
		if s.Raw.Cash != nil {
			cash = append(cash, s.Raw.Cash.ForeignNet)
		}
		if s.Raw.Margin != nil {
			margin = append(margin, s.Raw.Margin.MarginBalanceLots)
		}
	}
	h.FuturesNetOI, h.ForeignNet, h.MarginLots = reverse(futs), reverse(cash), reverse(margin)
	return h
}

func reverse(in []float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// indexOfDate binary-searches an ascending date axis, returning -1 when absent.
func indexOfDate(dates []string, d string) int {
	lo, hi := 0, len(dates)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case dates[mid] == d:
			return mid
		case dates[mid] < d:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return -1
}

// reDecide re-runs the regime engine from a persisted StructureView. It exists so a stored
// snapshot can be re-judged — and so tests can prove the verdict never depended on the score.
func reDecide(v model.StructureView) model.Regime {
	return analyzer.DecideRegime(v, model.StructureThresholds{}.Defaulted()).Regime
}
