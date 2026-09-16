// Command ep10_convergence is the EP-10 MEASUREMENT of how the report's legacy ④ 價位計畫 block
// and the EP-8 ⑲ 進場計畫 block relate to each other on a real, full universe.
//
// It is a MEASUREMENT, not a decision and not a strategy study. It changes nothing, tunes
// nothing, and writes only where -out points. The convergence decision EP-10 took from these
// numbers is written up in docs/EP10_LEGACY_VS_ENTRYPLAN.md.
//
// # How the two blocks are observed
//
// The ⑲ visibility answer is NOT re-derived here. The tool renders the REAL report through
// internal/report with ShowEntryPlan on and asks whether each stock's own detail row contains a
// `wl-ep` section. That is the production gate itself (report_entryplan.go's epBuySideSemantic
// via the template), so the count cannot drift from the renderer.
//
// The legacy ④ answer is read off the WatchlistEntry fields the ④ block prints
// (report.go's "④ 價位計畫" rows): EntryZone, BreakoutPrice, SupportPrice, StopLossPrice and
// TakeProfitZone.
//
// # Provenance
//
// The session is reconstructed with internal/entryplanbacktest/recon, the EP-9 research layer,
// under the REPLAYED_PIT arm. Every EP-9 limit therefore applies verbatim and is stamped on the
// output: this is a replay-conditioned research observation, NOT production historical
// performance, and no outcome is measured here at all — the tool compares two PRESENTATIONS of
// the same session, not their results.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/report"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"gopkg.in/yaml.v3"
)

// Class is the A..E classification the EP-10 brief asks for.
type Class string

const (
	ClassLegacyOnly   Class = "A_LEGACY_ONLY"
	ClassEP19Only     Class = "B_EP19_ONLY"
	ClassBothConsist  Class = "C_BOTH_CONSISTENT"
	ClassBothContra   Class = "D_BOTH_CONTRADICTORY"
	ClassNeither      Class = "E_NEITHER"
	ClassUnclassified Class = "UNCLASSIFIED"
)

// Dimension names one exact semantic disagreement. No numeric tolerance is invented anywhere:
// every dimension is a statement that is either true or false about the two published blocks.
type Dimension string

const (
	// DimExecutionAuthorization: ④ publishes a complete buy-side price plan while ⑲ publishes
	// NO executable price at all (Plan.HasExecutablePrices() is false). A reader sees prices in
	// one block and a refusal in the other.
	DimExecutionAuthorization Dimension = "EXECUTION_AUTHORIZATION"
	// DimEntryDisjoint: both blocks publish a NUMERIC entry band and the two bands do not
	// overlap — no single price satisfies both. Compared on the digits the report prints
	// (④ at one decimal, ⑲ at two), so the statement is about what the reader can see.
	DimEntryDisjoint Dimension = "ENTRY_BAND_DISJOINT"
	// DimEntryNotComparable: ④'s entry field is PROSE, not a band (rocket.go's entryZone emits
	// "拉回 5/10 日線量縮承接" and "等待型態成形" for three of six stages), so no numeric
	// comparison exists. This is a presentation fact, not a disagreement.
	DimEntryNotComparable Dimension = "ENTRY_NOT_NUMERICALLY_COMPARABLE"
	// DimStopOrdering: both publish a stop-like level and ④'s sits ABOVE ⑲'s invalidation,
	// i.e. acting on ④ exits a position that ⑲ still considers intact (or vice versa when
	// below). Recorded with its direction in StopLegacyAbove / StopLegacyBelow.
	DimStopOrdering Dimension = "STOP_LEVEL_DIFFERS"
	// DimTargetDisjoint: both publish a take-profit band and the two do not overlap.
	DimTargetDisjoint Dimension = "TARGET_BAND_DISJOINT"
	// DimStatusThesis: ⑲ resolved a thesis but its status is one a reader must not act on
	// (INSUFFICIENT_DATA / NO_VALID_ENTRY / TOO_EXTENDED) while ④ shows a full price plan.
	DimStatusThesis Dimension = "STATUS_THESIS"
)

