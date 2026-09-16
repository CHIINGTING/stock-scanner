package entryplanbacktest

import (
	"fmt"
	"strings"
	"time"
)

// ── Version and baseline stamps ───────────────────────────────────────────────────────────

// MethodologyVersion identifies the CONTRACT a run was produced under: the temporal rule, the
// horizon convention, the excursion window, the ambiguity class and the observation identity
// in this package.
//
// It is separate from entryplan.RuleVersion, which identifies the rule set that produced the
// PLAN. The two move independently and both must be on every row: a result can change because
// the strategy changed or because the way it was measured changed, and a row carrying only
// one stamp cannot say which.
//
// Bump this whenever any convention in this package changes. In particular, changing
// HorizonCloseIndex's counting rule or ExcursionWindow's bounds bumps it, and — per those
// functions' docs — must not happen after performance has been seen.
const MethodologyVersion = "EP9-v1"

// BaselineCommit is the committed EntryPlan baseline EP-9 validates, as a full SHA.
//
// EP-9 is a validation gate on a FROZEN artifact. Pinning the commit in code rather than in a
// report is what makes a result attributable: a CSV that says "EP6G-v1" describes the rule
// version, but the rule version does not identify the scanner, the bridge or the valuation
// projection around it, all three of which decide what a plan contains.
//
//	bd35d0a6d5a22509b0c54dd37236639504526420  R15 derivatives risk
//	bed3ebad3133b27530fed4b222eda9dccdb03f6b  EntryPlan integration
//	0a84d73c930be476acc09cc65c79c9262c08051c  README sync  <- this one
const BaselineCommit = "0a84d73c930be476acc09cc65c79c9262c08051c"

// ── Where output goes ─────────────────────────────────────────────────────────────────────

// DefaultOutputDir is where a Phase 2 run writes its CSV and JSON.
//
// TWO CONSTRAINTS DECIDED IT, and both are checked by ValidateOutputDir rather than trusted:
//
//  1. NOT THE SQLITE RESEARCH STORE. internal/store's `outcomes` table is the production
//     forward-return grader, one row per stock_snapshot_id (UNIQUE), and it is a statement
//     about what the SCANNER saw. Writing EP-9 rows into it would put research output under a
//     production uniqueness constraint it does not satisfy — an EP-9 observation is keyed by
//     (symbol, AsOf, RuleVersion, arm, wait), five fields the table has no columns for — and
//     would make the production table's contents depend on whether a study had been run.
//     data/research/*.db is also .gitignored (.gitignore:31) and rebuilt from scans, so a
//     study written there is a study that vanishes on the next rebuild.
//
//  2. ALREADY IGNORED BY GIT. reports/*/ is ignored (.gitignore:40), so
//     reports/entryplanbacktest/<run>/ keeps every runtime artifact out of version control by
//     default, while the repo's existing habit of committing a small hand-written SUMMARY
//     alongside (reports/backtest_*_summary_*.md) still works. Note that `reports/*.csv` is
//     ignored but some older CSVs are tracked from before that rule, so a bare
//     reports/<name>.csv is NOT a safe place: it looks ignored and sits beside tracked files
//     with the same shape.
const DefaultOutputDir = "reports/entryplanbacktest"

// ValidateOutputDir refuses the two destinations §26/§27 rule out.
func ValidateOutputDir(dir string) error {
	d := strings.TrimSpace(dir)
	if d == "" {
		return fmt.Errorf("entryplanbacktest: output directory is empty")
	}
	clean := strings.ReplaceAll(d, "\\", "/")
	if strings.Contains(clean, "data/research") {
		return fmt.Errorf("entryplanbacktest: %q is the research store — EP-9 results must "+
			"not be written into the SQLite outcomes table or beside it", dir)
	}
	if strings.Contains(clean, "data/market") || strings.Contains(clean, "data/analysis_history") ||
		strings.Contains(clean, "data/valuation") || strings.Contains(clean, "data/fundamental") {
		return fmt.Errorf("entryplanbacktest: %q is a production archive — a study must not "+
			"write into the evidence it reads", dir)
	}
	return nil
}

// ── Run metadata ──────────────────────────────────────────────────────────────────────────

