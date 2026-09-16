package study

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Cell is one (arm, wait) block plus every segmentation of it.
type Cell struct {
	Overall   Metrics            `json:"overall"`
	ByStatus  map[string]Metrics `json:"by_status"`
	BySeman   map[string]Metrics `json:"by_semantic"`
	ByRegime  map[string]Metrics `json:"by_regime"`
	ByPolicy  map[string]Metrics `json:"by_policy"`
	ByStratum map[string]Metrics `json:"by_stratum"`
}

// Comparison22 is EP-9 §22: zone fills against CHASE-ONLY fills.
//
// Chase-only means "arm C filled and arm B did not" — the fills the chase ceiling BOUGHT, and
// the only population in which the ceiling changed anything. Comparing arm C as a whole
// against arm B would compare two heavily overlapping sets (C fills everything B fills) and
// would dilute the difference to invisibility.
//
// A HIGHER FILL RATE IS NOT AN IMPROVEMENT. Arm C fills strictly more often by construction —
// MaxChasePrice >= IdealEntry.High is a production invariant — so the fill-rate line is
// arithmetic, not evidence. What decides whether the ceiling helps is what the extra fills
// DID: their returns, their excursions and their invalidation hits.
type Comparison22 struct {
	Wait          int     `json:"wait_sessions"`
	ZoneFills     Metrics `json:"zone_fills"`
	ChaseOnly     Metrics `json:"chase_only_fills"`
	ChaseOnlyN    int     `json:"chase_only_n"`
	ZoneN         int     `json:"zone_n"`
	Interpretable bool    `json:"interpretable"`
	Note          string  `json:"note"`
}

// Comparison23 is EP-9 §23: the executable arms against the SIGNAL_CLOSE benchmark.
//
// Both halves are reported and the difference between them IS the finding:
//
//	Unconditional  every candidate of each arm. The arms have different populations, so this
//	               mixes an execution effect with a SELECTION effect — the observations arm B
//	               filled are the ones that pulled back, which is not a random subset.
//	Matched        arm A restricted to the SAME observations arm B/C filled. The selection is
//	               held fixed, so the remaining difference is the execution.
//
// THE SELECTION EFFECT IS NOT A NUISANCE TO BE REMOVED, it is a property of the rule: waiting
// for a pullback IS a selection. The matched comparison isolates the execution; the
// unconditional one is what a trader following the rule would actually have got. Reporting
// only one of them would answer a different question from the one asked.
type Comparison23 struct {
	Arm  Arm `json:"arm"`
	Wait int `json:"wait_sessions"`

	ArmUnconditional      Metrics `json:"arm_unconditional"`
	BaselineUnconditional Metrics `json:"baseline_unconditional"`
	ArmMatched            Metrics `json:"arm_matched"`
	BaselineMatched       Metrics `json:"baseline_matched"`

	SelectionEffectNote string `json:"selection_effect_note"`
}

// OpportunityCost answers "what did missing cost us", and it is the one thing arm D cannot
// answer on its own.
//
// Arm D publishes MissedRate and NOTHING ELSE, deliberately: a trade that did not happen has
// no entry price, so it has no return, and fabricating one would put a number in a row whose
// entire content is that there was no trade. But the QUESTION — did the zone rule
// systematically skip winners or losers — is answerable from data already in hand, without
// inventing anything:
//
//	take the SIGNAL-CLOSE BENCHMARK outcome (arm A, which exists for every observation)
//	and split it by whether arm B filled.
//
// THIS IS NOT AN ENTRYPLAN FILL AND MUST NEVER BE PRESENTED AS ONE. Every number here is the
// unexecutable arm-A benchmark, restricted to a sub-population. It says what the STOCK did
// from the signal close, not what a trader following the plan got — the trader got nothing on
// a missed observation, which is what MissedRate already says.
type OpportunityCost struct {
	Wait int `json:"wait_sessions"`

	// MissedN / FilledN partition the observations arm B had an order for.
	MissedN int `json:"missed_n"`
	FilledN int `json:"filled_n"`

	// MissedBenchmark is the signal-close benchmark restricted to the observations arm B
	// MISSED; FilledBenchmark is the same benchmark restricted to the ones it FILLED. The
	// comparison between them is the answer: if the missed population's benchmark outperforms
	// the filled one's, the zone rule skipped winners.
	MissedBenchmark Metrics `json:"missed_benchmark"`
	FilledBenchmark Metrics `json:"filled_benchmark"`

	Note string `json:"note"`
}

