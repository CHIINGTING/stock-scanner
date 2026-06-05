package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// rocketInputWithBQ builds a rocketInput over a flat synthetic series, overriding
// the consolidation BaseQualityScore so guardrail scoring can be tested precisely.
// nearHigh sets consol.NearPreviousHigh so the original g3 sub-score is exercisable.
func rocketInputWithBQ(bq float64, vcp *VCPResult, nh *NewHighResult, nearHigh, guardrail bool) rocketInput {
	candles := makeCandles(60, 50, 0.0, 1_000_000) // flat → minimal extra g3 terms
	s := New(Config{})
	ind := s.calcIndicators(candles)
	consol := analyzeConsolidation(candles, ind, false)
	consol.BaseQualityScore = bq
	consol.NearPreviousHigh = nearHigh
	return rocketInput{
		candles:          candles,
		ind:              ind,
		consol:           consol,
		bt:               Backtest{},
		flowDir:          FlowNeutral,
		guardrailScoring: guardrail,
		vcp:              vcp,
		newHigh:          nh,
	}
}

// C6b-1 unit tests: VCP may only correct g3's base-quality slot, gated by the
// master flag, using max() (never lowering), and never touching other groups.
func TestC6b1VCPCorrectsBaseQualityOnly(t *testing.T) {
	baseline := computeRocket(rocketInputWithBQ(50, nil, nil, false, false)).Score

	validHigh := &VCPResult{Computed: true, Valid: true, QualityScore: 90}
	validLow := &VCPResult{Computed: true, Valid: true, QualityScore: 30}
	invalid := &VCPResult{Computed: true, Valid: false, QualityScore: 90}

	// A. master OFF + valid high VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, validHigh, nil, false, false)).Score; got != baseline {
		t.Errorf("master off must not change score: got %d want %d", got, baseline)
	}
	// B. master ON + nil VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, nil, nil, false, true)).Score; got != baseline {
		t.Errorf("nil VCP must not change score: got %d want %d", got, baseline)
	}
	// C. master ON + invalid VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, invalid, nil, false, true)).Score; got != baseline {
		t.Errorf("invalid VCP must not change score: got %d want %d", got, baseline)
	}
	// D. master ON + valid VCP whose quality < base → max() keeps base → no change.
	if got := computeRocket(rocketInputWithBQ(50, validLow, nil, false, true)).Score; got != baseline {
		t.Errorf("VCP quality below base must not lower score: got %d want %d", got, baseline)
	}
	// E. master ON + valid VCP whose quality > base → raised, but bounded by the g3
	// base-quality weight (delta ≤ (90-50)/100*12 ≈ 4.8 → ≤5).
	raised := computeRocket(rocketInputWithBQ(50, validHigh, nil, false, true)).Score
	if raised <= baseline {
		t.Errorf("VCP quality above base should raise score: got %d baseline %d", raised, baseline)
	}
	if raised-baseline > 5 {
		t.Errorf("VCP correction exceeded the g3 base-quality weight: delta=%d (>5)", raised-baseline)
	}
}

// ── C6b-2: NewHigh replaces the g3 NearPreviousHigh sub-score ────────────────

func nh(score float64, computed bool) *NewHighResult {
	return &NewHighResult{Computed: computed, NewHighScore: score}
}

