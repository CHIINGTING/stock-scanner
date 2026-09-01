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
