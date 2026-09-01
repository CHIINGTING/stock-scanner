package fundamental

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parsing the MOPS datasets.
//
// Two things here are easy to get wrong and expensive to get wrong quietly: ROC years, and
// the difference between "the field was blank" and "the value was zero". Both are handled
// once, here, and both have tests.

// thousandTWD is the sources' monetary unit. Converted at the boundary so no downstream type
// has to carry a scale.
const thousandTWD = 1000

// parseROCDate converts a Republic-of-China date like "1150831" to 2026-08-31.
//
// ROC year + 1911 = Gregorian. Treating 115 as the year 115 AD would put every publication
// date nineteen centuries in the past, and a point-in-time filter would then admit every
// record at every date — the look-ahead would be total and silent.
func parseROCDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) != 7 {
		return time.Time{}, fmt.Errorf("ROC date %q: want 7 digits (YYYMMDD)", s)
	}
	y, err := strconv.Atoi(s[:3])
	if err != nil {
		return time.Time{}, fmt.Errorf("ROC date %q: %w", s, err)
	}
	m, err := strconv.Atoi(s[3:5])
	if err != nil {
		return time.Time{}, fmt.Errorf("ROC date %q: %w", s, err)
	}
	d, err := strconv.Atoi(s[5:7])
	if err != nil {
		return time.Time{}, fmt.Errorf("ROC date %q: %w", s, err)
	}
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, fmt.Errorf("ROC date %q: month %d day %d out of range", s, m, d)
	}
	// Taipei, because these are Taiwanese disclosure dates. A UTC parse would place the
	// boundary eight hours early and let a record become visible a day too soon.
	return time.Date(y+1911, time.Month(m), d, 0, 0, 0, 0, taipei()), nil
}

// parseROCYearMonth converts "11507" to (2026, 7).
func parseROCYearMonth(s string) (year, month int, err error) {
	s = strings.TrimSpace(s)
	if len(s) != 5 {
		return 0, 0, fmt.Errorf("ROC year-month %q: want 5 digits (YYYMM)", s)
	}
	y, err := strconv.Atoi(s[:3])
	if err != nil {
		return 0, 0, fmt.Errorf("ROC year-month %q: %w", s, err)
	}
	m, err := strconv.Atoi(s[3:])
	if err != nil {
		return 0, 0, fmt.Errorf("ROC year-month %q: %w", s, err)
	}
	if m < 1 || m > 12 {
		return 0, 0, fmt.Errorf("ROC year-month %q: month %d out of range", s, m)
	}
	return y + 1911, m, nil
}

// parseROCYear converts "115" to 2026.
func parseROCYear(s string) (int, error) {
	y, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("ROC year %q: %w", s, err)
	}
	// A four-digit value is already Gregorian; anything else is ROC. The guard exists because
	// silently adding 1911 to 2026 would produce 3937 and every date comparison would fail in
	// a way no one could read.
	if y >= 1911 {
		return y, nil
	}
	return y + 1911, nil
}

// parseNumber reads a MOPS numeric cell.
//
// The crucial part is the (nil, nil) return: an EMPTY cell is missing data, and must not
// become 0. These datasets leave a field blank whenever it does not apply to the industry —
// a bank has no 營業成本 — and a parser that answered "0" would report that every bank had
// zero cost of sales, which reads as a 100% gross margin.
func parseNumber(s string) (*float64, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "", "-", "--", "N/A", "n/a":
		return nil, nil
	}
	// Thousands separators, and parentheses for negatives, both appear in MOPS exports.
	s = strings.ReplaceAll(s, ",", "")
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// A value that is present but unreadable is an ERROR, not a missing value. Silently
		// dropping it would hide a format change until someone noticed the numbers were wrong.
		return nil, fmt.Errorf("unreadable number %q: %w", s, err)
	}
	if neg {
		v = -v
	}
	return &v, nil
}

// mustMoney converts a parsed thousands figure to whole TWD.
func toMoney(v *float64) *Money {
	if v == nil {
		return nil
	}
	m := Money(*v * thousandTWD)
	return &m
}

func taipei() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		// Fixed offset fallback: Taiwan has had no DST since 1979, so +08:00 is exact.
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}
