// Package store is the R13 SQLite evidence / analysis / audit store.
//
// It is a RECORD of what the scanner already decided, never a second opinion about it. The
// existing scanner remains the source of truth: this package persists its output, the
// evidence that output was derived from, whatever the multi-agent layer later concludes
// about that evidence, and — once the future is known — what actually happened. Nothing
// here computes an indicator, and nothing here is read back into a score, an action or a
// sort order.
//
// The chain every row hangs from:
//
//	scan_runs → stock_snapshots → evidence
//	                           ↘ analysis_runs → agent_analysis
//	                                          ↘ decisions (AI_JUDGE)
//	                           ↘ decisions (SCANNER)
//	                           ↘ outcomes
//
// Five structural guarantees are enforced by the SCHEMA rather than by convention, because
// a comment cannot stop a future caller:
//
//   - every row is reachable from exactly one scan_run, so "which run said this?" always
//     has an answer, and a run can be deleted whole (ON DELETE CASCADE);
//   - one agent may record exactly one verdict per ANALYSIS RUN (UNIQUE(analysis_run_id,
//     agent)). Within an execution an agent cannot revise its conclusion after seeing a
//     peer's — the forbidden debate loop has nowhere to be stored — while a NEW analysis run
//     over the same snapshot is free to reach a different answer. Replay, prompt A/B and
//     re-analysis with a newer agent are all first-class, and each is its own immutable
//     history rather than an edit to the previous one;
//   - the scanner's decision is snapshot-level and written once; the AI judge's decision is
//     analysis-run-level. Two partial unique indexes enforce exactly that, so any number of
//     AI verdicts can accumulate over a snapshot without any of them being able to touch
//     what the scanner decided;
//   - an outcome hangs off the SNAPSHOT, not off an opinion. The forward return of a stock
//     is a property of (symbol, date) — the same number for every opinion held about it.
//     Storing it once removes any way for the scanner's copy and the AI's copy to drift;
//   - nothing is ever UPDATEd to record a revision. Corrections are new rows in new runs.
//
// This store is ADDITIVE. internal/analysishistory remains the authoritative JSON sidecar
// that internal/validator reads, and R13 does not change either contract. Both are written
// from the same canonical in-memory scan result — this package must never be populated by
// re-parsing the sidecar, which would make it a second translation of a second source.
//
// Times are stored as RFC3339 UTC text, matching the JSON snapshot stores already in this
// repo (internal/institution, internal/news, internal/etfflow). Optional numbers are
// pointers so "not measured" stays distinct from a real 0 — the MISSING ≠ NEUTRAL contract
// this repo applies everywhere else.
package store

import "time"

// SourceTab records which part of a scan produced a snapshot. The same symbol can legitimately
// appear in more than one tab of a single run (a watchlist stock is usually also in the market
// scan), and their scanner decisions differ, so the tab is part of a snapshot's identity.
// Values match internal/validator's existing vocabulary rather than inventing a second one.
const (
	TabWatchlist = "watchlist"
	TabMarket    = "market"
	TabPositions = "positions"
)

// DecisionSource discriminates who made a decision. The scanner's own decision is recorded
// alongside the AI judge's so R13-M7 can score them against each other; neither may
// overwrite the other.
//
// These literals also appear inside the decisions CHECK constraint and its two partial
// unique indexes. TestDecisionSourceConstantsMatchSchema keeps the Go and SQL copies from
// drifting apart.
const (
	DecisionSourceScanner = "SCANNER"
	DecisionSourceJudge   = "AI_JUDGE"
)

// AnalysisMode says why a multi-agent execution ran. LIVE is a scan's own analysis; REPLAY
// is a later re-analysis of an already-stored snapshot (a new agent build, a new prompt set,
// a model A/B, a research backtest). Both produce equally real, equally immutable history —
// the mode is what lets R13-M7 exclude replays from live accuracy statistics, or study them
// on purpose.
// Agent names. Defined here, once, so a rename is a compile error rather than a silent
// split into two populations that group-by no longer joins.
const (
	// AgentOpenAIAnalyst is the R11 single-model reading attached by internal/ai. It is an
	// ANALYST, not a judge: it produces a description, never a BUY/WATCH/SELL, which is why
	// rows it writes leave Verdict empty.
	AgentOpenAIAnalyst = "OPENAI_ANALYST"
)

const (
	ModeLive   = "LIVE"
	ModeReplay = "REPLAY"
)

// Analysis run status. A run that died keeps its rows and its FAILED (or never-finished)
// status, which is what makes a partial execution identifiable instead of silently
// contributing half a panel of verdicts to an accuracy statistic.
const (
	RunStatusRunning  = "RUNNING"
	RunStatusComplete = "COMPLETE"
	RunStatusFailed   = "FAILED"
)

