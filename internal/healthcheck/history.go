package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/store"
)

// Persisting a health check, so that a judgement made today can be graded later.
//
// # It reuses the R13 schema rather than building a second one
//
// scan_runs → stock_snapshots → evidence / analysis_runs → agent_analysis → outcomes is
// already the shape of "a judgement about a stock at a point in time, and what happened
// next". A second set of tables would mean a second outcome pipeline, a second definition of
// forward return, and two answers to "was this call right".
//
// # How a health check is marked, and why that matters
//
// Every row this writes is tagged:
//
//	stock_snapshots.source_tab  = "health_check"   (scans use watchlist / market / positions)
//	scan_runs.note              = HistoryNote
//	scan_runs.scanner_version   = HistoryVersion
//
// This matters because internal/store's own queries do NOT all filter on source_tab —
// OutcomeRepo.PendingHorizon, for one, selects every snapshot with an unfilled horizon. That
// is CORRECT for outcomes: a forward return is measured the same way whoever made the call,
// and having health checks graded is the entire point of M10. It is NOT correct for accuracy
// statistics over the scanner's own decisions, and any such query has to exclude
// source_tab = "health_check" or it will be scoring a different population than it thinks.
//
// That is a real constraint this layer imposes on its neighbours, so it is stated here rather
// than left to be discovered. Two specifics, because the general advice is not enough:
//
//	scan_runs has NO source_tab column. Every check opens its own run (universe_size = 1), so
//	ScanRepo.RunsByDate returns them alongside real scans and CANNOT be filtered the way
//	snapshots can. A caller that wants scans only must filter on scanner_version != the
//	HistoryVersion here, or on note.
//
//	One stock can hold several snapshots for one day. UNIQUE(scan_run_id, symbol, source_tab)
//	dedupes within a run, and every check gets a fresh run — so pressing the button three
//	times leaves three snapshots with the same symbol and trading_date. That is the honest
//	record of three judgements, but any aggregate over health checks MUST dedupe (latest per
//	symbol per day is the obvious rule) or it counts one opinion three times. M10 has to
//	decide this before it computes anything; it is written here because M10 will otherwise
//	inherit it silently.
//
// # No decision row is ever written
//
// The decisions table holds the scanner's action and the R13 judge's verdict, and R13-M7
// scores those two against each other. A health check is neither. Writing into it would put
// rows into a population somebody else is counting, and the entry assessment is not the
// scanner's decision however similar it may look. The assessment is stored as EVIDENCE
// instead, under its own category, where it is queryable without being counted as somebody
// else's call.

// Marks that identify rows written by this layer.
const (
	// TabHealthCheck is the source_tab every health-check snapshot carries.
	TabHealthCheck = "health_check"
	// HistoryNote goes on the scan_run so the row is self-describing in a SQL client.
	HistoryNote = "R14 interactive health check (sidecar; not a scan)"
	// HistoryVersion identifies this writer.
	HistoryVersion = "r14-health-dashboard"
	// AgentHealthJudge is the agent name for the AI reading.
	AgentHealthJudge = "HEALTH_JUDGE"
	// PromptSetVersion identifies the judge's instruction + schema pair. Bump it when either
	// changes, because that is the granularity at which two readings stop being comparable.
	PromptSetVersion = "r14-judge-v1"
)

// Evidence categories. The first four reuse internal/store's own vocabulary; the rest are
// new because R13 had no valuation, target or assessment evidence to name.
const (
	CategoryPrice       = "price"
	CategoryValuation   = "valuation"
	CategoryFundamental = "fundamental"
	CategoryTarget      = "target_price"
	CategoryAssessment  = "assessment"
)

// HistoryConfig controls persistence.
//
// Enabled defaults to FALSE. Writing into a database another system reads is not something to
// switch on by accident, and a dashboard that answers questions is useful without it.
type HistoryConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// History writes health checks into the R13 database.
type History struct {
	store *store.Store
}

