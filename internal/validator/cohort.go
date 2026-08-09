package validator

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/analysishistory"
)

// cohort.go joins the structured history sidecars (SidecarStore) with the EXISTING price
// machinery (PriceStore.Outcome → forward returns + MFE/MAE) to measure per-cohort edge
// (SPEC §18). It adds no new price/forward logic — it only buckets and aggregates.
//
// Completeness exclusion (§8): cohort predicates that rely on streaks/transitions must
// themselves require dataComplete / streakComplete, so incomplete data never pollutes a
// cohort. The built-in cohorts below do this via the helper accessors.

// Cohort is a named predicate over one sidecar stock record.
type Cohort struct {
	Name  string
	Match func(analysishistory.StockData) bool
}

// CohortStats aggregates forward outcomes for one cohort.
type CohortStats struct {
	Name          string
	N             int             // matched signals with an evaluable outcome
	Wins          int             // primary-horizon return > 0
	WinRate       float64         // Wins / N
	AvgReturnByH  map[int]float64 // horizon → mean return %
	AvgMFE        float64         // mean MaxRunup %
	AvgMAE        float64         // mean MaxDrawdown %
	PrimaryReturn float64         // mean primary-horizon return %
}

// EvaluateCohorts scans the given signal dates, loads each sidecar, matches records to
// cohorts, computes forward outcomes from the price cache, and aggregates. primaryHorizon
// selects the win/headline horizon (falls back to the largest available for a signal).
func EvaluateCohorts(sc *SidecarStore, ps *PriceStore, dates []time.Time, horizons []int, primaryHorizon int, cohorts []Cohort) []CohortStats {
	type acc struct {
		n, wins        int
		retSum         map[int]float64
		retCount       map[int]int
		mfeSum, maeSum float64
		primSum        float64
		primCount      int
	}
	accs := make([]acc, len(cohorts))
	for i := range accs {
		accs[i] = acc{retSum: map[int]float64{}, retCount: map[int]int{}}
	}

	for _, date := range dates {
		snap, ok := sc.Load(date)
		if !ok {
			continue
		}
		for _, st := range snap.Stocks {
			for ci := range cohorts {
				if !cohorts[ci].Match(st) {
					continue
				}
				series := ps.Series(st.Code)
				if series == nil {
					continue
				}
				oc, ok := series.Outcome(date, horizons)
				if !ok || len(oc.Returns) == 0 {
					continue
				}
				a := &accs[ci]
				a.n++
				a.mfeSum += oc.MaxRunup
				a.maeSum += oc.MaxDrawdown
				for h, r := range oc.Returns {
					a.retSum[h] += r
					a.retCount[h]++
				}
				if pr, ok := primaryReturn(oc.Returns, primaryHorizon); ok {
					a.primSum += pr
					a.primCount++
					if pr > 0 {
						a.wins++
					}
				}
			}
		}
	}

	out := make([]CohortStats, len(cohorts))
	for i := range cohorts {
		a := accs[i]
		cs := CohortStats{Name: cohorts[i].Name, N: a.n, Wins: a.wins, AvgReturnByH: map[int]float64{}}
		if a.n > 0 {
			cs.AvgMFE = a.mfeSum / float64(a.n)
			cs.AvgMAE = a.maeSum / float64(a.n)
		}
		for h, sum := range a.retSum {
			if a.retCount[h] > 0 {
				cs.AvgReturnByH[h] = sum / float64(a.retCount[h])
			}
		}
		if a.primCount > 0 {
			cs.WinRate = float64(a.wins) / float64(a.primCount)
			cs.PrimaryReturn = a.primSum / float64(a.primCount)
		}
		out[i] = cs
	}
	return out
}

// primaryReturn picks the primary horizon's return, falling back to the largest available.
func primaryReturn(returns map[int]float64, primary int) (float64, bool) {
	if r, ok := returns[primary]; ok {
		return r, true
	}
	best, bestH, found := 0.0, -1, false
	for h, r := range returns {
		if h > bestH {
			bestH, best, found = h, r, true
		}
	}
	return best, found
}

// ── Built-in cohorts (completeness-gated) ────────────────────────────────────────

// TrustBuyForeignReversalNotOverheated is the "投信連買 + 外資由賣轉買 + BIAS20 未過熱"
// cohort (§18). Requires complete data on both institution categories.
func TrustBuyForeignReversalNotOverheated(minTrustDays int) Cohort {
	return Cohort{
		Name: "投信連買+外資由賣轉買+BIAS20未過熱",
		Match: func(s analysishistory.StockData) bool {
			if s.Institution == nil || s.Bias == nil {
				return false
			}
			tr := s.Institution.InvestmentTrust
			fr := s.Institution.Foreign
			if !tr.DataComplete || !tr.StreakComplete || !fr.DataComplete {
				return false
			}
			overheated := s.Bias.Risk == "HIGH" || s.Bias.Risk == "EXTREME"
			return tr.ConsecutiveBuyDays >= minTrustDays &&
				fr.Transition == "SELL_TO_BUY" && !overheated
		},
	}
}

// HighBiasForeignDistribution is the "高正乖離 + 法人由買轉賣" reduce-warning cohort (§18).
func HighBiasForeignDistribution() Cohort {
	return Cohort{
		Name: "高正乖離+法人由買轉賣",
		Match: func(s analysishistory.StockData) bool {
			if s.Institution == nil || s.Bias == nil {
				return false
			}
			fr := s.Institution.Foreign
			if !fr.DataComplete {
				return false
			}
			high := s.Bias.Risk == "HIGH" || s.Bias.Risk == "EXTREME"
			return high && fr.Transition == "BUY_TO_SELL"
		},
	}
}