// OpportunityNote is printed with every opportunity-cost table.
const OpportunityNote = "OPPORTUNITY COST, NOT AN ENTRYPLAN FILL. Both rows are the " +
	"unexecutable A_SIGNAL_CLOSE_BASELINE benchmark — what the STOCK did from the signal " +
	"close — split by whether the zone arm filled. A trader following the plan received " +
	"NOTHING on a missed observation; that fact is MissedRate, and arm D's own distributions " +
	"stay N=0. No fill price is fabricated anywhere."

// Run is one complete study pass: one regime provenance, every arm, every wait window.
type Run struct {
	Meta entryplanbacktest.RunMetadata `json:"meta"`

	// Provenance is the arm-level regime provenance this whole run was computed under. The
	// user's B9 ruling: metrics exist only for REPLAYED_PIT, and the blind pass is a coverage
	// statement with no metrics.
	Provenance  entryplanbacktest.RegimeProvenance `json:"regime_provenance"`
	MetricsHeld bool                               `json:"metrics_held"`
	Holds       string                             `json:"conclusions_hold_under"`
	Caveats     []string                           `json:"caveats"`

	Cells        map[string]Cell `json:"cells"` // "ARM/W" → cell
	Comparison22 []Comparison22  `json:"comparison_22"`
	Comparison23 []Comparison23  `json:"comparison_23"`
	// Opportunity is EP-9 §28 Q5: what the plans that never filled went on to do.
	Opportunity []OpportunityCost `json:"opportunity_cost"`

	// NonExecutable is the EP-9 §18 EXCLUDED block, keyed like Cells. These plans published a
	// zone but their STATUS is INSUFFICIENT_DATA or NO_VALID_ENTRY, so the layer never
	// published them as entries. They are reported here, in full, and are in NO headline
	// metric. See ExecutableStatuses.
	NonExecutable map[string]Metrics `json:"non_executable_excluded_block"`
	// NonExecutableReasons is the reason census of that block alone — the evidence for WHY it
	// exists, which is the point of keeping it.
	NonExecutableReasons map[string]int `json:"non_executable_reasons"`
	// NonExecutableStatuses is its status census.
	NonExecutableStatuses map[string]int `json:"non_executable_statuses"`
	// NonExecutableStatusesByRegime is the same block counted by regime, which is the fact
	// that makes it interpretable: the missing evaluator is attached to the SIDEWAYS and
	// DISTRIBUTION permissions, so the block should be concentrated there.
	NonExecutableStatusesByRegime map[string]int `json:"non_executable_by_regime"`

	Integrity *IntegrityReport `json:"integrity"`

	// zoneComp carries the zone-fill half of the §22 comparison between passes. Unexported:
	// it is scaffolding, not a result.
	zoneComp map[int]*compAcc `json:"-"`

	StratumCounts map[Stratum]int   `json:"stratum_counts"`
	MA60Counts    map[MA60Label]int `json:"ma60_counts"`
	StatusCounts  map[string]int    `json:"status_counts"`
	// ReasonCounts is the census of every Reason code the plans carried. It is what makes the
	// §31 MA60 answer readable: "0 plans blocked by a level reason" means nothing until a
	// reader can see WHICH reason the plans actually failed on.
	ReasonCounts map[string]int `json:"reason_counts"`
	RegimeCounts map[string]int `json:"regime_counts"`

	ReplayDigest string `json:"replay_archive_digest"`
	CacheDigest  string `json:"price_cache_digest"`
	DigestCovers string `json:"digests_cover"`
}

// CellKey is the map key for one (arm, wait) cell.
func CellKey(a Arm, w int) string { return fmt.Sprintf("%s/W%d", a, w) }

