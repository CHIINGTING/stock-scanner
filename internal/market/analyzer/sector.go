package analyzer

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// sector blend weights: cycle strength dominates, news sentiment tilts. BASELINE.
const (
	sectorCycleWeight = 0.7
	sectorNewsWeight  = 0.3
)

// blendSector combines a sector's 0..100 cycle score with its -100..100 news sentiment
// (mapped to 0..100) into a single 0..100 standing.
func blendSector(s model.SectorScore) float64 {
	newsNorm := clamp(50+s.NewsScore/2, 0, 100)
	return clamp(sectorCycleWeight*s.CycleScore+sectorNewsWeight*newsNorm, 0, 100)
}

// stars maps a 0..100 blend to a ★1–5 rating.
func stars(blend float64) int {
	switch {
	case blend >= 80:
		return 5
	case blend >= 65:
		return 4
	case blend >= 50:
		return 3
	case blend >= 35:
		return 2
	default:
		return 1
	}
}

// AnalyzeSectorCycle scores the Sector Cycle module as the mean sector blend. Empty input →
// unavailable (the service excludes it from the aggregate).
func AnalyzeSectorCycle(sectors []model.SectorScore, weight float64) model.ModuleScore {
	if len(sectors) == 0 {
		return model.ModuleScore{Name: model.ModuleSectorCycle, Weight: weight, State: "n/a", Available: false}
	}
	var sum float64
	for _, s := range sectors {
		sum += blendSector(s)
	}
	avg := sum / float64(len(sectors))
	return model.ModuleScore{
		Name:      model.ModuleSectorCycle,
		Score:     avg,
		Weight:    weight,
		State:     fmt.Sprintf("avg %.0f", avg),
		Detail:    fmt.Sprintf("%d sectors", len(sectors)),
		Available: true,
	}
}

// RankSectors returns the sectors sorted strongest-first with ★ ratings and a short reason.
func RankSectors(sectors []model.SectorScore) []model.SectorRank {
	ranks := make([]model.SectorRank, 0, len(sectors))
	for _, s := range sectors {
		blend := blendSector(s)
		ranks = append(ranks, model.SectorRank{
			Name:   s.Name,
			Stars:  stars(blend),
			Score:  blend,
			Reason: fmt.Sprintf("Cycle %.0f・News %+.0f", s.CycleScore, s.NewsScore),
		})
	}
	sort.SliceStable(ranks, func(i, j int) bool { return ranks[i].Score > ranks[j].Score })
	return ranks
}

// TopOpportunities returns up to n strongest sectors (★≥4), with the reasons that qualified.
func TopOpportunities(sectors []model.SectorScore, n int) []model.Opportunity {
	sorted := sortedByBlend(sectors, true)
	var out []model.Opportunity
	for _, s := range sorted {
		if len(out) >= n {
			break
		}
		if stars(blendSector(s)) < 4 {
			continue
		}
		var reasons []string
		if s.CycleScore >= 65 {
			reasons = append(reasons, fmt.Sprintf("景氣循環偏強 (Cycle %.0f)", s.CycleScore))
		}
		if s.NewsScore > 10 {
			reasons = append(reasons, fmt.Sprintf("新聞偏多 (News %+.0f)", s.NewsScore))
		}
		if len(reasons) == 0 {
			reasons = append(reasons, fmt.Sprintf("綜合評分居前 (%.0f)", blendSector(s)))
		}
		out = append(out, model.Opportunity{Sector: s.Name, Reasons: reasons})
	}
	return out
}

// TopRisks returns up to n weakest sectors (★≤2), with the reasons that flagged them.
func TopRisks(sectors []model.SectorScore, n int) []model.Risk {
	sorted := sortedByBlend(sectors, false)
	var out []model.Risk
	for _, s := range sorted {
		if len(out) >= n {
			break
		}
		if stars(blendSector(s)) > 2 {
			continue
		}
		var reasons []string
		if s.CycleScore <= 40 {
			reasons = append(reasons, fmt.Sprintf("景氣循環轉弱 (Cycle %.0f)", s.CycleScore))
		}
		if s.NewsScore < -10 {
			reasons = append(reasons, fmt.Sprintf("新聞偏空 (News %+.0f)", s.NewsScore))
		}
		if len(reasons) == 0 {
			reasons = append(reasons, fmt.Sprintf("綜合評分落後 (%.0f)", blendSector(s)))
		}
		out = append(out, model.Risk{Sector: s.Name, Reasons: reasons})
	}
	return out
}

// sortedByBlend returns a copy of sectors sorted by blend, descending if desc else ascending.
func sortedByBlend(sectors []model.SectorScore, desc bool) []model.SectorScore {
	cp := append([]model.SectorScore(nil), sectors...)
	sort.SliceStable(cp, func(i, j int) bool {
		if desc {
			return blendSector(cp[i]) > blendSector(cp[j])
		}
		return blendSector(cp[i]) < blendSector(cp[j])
	})
	return cp
}