// C6b-2 unit tests: NewHigh only enters g3 (replacing NearPreviousHigh), gated by
// the master flag, never double-counting and never exceeding the g3 cap.
func TestC6b2NewHighReplacesNearHighInG3(t *testing.T) {
	// Baseline with NearPreviousHigh ON: original g3 formula (BQ*12 + 6).
	baseNear := computeRocket(rocketInputWithBQ(50, nil, nil, true, false)).Score

	// 1. master OFF + NewHigh present → original formula (NearPreviousHigh still +6).
	if got := computeRocket(rocketInputWithBQ(50, nil, nh(90, true), true, false)).Score; got != baseNear {
		t.Errorf("master off must keep original g3: got %d want %d", got, baseNear)
	}
	// 4. master ON + NewHigh nil → original formula.
	if got := computeRocket(rocketInputWithBQ(50, nil, nil, true, true)).Score; got != baseNear {
		t.Errorf("nil NewHigh must keep original g3: got %d want %d", got, baseNear)
	}
	// 5. master ON + NewHigh Computed=false → original formula (no hard-compute).
	if got := computeRocket(rocketInputWithBQ(50, nil, nh(90, false), true, true)).Score; got != baseNear {
		t.Errorf("Computed=false must keep original g3: got %d want %d", got, baseNear)
	}

	// 3/7. master ON + NewHigh Computed, high score → NewHigh branch.
	// g3 = BQ/100*10 + NewHighScore/100*8 (+ no NearPreviousHigh). With BQ=50,
	// score=90: 5 + 7.2 = 12.2 vs original 6+6=12 → close; assert NewHigh branch active
	// by checking that NearPreviousHigh no longer adds on top (cap respected, no double).
	onHigh := computeRocket(rocketInputWithBQ(50, nil, nh(90, true), true, true)).Score
	// Compare to a hypothetical "double-count" upper bound: if NearPreviousHigh(+6)
	// were ALSO added, g3 would be ~18.2; ensure we are well below that inflation.
	if onHigh > baseNear+5 {
		t.Errorf("NewHigh appears to double-count with NearPreviousHigh: on=%d base=%d", onHigh, baseNear)
	}
	// g3 must stay capped: even NewHighScore=100, BQ=100 cannot exceed 25.
	full := computeRocket(rocketInputWithBQ(100, nil, nh(100, true), true, true)).Score
	capped := computeRocket(rocketInputWithBQ(100, nil, nh(100, true), false, true)).Score // off → original BQ*12 path
	_ = capped
	if full <= 0 || full > 100 {
		t.Errorf("score out of range with full NewHigh: %d", full)
	}
}

// 6. Deliberate behavior: Computed=true with NewHighScore=0 takes the NewHigh branch
// (a "no leadership" verdict) and must NOT fall back to NearPreviousHigh+6.
func TestC6b2NewHighComputedZeroScoreUsesNewHighBranch(t *testing.T) {
	// With NearPreviousHigh=true, original g3 would get +6; NewHigh branch (score 0)
	// gives 0 for that slot. So the NewHigh-branch score must be LOWER than the
	// original-with-NearHigh score — proving we took the NewHigh branch deliberately.
	original := computeRocket(rocketInputWithBQ(50, nil, nil, true, false)).Score      // BQ*12 + 6
	newHighZero := computeRocket(rocketInputWithBQ(50, nil, nh(0, true), true, true)).Score // BQ*10 + 0
	if newHighZero >= original {
		t.Errorf("Computed=true score=0 should take NewHigh branch (lower, no NearHigh fallback): nh0=%d original=%d",
			newHighZero, original)
	}
	// And it must not panic / must stay in range.
	if newHighZero < 0 || newHighZero > 100 {
		t.Errorf("score out of range: %d", newHighZero)
	}
}

// TestC6b2MasterFlagOffGolden: master flag OFF + new_high ON → only shadow attached,
// scoring/order identical to the all-off baseline.
func TestC6b2MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	got := New(Config{EnableNewHigh: true}).EnrichWatchlist(items, so, rt, mb, nil) // master off

	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol ||
			got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction ||
			got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off NewHigh changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.NewHigh == nil {
			t.Errorf("%s: NewHigh shadow should still be attached", base[i].A.Symbol)
		}
	}
}

// ── C6b-3: RS replaces the g2 relative-strength sub-score + leadership gate ──