// Execute runs every arm and wait window over the observations and returns the finished run.
//
// Identity is enforced per (arm, wait) pass through one registry for the whole run, so the
// same (symbol, session, rule version, arm, wait) can never be counted twice into any metric.
func Execute(obs []Observation, prov entryplanbacktest.RegimeProvenance, caveats []string) *Run {
	r := &Run{
		Provenance:                    prov,
		MetricsHeld:                   prov == entryplanbacktest.RegimeReplayedPIT,
		Caveats:                       caveats,
		Cells:                         map[string]Cell{},
		Integrity:                     NewIntegrityReport(200),
		zoneComp:                      map[int]*compAcc{},
		StratumCounts:                 map[Stratum]int{},
		MA60Counts:                    map[MA60Label]int{},
		StatusCounts:                  map[string]int{},
		ReasonCounts:                  map[string]int{},
		NonExecutable:                 map[string]Metrics{},
		NonExecutableReasons:          map[string]int{},
		NonExecutableStatuses:         map[string]int{},
		NonExecutableStatusesByRegime: map[string]int{},
		RegimeCounts:                  map[string]int{},
	}
	r.Holds = "UNDER REPLAYED REGIME ONLY — posture is always UNKNOWN so analyzer rules R4/R5 " +
		"can never fire, and breadth is measured over the price cache's current membership " +
		"(survivorship; +3.91pp breadth_above_ma20 and +8.21pp advancing_ratio against the live " +
		"snapshot on 2026-08-31). These results are NOT a validation of production behaviour."
	if prov != entryplanbacktest.RegimeReplayedPIT {
		r.Holds = "COVERAGE STATEMENT ONLY — no metric is computed on a non-REPLAYED_PIT pass."
	}

	for i := range obs {
		o := &obs[i]
		r.StratumCounts[o.Stratum]++
		r.MA60Counts[o.MA60Block]++
		r.StatusCounts[string(o.Status)]++
		for _, rs := range o.Reasons {
			r.ReasonCounts[string(rs)]++
		}
		// The §18 excluded block's own census, restricted to plans that DID publish a zone —
		// a zoneless INSUFFICIENT_DATA plan was never a candidate for any arm and belongs in
		// the funnel, not here.
		if o.ZoneHigh != nil && !o.ExecutableStatus() {
			r.NonExecutableStatuses[string(o.Status)]++
			r.NonExecutableStatusesByRegime[string(o.Regime)]++
			for _, rs := range o.Reasons {
				r.NonExecutableReasons[string(rs)]++
			}
		}
		r.RegimeCounts[string(o.Regime)]++
	}

	ids := entryplanbacktest.NewObservationSet()

	// STREAMING, not retained. See Acc: holding every evaluated row peaked at 1.54 GB on a
	// 60-session slice and would have passed 9 GB on the real run. Nothing survives a pass but
	// the accumulators; the cross-arm comparisons re-evaluate the observations they need,
	// which is cheap next to the reconstruction and costs no memory.
	for _, w := range WaitWindows {
		// B and C first, so the keys arm A's MATCHED comparison needs are known before A runs
		// and A never has to be retained in full.
		for _, arm := range []Arm{ArmZoneLimit, ArmChase} {
			cb := newCellBuilder(arm, w)
			ne := NewNonExecutableAcc()
			var comp *compAcc
			if arm == ArmZoneLimit {
				comp = newCompAcc(w)
			}
			for i := range obs {
				row := Evaluate(&obs[i], arm, w)
				r.Integrity.Check(&row, ids, prov)
				if r.MetricsHeld {
					cb.add(&row)
					ne.Add(&row)
					if comp != nil {
						comp.addZone(&row)
					}
				}
			}
			if r.MetricsHeld {
				r.Cells[CellKey(arm, w)] = cb.finish()
				r.NonExecutable[CellKey(arm, w)] = ne.Finish(arm, w,
					"NON-EXECUTABLE STATUS (§18 excluded)")
				if comp != nil {
					r.zoneComp[w] = comp
				}
			}
		}
		// Arm D is the complement of arm B: same rows, relabelled, NO FILL FABRICATED.
		if r.MetricsHeld {
			d := NewAcc()
			for i := range obs {
				row := Evaluate(&obs[i], ArmZoneLimit, w)
				row.Arm = ArmMissed
				// AddCandidateOnly, NOT Add: arm D fabricates no fill, so it must also
				// publish no outcome. Every distribution in the D cell is N=0 and only
				// MissedRate can be read off it.
				d.AddCandidateOnly(&row)
			}
			r.Cells[CellKey(ArmMissed, w)] = Cell{Overall: d.Finish(ArmMissed, w, "")}
		}
		// Chase-only: arm C filled where arm B did not.
		if r.MetricsHeld {
			zoneFilled := map[string]bool{}
			for i := range obs {
				if Evaluate(&obs[i], ArmZoneLimit, w).Fill.Filled() {
					zoneFilled[key(&obs[i])] = true
				}
			}
			chaseOnly := NewAcc()
			for i := range obs {
				row := Evaluate(&obs[i], ArmChase, w)
				if row.Fill.Filled() && !zoneFilled[key(&obs[i])] {
					chaseOnly.Add(&row)
				}
			}
			zc := r.zoneComp[w]
			// BOTH N VALUES ARE READ OFF THE FINISHED ACCUMULATOR, never counted alongside
			// it. See compAcc: a parallel counter does not see the §18 filter, and a label
			// that disagrees with the distribution beside it is the defect this study exists
			// to avoid publishing.
			zoneFills := zc.acc.Finish(ArmZoneLimit, w, "zone fills")
			chaseOnlyM := chaseOnly.Finish(ArmChase, w, "chase-only fills")
			c := Comparison22{Wait: w,
				ZoneFills:  zoneFills,
				ChaseOnly:  chaseOnlyM,
				ZoneN:      zoneFills.NFilled,
				ChaseOnlyN: chaseOnlyM.NFilled,
			}
			c.Interpretable = c.ChaseOnlyN >= 30
			c.Note = comparison22Note
			if !c.Interpretable {
				c.Note += fmt.Sprintf(" N=%d chase-only fills is too small to conclude from.", c.ChaseOnlyN)
			}
			r.Comparison22 = append(r.Comparison22, c)
		}
		// EP-9 §28 Q5, the opportunity cost of a missed entry. Computed from arm A rows
		// split by arm B's fill outcome; no fill is invented for the missed side.
		if r.MetricsHeld {
			missedB, filledB := NewAcc(), NewAcc()
			var missedN, filledN int
			for i := range obs {
				zone := Evaluate(&obs[i], ArmZoneLimit, w)
				if !zone.ExecutableStatus {
					continue // EP-9 §18: not part of the headline population
				}
				if zone.Fill.Outcome != FillFilled && zone.Fill.Outcome != FillMissed {
					continue // NOT_APPLICABLE / UNAVAILABLE: arm B never had an order here
				}
				bench := Evaluate(&obs[i], ArmSignalCloseBaseline, w)
				if zone.Fill.Filled() {
					filledN++
					filledB.Add(&bench)
				} else {
					missedN++
					missedB.Add(&bench)
				}
			}
			r.Opportunity = append(r.Opportunity, OpportunityCost{Wait: w,
				MissedN: missedN, FilledN: filledN,
				MissedBenchmark: missedB.Finish(ArmSignalCloseBaseline, w, "benchmark | zone arm MISSED"),
				FilledBenchmark: filledB.Finish(ArmSignalCloseBaseline, w, "benchmark | zone arm FILLED"),
				Note:            OpportunityNote})
		}

		// Arm A last, and its MATCHED half is restricted to the keys B/C actually filled.
		cbA := newCellBuilder(ArmSignalCloseBaseline, w)
		baseAll := NewAcc()
		for i := range obs {
			row := Evaluate(&obs[i], ArmSignalCloseBaseline, w)
			r.Integrity.Check(&row, ids, prov)
			if !r.MetricsHeld {
				continue
			}
			cbA.add(&row)
			baseAll.Add(&row)
		}
		if !r.MetricsHeld {
			continue
		}
		r.Cells[CellKey(ArmSignalCloseBaseline, w)] = cbA.finish()

		for _, arm := range []Arm{ArmZoneLimit, ArmChase} {
			all, matched := NewAcc(), NewAcc()
			mb := NewAcc()
			for i := range obs {
				row := Evaluate(&obs[i], arm, w)
				all.Add(&row)
				if row.Fill.Filled() {
					matched.Add(&row)
					br := Evaluate(&obs[i], ArmSignalCloseBaseline, w)
					mb.Add(&br)
				}
			}
			r.Comparison23 = append(r.Comparison23, Comparison23{
				Arm: arm, Wait: w,
				ArmUnconditional:      all.Finish(arm, w, "unconditional"),
				BaselineUnconditional: baseAll.Finish(ArmSignalCloseBaseline, w, "unconditional"),
				ArmMatched:            matched.Finish(arm, w, "matched (filled only)"),
				BaselineMatched:       mb.Finish(ArmSignalCloseBaseline, w, "matched (arm filled)"),
				SelectionEffectNote:   selectionEffectNote,
			})
		}
	}
	return r
}

