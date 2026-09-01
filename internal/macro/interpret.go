package macro

import "time"

// Deterministic interpretation — a pure function from facts to described states.
//
// Every rule here is a comparison against a named constant, and every constant is in this
// file. Nothing predicts: "EASING" says the last policy move was a cut, not that another is
// coming. There is no macro score, because nothing in this repo has established how a rate
// cut, an inverted curve and a rising VIX should be weighed against each other, and a number
// invented for a dashboard would be a claim nobody could check.

// Thresholds. One place, so two callers cannot disagree about where flat ends.
const (
	// curveFlatBand is the half-width, in percentage points, of the band around zero that
	// counts as flat. Below it the curve is inverted, above it upward-sloping.
	curveFlatBand = 0.10
	// inflationEpsilon is the smallest change in the year-on-year rate, in percentage
	// points, that counts as a trend. Without it a move from 3.20% to 3.19% would be
	// reported as disinflation.
	inflationEpsilon = 0.001 // ratios: 0.001 = 0.1 percentage point

	vixLow      = 15.0
	vixNormal   = 20.0
	vixElevated = 30.0
	vixHigh     = 40.0

	// Freshness tolerances, in days, by publication frequency. One threshold for everything
	// would be wrong in both directions: a Treasury yield from twenty days ago is stale,
	// while a CPI print from twenty days ago is simply the current one.
	//
	// The monthly figure is measured from the END of its period, not the start. A July CPI
	// print covers a month that ended on the 31st and is published in mid-August; measuring
	// from July 1st would make the most recent figure available look two months old and mark
	// every monthly series permanently stale. Forty-five days from period end means "the
	// next month's release is now overdue", which is what stale should mean here.
	dailyToleranceDays   = 5
	monthlyToleranceDays = 45
)

// FedDirection describes the LAST policy move — never the next one.
type FedDirection string

const (
	FedTightening FedDirection = "TIGHTENING"
	FedEasing     FedDirection = "EASING"
	FedUnchanged  FedDirection = "UNCHANGED"
	FedUnknown    FedDirection = "UNKNOWN"
)

// CurveState describes the 2s10s slope.
type CurveState string

const (
	CurveInverted CurveState = "INVERTED"
	CurveFlat     CurveState = "FLAT"
	CurveNormal   CurveState = "NORMAL"
	CurveUnknown  CurveState = "UNKNOWN"
)

// InflationTrend compares the year-on-year RATE with the month before — never the index,
// which rises almost monotonically and would report acceleration forever.
type InflationTrend string

const (
	InflationAccelerating InflationTrend = "ACCELERATING"
	InflationDecelerating InflationTrend = "DECELERATING"
	InflationStable       InflationTrend = "STABLE"
	InflationUnknown      InflationTrend = "UNKNOWN"
)

// LaborState is a two-signal reading. MIXED is a real answer, not a failure.
type LaborState string

const (
	LaborStrengthening LaborState = "STRENGTHENING"
	LaborWeakening     LaborState = "WEAKENING"
	LaborMixed         LaborState = "MIXED"
	LaborUnknown       LaborState = "UNKNOWN"
)

// VIXState is a volatility regime, not a trading signal.
type VIXState string

const (
	VIXLow      VIXState = "LOW"
	VIXNormal   VIXState = "NORMAL"
	VIXElevated VIXState = "ELEVATED"
	VIXHigh     VIXState = "HIGH"
	VIXExtreme  VIXState = "EXTREME"
	VIXUnknown  VIXState = "UNKNOWN"
)

// Freshness says whether an observation is recent for ITS OWN frequency.
type Freshness string

const (
	Fresh            Freshness = "FRESH"
	Stale            Freshness = "STALE"
	FreshnessUnknown Freshness = "UNKNOWN"
)

// RiskFlag is a named condition, each traceable to one state above.
type RiskFlag string

const (
	FlagYieldCurveInverted    RiskFlag = "YIELD_CURVE_INVERTED"
	FlagInflationAccelerating RiskFlag = "INFLATION_ACCELERATING"
	FlagLaborWeakening        RiskFlag = "LABOR_WEAKENING"
	FlagVIXElevated           RiskFlag = "VIX_ELEVATED"
	FlagFedTightening         RiskFlag = "FED_TIGHTENING"
)