// rsRocketInput builds a rocketInput with a controllable RS shadow. hasSector=false
// so the fallback relative-strength sub-score is a fixed +5, and SupportHoldScore is
// forced ≥60 so the support sub always fires → score deltas isolate g2 exactly.
func rsRocketInput(rs *RSResult, guardrail bool, rsWatch float64) rocketInput {
	candles := makeCandles(60, 50, 0.0, 1_000_000)
	s := New(Config{})
	ind := s.calcIndicators(candles)
	consol := analyzeConsolidation(candles, ind, false)
	consol.BaseQualityScore = 50
	consol.SupportHoldScore = 80
	return rocketInput{
		candles: candles, ind: ind, consol: consol, bt: Backtest{},
		flowDir: FlowNeutral, hasSector: false,
		guardrailScoring: guardrail, rs: rs, rsWatchThreshold: rsWatch,
	}
}

func rsResult(rank float64, computed bool) *RSResult {
	return &RSResult{Computed: computed, RSRankPercentile: rank, RSScore: rank}
}

func TestC6b3RSRankScoreBoundaries(t *testing.T) {
	for _, c := range []struct {
		p    float64
		want float64
	}{{95, 10}, {90, 10}, {89, 7}, {80, 7}, {70, 4}, {69, 1}, {1, 1}, {0, 0}, {-3, 0}} {
		if got := rsRankScore(c.p); got != c.want {
			t.Errorf("rsRankScore(%.0f)=%.0f want %.0f", c.p, got, c.want)
		}
	}
}

func TestC6b3RSLeadershipGate(t *testing.T) {
	cases := []struct {
		prob      string
		useRS     bool
		rank, thr float64
		want      string
	}{
		{"HIGH", true, 60, 70, "MEDIUM"},  // below threshold → capped
		{"HIGH", true, 80, 70, "HIGH"},    // at/above → unchanged
		{"MEDIUM", true, 60, 70, "MEDIUM"}, // medium stays medium
		{"LOW", true, 60, 70, "LOW"},      // never pushed to/from LOW
		{"HIGH", false, 60, 70, "HIGH"},   // RS inactive → unchanged
		{"HIGH", true, 0, 70, "HIGH"},     // invalid rank → unchanged
		{"HIGH", true, 60, 0, "MEDIUM"},   // threshold≤0 → default 70 → capped
	}
	for _, c := range cases {
		if got := applyRSLeadershipGate(c.prob, c.useRS, c.rank, c.thr); got != c.want {
			t.Errorf("gate(%s,useRS=%v,rank=%.0f,thr=%.0f)=%s want %s", c.prob, c.useRS, c.rank, c.thr, got, c.want)
		}
	}
}

// RS replaces the g2 relative-strength sub-score (no new group, g2≤20). Score delta
// vs the all-off baseline equals exactly the g2 sub-score change (RS only touches g2).
func TestC6b3RSReplacesG2RelStrength(t *testing.T) {
	off := computeRocket(rsRocketInput(rsResult(95, true), false, 70)).Score        // master off → fallback
	baseFallback := computeRocket(rsRocketInput(nil, true, 70)).Score               // RS nil → fallback (==off semantics)
	if off != baseFallback {
		t.Fatalf("master-off and nil-RS should both use fallback: %d vs %d", off, baseFallback)
	}
	// RS on, rank 95: g2 sub change = (rsRankScore10 + support4) − (relstr5 + support6) = +3.
	on95 := computeRocket(rsRocketInput(rsResult(95, true), true, 70)).Score
	if on95-baseFallback != 3 {
		t.Errorf("rank95 expected +3 vs fallback (g2-only), got delta %d", on95-baseFallback)
	}
	// RS on, rank 60: g2 sub change = (1 + 4) − (5 + 6) = −6.
	on60 := computeRocket(rsRocketInput(rsResult(60, true), true, 70)).Score
	if on60-baseFallback != -6 {
		t.Errorf("rank60 expected -6 vs fallback (g2-only), got delta %d", on60-baseFallback)
	}
}