func key(o *Observation) string { return o.Symbol + "|" + o.SignalAsOf }

// compAcc holds the zone-fill half of the §22 comparison while a pass streams.
//
// IT KEEPS NO COUNT OF ITS OWN. An earlier version incremented a parallel `n` beside
// acc.Add(), and because Add applies the EP-9 §18 population filter while `n++` did not, the
// two diverged by exactly the excluded population: the §22 table printed "zone fills N = 3,662"
// beside a distribution of N = 3,384 on the same row. A count that can disagree with the
// sample it labels is worse than no count, so the only count is the accumulator's own.
type compAcc struct {
	acc *Acc
}

func newCompAcc(int) *compAcc { return &compAcc{acc: NewAcc()} }

func (c *compAcc) addZone(r *Row) {
	if r.Fill.Filled() {
		c.acc.Add(r)
	}
}

// cellBuilder feeds one row to the overall accumulator and to every segment it belongs to, so
// a segmented cell costs one pass rather than one pass per segment.
type cellBuilder struct {
	arm  Arm
	wait int

	overall *Acc
	status  map[string]*Acc
	seman   map[string]*Acc
	regime  map[string]*Acc
	policy  map[string]*Acc
	stratum map[string]*Acc
}

func newCellBuilder(arm Arm, w int) *cellBuilder {
	b := &cellBuilder{arm: arm, wait: w, overall: NewAcc(),
		status:  map[string]*Acc{},
		seman:   map[string]*Acc{},
		regime:  map[string]*Acc{},
		policy:  map[string]*Acc{},
		stratum: map[string]*Acc{}}
	// Every canonical key is pre-created at zero so an absent segment is reported as N=0
	// rather than omitted. WAIT_BREAKOUT is the one that matters: "N=0" is EP-9 §19's
	// FINDING, and a missing row would read as "not measured".
	for _, s := range []entryplan.EntryStatus{entryplan.StatusBuyNow, entryplan.StatusWaitPullback,
		entryplan.StatusTooExtended, entryplan.StatusWaitBreakout, entryplan.StatusNoValidEntry,
		entryplan.StatusInsufficientData} {
		b.status[string(s)] = NewAcc()
	}
	for _, s := range []entryplan.EntrySemantic{entryplan.EntrySemanticPullback,
		entryplan.EntrySemanticBreakout, entryplan.EntrySemanticUnknown} {
		b.seman[string(s)] = NewAcc()
	}
	for _, g := range []model.Regime{model.RegimeBull, model.RegimeBullPullback,
		model.RegimeSideways, model.RegimeDistribution, model.RegimeBear, model.RegimeUnknown} {
		b.regime[string(g)] = NewAcc()
	}
	for _, s := range AllStrata {
		b.stratum[string(s)] = NewAcc()
	}
	return b
}

