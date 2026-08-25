package fx

import "math"

// USD/TWD decomposed against the broad dollar.
//
// "USDTWD rose" is not the same fact as "the TWD weakened". If the dollar rose against
// everything, USD/TWD rose too and Taiwan had nothing to do with it. The research question —
// does the CURRENCY carry information about Taiwanese equities — cannot be answered without
// separating those, and USD/TWD alone cannot separate them.
//
// The split here is deliberately the simplest one that is interpretable and causal:
//
//	ΔUSDTWD_t  =  α + β·ΔDXY_t  +  residual_t
//
// β and α come from a trailing window that ENDS BEFORE t. The residual is what the local
// currency did that the dollar does not explain.
//
// # Why the fit excludes the point it explains
//
// A full-sample regression, or one whose window includes t, lets the observation help choose
// the coefficients that then "predict" it. The residual shrinks, the decomposition looks
// clean, and the whole thing is look-ahead wearing a lab coat. TestDecompositionExcludesThePoint
// holds the window open by one.

// Decomposition is one Taiwan session's split of the local currency move.
type Decomposition struct {
	Status Status `json:"status"`

	AsOfTaiwanDate string `json:"as_of_taiwan_date"`
	// BarDate is the FX session both changes were measured on — strictly before the Taiwan
	// date, same contract as Metrics.
	BarDate string `json:"bar_date"`

	Window int     `json:"window"`
	Alpha  float64 `json:"alpha"`
	Beta   float64 `json:"beta"`
	R2     float64 `json:"r2"`

	USDTWDChange float64 `json:"usdtwd_change"`
	DXYChange    float64 `json:"dxy_change"`
	// Predicted is what the broad dollar alone implies for USD/TWD.
	Predicted float64 `json:"predicted"`
	// Residual is the TWD-SPECIFIC move: positive = TWD weaker than the dollar explains.
	Residual float64 `json:"residual"`
	// ResidualZ scales the residual by the trailing dispersion of residuals, so "unusually
	// TWD-specific" means the same thing across volatility regimes.
	ResidualZ float64 `json:"residual_z"`
}

// Residual direction, named after the LOCAL currency and independent of the dollar.
const (
	ResidualTWDWeak    = "RESIDUAL_TWD_WEAK"   // TWD fell by more than the dollar explains
	ResidualTWDStrong  = "RESIDUAL_TWD_STRONG" // TWD held up better than the dollar explains
	ResidualTWDNeutral = "RESIDUAL_TWD_NEUTRAL"
	ResidualUnknown    = "UNKNOWN"
)

// ResidualContext buckets a residual z-score using the same bands as the headline context,
// so "strong" means the same thing in both.
func ResidualContext(z float64, cfg Config) string {
	cfg = cfg.Defaulted()
	switch {
	case z >= cfg.MildZ:
		return ResidualTWDWeak
	case z <= -cfg.MildZ:
		return ResidualTWDStrong
	default:
		return ResidualTWDNeutral
	}
}