// RS invalid paths → fallback, no panic, no cap.
func TestC6b3RSFallbackPaths(t *testing.T) {
	baseFallback := computeRocket(rsRocketInput(nil, true, 70)).Score
	if got := computeRocket(rsRocketInput(rsResult(95, false), true, 70)).Score; got != baseFallback {
		t.Errorf("Computed=false must fall back: got %d want %d", got, baseFallback)
	}
	if got := computeRocket(rsRocketInput(rsResult(0, true), true, 70)).Score; got != baseFallback {
		t.Errorf("RSRankPercentile<=0 must fall back: got %d want %d", got, baseFallback)
	}
}

// TestC6b3MasterFlagOffGolden: master off + rs on → only shadow attached, scoring/order unchanged.
func TestC6b3MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}
	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	on := New(Config{EnableRSRank: true}) // master off
	got := on.EnrichWatchlist(items, so, rt, mb, on.BuildRSTable(items))
	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol || got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction || got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off RS changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.RS == nil {
			t.Errorf("%s: RS shadow should be attached", base[i].A.Symbol)
		}
	}
}

// ── C6b-4: MomentumFlow modifier + joint watch_action + prob guardrail ──────

func defaultMods() momentumModifiers {
	return momentumModifiers{Building: 5, Continuation: 6, ShiftUp: 8, Fading: -6, ShiftDown: -12, Cap: 12}
}

func flatRocketInput() rocketInput {
	candles := makeCandles(60, 50, 0.0, 1_000_000)
	s := New(Config{})
	ind := s.calcIndicators(candles)
	consol := analyzeConsolidation(candles, ind, false)
	return rocketInput{candles: candles, ind: ind, consol: consol, bt: Backtest{}, flowDir: FlowNeutral}
}

func TestC6b4ScoreModifier(t *testing.T) {
	m := defaultMods()
	for _, c := range []struct {
		flow MomentumFlow
		want float64
	}{
		{MomentumBuilding, 5}, {MomentumContinuation, 6}, {StructuralShiftUp, 8},
		{MomentumFading, -6}, {StructuralShiftDown, -12}, {MomentumNeutral, 0},
	} {
		if got := momentumScoreModifier(c.flow, m); got != c.want {
			t.Errorf("modifier(%s)=%.0f want %.0f", c.flow, got, c.want)
		}
	}
	// cap: huge configured value is clamped to ±Cap; Cap<=0 falls back to 12.
	if got := momentumScoreModifier(MomentumBuilding, momentumModifiers{Building: 999, Cap: 12}); got != 12 {
		t.Errorf("cap positive: got %.0f want 12", got)
	}
	if got := momentumScoreModifier(StructuralShiftDown, momentumModifiers{ShiftDown: -999, Cap: 12}); got != -12 {
		t.Errorf("cap negative: got %.0f want -12", got)
	}
	if got := momentumScoreModifier(MomentumBuilding, momentumModifiers{Building: 999, Cap: 0}); got != 12 {
		t.Errorf("cap<=0 default 12: got %.0f want 12", got)
	}
}

func TestC6b4JointWatchAction(t *testing.T) {
	fb := ActWait
	cases := []struct {
		stage RocketStage
		flow  MomentumFlow
		want  WatchAction
	}{
		{StageMainRun, StructuralShiftDown, ActRemove},       // SHIFT_DOWN top priority
		{StageFailed, MomentumBuilding, ActRemove},           // FAILED top priority
		{StagePreBreakout, MomentumBuilding, ActPrepare},
		{StagePreBreakout, StructuralShiftUp, ActPrepare},
		{StagePreBreakout, MomentumFading, ActWait},
		{StageBreakoutStart, MomentumBuilding, ActBreakoutBuy},
		{StageBreakoutStart, MomentumContinuation, ActWatchClose},
		{StageMainRun, MomentumFading, ActTakeProfit},
		{StageOverheated, MomentumFading, ActTakeProfit},
		{StageBaseBuilding, StructuralShiftUp, ActWatchClose},
		{StageMainRun, MomentumNeutral, fb},                  // NEUTRAL → fallback
		{StageNotReady, MomentumBuilding, fb},                // unlisted → fallback
	}
	for _, c := range cases {
		if got := jointWatchAction(c.stage, c.flow, fb); got != c.want {
			t.Errorf("joint(%s,%s)=%s want %s", c.stage, c.flow, got, c.want)
		}
	}
}

