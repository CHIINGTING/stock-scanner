package valuation

import (
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
)

// EntryPlanEvidence is the smallest valuation-domain view needed by EntryPlan.
// It contains only the BASE scenario and the independent model-suitability verdict.
type EntryPlanEvidence struct {
	Status      Availability
	BaseTarget  *float64
	Suitability Suitability
	AsOf        string
	Model       string
}

// BuildEntryPlanEvidence is pure over PIT-filtered inputs. Its date checks are defence in
// depth: callers must use LoadValuation/LoadView(asOf), but future-dated records are refused
// even if a caller hands one in directly.
func BuildEntryPlanEvidence(asOf, symbol string, current float64, bars []fetcher.Candle, v *Valuation, f *fundamental.View) EntryPlanEvidence {
	out := EntryPlanEvidence{Status: Unavailable, AsOf: asOf, Suitability: SuitabilityInsufficientData}
	if _, err := time.Parse("2006-01-02", asOf); err != nil || v == nil || v.TrailingDate == "" || v.TrailingDate > asOf {
		return out
	}
	var baseClose *float64
	for i := len(bars) - 1; i >= 0; i-- {
		d := bars[i].Date.Format("2006-01-02")
		if d == v.TrailingDate && d <= asOf && bars[i].Close > 0 {
			x := bars[i].Close
			baseClose = &x
			break
		}
	}
	tp := ComputeTargetPrice(TargetInput{Symbol: symbol, AsOf: asOf, CurrentPrice: &current,
		EPSBasePrice: baseClose, EPSBaseDate: v.TrailingDate, Valuation: *v})
	out.Status, out.Model = tp.Status, tp.Rule
	for i := range tp.Scenarios {
		if tp.Scenarios[i].Name == ScenarioBase && tp.Scenarios[i].Status == Available {
			out.BaseTarget = tp.Scenarios[i].TargetPrice
		}
	}
	in := SuitabilityInput{Persistence: v.HistoricalPE.Persistence,
		WindowSessions: v.HistoricalPE.Quality.WindowSessions,
		RelativeIQR:    v.HistoricalPE.Dispersion.RelativeIQR}
	if out.BaseTarget != nil {
		if up, ok := UpsidePct(current, *out.BaseTarget); ok {
			in.BaseUpsidePct = &up
		}
	}
	if f != nil && !f.ObservedAt.IsZero() && f.ObservedAt.Format("2006-01-02") <= asOf && f.Financials != nil &&
		!f.Financials.PublishedAt.IsZero() && f.Financials.PublishedAt.Format("2006-01-02") <= asOf {
		fin := f.Financials
		in.EarningsPeriod = fmt.Sprintf("%d-Q%d", fin.Period.Year, fin.Period.Quarter)
		in.EarningsSource = f.Source
		if fin.CumulativeEPS != nil {
			switch {
			case *fin.CumulativeEPS > 0 && fin.NetIncome > 0:
				in.Earnings = EarningsPositive
			case *fin.CumulativeEPS <= 0 && fin.NetIncome <= 0:
				in.Earnings = EarningsNonPositive
			default:
				in.Earnings = EarningsUnknown
			}
		}
	}
	out.Suitability = ClassifySuitability(in).Suitability
	return out
}
