package valuation

import "time"

// shiftDate moves a YYYY-MM-DD date by n days, returning "" if it cannot be parsed.
//
// Kept here rather than inlined so the window arithmetic has one implementation: a cutoff
// computed two different ways is a cutoff that will eventually differ by a day.
func shiftDate(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// inclusiveDays is the number of calendar days from a to b, counting both ends.
//
// Inclusive because it measures a SPAN of observations, not a gap between them: a single
// session spans one day, not zero, and two consecutive sessions span two. An exclusive count
// would report the width of a one-sample distribution as 0 and make it indistinguishable from
// having no samples at all.
//
// Returns 0 when either date is unparseable or the order is reversed, so a malformed archive
// produces a span that fails every threshold rather than one that passes them.
func inclusiveDays(a, b string) int {
	start, err := time.Parse("2006-01-02", a)
	if err != nil {
		return 0
	}
	end, err := time.Parse("2006-01-02", b)
	if err != nil {
		return 0
	}
	if end.Before(start) {
		return 0
	}
	return int(end.Sub(start).Hours()/24) + 1
}