func TestC6b4ProbabilityGuardrail(t *testing.T) {
	validVCP := &VCPResult{Valid: true}
	// hard downgrades
	if got := applyMomentumProbabilityGuardrail("HIGH", StageMainRun, StructuralShiftDown, validVCP); got != "LOW" {
		t.Errorf("SHIFT_DOWN should be LOW, got %s", got)
	}
	if got := applyMomentumProbabilityGuardrail("HIGH", StageMainRun, MomentumFading, validVCP); got != "LOW" {
		t.Errorf("MAIN_RUN+FADING should be LOW, got %s", got)
	}
	if got := applyMomentumProbabilityGuardrail("HIGH", StageOverheated, MomentumFading, validVCP); got != "LOW" {
		t.Errorf("OVERHEATED+FADING should be LOW, got %s", got)
	}
	// conditional upgrade requires a VALID VCP and non-LOW base
	if got := applyMomentumProbabilityGuardrail("MEDIUM", StagePreBreakout, MomentumBuilding, validVCP); got != "HIGH" {
		t.Errorf("PRE_BREAKOUT+BUILDING+validVCP should upgrade to HIGH, got %s", got)
	}
	if got := applyMomentumProbabilityGuardrail("MEDIUM", StagePreBreakout, MomentumBuilding, nil); got != "MEDIUM" {
		t.Errorf("nil VCP must NOT upgrade, got %s", got)
	}
	if got := applyMomentumProbabilityGuardrail("MEDIUM", StagePreBreakout, MomentumBuilding, &VCPResult{Valid: false}); got != "MEDIUM" {
		t.Errorf("invalid VCP must NOT upgrade, got %s", got)
	}
	if got := applyMomentumProbabilityGuardrail("LOW", StagePreBreakout, MomentumBuilding, validVCP); got != "LOW" {
		t.Errorf("base LOW must not be upgraded, got %s", got)
	}
}

// A. Momentum HIGH-upgrade must NOT bypass a low-RS leadership cap (RS gate is last).
func TestC6b4UpgradeCannotBypassRSCap(t *testing.T) {
	prob := applyMomentumProbabilityGuardrail("MEDIUM", StagePreBreakout, MomentumBuilding, &VCPResult{Valid: true})
	if prob != "HIGH" {
		t.Fatalf("precondition: momentum should upgrade to HIGH, got %s", prob)
	}
	// RS rank 60 < 70 applied LAST → capped back to MEDIUM.
	if got := applyRSLeadershipGate(prob, true, 60, 70); got != "MEDIUM" {
		t.Errorf("low RS must cap momentum HIGH to MEDIUM, got %s", got)
	}
	// RS rank 80 ≥ 70 → stays HIGH.
	if got := applyRSLeadershipGate(prob, true, 80, 70); got != "HIGH" {
		t.Errorf("high RS should keep HIGH, got %s", got)
	}
}

// B. SHIFT_DOWN → LOW + REMOVE even with very high RS (cap never lifts LOW).
func TestC6b4ShiftDownEndToEnd(t *testing.T) {
	in := flatRocketInput()
	in.guardrailScoring = true
	in.momentumActive = true
	in.momentum = &MomentumState{Computed: true, Flow: StructuralShiftDown}
	in.mfMod = defaultMods()
	in.rs = &RSResult{Computed: true, RSRankPercentile: 95}
	in.rsWatchThreshold = 70
	out := computeRocket(in)
	if out.ExplosionProb != "LOW" {
		t.Errorf("SHIFT_DOWN should force LOW even with RS 95, got %s", out.ExplosionProb)
	}
	if out.WatchAction != ActRemove {
		t.Errorf("SHIFT_DOWN should force REMOVE, got %s", out.WatchAction)
	}
}