// OpenHistory opens the database and migrates it, exactly as R13 does.
func OpenHistory(cfg HistoryConfig) (*History, error) {
	s, err := store.Open(store.Config{Path: cfg.Path})
	if err != nil {
		return nil, fmt.Errorf("healthcheck: open history: %w", err)
	}
	return &History{store: s}, nil
}

// Close releases the database.
func (h *History) Close() error {
	if h == nil || h.store == nil {
		return nil
	}
	return h.store.Close()
}

// Save records one health check and returns the stock_snapshot id it is anchored to.
//
// The snapshot id is what M10 needs: an outcome is measured from a snapshot's close, so the
// id is the handle by which "was this call right" is later answered.
func (h *History) Save(ctx context.Context, hc *Health) (int64, error) {
	if h == nil || h.store == nil {
		return 0, fmt.Errorf("healthcheck: no history store")
	}
	if hc == nil {
		return 0, fmt.Errorf("healthcheck: nothing to save")
	}
	now := time.Now().UTC()

	run, err := h.store.Scans().StartRun(ctx, store.ScanRun{
		RunUID:         runUID(hc, now),
		TradingDate:    hc.AsOf,
		StartedAt:      now,
		ScannerVersion: HistoryVersion,
		MarketRegime:   hc.Market.Regime,
		MarketScore:    floatOrNil(hc.Market.Score),
		UniverseSize:   1,
		Note:           HistoryNote,
	})
	if err != nil {
		return 0, fmt.Errorf("healthcheck: start run: %w", err)
	}

	snap, err := h.store.Scans().SaveSnapshot(ctx, store.StockSnapshot{
		ScanRunID:   run.ID,
		Symbol:      hc.Identity.Symbol,
		Name:        hc.Identity.Name,
		TradingDate: hc.AsOf,
		SourceTab:   TabHealthCheck,
		// Close is the basis every future outcome is measured from. Nil when there was no
		// usable price, in which case no outcome can ever be computed for this row — which
		// is the honest record, not a zero.
		Close:         floatOrNil(hc.Price.Close),
		ScannerAction: hc.Technical.ScannerAction,
		ScannerScore:  intAsFloat(hc.Technical.ScannerScore),
		RocketScore:   intOrNil(hc.Technical.RocketScore),
		RocketStage:   hc.Technical.Stage,
		WatchAction:   hc.Technical.WatchAction,
		Sector:        hc.Identity.Industry,
		CreatedAt:     now,
	})
	if err != nil {
		return 0, fmt.Errorf("healthcheck: save snapshot: %w", err)
	}

	if _, err := h.store.Evidence().SaveMany(ctx, snap.ID, evidenceRows(hc)); err != nil {
		return snap.ID, fmt.Errorf("healthcheck: save evidence: %w", err)
	}

	if err := h.saveAI(ctx, snap.ID, hc, now); err != nil {
		return snap.ID, err
	}

	// Open the outcome row NOW, with this check's own close as the basis.
	//
	// Two reasons it belongs here and not in the grader. The basis price is a property of the
	// judgement — it is what the reader saw — and fixing it at save time means a later grading
	// pass cannot choose a different one. And OutcomeRepo.PendingHorizon joins outcomes, so a
	// snapshot with no row is invisible to it: without this the grader would find nothing,
	// forever, and the silence would look like "no results yet".
	//
	// Ensure is idempotent and refuses to move an existing base, so a re-save cannot redefine
	// a measurement already under way. A snapshot with no usable close gets no row at all,
	// which is correct: no outcome can ever be measured from it, and an outcome row with a
	// zero basis would divide by it.
	if base, ok := hc.Price.Close.Float(); ok && base > 0 {
		if _, err := h.store.Outcomes().Ensure(ctx, snap.ID, base); err != nil {
			return snap.ID, fmt.Errorf("healthcheck: open outcome: %w", err)
		}
	}

	if err := h.store.Scans().FinishRun(ctx, run.ID, time.Now().UTC()); err != nil {
		return snap.ID, fmt.Errorf("healthcheck: finish run: %w", err)
	}
	return snap.ID, nil
}

