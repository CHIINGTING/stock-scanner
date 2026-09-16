package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ScopeReport is what the measurement produces. Every number in it is measured; nothing here
// is projected except the fields whose names say Projected.
type ScopeReport struct {
	BaselineCommit     string `json:"baseline_commit"`
	MethodologyVersion string `json:"methodology_version"`
	RuleVersion        string `json:"rule_version"`
	RunDir             string `json:"run_dir"`

	SymbolsLoaded         int     `json:"symbols_loaded"`
	CacheLoadSeconds      float64 `json:"cache_load_seconds"`
	AxisFrom              string  `json:"axis_from"`
	AxisTo                string  `json:"axis_to"`
	EligibleSessions      int     `json:"eligible_sessions"`
	EligibleFrom          string  `json:"eligible_from"`
	EligibleTo            string  `json:"eligible_to"`
	ReplayCoveredSessions int     `json:"replay_covered_sessions"`

	ScannerConfig ScannerFilters `json:"scanner_config"`

	Cost      []SessionCost `json:"cost"`
	PeakHeapM float64       `json:"peak_heap_mb"`

	Fidelity FidelityDelta `json:"fidelity_delta"`

	Funnels           map[string]*recon.Funnel `json:"funnels"`
	FunnelPassSeconds float64                  `json:"funnel_pass_seconds"`

	ExpectedZeros ExpectedZeros `json:"expected_zeros"`

	ReplayManifestDigest string   `json:"replay_manifest_digest,omitempty"`
	ReplayCaveats        []string `json:"replay_caveats,omitempty"`
}

// ScannerFilters records the production filter values the funnel depends on. Recorded
// because a funnel read without them is uninterpretable.
type ScannerFilters struct {
	MinPrice                   float64 `json:"min_price"`
	MinAvgVolume               float64 `json:"min_avg_volume"`
	TopN                       int     `json:"top_n"`
	UseAdjustedClose           bool    `json:"use_adjusted_close"`
	EnableSignalGuardrailScore bool    `json:"enable_signal_guardrail_scoring"`
	EnableRSRank               bool    `json:"enable_rs_rank"`
	EnableNewHigh              bool    `json:"enable_new_high"`
	EnableVCP                  bool    `json:"enable_vcp"`
	EnableMomentumFlow         bool    `json:"enable_momentum_flow"`
	EnableEntryPlan            bool    `json:"enable_entry_plan"`
	EnableTrendExtension       bool    `json:"enable_trend_extension"`
	EnableCandlestick          bool    `json:"enable_candlestick"`
	EnableTechnicalIndicators  bool    `json:"enable_technical_indicators"`
	EnableBias                 bool    `json:"enable_bias"`
}

// SessionCost is one timed reconstruction.
type SessionCost struct {
	Date     string  `json:"date"`
	Arm      string  `json:"arm"`
	Universe int     `json:"universe"`
	Entries  int     `json:"entries"`
	Seconds  float64 `json:"seconds"`
	// RotationSeconds / RSSeconds / EnrichSeconds are measured separately so the dominant
	// cost is a measurement rather than a guess.
	RotationSeconds float64 `json:"rotation_seconds"`
	RSSeconds       float64 `json:"rs_seconds"`
	EnrichSeconds   float64 `json:"enrich_seconds"`
	PlanSeconds     float64 `json:"plan_seconds"`
}

// FidelityDelta is what dropping sector rotation and the RS table would have cost.
type FidelityDelta struct {
	Sessions int `json:"sessions"`
	Plans    int `json:"plans"`
	// SectoredPlans is how many of those plans belong to a symbol configs/sectors.yaml
	// actually lists. It is the DENOMINATOR that makes a zero interpretable: only a sectored
	// stock can see a non-neutral flow direction, so only a sectored stock can have its
	// consolidation — and therefore its zone — moved by dropping the rotation.
	SectoredPlans int `json:"sectored_plans"`
	// SectoredZonePlans narrows it again to the sectored plans that actually carry a zone,
	// which is the population a "zone changed" count can be non-zero over at all.
	SectoredZonePlans int `json:"sectored_zone_plans"`
	ZonePlans         int `json:"zone_plans"`

	PlansDiffering      int            `json:"plans_differing"`
	StatusChanged       int            `json:"status_changed"`
	SemanticChanged     int            `json:"semantic_changed"`
	ZonePresenceChanged int            `json:"zone_presence_changed"`
	ZoneChanged         int            `json:"zone_changed"`
	TargetChanged       int            `json:"target_changed"`
	ScoreChanged        int            `json:"score_changed"`
	ActionChanged       int            `json:"action_changed"`
	ConsolBucketChanged int            `json:"consolidation_bucket_changed"`
	StatusMoves         map[string]int `json:"status_moves"`
}