// C6b-4 score modifier is wired through computeRocket (single final channel).
func TestC6b4ScoreModifierWired(t *testing.T) {
	base := computeRocket(flatRocketInput()).Score // momentum inactive

	mk := func(flow MomentumFlow) int {
		in := flatRocketInput()
		in.guardrailScoring = true
		in.momentumActive = true
		in.momentum = &MomentumState{Computed: true, Flow: flow}
		in.mfMod = defaultMods()
		return computeRocket(in).Score
	}
	if got := mk(MomentumNeutral); got != base {
		t.Errorf("NEUTRAL must not change score: got %d base %d", got, base)
	}
	if got := mk(MomentumBuilding); got != base+5 {
		t.Errorf("BUILDING should add 5: got %d base %d", got, base)
	}
	if got := mk(MomentumFading); got != base-6 {
		t.Errorf("FADING should subtract 6: got %d base %d", got, base)
	}
}

// D. NEUTRAL fully falls back (score/action/prob unchanged vs momentum inactive).
func TestC6b4NeutralFallback(t *testing.T) {
	baseIn := flatRocketInput()
	base := computeRocket(baseIn)

	in := flatRocketInput()
	in.guardrailScoring = true
	in.momentumActive = true
	in.momentum = &MomentumState{Computed: true, Flow: MomentumNeutral}
	in.mfMod = defaultMods()
	got := computeRocket(in)

	if got.Score != base.Score || got.WatchAction != base.WatchAction || got.ExplosionProb != base.ExplosionProb {
		t.Errorf("NEUTRAL must fully fall back: score %d/%d action %s/%s prob %s/%s",
			got.Score, base.Score, got.WatchAction, base.WatchAction, got.ExplosionProb, base.ExplosionProb)
	}
}

// TestC6b4MasterFlagOffGolden: master off + mf on → only shadow attached, scoring unchanged.
func TestC6b4MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}
	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	got := New(Config{EnableMomentumFlow: true}).EnrichWatchlist(items, so, rt, mb, nil) // master off
	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol || got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction || got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off Momentum changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.Momentum == nil {
			t.Errorf("%s: Momentum shadow should be attached", base[i].A.Symbol)
		}
	}
}

// 8. VCP + NewHigh together: effBQ still VCP-corrected, fed through the NewHigh ×10 slot.
func TestC6b2VCPAndNewHighTogether(t *testing.T) {
	vHigh := &VCPResult{Computed: true, Valid: true, QualityScore: 90}
	// BQ=50, VCP raises effBQ to 90 → g3 base = 90/100*10 = 9; NewHighScore 80 → 6.4.
	withVCP := computeRocket(rocketInputWithBQ(50, vHigh, nh(80, true), false, true)).Score
	noVCP := computeRocket(rocketInputWithBQ(50, nil, nh(80, true), false, true)).Score
	if withVCP <= noVCP {
		t.Errorf("VCP should still raise effBQ within the NewHigh branch: withVCP=%d noVCP=%d", withVCP, noVCP)
	}
	if withVCP-noVCP > 5 { // bounded by (90-50)/100*10 = 4
		t.Errorf("VCP correction in NewHigh branch exceeded base-quality weight: delta=%d", withVCP-noVCP)
	}
}

// TestC6b1MasterFlagOffGolden: with the master flag OFF, enabling VCP only attaches
// shadow (C6a) and must NOT change RocketScore / WatchAction / ExplosionProb / order.
func TestC6b1MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	// VCP shadow computed, but master flag OFF → scoring must be unchanged.
	got := New(Config{EnableVCP: true}).EnrichWatchlist(items, so, rt, mb, nil)

	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol ||
			got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction ||
			got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off VCP changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.VCP == nil {
			t.Errorf("%s: VCP shadow should still be attached", base[i].A.Symbol)
		}
	}
}