// Row is one stock's comparison.
type Row struct {
	Symbol string `json:"symbol"`
	// Legacy ④
	LegacyPresent    bool    `json:"legacy_present"`
	LegacyEntryText  string  `json:"legacy_entry_text"`
	LegacyEntryLow   float64 `json:"legacy_entry_low,omitempty"`
	LegacyEntryHigh  float64 `json:"legacy_entry_high,omitempty"`
	LegacyEntryBand  bool    `json:"legacy_entry_is_band"`
	LegacyStop       float64 `json:"legacy_stop,omitempty"`
	LegacyTPLow      float64 `json:"legacy_tp_low,omitempty"`
	LegacyTPHigh     float64 `json:"legacy_tp_high,omitempty"`
	LegacyFabricated bool    `json:"legacy_fabricated_stop"`
	// ⑲
	EP19Rendered  bool    `json:"ep19_rendered"`
	EP19Status    string  `json:"ep19_status,omitempty"`
	EP19Executabl bool    `json:"ep19_has_executable_prices"`
	EP19ZoneLow   float64 `json:"ep19_zone_low,omitempty"`
	EP19ZoneHigh  float64 `json:"ep19_zone_high,omitempty"`
	EP19Invalid   float64 `json:"ep19_invalidation,omitempty"`
	EP19T1        float64 `json:"ep19_target1,omitempty"`
	EP19T2        float64 `json:"ep19_target2,omitempty"`

	Class      Class       `json:"class"`
	Dimensions []Dimension `json:"dimensions,omitempty"`
}

// Report is the machine-readable artifact. It is SELF-DESCRIBING in the EP-9 sense: a reader
// who finds this file detached from the repo can still tell what it is and what it is not.
type Report struct {
	Tool                       string            `json:"tool"`
	MethodologyVersion         string            `json:"methodology_version"`
	EntryPlanRuleVersion       string            `json:"entryplan_rule_version"`
	RegimeStudyArm             string            `json:"regime_study_arm"`
	RegimeProvenance           string            `json:"regime_provenance"`
	Regime                     string            `json:"regime"`
	AdjustmentCohort           string            `json:"adjustment_cohort"`
	ExecutionArm               string            `json:"execution_arm"`
	WaitWindow                 string            `json:"wait_window"`
	Horizon                    string            `json:"horizon"`
	PopulationClass            string            `json:"population_class"`
	ProductionHistoricalExec   string            `json:"production_historical_execution"`
	ReplayNotProductionWired   bool              `json:"replay_not_production_wired"`
	ArchivedRegimeN            int               `json:"archived_regime_n"`
	Posture                    string            `json:"posture"`
	PostureDependentRules      string            `json:"posture_dependent_rules"`
	BreadthSurvivorshipBias    bool              `json:"breadth_survivorship_bias"`
	NotProductionHistoricalPer string            `json:"not_production_historical_performance"`
	NotBacktestFitted          bool              `json:"not_backtest_fitted"`
	Caveats                    []string          `json:"caveats"`
	Session                    string            `json:"session"`
	GeneratedAt                string            `json:"generated_at"`
	Universe                   int               `json:"universe"`
	Entries                    int               `json:"entries"`
	Classes                    map[Class]int     `json:"classes"`
	Dimensions                 map[Dimension]int `json:"dimension_counts"`
	StopLegacyAbove            int               `json:"stop_legacy_above_invalidation"`
	StopLegacyBelow            int               `json:"stop_legacy_below_invalidation"`
	StopEqual                  int               `json:"stop_equal_after_display_rounding"`
	StatusCounts               map[string]int    `json:"ep19_status_counts"`
	Rows                       []Row             `json:"rows"`
}