// RunMetadata is the header every Phase 2 output file must carry.
//
// It exists because the recurring failure in this repo's research artifacts is a number
// presented without the conditions that produced it: a table that does not say which rule
// version, which commit, which dates, which symbols, how many observations were dropped and
// why, or where the regimes came from, is a table nobody can reproduce or refute.
//
// Counts are on the header rather than only in the funnel because the funnel is the thing
// most likely to be edited; the header is what a reader checks it against.
type RunMetadata struct {
	// MethodologyVersion and BaselineCommit — what was measured, and how. See the constants.
	MethodologyVersion string `json:"methodology_version"`
	BaselineCommit     string `json:"baseline_commit"`
	// RuleVersion is entryplan.RuleVersion, copied from the plans. Not defaulted here: the
	// value must come from the plans that were actually produced.
	RuleVersion string `json:"rule_version"`

	// DatasetFrom / DatasetTo are the SIGNAL-session date range, inclusive, YYYY-MM-DD.
	DatasetFrom string `json:"dataset_from"`
	DatasetTo   string `json:"dataset_to"`
	// SymbolsLoaded is how many symbols the universe held; SymbolsObserved is how many
	// contributed at least one observation. The two differ by the warm-up and the filters,
	// and reporting only the first overstates coverage.
	SymbolsLoaded   int `json:"symbols_loaded"`
	SymbolsObserved int `json:"symbols_observed"`

	// Arm and WaitSessions are the run's execution assumption, repeated from the rows so a
	// file can be identified without parsing them.
	Arm          ExecutionArm `json:"execution_arm"`
	WaitSessions int          `json:"wait_sessions"`

	// Observations is how many rows the run accepted; Exclusions is how many candidate
	// stock-sessions were dropped, and ExclusionsByReason says why. The two must reconcile
	// against the funnel — see ValidateCounts.
	Observations       int            `json:"observations"`
	Exclusions         int            `json:"exclusions"`
	ExclusionsByReason map[string]int `json:"exclusions_by_reason"`

	// RegimeProvenanceCounts and ValuationProvenanceCounts are the per-observation
	// provenance census. A run with every observation at UNAVAILABLE is a legal run and a
	// very different one from a run with archived regimes, and the header has to say which.
	RegimeProvenanceCounts    map[RegimeProvenance]int    `json:"regime_provenance_counts"`
	ValuationProvenanceCounts map[ValuationProvenance]int `json:"valuation_provenance_counts"`

	// GeneratedAt is the wall clock at write time. It is the ONE clock reading in this
	// package's contract and it is metadata about the FILE, never an input to anything.
	GeneratedAt time.Time `json:"generated_at"`
}

// Validate refuses a header that would make its own file uninterpretable.
//
// It checks presence and internal consistency only. It cannot check that the numbers are
// TRUE — that is the funnel's job and the funnel is Phase 2 — so it deliberately does not
// claim to.
func (m RunMetadata) Validate() error {
	if m.MethodologyVersion == "" {
		return fmt.Errorf("entryplanbacktest: run carries no MethodologyVersion")
	}
	if m.BaselineCommit == "" {
		return fmt.Errorf("entryplanbacktest: run carries no BaselineCommit")
	}
	if m.RuleVersion == "" {
		return fmt.Errorf("entryplanbacktest: run carries no entryplan RuleVersion")
	}
	if _, err := time.Parse(DateLayout, m.DatasetFrom); err != nil {
		return fmt.Errorf("entryplanbacktest: dataset_from %q is not YYYY-MM-DD", m.DatasetFrom)
	}
	if _, err := time.Parse(DateLayout, m.DatasetTo); err != nil {
		return fmt.Errorf("entryplanbacktest: dataset_to %q is not YYYY-MM-DD", m.DatasetTo)
	}
	if m.DatasetTo < m.DatasetFrom {
		return fmt.Errorf("entryplanbacktest: dataset range %s..%s runs backwards",
			m.DatasetFrom, m.DatasetTo)
	}
	if !m.Arm.Valid() {
		return fmt.Errorf("entryplanbacktest: run execution arm %q is not one of %v",
			m.Arm, AllExecutionArms)
	}
	if m.WaitSessions < 1 || m.WaitSessions > MaxWaitSessions {
		return fmt.Errorf("entryplanbacktest: run wait window %d sessions, want 1..%d",
			m.WaitSessions, MaxWaitSessions)
	}
	if m.SymbolsLoaded < 0 || m.SymbolsObserved < 0 || m.Observations < 0 || m.Exclusions < 0 {
		return fmt.Errorf("entryplanbacktest: run carries a negative count")
	}
	if m.SymbolsObserved > m.SymbolsLoaded {
		return fmt.Errorf("entryplanbacktest: %d symbols observed out of %d loaded",
			m.SymbolsObserved, m.SymbolsLoaded)
	}
	var byReason int
	for _, v := range m.ExclusionsByReason {
		if v < 0 {
			return fmt.Errorf("entryplanbacktest: negative exclusion count")
		}
		byReason += v
	}
	if byReason != m.Exclusions {
		return fmt.Errorf("entryplanbacktest: exclusions_by_reason sums to %d but exclusions "+
			"is %d — an unattributed exclusion is an exclusion nobody can audit",
			byReason, m.Exclusions)
	}
	if err := m.validateProvenanceCensus(); err != nil {
		return err
	}
	return nil
}

// validateProvenanceCensus requires both censuses to name only defined values and to sum to
// the observation count. A census that does not cover every observation would let a segment
// silently drop rows.
func (m RunMetadata) validateProvenanceCensus() error {
	var rTotal int
	for k, v := range m.RegimeProvenanceCounts {
		if !k.Valid() {
			return fmt.Errorf("entryplanbacktest: regime provenance census names %q", k)
		}
		if v < 0 {
			return fmt.Errorf("entryplanbacktest: negative regime provenance count for %q", k)
		}
		rTotal += v
	}
	if rTotal != m.Observations {
		return fmt.Errorf("entryplanbacktest: regime provenance census covers %d of %d "+
			"observations", rTotal, m.Observations)
	}
	var vTotal int
	for k, v := range m.ValuationProvenanceCounts {
		if !k.Valid() {
			return fmt.Errorf("entryplanbacktest: valuation provenance census names %q", k)
		}
		if v < 0 {
			return fmt.Errorf("entryplanbacktest: negative valuation provenance count for %q", k)
		}
		vTotal += v
	}
	if vTotal != m.Observations {
		return fmt.Errorf("entryplanbacktest: valuation provenance census covers %d of %d "+
			"observations", vTotal, m.Observations)
	}
	return nil
}
