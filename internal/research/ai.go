package research

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// R13-M3 — persisting the R11 analyst's reading.
//
// Until this file existed the AI ran, rendered into one report, and vanished. Nothing could
// be asked afterwards: not "was it right", not "what did it say in June", not "did it change
// its mind when the prompt changed". An opinion that leaves no record cannot be evaluated,
// and an unevaluatable opinion is the one kind this repo has no use for.
//
// # Identity
//
// One scan → at most ONE AI run, identified by a single run_uid shared by every stock in it.
// The schema stores one analysis_runs ROW per snapshot (stock_snapshot_id is NOT NULL, and
// UNIQUE(run_uid, stock_snapshot_id) is what makes replay possible), so the run is the UID,
// not the row count. That is the existing design; this file uses it rather than reshaping it.
//
// # Shadow
//
// Everything here reads WatchlistEntry.AI, which AttachAI has already finished writing by the
// time the scan reaches this point. It returns nothing the scanner consults and has no path
// to a score, an action, a probability or a sort position.

// AIPersistVersion identifies the driver that wrote these rows, so a later change in what is
// persisted stays distinguishable from a change in what the model said.
const AIPersistVersion = "r13-m3"

// AIResult reports what one RecordAI call persisted.
type AIResult struct {
	RunUID string
	// Runs is how many analysis_runs rows were opened — one per stock carrying a reading.
	Runs int
	// Agents is how many agent_analysis rows landed.
	Agents int
	// Skipped counts stocks whose own write failed. Logged and stepped over, never fatal.
	Skipped int
	// ByStatus counts the AI statuses persisted, successes and failures alike.
	ByStatus map[string]int
}

// analysisWasAttempted reports whether a status describes an analysis that was MEANT to
// happen — successful or not.
//
// The distinction matters because agent_analysis is the record of what the AI did, and two
// of internal/ai's statuses describe what it deliberately did NOT do:
//
//	SKIPPED   the candidate was not actionable, or fell beyond max_stocks — never asked
//	DISABLED  the feature was off — never asked
//
// Recording those would put a row in the table for every stock the layer chose to pass over,
// tripling the count while adding nothing: "how many did we decline" is already the
// difference between the watchlist and this table. A FAILURE is different — NO_API_KEY,
// ERROR and BAD_OUTPUT all describe an analysis that was intended and did not arrive, and
// dropping those would make the store's success rate a lie by omission.
func analysisWasAttempted(s ai.Status) bool {
	switch s {
	case ai.StatusSkipped, ai.StatusDisabled:
		return false
	}
	return true
}

// aiStructuredOutput is what goes into agent_analysis.output_json.
//
// The three lists are the shape the prose column cannot hold. Summary is deliberately NOT
// duplicated here: it lives in Reasoning, and two copies of one string is two things that
// can disagree.
type aiStructuredOutput struct {
	BullCase  []string `json:"bull_case,omitempty"`
	BearCase  []string `json:"bear_case,omitempty"`
	RiskFlags []string `json:"risk_flags,omitempty"`
}

// RecordAI persists the AI reading attached to a scan's watchlist.
//
// snapshots maps symbol → stock_snapshots.id, exactly as RecordScan returned it: this is what
// binds an AI opinion to the SAME snapshot the scanner's own decision was recorded against,
// so the two are comparable by construction rather than by matching on symbol and date.
//
// A nil Recorder, an empty map or a watchlist with no AI field returns a zero result and no
// error — a disabled feature costs one branch, not a wrapper at the call site.
func (r *Recorder) RecordAI(ctx context.Context, scanRunUID string,
	snapshots map[string]int64, entries []scanner.WatchlistEntry) (AIResult, error) {

	if r == nil || r.st == nil || len(snapshots) == 0 {
		return AIResult{}, nil
	}

	// Only stocks that actually carry a reading. A nil AI field means AttachAI never looked
	// at this stock (disabled, or not a candidate) — recording a row for it would invent an
	// opinion that was never formed.
	type pending struct {
		snapshotID int64
		analysis   *ai.Analysis
	}
	var work []pending
	for i := range entries {
		a := entries[i].AI
		if a == nil || !analysisWasAttempted(a.Status) {
			continue
		}
		id, ok := snapshots[entries[i].A.Symbol]
		if !ok {
			continue
		}
		work = append(work, pending{snapshotID: id, analysis: a})
	}
	if len(work) == 0 {
		return AIResult{}, nil
	}

	started := time.Now().UTC()
	res := AIResult{
		RunUID:   fmt.Sprintf("ai-%s-%d", scanRunUID, started.UnixNano()),
		ByStatus: map[string]int{},
	}

	for _, w := range work {
		if err := r.persistOneAI(ctx, res.RunUID, started, w.snapshotID, w.analysis, &res); err != nil {
			// One unwritable stock must not cost the rest of the run's record — the same
			// failure policy every other shadow layer in this repo follows.
			r.logf("research: AI row for snapshot %d not recorded: %v", w.snapshotID, err)
			res.Skipped++
		}
	}

	r.logf("research: AI run %s — %d analysis runs, %d agent rows, %d skipped (%s)",
		res.RunUID, res.Runs, res.Agents, res.Skipped, describeStatuses(res.ByStatus))
	return res, nil
}