// JudgeVerdict is the AI shadow layer's OWN decision vocabulary.
//
// It is deliberately not scanner.Action. The two are different domains: the scanner answers
// "what does the evidence score out to", the judge answers "what would you do about this
// position". Merging them would force false equivalences — AVOID is not SELL (one is "do
// not enter", the other is "get out"), and ADD is not STRONG BUY (one presumes an existing
// position, the other does not). R13-M7 will map between the two explicitly for comparison;
// until then they stay separate and neither pollutes the other.
type JudgeVerdict string

const (
	// VerdictAdd — a position already exists and the evidence supports increasing exposure.
	VerdictAdd JudgeVerdict = "ADD"
	// VerdictBuy — suitable as a NEW entry.
	VerdictBuy JudgeVerdict = "BUY"
	// VerdictWatch — the direction is attractive but no adequate entry condition has formed.
	VerdictWatch JudgeVerdict = "WATCH"
	// VerdictHold — an existing position may be carried, but exposure should not grow.
	VerdictHold JudgeVerdict = "HOLD"
	// VerdictReduce — an existing position should shed exposure. NOT the scanner's SELL.
	VerdictReduce JudgeVerdict = "REDUCE"
	// VerdictAvoid — with no position, do not enter; the current setup is excluded.
	VerdictAvoid JudgeVerdict = "AVOID"
)

// JudgeVerdicts is every valid verdict, in descending conviction, for callers that iterate.
var JudgeVerdicts = []JudgeVerdict{
	VerdictAdd, VerdictBuy, VerdictWatch, VerdictHold, VerdictReduce, VerdictAvoid,
}

// Valid reports whether v is one of the six defined verdicts. The store checks this on the
// way in: a typo that reaches the database becomes a category nobody aggregates.
func (v JudgeVerdict) Valid() bool {
	for _, known := range JudgeVerdicts {
		if v == known {
			return true
		}
	}
	return false
}

// Evidence categories. These name the DOMAIN a piece of evidence belongs to, which is also
// how R13-M3 hands each specialist agent only its own slice of the evidence.
const (
	CategoryTechnical   = "technical"
	CategoryInstitution = "institution"
	CategorySector      = "sector"
	CategoryNews        = "news"
	CategoryMarket      = "market"
	CategoryRisk        = "risk"
)

// ScanRun is one execution of the scanner.
//
// FinishedAt is nil while the run is in flight. A run that crashed keeps its rows and its
// nil FinishedAt, which is what makes a partial run identifiable later instead of silently
// polluting accuracy statistics.
type ScanRun struct {
	ID          int64
	RunUID      string // stable external id, safe to print in a report
	TradingDate string // YYYY-MM-DD — the analysis date, not the wall clock
	StartedAt   time.Time
	FinishedAt  *time.Time
	// ScannerVersion is whatever build identity the caller can supply (git sha, tag).
	// Empty is allowed: an unknown build is recorded as unknown, not guessed.
	ScannerVersion string
	// MarketRegime is internal/market/model.Regime at scan time, carried as text so this
	// package does not import the market model just to hold a label.
	MarketRegime string
	MarketScore  *float64
	UniverseSize int
	Note         string
}

// StockSnapshot is one stock as it stood in one scan run: the price the outcome will later
// be measured from, plus the scanner's own verdict. This is the anchor every other R13 row
// points at.
type StockSnapshot struct {
	ID          int64
	ScanRunID   int64
	Symbol      string
	Name        string
	TradingDate string // denormalised from the run; every outcome query filters on it
	SourceTab   string
	// Close is the basis price for outcome measurement. Nil when the scan had no usable
	// price, in which case no outcome can ever be computed for this row — recorded honestly
	// rather than defaulted to 0.
	Close *float64

	ScannerAction string // scanner.Action, verbatim — the source of truth
	ScannerScore  *float64
	RocketScore   *int
	RocketStage   string
	WatchAction   string
	Sector        string

	CreatedAt time.Time
}

// Evidence is one fact the scanner already computed, addressed by (category, key).
//
// A fact the scanner did not produce is simply ABSENT — there is no row and no zero. That
// is the whole reason this is key/value rather than a wide table: a wide table forces every
// column to exist for every stock, and a NULL there is indistinguishable from a column
// nobody has wired up yet.
//
// Exactly one of NumValue / TextValue carries the payload (enforced by a CHECK constraint).
type Evidence struct {
	ID              int64
	StockSnapshotID int64
	Category        string
	Key             string
	NumValue        *float64
	TextValue       *string
	Unit            string
	// Source names the scanner layer this came from (e.g. "scanner.BiasView"), so a
	// surprising number can be traced back to the code that produced it.
	Source    string
	CreatedAt time.Time
}