// Research is the interpreted view. Facts are carried through beside the states so a reader
// can always see what a label was derived from.
type Research struct {
	Status      Status `json:"status"`
	ArchiveDate string `json:"archive_date"`

	Fed        FedView        `json:"fed"`
	Treasury   TreasuryView   `json:"treasury"`
	Inflation  InflationView  `json:"inflation"`
	Labor      LaborView      `json:"labor"`
	Volatility VolatilityView `json:"volatility"`

	RiskFlags []RiskFlag `json:"risk_flags,omitempty"`

	PCEStatus          Status `json:"pce_status"`
	FOMCCalendarStatus Status `json:"fomc_calendar_status"`
}

type FedView struct {
	Status      Status       `json:"status"`
	Date        string       `json:"date,omitempty"`
	Freshness   Freshness    `json:"freshness"`
	EFFR        *float64     `json:"effr,omitempty"`
	TargetLower *float64     `json:"target_lower,omitempty"`
	TargetUpper *float64     `json:"target_upper,omitempty"`
	Direction   FedDirection `json:"direction"`
	// LastChangeBps is the last move in the target MIDPOINT, in basis points. Nil when no
	// earlier range is on record.
	LastChangeBps *float64 `json:"last_change_bps,omitempty"`
}

type TreasuryView struct {
	Status    Status     `json:"status"`
	Date      string     `json:"date,omitempty"`
	Freshness Freshness  `json:"freshness"`
	Yield2Y   *float64   `json:"yield_2y,omitempty"`
	Yield10Y  *float64   `json:"yield_10y,omitempty"`
	Spread    *float64   `json:"spread_2y10y,omitempty"`
	Curve     CurveState `json:"curve"`
}

type InflationView struct {
	Status     Status         `json:"status"`
	Date       string         `json:"date,omitempty"`
	Freshness  Freshness      `json:"freshness"`
	CPIYoY     *float64       `json:"cpi_yoy,omitempty"`
	CoreCPIYoY *float64       `json:"core_cpi_yoy,omitempty"`
	Trend      InflationTrend `json:"trend"`
}

type LaborView struct {
	Status        Status     `json:"status"`
	Date          string     `json:"date,omitempty"`
	Freshness     Freshness  `json:"freshness"`
	Unemployment  *float64   `json:"unemployment_rate,omitempty"`
	PayrollChange *float64   `json:"payroll_change,omitempty"`
	State         LaborState `json:"state"`
}

type VolatilityView struct {
	Status    Status    `json:"status"`
	Date      string    `json:"date,omitempty"`
	Freshness Freshness `json:"freshness"`
	VIX       *float64  `json:"vix,omitempty"`
	State     VIXState  `json:"state"`
}

// Interpret turns a factual snapshot into described states.
//
// Pure: asOfDate is passed in rather than read from the clock, so freshness is reproducible
// and a historical run judges staleness against its own date rather than today's.
func Interpret(s *Snapshot, asOfDate string) Research {
	if s == nil {
		return Research{
			Status: Unavailable, PCEStatus: NotImplemented, FOMCCalendarStatus: NotImplemented,
			Fed:        FedView{Status: Unavailable, Direction: FedUnknown, Freshness: FreshnessUnknown},
			Treasury:   TreasuryView{Status: Unavailable, Curve: CurveUnknown, Freshness: FreshnessUnknown},
			Inflation:  InflationView{Status: Unavailable, Trend: InflationUnknown, Freshness: FreshnessUnknown},
			Labor:      LaborView{Status: Unavailable, State: LaborUnknown, Freshness: FreshnessUnknown},
			Volatility: VolatilityView{Status: Unavailable, State: VIXUnknown, Freshness: FreshnessUnknown},
		}
	}

	r := Research{
		ArchiveDate:        s.ArchiveDate,
		PCEStatus:          s.PCEStatus,
		FOMCCalendarStatus: s.FOMCCalendarStatus,
	}
	r.Fed = interpretFed(s.Fed, asOfDate)
	r.Treasury = interpretTreasury(s.Treasury, asOfDate)
	r.Inflation = interpretInflation(s.Inflation, asOfDate)
	r.Labor = interpretLabor(s.Labor, asOfDate)
	r.Volatility = interpretVIX(s.Volatility, asOfDate)

	r.Status = AggregateStatus(r.Fed.Status, r.Treasury.Status, r.Inflation.Status,
		r.Labor.Status, r.Volatility.Status)
	r.RiskFlags = riskFlags(r)
	return r
}