func main() {
	log.SetFlags(0)
	var (
		cacheDir   = flag.String("cache", ".cache", "price cache directory (read-only)")
		configPath = flag.String("config", "configs/config.yaml", "scanner config")
		sectorFile = flag.String("sectors", "configs/sectors.yaml", "sector membership")
		replayDir  = flag.String("replay", marketservice.DefaultReplayDir, "regime replay archive (read-only)")
		date       = flag.String("date", "", "session YYYY-MM-DD; empty = newest replay-covered eligible session")
		out        = flag.String("out", "", "output directory (REQUIRED; must not be a production archive)")
		rows       = flag.Bool("rows", true, "include the per-stock rows in the JSON")
	)
	flag.Parse()

	if *out == "" {
		log.Fatal("-out is required")
	}
	if err := entryplanbacktest.ValidateOutputDir(*out); err != nil {
		log.Fatalf("%v", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatalf("%v", err)
	}

	sc, err := loadScannerConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// Turned on HERE, in research code, exactly as EP-9 does — the production config is not edited.
	sc.EnableEntryPlan = true

	sl, err := fetcher.LoadSectorList(*sectorFile)
	if err != nil {
		log.Fatalf("sectors: %v", err)
	}
	cache, err := recon.LoadCache(*cacheDir, recon.EligibilityBars)
	if err != nil {
		log.Fatalf("cache: %v", err)
	}
	replay, err := recon.OpenReplayArchive(*replayDir)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}

	session := *date
	if session == "" {
		eligible := cache.EligibleSessions(recon.EligibilityBars, entryplanbacktest.ExcursionSessions)
		for i := len(eligible) - 1; i >= 0; i-- {
			if replay.Covers(eligible[i]) {
				session = eligible[i]
				break
			}
		}
	}
	if session == "" {
		log.Fatal("no replay-covered eligible session found")
	}
	if !replay.Covers(session) {
		log.Fatalf("the replay archive does not cover %s; a REPLAYED_PIT run must not be downgraded", session)
	}
	regimeRow, err := replay.Row(session)
	if err != nil {
		log.Fatalf("replay row: %v", err)
	}

	r := &recon.Reconstructor{Scanner: scanner.New(sc), Cache: cache,
		Sectors: recon.NewSectorPanel(sl), Replay: replay, MinBars: recon.EligibilityBars}
	res, err := r.Reconstruct(session, recon.ArmReplayedPIT, recon.FullFidelity)
	if err != nil {
		log.Fatalf("reconstruct: %v", err)
	}
	fmt.Printf("session %s  universe %d  entries %d  regime %s\n",
		session, res.Universe, len(res.Entries), regimeRow.Regime)

	visible, err := renderVisibility(res.Entries, session)
	if err != nil {
		log.Fatalf("render: %v", err)
	}

	rep := Report{
		Tool:                     "scripts/ep10_convergence",
		MethodologyVersion:       entryplanbacktest.MethodologyVersion,
		EntryPlanRuleVersion:     entryplan.RuleVersion,
		RegimeStudyArm:           string(recon.ArmReplayedPIT),
		RegimeProvenance:         string(entryplanbacktest.RegimeReplayedPIT),
		Regime:                   string(regimeRow.Regime),
		AdjustmentCohort:         "NOT_STRATIFIED (no outcome is measured; adjustment strata only affect outcomes)",
		ExecutionArm:             "NONE (no order is simulated)",
		WaitWindow:               "NONE (no order is simulated)",
		Horizon:                  "NONE (no forward return is measured)",
		PopulationClass:          "PRESENTATION_COMPARISON (not EXECUTABLE, not NON_EXECUTABLE_COUNTERFACTUAL — nothing is executed)",
		ProductionHistoricalExec: "NOT_EVALUABLE",
		ReplayNotProductionWired: true,
		ArchivedRegimeN:          0,
		Posture:                  "UNKNOWN",
		PostureDependentRules:    "NOT_EVALUABLE",
		BreadthSurvivorshipBias:  true,
		NotProductionHistoricalPer: "This file measures PRESENTATION agreement between two report blocks " +
			"on one replayed session. It contains no return, no fill and no performance statement.",
		NotBacktestFitted: true,
		Caveats:           recon.ReplayCaveats(),
		Session:           session,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Universe:          res.Universe,
		Entries:           len(res.Entries),
		Classes:           map[Class]int{},
		Dimensions:        map[Dimension]int{},
		StatusCounts:      map[string]int{},
	}

	for i := range res.Entries {
		row := classify(res.Entries[i], visible[i], &rep)
		rep.Classes[row.Class]++
		for _, d := range row.Dimensions {
			rep.Dimensions[d]++
		}
		if row.EP19Rendered {
			rep.StatusCounts[row.EP19Status]++
		}
		if *rows {
			rep.Rows = append(rep.Rows, row)
		}
	}

	b, _ := json.MarshalIndent(rep, "", "  ")
	path := filepath.Join(*out, "ep10_convergence_"+session+".json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		log.Fatalf("%v", err)
	}
	printSummary(rep)
	fmt.Printf("\nwrote %s\n", path)
}