// AnalysisRun is ONE multi-agent execution over ONE stock snapshot.
//
// This layer exists so that "an agent may not revise its verdict" does not degrade into "a
// snapshot may only ever be analysed once". The first is the invariant that prevents agents
// talking each other into a shared bias; the second would have made replay, prompt
// versioning, model A/B and research backtests impossible — precisely the questions R13-M7
// is built to answer.
//
// RunUID identifies the whole execution and is SHARED by every snapshot in that batch, so
// "which multi-agent execution produced this panel of verdicts" is answerable across stocks.
// Uniqueness is therefore per (RunUID, StockSnapshotID), not per RunUID alone.
//
// The version fields live here rather than on each agent row: what is versioned is the SET
// of agents and prompts that ran together. Changing one agent's prompt produces a new set
// version, which is exactly the granularity at which a result becomes non-comparable.
type AnalysisRun struct {
	ID              int64
	StockSnapshotID int64
	RunUID          string // shared across every snapshot of one execution
	Mode            string // ModeLive | ModeReplay
	// OrchestratorVersion identifies the multi-agent driver build (e.g. "r13-m3").
	OrchestratorVersion string
	// PromptSetVersion identifies the prompt/agent-implementation set (e.g. "r13-v2"). With
	// AgentAnalysis.Model this answers "which logic produced this prediction".
	PromptSetVersion string
	Status           string // RunStatusRunning | RunStatusComplete | RunStatusFailed
	StartedAt        time.Time
	FinishedAt       *time.Time
	CreatedAt        time.Time
}

// AgentAnalysis is one specialist agent's verdict within one analysis run.
//
// It binds to the ANALYSIS RUN, not to the snapshot: UNIQUE(analysis_run_id, agent) still
// makes "Technical: BULLISH … Technical: actually WATCH" impossible inside an execution,
// while leaving a later run free to reach a different conclusion about the same snapshot.
//
// Status reuses internal/ai's vocabulary (OK / DISABLED / NO_API_KEY / SKIPPED / ERROR /
// BAD_OUTPUT) so a failed agent is recorded as a failure rather than as a neutral opinion.
type AgentAnalysis struct {
	ID            int64
	AnalysisRunID int64
	Agent         string
	Verdict       string
	Score         *float64
	Confidence    *float64
	Reasoning     string
	// Model is per agent, because agents may legitimately run on different models within one
	// run. The prompt-set version that governed them all lives on AnalysisRun.
	Model  string
	Status string
	// OutputJSON is the agent's STRUCTURED output, when it produced any. Reasoning stays
	// prose; anything with shape (lists of cases, flags) belongs here, so neither column has
	// to be parsed to find out what kind of content it holds. Empty means the agent produced
	// no structured output — which is not the same as "{}".
	OutputJSON string
	LatencyMs  *int64
	Tokens     *int
	CreatedAt  time.Time
}

// Decision is one party's conclusion about one stock snapshot.
//
// The two sources sit at different levels, and the schema enforces it:
//
//	SCANNER   snapshot-level  — AnalysisRunID is nil, at most one per snapshot, never rewritten
//	AI_JUDGE  run-level       — AnalysisRunID is set, at most one per analysis run
//
// So a snapshot can carry the scanner's BUY alongside run #1's WATCH and run #2's BUY, all
// three coexisting, none able to overwrite another.
//
// Supporting / Opposing hold the judge's evidence lists as JSON arrays. They are stored as
// text because their shape belongs to the judge, not to the schema.
type Decision struct {
	ID              int64
	StockSnapshotID int64
	// AnalysisRunID is nil for a scanner decision and required for a judge decision.
	AnalysisRunID *int64
	Source        string // DecisionSourceScanner | DecisionSourceJudge
	// Decision is a scanner.Action verbatim when Source is SCANNER, and a JudgeVerdict when
	// Source is AI_JUDGE. Only the latter is validated here: the scanner's vocabulary
	// belongs to the scanner, and copying it unexamined is what makes this a faithful record
	// rather than a second opinion.
	Decision   string
	Confidence *float64
	Reasoning  string
	Supporting string // JSON array, empty string when none
	Opposing   string // JSON array, empty string when none
	Model      string
	CreatedAt  time.Time
}

// Outcome is what actually happened after a snapshot, measured from BaseClose.
//
// Every horizon is a pointer and starts nil. A horizon is filled in exactly once, when
// enough bars exist to know it; the repository refuses to overwrite a value that is already
// there. That is what keeps a later, better-informed run from quietly rewriting history and
// flattering the accuracy statistics.
type Outcome struct {
	ID              int64
	StockSnapshotID int64
	BaseClose       float64
	Return1D        *float64
	Return3D        *float64
	Return5D        *float64
	Return10D       *float64
	Return20D       *float64
	MaxGain20D      *float64
	MaxDrawdown20D  *float64
	// BarsObserved is how many forward trading days have been seen so far. It is what
	// distinguishes "20-day return is nil because the future has not happened" from
	// "…because the data is missing".
	BarsObserved    int
	FirstComputedAt time.Time
	LastUpdatedAt   time.Time
}