// persistOneAI writes one stock's analysis run plus its single agent row.
func (r *Recorder) persistOneAI(ctx context.Context, runUID string, started time.Time,
	snapshotID int64, a *ai.Analysis, res *AIResult) error {

	run, err := r.st.Analyses().StartAnalysisRun(ctx, store.AnalysisRun{
		StockSnapshotID:     snapshotID,
		RunUID:              runUID,
		Mode:                store.ModeLive,
		OrchestratorVersion: AIPersistVersion,
		PromptSetVersion:    ai.PromptSetVersion,
		Status:              store.RunStatusRunning,
		StartedAt:           started,
	})
	if err != nil {
		return err
	}
	res.Runs++

	status := string(a.Status)
	res.ByStatus[status]++

	agentRow := store.AgentAnalysis{
		AnalysisRunID: run.ID,
		Agent:         store.AgentOpenAIAnalyst,
		// Verdict stays EMPTY on purpose. The R11 analyst describes; it does not decide.
		// Writing a BUY/WATCH here would make a shadow layer look like a decision layer to
		// anything that later reads this table.
		Verdict:   "",
		Reasoning: a.Summary,
		Model:     a.Model,
		Status:    status,
	}
	// Confidence is a pointer because 0.0 is a real reading ("no confidence") and absence is
	// not. A failed analysis reports neither.
	if a.Status == ai.StatusOK {
		conf := a.Confidence
		agentRow.Confidence = &conf
	}
	if a.LatencyMs > 0 {
		ms := a.LatencyMs
		agentRow.LatencyMs = &ms
	}
	if a.Tokens > 0 {
		tk := a.Tokens
		agentRow.Tokens = &tk
	}
	// A non-OK analysis carries its one-line reason instead of a summary, so the failure is
	// legible later without having to re-run anything.
	if a.Status != ai.StatusOK && a.Reason != "" {
		agentRow.Reasoning = a.Reason
	}
	if js := marshalAIOutput(a); js != "" {
		agentRow.OutputJSON = js
	}

	if _, err := r.st.Analyses().SaveAgentAnalysis(ctx, agentRow); err != nil {
		// The run row stands and is stamped below; only the verdict is missing. Leaving the
		// run open would be worse — it would look like an execution that never terminated.
		r.logf("research: agent row for snapshot %d not recorded: %v", snapshotID, err)
		_ = r.st.Analyses().FinishAnalysisRun(ctx, run.ID, store.RunStatusFailed, time.Now().UTC())
		return err
	}
	res.Agents++

	runStatus := store.RunStatusComplete
	if a.Status != ai.StatusOK {
		runStatus = store.RunStatusFailed
	}
	if err := r.st.Analyses().FinishAnalysisRun(ctx, run.ID, runStatus, time.Now().UTC()); err != nil {
		r.logf("research: AI run %d recorded but not stamped: %v", run.ID, err)
	}
	return nil
}

// marshalAIOutput serialises the structured half of an analysis, or "" when there is none.
//
// "" and "{}" are deliberately different: the first means the agent produced no structure,
// the second would mean it produced an empty one.
func marshalAIOutput(a *ai.Analysis) string {
	out := aiStructuredOutput{BullCase: a.BullCase, BearCase: a.BearCase, RiskFlags: a.RiskFlags}
	if len(out.BullCase) == 0 && len(out.BearCase) == 0 && len(out.RiskFlags) == 0 {
		return ""
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

// describeStatuses renders the AI status tally.
//
// Deliberately NOT describeCounts: that one names the six EVIDENCE categories, and reusing it
// here printed "technical=0 institution=0 …" for a map that never contains those keys — a log
// line that was not merely useless but actively misleading about what had been recorded.
func describeStatuses(byStatus map[string]int) string {
	if len(byStatus) == 0 {
		return "no statuses"
	}
	keys := make([]string, 0, len(byStatus))
	for k := range byStatus {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, byStatus[k]))
	}
	return strings.Join(parts, " ")
}