// saveAI records the judge's reading, INCLUDING its failures.
//
// A run where the model was unreachable is recorded with that status rather than omitted: a
// later reader asking "what did the AI say about this" must be able to tell "nothing, it was
// down" from "we never asked".
func (h *History) saveAI(ctx context.Context, snapshotID int64, hc *Health, now time.Time) error {
	run, err := h.store.Analyses().StartAnalysisRun(ctx, store.AnalysisRun{
		StockSnapshotID:     snapshotID,
		RunUID:              runUID(hc, now),
		Mode:                store.ModeLive,
		OrchestratorVersion: HistoryVersion,
		PromptSetVersion:    PromptSetVersion,
		Status:              store.RunStatusRunning,
		StartedAt:           now,
	})
	if err != nil {
		return fmt.Errorf("healthcheck: start analysis run: %w", err)
	}

	blob, err := json.Marshal(hc.AI)
	if err != nil {
		blob = nil
	}
	// Verdict stays EMPTY. The judge produces prose, not a call — the verdict on this page is
	// the deterministic assessment, and putting it here would attribute it to the model.
	if _, err := h.store.Analyses().SaveAgentAnalysis(ctx, store.AgentAnalysis{
		AnalysisRunID: run.ID,
		Agent:         AgentHealthJudge,
		Reasoning:     hc.AI.Summary,
		Model:         hc.AI.Model,
		Status:        aiStatus(hc.AI.Status),
		OutputJSON:    string(blob),
		CreatedAt:     now,
	}); err != nil {
		return fmt.Errorf("healthcheck: save agent analysis: %w", err)
	}

	status := store.RunStatusComplete
	if hc.AI.Status != StatusAvailable {
		status = store.RunStatusFailed
	}
	if err := h.store.Analyses().FinishAnalysisRun(ctx, run.ID, status, time.Now().UTC()); err != nil {
		return fmt.Errorf("healthcheck: finish analysis run: %w", err)
	}
	return nil
}

// aiStatus maps this package's vocabulary onto internal/ai's, which is what the agent_analysis
// column already holds for R11 and R13 rows.
func aiStatus(s Status) string {
	switch s {
	case StatusAvailable:
		return "OK"
	case StatusDisabled:
		return "DISABLED"
	default:
		return "ERROR"
	}
}

// runUID is stable within one save and identifiable in a SQL client.
func runUID(hc *Health, now time.Time) string {
	return fmt.Sprintf("r14-health-%s-%s-%d", hc.Identity.Symbol, hc.AsOf, now.UnixNano())
}