func interpretFed(f FedSnapshot, asOf string) FedView {
	v := FedView{
		Status: f.Status, Date: f.Date, EFFR: f.EFFR,
		TargetLower: f.TargetLower, TargetUpper: f.TargetUpper,
		Direction: FedUnknown, Freshness: freshness(f.Date, asOf, dailyToleranceDays),
	}
	if f.Status != Available {
		return v
	}
	// Direction comes from the TARGET RANGE, never from EFFR. EFFR wanders a basis point or
	// two on funding conditions with no policy change at all, and reading that as a decision
	// would report a rate move most weeks.
	cur, ok := midpoint(f.TargetLower, f.TargetUpper)
	if !ok {
		return v
	}
	prev, ok := previousDistinctMidpoint(f.History, cur)
	if !ok {
		// Only one range on record — the direction is genuinely unknown, not "unchanged".
		return v
	}
	bps := (cur - prev) * 100
	v.LastChangeBps = &bps
	switch {
	case cur > prev:
		v.Direction = FedTightening
	case cur < prev:
		v.Direction = FedEasing
	default:
		v.Direction = FedUnchanged
	}
	return v
}

// previousDistinctMidpoint walks back for the most recent range that DIFFERS from the current
// one. Comparing with merely the previous day would report UNCHANGED on every day but the
// one a decision landed, which says nothing about the stance.
func previousDistinctMidpoint(history []FedObservation, current float64) (float64, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		m, ok := midpoint(history[i].TargetLower, history[i].TargetUpper)
		if !ok {
			continue
		}
		if m != current {
			return m, true
		}
	}
	return 0, false
}

func midpoint(lo, hi *float64) (float64, bool) {
	if lo == nil || hi == nil {
		return 0, false
	}
	return (*lo + *hi) / 2, true
}

func interpretTreasury(t TreasurySnapshot, asOf string) TreasuryView {
	v := TreasuryView{
		Status: t.Status, Date: t.Date, Yield2Y: t.Yield2Y, Yield10Y: t.Yield10Y,
		Spread: t.Spread2Y10Y, Curve: CurveUnknown,
		Freshness: freshness(t.Date, asOf, dailyToleranceDays),
	}
	if t.Spread2Y10Y == nil {
		return v
	}
	switch s := *t.Spread2Y10Y; {
	case s < -curveFlatBand:
		v.Curve = CurveInverted
	case s > curveFlatBand:
		v.Curve = CurveNormal
	default:
		v.Curve = CurveFlat
	}
	return v
}

func interpretInflation(in InflationSnapshot, asOf string) InflationView {
	v := InflationView{
		Status: in.Status, Date: in.CoreCPIIndex.Date, CPIYoY: in.CPIYoY,
		CoreCPIYoY: in.CoreCPIYoY, Trend: InflationUnknown,
	}
	if v.Date == "" {
		v.Date = in.CPIIndex.Date
	}
	v.Freshness = freshness(monthEnd(v.Date), asOf, monthlyToleranceDays)

	// The trend compares the year-on-year RATE with the month before. Comparing index levels
	// would report acceleration essentially forever, since the index almost always rises.
	if in.CoreCPIYoY == nil || in.PriorCoreCPIYoY == nil {
		return v
	}
	switch d := *in.CoreCPIYoY - *in.PriorCoreCPIYoY; {
	case d > inflationEpsilon:
		v.Trend = InflationAccelerating
	case d < -inflationEpsilon:
		v.Trend = InflationDecelerating
	default:
		v.Trend = InflationStable
	}
	return v
}