// ── ⑲ visibility, read off the real renderer ──────────────────────────────────────────────

var detailRowRe = regexp.MustCompile(`<tr class="sector-detail" id="wdetail-(\d+)">`)

// renderVisibility renders ONE report holding every entry with ShowEntryPlan on, then reports
// per entry index whether that stock's own detail row carries a ⑲ section. Splitting on the
// detail-row marker (which carries the entry's index) means a hidden section never shifts the
// correspondence.
func renderVisibility(entries []scanner.WatchlistEntry, session string) ([]bool, error) {
	dir, err := os.MkdirTemp("", "ep10-convergence-render")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	d, err := recon.ParseDate(session)
	if err != nil {
		return nil, err
	}
	rp := report.New(report.Config{OutputDir: dir})
	if err := rp.Generate(nil, nil, entries, nil, "-", d,
		report.GuardrailViewOptions{ShowEntryPlan: true}, nil, nil); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report_"+d.Format("20060102")+".html"))
	if err != nil {
		return nil, err
	}
	html := string(raw)

	idx := detailRowRe.FindAllStringSubmatchIndex(html, -1)
	if len(idx) != len(entries) {
		return nil, fmt.Errorf("found %d detail rows for %d entries", len(idx), len(entries))
	}
	out := make([]bool, len(entries))
	for i, m := range idx {
		end := len(html)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		var n int
		if _, err := fmt.Sscanf(html[m[2]:m[3]], "%d", &n); err != nil || n != i {
			return nil, fmt.Errorf("detail row %d carries index %q", i, html[m[2]:m[3]])
		}
		out[i] = strings.Contains(html[m[0]:end], `<section class="wl-sec wl-ep">`)
	}
	return out, nil
}

// ── classification ────────────────────────────────────────────────────────────────────────

var bandRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*[~～]\s*([0-9]+(?:\.[0-9]+)?)`)
var singleNumRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)`)