// ExpectedZeros is EP-9 §19 and §6, measured on reconstructed plans rather than inferred.
type ExpectedZeros struct {
	Sessions                   int `json:"sessions"`
	Plans                      int `json:"plans"`
	BreakoutSemantic           int `json:"breakout_semantic"`
	WaitBreakoutStatus         int `json:"wait_breakout_status"`
	ScannerBreakoutBuy         int `json:"scanner_breakout_buy_watch_action"`
	ValuationCeilingApplied    int `json:"valuation_ceiling_applied"`
	ValuationCeilingNotBinding int `json:"valuation_ceiling_not_binding"`
	ValuationCeilingUnavail    int `json:"valuation_ceiling_unavailable"`
}

func runScope(r *recon.Reconstructor, replay *recon.ReplayArchive, costSessions, fidSessions,
	funnelSessions []string, runDir string, sc scanner.Config) *ScopeReport {

	rep := &ScopeReport{
		BaselineCommit:     entryplanbacktest.BaselineCommit,
		MethodologyVersion: entryplanbacktest.MethodologyVersion,
		RuleVersion:        entryplan.RuleVersion,
		Funnels:            map[string]*recon.Funnel{},
		ScannerConfig: ScannerFilters{
			MinPrice: sc.MinPrice, MinAvgVolume: sc.MinAvgVolume, TopN: sc.TopN,
			UseAdjustedClose:           sc.UseAdjustedClose,
			EnableSignalGuardrailScore: sc.EnableSignalGuardrailScoring,
			EnableRSRank:               sc.EnableRSRank, EnableNewHigh: sc.EnableNewHigh,
			EnableVCP: sc.EnableVCP, EnableMomentumFlow: sc.EnableMomentumFlow,
			EnableEntryPlan: sc.EnableEntryPlan, EnableTrendExtension: sc.EnableTrendExtension,
			EnableCandlestick:         sc.EnableCandlestick,
			EnableTechnicalIndicators: sc.EnableTechnicalIndicators, EnableBias: sc.EnableBias,
		},
		ReplayCaveats: recon.ReplayCaveats(),
	}

	// ── 1. Cost, both arms ────────────────────────────────────────────────────────────────
	fmt.Println("── cost ──")
	var peak float64
	blind := recon.NewFunnel(recon.ArmRegimeBlind, recon.FullFidelity)
	pit := recon.NewFunnel(recon.ArmReplayedPIT, recon.FullFidelity)
	var pitDates []string

	for _, d := range costSessions {
		for _, arm := range []recon.Arm{recon.ArmRegimeBlind, recon.ArmReplayedPIT} {
			if arm == recon.ArmReplayedPIT && !replay.Covers(d) {
				fmt.Printf("  %s  %-13s SKIPPED (no replay file — never downgraded to blind)\n", d, arm)
				continue
			}
			res, err := r.Reconstruct(d, arm, recon.FullFidelity)
			if err != nil {
				fmt.Printf("  %s  %-13s ERROR %v\n", d, arm, err)
				continue
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if h := float64(ms.HeapAlloc) / (1 << 20); h > peak {
				peak = h
			}
			rep.Cost = append(rep.Cost, SessionCost{Date: d, Arm: string(arm),
				Universe: res.Universe, Entries: len(res.Entries), Seconds: res.Elapsed.Seconds()})
			fmt.Printf("  %s  %-13s universe %4d  entries %4d  %6.2fs\n",
				d, arm, res.Universe, len(res.Entries), res.Elapsed.Seconds())
		}
	}
	rep.PeakHeapM = peak

	// ── 2. Where the time goes ────────────────────────────────────────────────────────────
	if len(costSessions) > 0 {
		rep.Cost = append(rep.Cost, breakdown(r, costSessions[len(costSessions)-1]))
	}

	// ── 2b. Funnel pass, both arms, over the wider session set ────────────────────────────
	fmt.Printf("\n── funnel pass over %d sessions, both arms ──\n", len(funnelSessions))
	fpStart := time.Now()
	for i, d := range funnelSessions {
		res, err := r.Reconstruct(d, recon.ArmRegimeBlind, recon.FullFidelity)
		if err == nil {
			blind.Add(res)
		}
		if replay.Covers(d) {
			res, err := r.Reconstruct(d, recon.ArmReplayedPIT, recon.FullFidelity)
			if err == nil {
				pit.Add(res)
				pitDates = append(pitDates, d)
			}
		}
		if (i+1)%50 == 0 {
			fmt.Printf("    %d/%d  (%.0fs)\n", i+1, len(funnelSessions), time.Since(fpStart).Seconds())
		}
	}
	rep.FunnelPassSeconds = time.Since(fpStart).Seconds()
	rep.Funnels["REGIME_BLIND/FULL"] = blind
	rep.Funnels["REPLAYED_PIT/FULL"] = pit

	// ── 3. Fidelity delta ─────────────────────────────────────────────────────────────────
	fmt.Println("\n── fidelity delta (FULL vs EMPTY sector/RS; measurement only, never a result arm) ──")
	rep.Fidelity = measureFidelity(r, replay, fidSessions, r.Sectors.Codes())
	f := rep.Fidelity
	fmt.Printf("  sessions %d  plans %d  (sectored %d, with a zone %d, sectored+zone %d)\n",
		f.Sessions, f.Plans, f.SectoredPlans, f.ZonePlans, f.SectoredZonePlans)
	fmt.Printf("  differing %d  status %d  semantic %d  zone presence %d  zone bounds %d  "+
		"target %d  consolidation bucket %d  rocket score %d  watch action %d\n",
		f.PlansDiffering, f.StatusChanged, f.SemanticChanged, f.ZonePresenceChanged,
		f.ZoneChanged, f.TargetChanged, f.ConsolBucketChanged, f.ScoreChanged, f.ActionChanged)

	// ── 4. Expected zeros, measured ───────────────────────────────────────────────────────
	fmt.Println("\n── expected zeros (measured on reconstructed plans) ──")
	rep.ExpectedZeros = measureZeros(r, replay, funnelSessions)
	z := rep.ExpectedZeros
	fmt.Printf("  plans %d  BREAKOUT semantic %d  WAIT_BREAKOUT %d  scanner BREAKOUT_BUY %d\n",
		z.Plans, z.BreakoutSemantic, z.WaitBreakoutStatus, z.ScannerBreakoutBuy)
	fmt.Printf("  valuation ceiling: APPLIED %d  NOT_BINDING %d  UNAVAILABLE %d\n",
		z.ValuationCeilingApplied, z.ValuationCeilingNotBinding, z.ValuationCeilingUnavail)

	// ── 5. Funnels ────────────────────────────────────────────────────────────────────────
	fmt.Println("\n── coverage funnel ──")
	names := make([]string, 0, len(rep.Funnels))
	for k := range rep.Funnels {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		printFunnel(n, rep.Funnels[n])
	}

	// ── 6. Make REPLAYED_PIT re-derivable ─────────────────────────────────────────────────
	if len(pitDates) > 0 {
		m, err := replay.HashDates(pitDates)
		if err == nil {
			rep.ReplayManifestDigest = m.Digest
			b, _ := json.MarshalIndent(m, "", "  ")
			_ = os.WriteFile(filepath.Join(runDir, "replay_manifest.json"), append(b, '\n'), 0o644)
			_ = replay.CopyDates(pitDates, filepath.Join(runDir, "market_replay"))
			fmt.Printf("\nreplay archive copied and hashed: %d files, digest %s\n",
				len(pitDates), m.Digest[:16])
		} else {
			fmt.Printf("\nreplay manifest failed: %v\n", err)
		}
	}
	return rep
}

// breakdown re-runs one session and reports where the time went, using the stage timings the
// reconstructor measures itself rather than a second copy of the call sequence.
func breakdown(r *recon.Reconstructor, date string) SessionCost {
	c := SessionCost{Date: date, Arm: "BREAKDOWN"}
	res, err := r.Reconstruct(date, recon.ArmRegimeBlind, recon.FullFidelity)
	if err != nil {
		return c
	}
	c.Universe, c.Entries, c.Seconds = res.Universe, len(res.Entries), res.Elapsed.Seconds()
	c.RotationSeconds = res.Stages.Rotation.Seconds()
	c.RSSeconds = res.Stages.RSTable.Seconds()
	c.EnrichSeconds = res.Stages.Enrich.Seconds()
	c.PlanSeconds = res.Stages.AttachPlan.Seconds()
	fmt.Printf("\n  breakdown %s: truncate %.2fs  rotation %.2fs  RS %.2fs  enrich %.2fs  "+
		"entryplan %.2fs  (total %.2fs)\n", date, res.Stages.Truncate.Seconds(),
		c.RotationSeconds, c.RSSeconds, c.EnrichSeconds, c.PlanSeconds, c.Seconds)
	return c
}

// measureFidelity compares FULL against EMPTY context UNDER THE REPLAYED_PIT ARM.
//
// It must not be measured regime-blind: with Available=false every plan is INSUFFICIENT_DATA
// with no zone, so a "0 zones changed" result would say nothing about the context and
// everything about the regime. Sessions with no replay file are skipped rather than
// downgraded.
func measureFidelity(r *recon.Reconstructor, replay *recon.ReplayArchive, sessions []string, sectored map[string]bool) FidelityDelta {
	d := FidelityDelta{StatusMoves: map[string]int{}}
	for _, s := range sessions {
		if !replay.Covers(s) {
			continue
		}
		full, err := r.Reconstruct(s, recon.ArmReplayedPIT, recon.FullFidelity)
		if err != nil {
			continue
		}
		empty, err := r.Reconstruct(s, recon.ArmReplayedPIT, recon.EmptyContext)
		if err != nil {
			continue
		}
		d.Sessions++
		byCode := make(map[string]*scanner.WatchlistEntry, len(empty.Entries))
		for i := range empty.Entries {
			byCode[empty.Entries[i].A.Symbol] = &empty.Entries[i]
		}
		for i := range full.Entries {
			a := &full.Entries[i]
			b := byCode[a.A.Symbol]
			if a.EntryPlan == nil || b == nil || b.EntryPlan == nil {
				continue
			}
			d.Plans++
			if sectored[a.A.Symbol] {
				d.SectoredPlans++
				if a.EntryPlan.IdealEntry != nil || b.EntryPlan.IdealEntry != nil {
					d.SectoredZonePlans++
				}
			}
			if a.EntryPlan.IdealEntry != nil {
				d.ZonePlans++
			}
			changed := false
			if a.EntryPlan.Status != b.EntryPlan.Status {
				d.StatusChanged++
				d.StatusMoves[string(a.EntryPlan.Status)+"→"+string(b.EntryPlan.Status)]++
				changed = true
			}
			if recon.PlanSemantic(a.EntryPlan) != recon.PlanSemantic(b.EntryPlan) {
				d.SemanticChanged++
				changed = true
			}
			if (a.EntryPlan.IdealEntry == nil) != (b.EntryPlan.IdealEntry == nil) {
				d.ZonePresenceChanged++
				changed = true
			}
			if a.Consol.Bucket != b.Consol.Bucket {
				d.ConsolBucketChanged++
				changed = true
			}
			if !sameZone(a.EntryPlan, b.EntryPlan) {
				d.ZoneChanged++
				changed = true
			}
			if !samePtr(target1(a.EntryPlan), target1(b.EntryPlan)) {
				d.TargetChanged++
				changed = true
			}
			if a.RocketScore != b.RocketScore {
				d.ScoreChanged++
				changed = true
			}
			if a.WatchAction != b.WatchAction {
				d.ActionChanged++
				changed = true
			}
			if changed {
				d.PlansDiffering++
			}
		}
	}
	return d
}

// measureZeros counts the §19 and §6 zeros on REPLAYED_PIT plans — the only arm that produces
// an entry at all, so the only arm on which "no BREAKOUT was ever published" is a real
// measurement rather than a consequence of an unresolved policy.
func measureZeros(r *recon.Reconstructor, replay *recon.ReplayArchive, sessions []string) ExpectedZeros {
	var z ExpectedZeros
	for _, s := range sessions {
		if !replay.Covers(s) {
			continue
		}
		res, err := r.Reconstruct(s, recon.ArmReplayedPIT, recon.FullFidelity)
		if err != nil {
			continue
		}
		z.Sessions++
		for i := range res.Entries {
			e := &res.Entries[i]
			if e.WatchAction == scanner.ActBreakoutBuy {
				z.ScannerBreakoutBuy++
			}
			p := e.EntryPlan
			if p == nil {
				continue
			}
			z.Plans++
			if recon.PlanSemantic(p) == entryplan.EntrySemanticBreakout {
				z.BreakoutSemantic++
			}
			if p.Status == entryplan.StatusWaitBreakout {
				z.WaitBreakoutStatus++
			}
			if p.EntryTrace == nil {
				continue
			}
			switch p.EntryTrace.Targets.ValuationCeiling {
			case entryplan.ValuationCeilingApplied:
				z.ValuationCeilingApplied++
			case entryplan.ValuationCeilingNotBinding:
				z.ValuationCeilingNotBinding++
			case entryplan.ValuationCeilingUnavailable:
				z.ValuationCeilingUnavail++
			}
		}
	}
	return z
}

func sameZone(a, b *entryplan.Plan) bool {
	if (a.IdealEntry == nil) != (b.IdealEntry == nil) {
		return false
	}
	if a.IdealEntry == nil {
		return true
	}
	return a.IdealEntry.Low == b.IdealEntry.Low && a.IdealEntry.High == b.IdealEntry.High
}

func target1(p *entryplan.Plan) *float64 {
	if p.Target1 == nil {
		return nil
	}
	v := p.Target1.Price
	return &v
}

func samePtr(a, b *float64) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func printFunnel(name string, f *recon.Funnel) {
	fmt.Printf("\n  [%s]  sessions=%d\n", name, f.Sessions)
	fmt.Printf("    %-42s %8d\n", "candidate stock-session observations", f.Candidates)
	fmt.Printf("    %-42s %8d\n", "scanner observations available", f.ScannerObservations)
	fmt.Printf("    %-42s %8d\n", "  EntrySemantic resolved", f.SemanticResolved)
	fmt.Printf("    %-42s %8d\n", "  EntrySemantic unresolved", f.SemanticUnresolved)
	fmt.Printf("    %-42s %8d\n", "plans generated", f.PlansGenerated)
	for _, l := range f.StatusLines() {
		fmt.Printf("      %s\n", l)
	}
	fmt.Printf("    %-42s %8d\n", "plans with IdealEntry zone", f.WithZone)
	fmt.Printf("    %-42s %8d\n", "plans with MaxChasePrice", f.WithMaxChase)
	fmt.Printf("    %-42s %8d\n", "plans with Invalidation", f.WithInvalidation)
	fmt.Printf("    %-42s %8d\n", "plans with Target1", f.WithTarget1)
	fmt.Printf("    %-42s %8d\n", "plans with Target2", f.WithTarget2)
	fmt.Printf("    %-42s %8d\n", "plans with suitable valuation ceiling", f.WithSuitableValuationCeiling)
	if err := f.Reconcile(); err != nil {
		fmt.Printf("    RECONCILE FAILED: %v\n", err)
	} else {
		fmt.Printf("    reconciles: OK\n")
	}
}
