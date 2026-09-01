package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AnalysisRepo persists multi-agent executions, the agent verdicts inside them, and the
// decisions built from those verdicts.
//
// Everything here is append-only. There is deliberately no Update method anywhere in this
// file: a verdict recorded is a verdict kept, and a changed mind is a NEW analysis run, not
// an edit. That is what keeps the history replayable and the accuracy statistics honest.
type AnalysisRepo struct{ s *Store }

// Analyses returns the analysis-run / agent-analysis / decision repository.
func (s *Store) Analyses() *AnalysisRepo { return &AnalysisRepo{s: s} }

// ── analysis runs ────────────────────────────────────────────────────────────────────

// StartAnalysisRun opens a multi-agent execution over one snapshot.
//
// The same RunUID is used for every snapshot of one batch, so the panel of verdicts produced
// by a single execution can be recovered across stocks. Re-using a RunUID for a snapshot it
// already covered fails — that is a caller bug, not a second attempt.
func (r *AnalysisRepo) StartAnalysisRun(ctx context.Context, run AnalysisRun) (AnalysisRun, error) {
	if run.StockSnapshotID == 0 {
		return AnalysisRun{}, errors.New("store: analysis run needs a stock_snapshot_id")
	}
	if run.RunUID == "" {
		return AnalysisRun{}, errors.New("store: analysis run needs a run_uid")
	}
	switch run.Mode {
	case ModeLive, ModeReplay:
	default:
		return AnalysisRun{}, fmt.Errorf("store: unknown analysis mode %q", run.Mode)
	}
	if run.Status == "" {
		run.Status = RunStatusRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = nowUTC()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = run.StartedAt
	}

	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO analysis_runs
			(stock_snapshot_id, run_uid, mode, orchestrator_version, prompt_set_version,
			 status, started_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.StockSnapshotID, run.RunUID, run.Mode, run.OrchestratorVersion,
		run.PromptSetVersion, run.Status, formatTime(run.StartedAt), formatTime(run.CreatedAt))
	if err != nil {
		return AnalysisRun{}, fmt.Errorf("store: insert analysis run %s for snapshot %d: %w",
			run.RunUID, run.StockSnapshotID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AnalysisRun{}, fmt.Errorf("store: analysis run id: %w", err)
	}
	run.ID = id
	run.FinishedAt = nil
	return run, nil
}

// FinishAnalysisRun closes a run with its terminal status. Like a scan run it stamps once:
// a second call fails rather than rewriting the duration or the outcome of the execution.
func (r *AnalysisRepo) FinishAnalysisRun(ctx context.Context, runID int64, status string, at time.Time) error {
	switch status {
	case RunStatusComplete, RunStatusFailed:
	default:
		return fmt.Errorf("store: %q is not a terminal analysis-run status", status)
	}
	if at.IsZero() {
		at = nowUTC()
	}
	res, err := r.s.db.ExecContext(ctx,
		`UPDATE analysis_runs SET status = ?, finished_at = ? WHERE id = ? AND finished_at IS NULL`,
		status, formatTime(at), runID)
	if err != nil {
		return fmt.Errorf("store: finish analysis run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish analysis run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: analysis run %d is missing or already finished: %w", runID, ErrNotFound)
	}
	return nil
}

const analysisRunCols = `id, stock_snapshot_id, run_uid, mode, orchestrator_version,
	prompt_set_version, status, started_at, finished_at, created_at`

// AnalysisRun fetches one run by id.
func (r *AnalysisRepo) AnalysisRun(ctx context.Context, id int64) (AnalysisRun, error) {
	row := r.s.db.QueryRowContext(ctx, `SELECT `+analysisRunCols+` FROM analysis_runs WHERE id = ?`, id)
	run, err := scanAnalysisRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalysisRun{}, fmt.Errorf("store: analysis run %d: %w", id, ErrNotFound)
	}
	return run, err
}