func classify(e scanner.WatchlistEntry, rendered bool, rep *Report) Row {
	row := Row{Symbol: e.A.Symbol, EP19Rendered: rendered}

	// ── ④, exactly the fields the block prints ──
	row.LegacyEntryText = e.EntryZone
	row.LegacyStop = e.StopLossPrice
	if m := bandRe.FindStringSubmatch(e.EntryZone); m != nil {
		row.LegacyEntryLow, row.LegacyEntryHigh = atof(m[1]), atof(m[2])
		row.LegacyEntryBand = true
	}
	if m := bandRe.FindStringSubmatch(e.TakeProfitZone); m != nil {
		row.LegacyTPLow, row.LegacyTPHigh = atof(m[1]), atof(m[2])
	}
	// The ④ block publishes a price plan whenever it prints any of its four price rows.
	row.LegacyPresent = e.EntryZone != "" || e.StopLossPrice > 0 || e.TakeProfitZone != "" ||
		e.BreakoutPrice > 0 || e.SupportPrice > 0
	// scorer.go:740-741 substitutes 0.93*close when the computed stop is unusable; rocket.go:328
	// falls back to that value when neither BaseLow nor MA5 is usable. A stop at exactly
	// round1(0.93*close*0.99) is that fabrication surfacing in ④.
	row.LegacyFabricated = row.LegacyStop > 0 && round1(e.A.Close*0.93*0.99) == row.LegacyStop

	// ── ⑲, read off the published plan and nothing else ──
	p := e.EntryPlan
	if p != nil {
		row.EP19Status = string(p.Status)
		row.EP19Executabl = p.HasExecutablePrices()
		if p.IdealEntry != nil {
			row.EP19ZoneLow, row.EP19ZoneHigh = p.IdealEntry.Low, p.IdealEntry.High
		}
		if p.Invalidation != nil {
			row.EP19Invalid = p.Invalidation.Price
		}
		if p.Target1 != nil {
			row.EP19T1 = p.Target1.Price
		}
		if p.Target2 != nil {
			row.EP19T2 = p.Target2.Price
		}
	}

	switch {
	case row.LegacyPresent && !rendered:
		row.Class = ClassLegacyOnly
		return row
	case !row.LegacyPresent && rendered:
		row.Class = ClassEP19Only
	case !row.LegacyPresent && !rendered:
		row.Class = ClassNeither
		return row
	}

	// Both blocks are on screen. Find every exact semantic disagreement.
	var dims []Dimension
	if !row.EP19Executabl {
		dims = append(dims, DimExecutionAuthorization)
	}
	switch {
	case !row.LegacyEntryBand:
		dims = append(dims, DimEntryNotComparable)
	case row.EP19ZoneLow > 0 && row.EP19ZoneHigh > 0:
		// Compare on the digits the report prints: ④ at one decimal, ⑲ at two.
		lo, hi := round1(row.LegacyEntryLow), round1(row.LegacyEntryHigh)
		zlo, zhi := round2(row.EP19ZoneLow), round2(row.EP19ZoneHigh)
		if hi < zlo || zhi < lo {
			dims = append(dims, DimEntryDisjoint)
		}
	}
	if row.LegacyStop > 0 && row.EP19Invalid > 0 {
		ls, inv := round1(row.LegacyStop), round2(row.EP19Invalid)
		switch {
		case ls > inv:
			dims = append(dims, DimStopOrdering)
			rep.StopLegacyAbove++
		case ls < inv:
			dims = append(dims, DimStopOrdering)
			rep.StopLegacyBelow++
		default:
			rep.StopEqual++
		}
	}
	if row.LegacyTPLow > 0 && row.EP19T1 > 0 {
		hi := row.LegacyTPHigh
		if row.EP19T2 > 0 {
			if round1(hi) < round2(row.EP19T1) || round2(row.EP19T2) < round1(row.LegacyTPLow) {
				dims = append(dims, DimTargetDisjoint)
			}
		} else if round1(hi) < round2(row.EP19T1) {
			dims = append(dims, DimTargetDisjoint)
		}
	}
	switch entryplan.EntryStatus(row.EP19Status) {
	case entryplan.StatusInsufficientData, entryplan.StatusNoValidEntry, entryplan.StatusTooExtended:
		dims = append(dims, DimStatusThesis)
	}

	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
	row.Dimensions = dims
	// ENTRY_NOT_NUMERICALLY_COMPARABLE alone is a presentation fact, not a contradiction.
	material := false
	for _, d := range dims {
		if d != DimEntryNotComparable {
			material = true
		}
	}
	if row.Class == "" {
		if material {
			row.Class = ClassBothContra
		} else {
			row.Class = ClassBothConsist
		}
	}
	return row
}

func printSummary(rep Report) {
	fmt.Printf("\n=== EP-10 legacy ④ vs ⑲ convergence measurement ===\n")
	fmt.Printf("session %s  arm %s  regime %s  entries %d\n",
		rep.Session, rep.RegimeStudyArm, rep.Regime, rep.Entries)
	fmt.Printf("production_historical_execution = %s (replay-conditioned research observation)\n\n",
		rep.ProductionHistoricalExec)
	for _, c := range []Class{ClassLegacyOnly, ClassEP19Only, ClassBothConsist, ClassBothContra, ClassNeither} {
		fmt.Printf("  %-24s %d\n", c, rep.Classes[c])
	}
	fmt.Printf("\ndimensions (D rows may carry several):\n")
	keys := make([]string, 0, len(rep.Dimensions))
	for k := range rep.Dimensions {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-36s %d\n", k, rep.Dimensions[Dimension(k)])
	}
	fmt.Printf("  stop: legacy above ⑲ invalidation %d, below %d, equal %d\n",
		rep.StopLegacyAbove, rep.StopLegacyBelow, rep.StopEqual)
	fmt.Printf("\n⑲ statuses among rendered sections:\n")
	skeys := make([]string, 0, len(rep.StatusCounts))
	for k := range rep.StatusCounts {
		skeys = append(skeys, k)
	}
	sort.Strings(skeys)
	for _, k := range skeys {
		fmt.Printf("  %-20s %d\n", k, rep.StatusCounts[k])
	}
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func loadScannerConfig(path string) (scanner.Config, error) {
	var cfg struct {
		Scanner scanner.Config `yaml:"scanner"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg.Scanner, err
	}
	err = yaml.Unmarshal(b, &cfg)
	return cfg.Scanner, err
}