func interpretLabor(l LaborSnapshot, asOf string) LaborView {
	v := LaborView{
		Status: l.Status, Date: l.UnemploymentRate.Date, Unemployment: l.UnemploymentRate.Value,
		PayrollChange: l.PayrollChange, State: LaborUnknown,
	}
	if v.Date == "" {
		v.Date = l.PayrollLevel.Date
	}
	v.Freshness = freshness(monthEnd(v.Date), asOf, monthlyToleranceDays)

	// Two signals, and no forecast to compare them against — this repo has no consensus
	// source, so "beat expectations" is not available and is not invented.
	if l.UnemploymentRate.Value == nil || l.PriorUnemployment == nil || l.PayrollChange == nil {
		return v
	}
	unempFalling := *l.UnemploymentRate.Value < *l.PriorUnemployment
	unempRising := *l.UnemploymentRate.Value > *l.PriorUnemployment
	jobsAdded := *l.PayrollChange > 0

	switch {
	case unempFalling && jobsAdded:
		v.State = LaborStrengthening
	case unempRising && !jobsAdded:
		v.State = LaborWeakening
	default:
		// Conflicting or flat signals. MIXED is the honest answer, not a failure.
		v.State = LaborMixed
	}
	return v
}

func interpretVIX(vol VolatilitySnapshot, asOf string) VolatilityView {
	v := VolatilityView{
		Status: vol.Status, Date: vol.VIX.Date, VIX: vol.VIX.Value, State: VIXUnknown,
		Freshness: freshness(vol.VIX.Date, asOf, dailyToleranceDays),
	}
	if vol.VIX.Value == nil {
		return v
	}
	// Bands are half-open on the upper side: exactly 15.00 is NORMAL, not LOW.
	switch x := *vol.VIX.Value; {
	case x < vixLow:
		v.State = VIXLow
	case x < vixNormal:
		v.State = VIXNormal
	case x < vixElevated:
		v.State = VIXElevated
	case x < vixHigh:
		v.State = VIXHigh
	default:
		v.State = VIXExtreme
	}
	return v
}

// freshness compares an observation date with the run date, against a tolerance chosen for
// that series' publication frequency.
func freshness(obsDate, asOf string, toleranceDays int) Freshness {
	if obsDate == "" || asOf == "" {
		return FreshnessUnknown
	}
	o, err := time.Parse("2006-01-02", obsDate)
	if err != nil {
		return FreshnessUnknown
	}
	a, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		return FreshnessUnknown
	}
	if a.Sub(o) > time.Duration(toleranceDays)*24*time.Hour {
		return Stale
	}
	return Fresh
}

// riskFlags names conditions, each traceable to exactly one state above.
//
// Nothing here forecasts. An inverted curve is an inverted curve; whether a recession
// follows is a claim this layer has no basis for and does not make.
func riskFlags(r Research) []RiskFlag {
	var out []RiskFlag
	if r.Treasury.Curve == CurveInverted {
		out = append(out, FlagYieldCurveInverted)
	}
	if r.Inflation.Trend == InflationAccelerating {
		out = append(out, FlagInflationAccelerating)
	}
	if r.Labor.State == LaborWeakening {
		out = append(out, FlagLaborWeakening)
	}
	switch r.Volatility.State {
	case VIXElevated, VIXHigh, VIXExtreme:
		out = append(out, FlagVIXElevated)
	}
	if r.Fed.Direction == FedTightening {
		out = append(out, FlagFedTightening)
	}
	return out
}

// monthEnd turns a "YYYY-MM" key into the last day of that month.
//
// The last day, not the first: a monthly statistic describes a period that ENDED then, and
// dating it from the start would add a month to its apparent age.
func monthEnd(monthKey string) string {
	if len(monthKey) != 7 {
		return ""
	}
	t, err := time.Parse("2006-01", monthKey)
	if err != nil {
		return ""
	}
	// The first of the next month, minus a day.
	return t.AddDate(0, 1, -1).Format("2006-01-02")
}