// DecomposeAsOf splits the change over cfg.ContextWindow sessions into a dollar part and a
// TWD-specific part, using only information available to a Taiwan session on taiwanDate.
//
// window is how many past observations fit the regression; zero uses PercentileWindow.
func DecomposeAsOf(usdtwd, dxy *Series, taiwanDate string, cfg Config, window int) Decomposition {
	cfg = cfg.Defaulted()
	if window <= 0 {
		window = cfg.PercentileWindow
	}
	out := Decomposition{
		Status: StatusNoData, AsOfTaiwanDate: taiwanDate, Window: window,
	}
	if usdtwd == nil || dxy == nil || taiwanDate == "" {
		return out
	}

	// Align the two series on the dates BOTH cover, keeping only bars strictly before the
	// Taiwan session. The venues label their days differently (London vs New York), so the
	// intersection is the only set where a paired change means anything.
	dxyBy := make(map[string]float64, len(dxy.Bars))
	for _, b := range dxy.Bars {
		if b.Close > 0 && finite(b.Close) {
			dxyBy[b.Date] = b.Close
		}
	}
	var dates []string
	var a, d []float64
	for _, b := range usdtwd.Bars {
		if b.Date >= taiwanDate || b.Close <= 0 || !finite(b.Close) {
			continue
		}
		if dv, ok := dxyBy[b.Date]; ok {
			dates = append(dates, b.Date)
			a = append(a, b.Close)
			d = append(d, dv)
		}
	}
	w := cfg.ContextWindow
	if len(a) < w+1 {
		return out
	}
	out.BarDate = dates[len(dates)-1]
	out.Status = StatusInsufficientData

	// Paired windowed changes, one per aligned observation that has a full lookback.
	var series []pairedChange
	for i := w; i < len(a); i++ {
		if a[i-w] <= 0 || d[i-w] <= 0 {
			continue
		}
		y := (a[i]/a[i-w] - 1) * 100
		x := (d[i]/d[i-w] - 1) * 100
		if finite(x) && finite(y) {
			series = append(series, pairedChange{x, y})
		}
	}
	if len(series) < 2 {
		return out
	}

	cur := series[len(series)-1]
	out.USDTWDChange, out.DXYChange = cur.y, cur.x

	// The fit sees everything EXCEPT the observation it is about to explain.
	fit := series[:len(series)-1]
	if len(fit) > window {
		fit = fit[len(fit)-window:]
	}
	if len(fit) < cfg.PercentileMinSample {
		return out
	}

	alpha, beta, r2, ok := ols(fit)
	if !ok {
		return out
	}
	out.Alpha, out.Beta, out.R2 = alpha, beta, r2
	out.Predicted = alpha + beta*cur.x
	out.Residual = cur.y - out.Predicted
	if !finite(out.Predicted) || !finite(out.Residual) {
		out.Status = StatusInsufficientData
		return out
	}

	// Scale by the dispersion of the FITTING window's own residuals — again excluding the
	// current point, so the yardstick is not moved by what it measures.
	res := make([]float64, 0, len(fit))
	for _, o := range fit {
		res = append(res, o.y-(alpha+beta*o.x))
	}
	if sd := stdev(res); sd > 0 && finite(sd) {
		if z := out.Residual / sd; finite(z) {
			out.ResidualZ = z
		}
	}
	out.Status = StatusOK
	return out
}

// minRegressorSD is the smallest dispersion, in percentage points, that a window's dollar
// changes must show before a sensitivity can be fitted to them.
const minRegressorSD = 0.05

// pairedChange is one aligned observation: x = ΔDXY, y = ΔUSDTWD, over the same window.
type pairedChange struct{ x, y float64 }

// ols fits y = alpha + beta*x and returns the coefficients with R².
func ols(pts []pairedChange) (alpha, beta, r2 float64, ok bool) {
	n := float64(len(pts))
	if n < 2 {
		return 0, 0, 0, false
	}
	var sx, sy float64
	for _, p := range pts {
		sx += p.x
		sy += p.y
	}
	mx, my := sx/n, sy/n

	var sxy, sxx, syy float64
	for _, p := range pts {
		dx, dy := p.x-mx, p.y-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	// A regressor that barely varied over the window carries no information about the pair.
	// Dividing by it does not fail loudly — it returns an enormous coefficient that looks
	// like a number, and the residual built from it is noise amplified by 1/ε. Refusing is
	// the only safe answer, and it must be a NEAR-zero test rather than an exact one: an
	// exactly-zero variance is the case that never happens in real data.
	//
	// The floor is on the regressor's standard deviation in percentage points. These are
	// windowed percentage changes of a currency index; a window across which that varied by
	// less than a twentieth of a point cannot identify a sensitivity.
	if !finite(sxy) || !finite(sxx) || math.Sqrt(sxx/n) < minRegressorSD {
		return 0, 0, 0, false
	}
	beta = sxy / sxx
	alpha = my - beta*mx
	if syy > 0 {
		r2 = (sxy * sxy) / (sxx * syy)
	}
	if !finite(alpha) || !finite(beta) {
		return 0, 0, 0, false
	}
	return alpha, beta, r2, true
}