func (b *cellBuilder) add(r *Row) {
	b.overall.Add(r)
	bucket(b.status, string(r.Obs.Status)).Add(r)
	bucket(b.seman, string(r.Obs.Semantic)).Add(r)
	bucket(b.regime, string(r.Obs.Regime)).Add(r)
	pol := string(r.Obs.Policy)
	if pol == "" {
		pol = "(no policy: regime was not a model.Regime value)"
	}
	bucket(b.policy, pol).Add(r)
	bucket(b.stratum, string(r.Obs.Stratum)).Add(r)
}

func bucket(m map[string]*Acc, k string) *Acc {
	a, ok := m[k]
	if !ok {
		a = NewAcc()
		m[k] = a
	}
	return a
}

func (b *cellBuilder) finish() Cell {
	c := Cell{Overall: b.overall.Finish(b.arm, b.wait, ""),
		ByStatus: map[string]Metrics{}, BySeman: map[string]Metrics{},
		ByRegime: map[string]Metrics{}, ByPolicy: map[string]Metrics{},
		ByStratum: map[string]Metrics{}}
	for k, a := range b.status {
		c.ByStatus[k] = a.Finish(b.arm, b.wait, k)
	}
	for k, a := range b.seman {
		c.BySeman[k] = a.Finish(b.arm, b.wait, k)
	}
	for k, a := range b.regime {
		c.ByRegime[k] = a.Finish(b.arm, b.wait, k)
	}
	for k, a := range b.policy {
		c.ByPolicy[k] = a.Finish(b.arm, b.wait, k)
	}
	for k, a := range b.stratum {
		c.ByStratum[k] = a.Finish(b.arm, b.wait, k)
	}
	return c
}

const comparison22Note = "A higher fill rate is arithmetic, not an improvement: " +
	"MaxChasePrice >= IdealEntry.High is a production invariant, so arm C fills a superset of " +
	"arm B. Judge the ceiling on what the EXTRA fills did."

const selectionEffectNote = "UNCONDITIONAL mixes execution with SELECTION: the observations " +
	"this arm filled are the ones that pulled back, which is not a random subset, and the " +
	"baseline's unconditional population includes plans the arm never filled. MATCHED holds " +
	"the selection fixed by restricting the baseline to exactly the observations the arm " +
	"filled, so the remaining difference is execution. Also note the base sessions differ: " +
	"the baseline's Return@h is Close[T+h] while the arm's is Close[F+h] with F >= T+1, so " +
	"the arm's window ends later in calendar time."

// DigestOf rolls a set of per-file digests into one, over the sorted keys.
//
// A digest of digests, and what it covers is stated on the run rather than left implied: the
// EXACT files this run read. Hashing a whole directory would break the moment an unrelated
// file appeared beside them; hashing only the names would not notice a changed file.
func DigestOf(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s %s\n", k, files[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