// AnalysisRunsBySnapshot lists every execution over a snapshot, oldest first.
//
// More than one is normal and expected: the live run from scan day, plus any later replay.
// None of them replaces another.
func (r *AnalysisRepo) AnalysisRunsBySnapshot(ctx context.Context, snapshotID int64) ([]AnalysisRun, error) {
	rows, err := r.s.db.QueryContext(ctx,
		`SELECT `+analysisRunCols+` FROM analysis_runs WHERE stock_snapshot_id = ? ORDER BY started_at, id`,
		snapshotID)
	if err != nil {
		return nil, fmt.Errorf("store: analysis runs for snapshot %d: %w", snapshotID, err)
	}
	defer rows.Close()

	var out []AnalysisRun
	for rows.Next() {
		run, err := scanAnalysisRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func scanAnalysisRun(sc rowScanner) (AnalysisRun, error) {
	var (
		run        AnalysisRun
		startedAt  string
		createdAt  string
		finishedAt sql.NullString
	)
	if err := sc.Scan(&run.ID, &run.StockSnapshotID, &run.RunUID, &run.Mode,
		&run.OrchestratorVersion, &run.PromptSetVersion, &run.Status,
		&startedAt, &finishedAt, &createdAt); err != nil {
		return AnalysisRun{}, err
	}
	var err error
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return AnalysisRun{}, err
	}
	if run.CreatedAt, err = parseTime(createdAt); err != nil {
		return AnalysisRun{}, err
	}
	if run.FinishedAt, err = parseTimePtr(finishedAt); err != nil {
		return AnalysisRun{}, err
	}
	return run, nil
}

// ── agent analysis ───────────────────────────────────────────────────────────────────

// SaveAgentAnalysis records one agent's verdict within one analysis run.
//
// The UNIQUE(analysis_run_id, agent) constraint means a second write for the same pair
// FAILS. That is the point: it is what makes the banned "agent revises its answer after
// hearing a peer" loop impossible to persist. Reaching a different conclusion later is still
// possible — in a new analysis run, as new immutable history.
func (r *AnalysisRepo) SaveAgentAnalysis(ctx context.Context, a AgentAnalysis) (AgentAnalysis, error) {
	if a.AnalysisRunID == 0 {
		return AgentAnalysis{}, errors.New("store: agent analysis needs an analysis_run_id")
	}
	if a.Agent == "" {
		return AgentAnalysis{}, errors.New("store: agent analysis needs an agent name")
	}
	if a.Status == "" {
		return AgentAnalysis{}, errors.New("store: agent analysis needs a status")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = nowUTC()
	}

	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO agent_analysis
			(analysis_run_id, agent, verdict, score, confidence, reasoning, model, status,
			 latency_ms, tokens, output_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AnalysisRunID, a.Agent, a.Verdict, nullFloat(a.Score), nullFloat(a.Confidence),
		a.Reasoning, a.Model, a.Status, nullInt64(a.LatencyMs), nullInt(a.Tokens),
		a.OutputJSON, formatTime(a.CreatedAt))
	if err != nil {
		return AgentAnalysis{}, fmt.Errorf("store: insert %s analysis for run %d: %w",
			a.Agent, a.AnalysisRunID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AgentAnalysis{}, fmt.Errorf("store: agent analysis id: %w", err)
	}
	a.ID = id
	return a, nil
}

const agentCols = `id, analysis_run_id, agent, verdict, score, confidence, reasoning,
	model, status, latency_ms, tokens, output_json, created_at`

// AgentAnalysesByRun returns every agent's verdict from ONE execution, ordered by agent.
//
// This is what R13-M4's judge reads: its own run's peer CONCLUSIONS, not raw market data and
// not another run's answers. A verdict cannot be read before it is committed, because there
// is nowhere to publish a draft.
func (r *AnalysisRepo) AgentAnalysesByRun(ctx context.Context, analysisRunID int64) ([]AgentAnalysis, error) {
	return r.queryAgents(ctx,
		`SELECT `+agentCols+` FROM agent_analysis WHERE analysis_run_id = ? ORDER BY agent`,
		analysisRunID)
}

// AgentAnalysesBySnapshot returns every agent verdict ever recorded about a snapshot, across
// all of its analysis runs, oldest run first. This is the attribution view R13-M7 reads; it
// is NOT what the judge sees.
func (r *AnalysisRepo) AgentAnalysesBySnapshot(ctx context.Context, snapshotID int64) ([]AgentAnalysis, error) {
	return r.queryAgents(ctx, `
		SELECT a.id, a.analysis_run_id, a.agent, a.verdict, a.score, a.confidence, a.reasoning,
		       a.model, a.status, a.latency_ms, a.tokens, a.output_json, a.created_at
		FROM agent_analysis a
		JOIN analysis_runs r ON r.id = a.analysis_run_id
		WHERE r.stock_snapshot_id = ?
		ORDER BY r.started_at, r.id, a.agent`, snapshotID)
}

func (r *AnalysisRepo) queryAgents(ctx context.Context, q string, args ...any) ([]AgentAnalysis, error) {
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query agent analyses: %w", err)
	}
	defer rows.Close()

	var out []AgentAnalysis
	for rows.Next() {
		var (
			a         AgentAnalysis
			score     sql.NullFloat64
			conf      sql.NullFloat64
			latency   sql.NullInt64
			tokens    sql.NullInt64
			createdAt string
		)
		if err := rows.Scan(&a.ID, &a.AnalysisRunID, &a.Agent, &a.Verdict, &score, &conf,
			&a.Reasoning, &a.Model, &a.Status, &latency, &tokens, &a.OutputJSON,
			&createdAt); err != nil {
			return nil, fmt.Errorf("store: scan agent analysis: %w", err)
		}
		if a.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		a.Score = floatPtr(score)
		a.Confidence = floatPtr(conf)
		a.LatencyMs = int64Ptr(latency)
		a.Tokens = intPtr(tokens)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── decisions ────────────────────────────────────────────────────────────────────────

// SaveScannerDecision mirrors the scanner's own verdict onto a snapshot.
//
// The action is stored VERBATIM and is not validated against any vocabulary here: it belongs
// to internal/scanner, and copying it unexamined is what makes this a faithful record rather
// than a second opinion. Exactly one may exist per snapshot, and no AI activity — including
// any number of replays — can reach it.
func (r *AnalysisRepo) SaveScannerDecision(ctx context.Context, snapshotID int64, action string) (Decision, error) {
	if action == "" {
		return Decision{}, errors.New("store: scanner decision needs an action")
	}
	return r.saveDecision(ctx, Decision{
		StockSnapshotID: snapshotID,
		Source:          DecisionSourceScanner,
		Decision:        action,
	})
}

// SaveJudgeDecision records the AI judge's verdict for ONE analysis run.
//
// The verdict must be one of the six JudgeVerdict values — a typo that reached the database
// would become a category nobody aggregates. The snapshot id is derived from the run rather
// than taken from the caller, so a judge decision can never be filed against a snapshot its
// run did not analyse.
func (r *AnalysisRepo) SaveJudgeDecision(ctx context.Context, analysisRunID int64, d Decision) (Decision, error) {
	if analysisRunID == 0 {
		return Decision{}, errors.New("store: judge decision needs an analysis_run_id")
	}
	if !JudgeVerdict(d.Decision).Valid() {
		return Decision{}, fmt.Errorf("store: %q is not a JudgeVerdict (want one of %v)",
			d.Decision, JudgeVerdicts)
	}
	run, err := r.AnalysisRun(ctx, analysisRunID)
	if err != nil {
		return Decision{}, err
	}
	d.Source = DecisionSourceJudge
	d.StockSnapshotID = run.StockSnapshotID
	d.AnalysisRunID = &analysisRunID
	return r.saveDecision(ctx, d)
}

func (r *AnalysisRepo) saveDecision(ctx context.Context, d Decision) (Decision, error) {
	if d.StockSnapshotID == 0 {
		return Decision{}, errors.New("store: decision needs a stock_snapshot_id")
	}
	switch d.Source {
	case DecisionSourceScanner, DecisionSourceJudge:
	default:
		return Decision{}, fmt.Errorf("store: unknown decision source %q", d.Source)
	}
	if d.Decision == "" {
		return Decision{}, errors.New("store: decision needs a decision value")
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = nowUTC()
	}

	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO decisions
			(stock_snapshot_id, analysis_run_id, source, decision, confidence, reasoning,
			 supporting, opposing, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.StockSnapshotID, nullInt64(d.AnalysisRunID), d.Source, d.Decision,
		nullFloat(d.Confidence), d.Reasoning, d.Supporting, d.Opposing, d.Model,
		formatTime(d.CreatedAt))
	if err != nil {
		return Decision{}, fmt.Errorf("store: insert %s decision for snapshot %d: %w",
			d.Source, d.StockSnapshotID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Decision{}, fmt.Errorf("store: decision id: %w", err)
	}
	d.ID = id
	return d, nil
}

const decisionCols = `id, stock_snapshot_id, analysis_run_id, source, decision, confidence,
	reasoning, supporting, opposing, model, created_at`

// DecisionsBySnapshot returns every decision about a snapshot: the scanner's first, then each
// analysis run's judge verdict in run order. All of them coexist — this is the view that
// answers "the scanner said BUY, what did the AI say, and did that change between runs?".
func (r *AnalysisRepo) DecisionsBySnapshot(ctx context.Context, snapshotID int64) ([]Decision, error) {
	rows, err := r.s.db.QueryContext(ctx, `
		SELECT `+decisionCols+` FROM decisions
		WHERE stock_snapshot_id = ?
		ORDER BY CASE source WHEN 'SCANNER' THEN 0 ELSE 1 END, analysis_run_id, id`,
		snapshotID)
	if err != nil {
		return nil, fmt.Errorf("store: decisions for snapshot %d: %w", snapshotID, err)
	}
	defer rows.Close()

	var out []Decision
	for rows.Next() {
		var (
			d         Decision
			runID     sql.NullInt64
			conf      sql.NullFloat64
			createdAt string
		)
		if err := rows.Scan(&d.ID, &d.StockSnapshotID, &runID, &d.Source, &d.Decision, &conf,
			&d.Reasoning, &d.Supporting, &d.Opposing, &d.Model, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan decision: %w", err)
		}
		if d.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		d.AnalysisRunID = int64Ptr(runID)
		d.Confidence = floatPtr(conf)
		out = append(out, d)
	}
	return out, rows.Err()
}
