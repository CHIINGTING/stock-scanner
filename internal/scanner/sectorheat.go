package scanner

import (
	"sort"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// Sector Heat (R12) — a COMPONENT of the existing SectorRotation, not a second sector score.
//
// The rotation engine already answers "which sector is strong". Heat answers a narrower
// question the existing score cannot: is the move BROAD and PARTICIPATED, or is it two heavy
// names dragging an index of six? That distinction is what the existing aggregation loses,
// because every component there is a mean and none of them records how many members actually
// had data.
//
// Three things are therefore deliberate:
//
//   - it rides the SAME member loop and the SAME taxonomy (configs/sectors.yaml). There is
//     no second sector mapping and no second pass over price history;
//   - it aggregates with MEDIANS and RATIOS, never means, so one limit-up member cannot
//     define a sector — the existing AvgReturn20 has exactly that weakness;
//   - it refuses to produce a number at all below a member floor. A 97/100 computed from two
//     stocks is not a precise reading, it is a wrong one wearing a precise costume.
//
// Heat NEVER feeds SectorRotation.Score, Stage, OppScore or ordering, and never reaches
// RocketScore. It is read only by the R12 shadow layer and the report.
// ──────────────────────────────────────────────────────────────────────────────

// Sector heat status.
const (
	SectorHeatOK               = "OK"
	SectorHeatInsufficientData = "INSUFFICIENT_DATA"
)

// MinHeatMembers is the floor for computing Heat at all.
//
// Calibrated against the ACTUAL taxonomy rather than picked round: configs/sectors.yaml holds
// 36 sectors with a median of 6 members; the two smallest (電機機械, 電感) have 2. At 4, a
// ratio moves in steps of 25% — coarse, but each step is still backed by real members, and
// only the genuinely unmeasurable sectors are excluded. Below that a "breadth percentage" is
// arithmetic theatre: with 2 members it can only ever be 0, 50 or 100.
const MinHeatMembers = 4

// Heat component weights (sum = 100). BASELINE hypotheses, not calibrated constants — they
// encode "breadth matters most, volume least", which is the claim R12 validation tests.
const (
	wHeatBreadth       = 0.35
	wHeatRelMomentum   = 0.30
	wHeatParticipation = 0.20
	wHeatVolume        = 0.15
)

// volumeConfirmRatio is the per-member volume expansion bar for the VolumeConfirmation
// component. Reuses the existing rotation threshold rather than introducing a second notion
// of "volume expanded".
const volumeConfirmRatio = volExpRatioMin

// SectorHeatView is one sector's heat reading.
//
// Every component is 0–100. Status must be checked first: on INSUFFICIENT_DATA the component
// values are meaningless and Heat is not populated — missing is not 0.
type SectorHeatView struct {
	Status string

	Heat float64 // 0–100 composite; valid only when Status == OK

	// Components (0–100).
	Breadth            float64 // % of valid members closing above their own MA20
	RelativeMomentum   float64 // cross-sectional percentile of MedianReturn20 (filled later)
	Participation      float64 // % of valid members with a positive 20-day return
	VolumeConfirmation float64 // % of valid members whose volume ratio cleared the bar

	// Sample-size disclosure. A percentage without its denominator cannot be audited after
	// the fact — the same discipline internal/market/analyzer/breadth.go applies to the
	// market universe, applied here to a sector.
	MemberCount      int     // members configured in sectors.yaml for this sector
	ValidMemberCount int     // members that actually had enough history to measure
	Coverage         float64 // ValidMemberCount / MemberCount * 100

	// MedianReturn20 is the robust sector return used for the cross-sectional ranking.
	// Median, not mean: one +30% member must not speak for the group.
	MedianReturn20 float64

	// Codes are the evidence codes this reading emitted (see trendext.go for the vocabulary).
	Codes []string
}

// HeatMemberInput is the per-member data Heat consumes.
//
// Exported, together with ComputeSectorHeat/NormalizeSectorHeat, so the R12 backtest can
// score sectors with the SAME aggregation the live scanner uses. A validation run that
// measured a reimplementation would be validating the wrong code. It is built inside the existing
// member loop, so nothing is recomputed and no second price pass happens.
type HeatMemberInput struct {
	AboveMA20   bool
	MA20Valid   bool
	Return20    float64
	VolumeRatio float64
}

// ComputeSectorHeat builds the per-sector part of Heat. RelativeMomentum stays 0 here: it is
// cross-sectional by definition and can only be filled once every sector has been built (the
// same two-phase shape the existing normalizeRelStrength uses).
func ComputeSectorHeat(memberCount int, members []HeatMemberInput) SectorHeatView {
	v := SectorHeatView{
		Status:           SectorHeatInsufficientData,
		MemberCount:      memberCount,
		ValidMemberCount: len(members),
	}
	if memberCount > 0 {
		v.Coverage = float64(len(members)) / float64(memberCount) * 100
	}
	if len(members) < MinHeatMembers {
		// Deliberately leaves every component at zero AND Status at INSUFFICIENT_DATA. A
		// caller that reads the numbers without checking Status gets zeros, not a plausible
		// mid-range value it might act on.
		return v
	}
	v.Status = SectorHeatOK

	var aboveMA20, positive, volConfirm, ma20Known int
	returns := make([]float64, 0, len(members))
	for _, m := range members {
		if m.MA20Valid {
			ma20Known++
			if m.AboveMA20 {
				aboveMA20++
			}
		}
		if m.Return20 > 0 {
			positive++
		}
		if m.VolumeRatio >= volumeConfirmRatio {
			volConfirm++
		}
		returns = append(returns, m.Return20)
	}

	// Breadth is measured against its OWN denominator — members whose MA20 could not be
	// computed are excluded rather than counted as "not above".
	if ma20Known > 0 {
		v.Breadth = float64(aboveMA20) / float64(ma20Known) * 100
	}
	v.Participation = float64(positive) / float64(len(members)) * 100
	v.VolumeConfirmation = float64(volConfirm) / float64(len(members)) * 100
	v.MedianReturn20 = median(returns)
	return v
}

// NormalizeSectorHeat fills RelativeMomentum and the composite Heat across all sectors.
//
// RelativeMomentum is a CROSS-SECTIONAL percentile of MedianReturn20: a sector is hot
// relative to the other sectors available today, not against an absolute return threshold
// that would drift with the market. Sectors on INSUFFICIENT_DATA are excluded from the
// ranking population as well as from the output — a sector that cannot be measured must not
// shift where the measurable ones rank.
func NormalizeSectorHeat(results []SectorRotation) {
	pool := make([]float64, 0, len(results))
	for i := range results {
		if h := results[i].Heat; h != nil && h.Status == SectorHeatOK {
			pool = append(pool, h.MedianReturn20)
		}
	}
	if len(pool) == 0 {
		return
	}
	for i := range results {
		h := results[i].Heat
		if h == nil || h.Status != SectorHeatOK {
			continue
		}
		// A single measurable sector cannot be ranked against peers; 50 is the honest
		// "no cross-sectional information", and the code below records why.
		if len(pool) < 2 {
			h.RelativeMomentum = 50
		} else {
			p, _ := indicator.PercentileRank(pool, h.MedianReturn20, 1)
			h.RelativeMomentum = p
		}
		h.Heat = wHeatBreadth*h.Breadth +
			wHeatRelMomentum*h.RelativeMomentum +
			wHeatParticipation*h.Participation +
			wHeatVolume*h.VolumeConfirmation
		h.Codes = heatCodes(h)
	}
}

// heatCodes emits the deterministic evidence codes for a heat reading.
func heatCodes(h *SectorHeatView) []string {
	var codes []string
	switch {
	case h.Heat >= heatStrongMin:
		codes = append(codes, CodeSectorHeatStrong)
	case h.Heat <= heatWeakMax:
		codes = append(codes, CodeSectorHeatWeak)
	}
	if h.Participation >= heatParticipationStrongMin {
		codes = append(codes, CodeSectorParticipationStrong)
	}
	if h.VolumeConfirmation >= heatVolumeConfirmMin {
		codes = append(codes, CodeSectorVolumeConfirm)
	}
	return codes
}

// median returns the middle value of a sample (mean of the two middles when even).
// Returns 0 for an empty sample; callers only reach it with a non-empty one.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}