// evidenceRows flattens every deterministic figure into (category, key) rows.
//
// A missing figure produces NO ROW — the same contract the rest of this package keeps. There
// is no zero, and no "UNAVAILABLE" text masquerading as a value: a key that is absent is
// absent, which is exactly what makes a later query able to tell "we had no P/E" from "the
// P/E was low".
func evidenceRows(hc *Health) []store.Evidence {
	var out []store.Evidence
	num := func(category, key string, v Value) {
		if f, ok := v.Float(); ok {
			out = append(out, store.Num(category, key, f, v.Unit, v.Source))
		}
	}
	text := func(category, key, v, source string) {
		if v != "" {
			out = append(out, store.Text(category, key, v, source))
		}
	}

	// identity and timing — what this judgement was about, and as of when
	text(CategoryPrice, "as_of", hc.AsOf, HistoryVersion)
	text(CategoryPrice, "bar_date", hc.Price.BarDate, hc.Price.Source)
	text(CategoryPrice, "canonical_symbol", hc.Identity.CanonicalSymbol, "stockcatalog")
	text(CategoryPrice, "data_quality", string(hc.DataQuality.Level), HistoryVersion)
	num(CategoryPrice, "close", hc.Price.Close)
	num(CategoryPrice, "change_pct", hc.Price.ChangePct)
	num(CategoryPrice, "volume_ratio", hc.Price.VolumeRatio)

	num(store.CategoryTechnical, "ma20", hc.Technical.MA20)
	num(store.CategoryTechnical, "bias20", hc.Technical.MA20DistancePct)
	num(store.CategoryTechnical, "rsi", hc.Technical.RSI)
	num(store.CategoryTechnical, "atr", hc.Technical.ATR)
	text(store.CategoryTechnical, "scanner_action", hc.Technical.ScannerAction, scannerSource)
	// hasRisk, NOT "non-empty". The scanner writes "—" when a stock has no risk label, and
	// persisting that would put a full-width dash in the risk category of every unflagged
	// stock — contradicting this file's own contract that an absent key is absent, and
	// poisoning every aggregate M10 runs over it.
	if hasRisk(hc.Technical.RiskLabel) {
		text(store.CategoryRisk, "risk_label", hc.Technical.RiskLabel, scannerSource)
	}
	text(store.CategoryRisk, "limit_status", hc.Technical.LimitStatus, scannerSource)

	text(store.CategoryMarket, "regime", hc.Market.Regime, "internal/market")
	num(store.CategoryMarket, "score", hc.Market.Score)

	num(CategoryFundamental, "revenue", hc.Fundamental.Revenue)
	num(CategoryFundamental, "revenue_yoy", hc.Fundamental.RevenueYoY)
	num(CategoryFundamental, "gross_margin", hc.Fundamental.GrossMargin)
	num(CategoryFundamental, "operating_margin", hc.Fundamental.OperatingMargin)
	num(CategoryFundamental, "net_margin", hc.Fundamental.NetMargin)
	// EPS keys are named for their BASIS. "eps" alone would be the exact ambiguity this
	// repo spent M4 removing.
	num(CategoryFundamental, "eps_cumulative", hc.Fundamental.EPS.Cumulative)
	num(CategoryFundamental, "eps_quarter", hc.Fundamental.EPS.Quarter)
	num(CategoryFundamental, "eps_ttm", hc.Fundamental.EPS.TTM)
	text(CategoryFundamental, "eps_period", hc.Fundamental.EPS.Period, fundSource)

	num(CategoryValuation, "trailing_pe", hc.Valuation.TrailingPE)
	num(CategoryValuation, "pb_ratio", hc.Valuation.PBRatio)
	num(CategoryValuation, "dividend_yield", hc.Valuation.DividendYield)
	num(CategoryValuation, "historical_pe_median", hc.Valuation.Historical.Median)
	num(CategoryValuation, "historical_pe_percentile", hc.Valuation.Historical.CurrentPercentile)
	out = append(out, store.Num(CategoryValuation, "historical_pe_samples",
		float64(hc.Valuation.Historical.SampleCount), "筆", hc.Valuation.Source))

	// Evidence quality, persisted beside the statistics it qualifies.
	//
	// The rule version travels with the grade and is not optional: thresholds will change,
	// and a stored "LOW" with no version is a row nobody can interpret afterwards — it does
	// not say whether the stock got worse or the rule got stricter. The metrics are stored
	// too, so a future rule can be applied retroactively to rows graded under this one.
	q := hc.Valuation.Historical.Quality
	text(CategoryValuation, "quality", q.Quality, q.RuleVersion)
	text(CategoryValuation, "quality_rule_version", q.RuleVersion, HistoryVersion)
	out = append(out, store.Num(CategoryValuation, "quality_valid_samples",
		float64(q.ValidSamples), "筆", hc.Valuation.Source))
	out = append(out, store.Num(CategoryValuation, "quality_window_sessions",
		float64(q.WindowSessions), "個", hc.Valuation.Source))
	num(CategoryValuation, "quality_coverage", q.Coverage)

	// Model suitability (FU-11). Stored with its own rule version, for the same reason the
	// quality grade is: thresholds and rules move, and a bare "WEAK" with no version cannot
	// afterwards be told apart from a rule change.
	//
	// The canonical reason CODES are stored, not the wording. Wording is presentation and may
	// be reworded; a stored conclusion has to stay machine-readable.
	ms := hc.Valuation.ModelSuitability
	text(CategoryValuation, "model_suitability", ms.Suitability, ms.RuleVersion)
	text(CategoryValuation, "model_suitability_rule_version", ms.RuleVersion, HistoryVersion)
	text(CategoryValuation, "model_suitability_reasons", joinReasons(ms.Reasons), ms.RuleVersion)
	// The two evidence axes the verdict turns on, so a stored verdict can be re-derived
	// rather than merely re-read.
	text(CategoryValuation, "earnings_sign", ms.EarningsSign, ms.EarningsSource)
	text(CategoryValuation, "pe_persistence", ms.PEPersistence, hc.Valuation.Source)

	// The target price and every input that produced it, so a later reader can re-derive it.
	t := hc.Target
	text(CategoryTarget, "status", string(t.Status), HistoryVersion)
	text(CategoryTarget, "rule", t.Rule, HistoryVersion)
	text(CategoryTarget, "rule_detail", t.RuleDetail, HistoryVersion)
	text(CategoryTarget, "eps_basis", t.EPSBasis, t.EPSSource)
	text(CategoryTarget, "eps_base_date", t.EPSBaseDate, t.EPSSource)
	num(CategoryTarget, "eps", t.EPS)
	num(CategoryTarget, "eps_base_price", t.EPSBasePrice)
	num(CategoryTarget, "margin_of_safety", t.MarginOfSafety)
	for _, sc := range t.Scenarios {
		key := "scenario_" + lower(sc.Name)
		num(CategoryTarget, key+"_pe", sc.PEMultiple)
		num(CategoryTarget, key+"_target", sc.TargetPrice)
		num(CategoryTarget, key+"_upside", sc.UpsidePct)
	}

	for _, leg := range hc.Institution.Legs {
		key := "leg_" + lower(leg.Name)
		num(store.CategoryInstitution, key+"_today", leg.TodayNet)
		num(store.CategoryInstitution, key+"_5d", leg.Sum5D)
		num(store.CategoryInstitution, key+"_20d", leg.Sum20D)
	}

	out = append(out, store.Num(store.CategoryNews, "stock_signal_count",
		float64(hc.News.StockCount), "", "internal/news"))

	// The assessment: its verdict, the rule that decided it, and the thresholds in force.
	// Stored so a later reader can tell whether a call aged badly because the evidence moved
	// or because the rule did.
	text(CategoryAssessment, "verdict", hc.Assessment.Verdict, HistoryVersion)
	text(CategoryAssessment, "decided_by", hc.Assessment.DecidedBy, HistoryVersion)
	text(CategoryAssessment, "reason", hc.Assessment.Reason, HistoryVersion)
	out = append(out,
		store.Num(CategoryAssessment, "threshold_margin_attractive",
			MarginAttractive, "%", HistoryVersion),
		store.Num(CategoryAssessment, "threshold_percentile_expensive",
			PercentileExpensive, "%", HistoryVersion))
	return out
}

func lower(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= 'A' && r <= 'Z' {
			b[i] = r + 32
		}
	}
	return string(b)
}

func floatOrNil(v Value) *float64 {
	if f, ok := v.Float(); ok {
		return &f
	}
	return nil
}

func intOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func intAsFloat(v int) *float64 {
	if v == 0 {
		return nil
	}
	f := float64(v)
	return &f
}

// joinReasons renders the canonical codes as one comma-separated field.
//
// One row rather than N: the evidence table is key/value, and a variable number of rows per
// check would make "which reasons did this check have" a query over an unknown key space.
func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += ","
		}
		out += r
	}
	return out
}
