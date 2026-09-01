package macro

import "math"

// Derived values — all pure. No clock, no network, no filesystem: everything arrives as an
// argument, so a snapshot can be rebuilt from its inputs and every case below is testable
// without a fixture server.

// BuildInflation turns the raw index series into rates.
//
// The index and the rate are kept apart because they are different quantities that look
// alike: CPI is 333.918, and that is a level. Dividing this year's level by last year's is
// what produces 2.7%, and a layer that stored one under the other's name would be wrong by
// two orders of magnitude in a way no reader would question.
func BuildInflation(cpi, coreCPI []blsPoint) InflationSnapshot {
	out := InflationSnapshot{Status: Unavailable}
	if len(cpi) == 0 && len(coreCPI) == 0 {
		out.Reason = "no price index observations"
		return out
	}

	if n := len(cpi); n > 0 {
		last := cpi[n-1]
		out.CPIIndex = Observation{Date: monthKey(last), Value: ptr(last.Value)}
		out.CPIYoY = yearOverYear(cpi, n-1)
		out.CPIMoM = monthOverMonth(cpi, n-1)
	}
	if n := len(coreCPI); n > 0 {
		last := coreCPI[n-1]
		out.CoreCPIIndex = Observation{Date: monthKey(last), Value: ptr(last.Value)}
		out.CoreCPIYoY = yearOverYear(coreCPI, n-1)
		out.CoreCPIMoM = monthOverMonth(coreCPI, n-1)
		// The previous month's year-on-year rate, so a trend can be judged without
		// re-deriving the whole series downstream.
		if n >= 2 {
			out.PriorCoreCPIYoY = yearOverYear(coreCPI, n-2)
		}
	}

	switch {
	case out.CPIIndex.Value != nil && out.CoreCPIIndex.Value != nil:
		out.Status = Available
	case out.CPIIndex.Value != nil || out.CoreCPIIndex.Value != nil:
		out.Status = Partial
	}
	return out
}

// yearOverYear compares the point at idx with the SAME MONTH twelve observations earlier.
//
// It walks back by calendar month rather than by twelve array positions, because a gap in
// the series would otherwise silently compare against the wrong month — an eleven-month or
// thirteen-month change wearing a year-on-year label.
func yearOverYear(pts []blsPoint, idx int) *float64 {
	if idx < 0 || idx >= len(pts) {
		return nil
	}
	cur := pts[idx]
	for i := idx - 1; i >= 0; i-- {
		p := pts[i]
		if p.Year == cur.Year-1 && p.Month == cur.Month {
			return ratio(cur.Value, p.Value)
		}
	}
	return nil
}

// monthOverMonth compares with the immediately preceding calendar month.
func monthOverMonth(pts []blsPoint, idx int) *float64 {
	if idx < 1 || idx >= len(pts) {
		return nil
	}
	cur, prev := pts[idx], pts[idx-1]
	wantY, wantM := cur.Year, cur.Month-1
	if wantM == 0 {
		wantY, wantM = cur.Year-1, 12
	}
	if prev.Year != wantY || prev.Month != wantM {
		// The preceding entry is not the preceding MONTH. A gap must not become a
		// two-month change reported as one.
		return nil
	}
	return ratio(cur.Value, prev.Value)
}

// ratio is (current − base) / base, refusing a base that cannot support a percentage.
func ratio(current, base float64) *float64 {
	if base == 0 || math.IsNaN(base) || math.IsNaN(current) {
		return nil
	}
	v := (current - base) / base
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// BuildLabor turns the raw employment series into the figures markets use.
func BuildLabor(unemployment, payrolls []blsPoint) LaborSnapshot {
	out := LaborSnapshot{Status: Unavailable}
	if len(unemployment) == 0 && len(payrolls) == 0 {
		out.Reason = "no labour observations"
		return out
	}

	if n := len(unemployment); n > 0 {
		last := unemployment[n-1]
		out.UnemploymentRate = Observation{Date: monthKey(last), Value: ptr(last.Value)}
		if n >= 2 {
			out.PriorUnemployment = ptr(unemployment[n-2].Value)
		}
	}
	if n := len(payrolls); n > 0 {
		last := payrolls[n-1]
		// The LEVEL — total nonfarm employment in thousands of persons.
		out.PayrollLevel = Observation{Date: monthKey(last), Value: ptr(last.Value)}
		if n >= 2 {
			// The CHANGE is what "nonfarm payrolls +73K" means. Publishing the level under
			// that name would overstate it by three orders of magnitude.
			d := last.Value - payrolls[n-2].Value
			out.PayrollChange = &d
		}
	}

	switch {
	case out.UnemploymentRate.Value != nil && out.PayrollLevel.Value != nil:
		out.Status = Available
	case out.UnemploymentRate.Value != nil || out.PayrollLevel.Value != nil:
		out.Status = Partial
	}
	return out
}

// AggregateStatus combines the component statuses.
//
// PARTIAL exists so one dead feed does not erase four live ones: a missing VIX says nothing
// about whether the Fed cut rates.
func AggregateStatus(components ...Status) Status {
	var available, total int
	for _, s := range components {
		total++
		if s == Available || s == Partial {
			available++
		}
	}
	switch {
	case total == 0 || available == 0:
		return Unavailable
	case available == total:
		return Available
	default:
		return Partial
	}
}

func monthKey(p blsPoint) string {
	return itoa4(p.Year) + "-" + itoa2(p.Month)
}

func itoa4(v int) string {
	return string(rune('0'+v/1000%10)) + string(rune('0'+v/100%10)) +
		string(rune('0'+v/10%10)) + string(rune('0'+v%10))
}

func itoa2(v int) string {
	return string(rune('0'+v/10%10)) + string(rune('0'+v%10))
}

func ptr(v float64) *float64 { return &v }
