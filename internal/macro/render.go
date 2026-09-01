package macro

import (
	"fmt"
	"io"
	"strings"
)

// Text rendering. It formats an already-interpreted Research and computes nothing — no
// threshold is applied here, so the CLI and any future page cannot disagree about what
// "elevated" means.

// RenderText writes the macro summary.
func RenderText(w io.Writer, r Research) {
	fmt.Fprintf(w, "Fed\n")
	line(w, "EFFR", pct(r.Fed.EFFR))
	line(w, "Target", targetRange(r.Fed.TargetLower, r.Fed.TargetUpper))
	line(w, "Direction", string(r.Fed.Direction))
	line(w, "Last change", bps(r.Fed.LastChangeBps))
	line(w, "Observed", observed(r.Fed.Date, r.Fed.Freshness))

	fmt.Fprintf(w, "\nTreasury\n")
	line(w, "2Y", pct(r.Treasury.Yield2Y))
	line(w, "10Y", pct(r.Treasury.Yield10Y))
	// The spread is a difference between two percentages, so its unit is percentage POINTS.
	// Printing it as "%" would invite it to be read as a percentage change.
	line(w, "2Y10Y", pp(r.Treasury.Spread))
	line(w, "Curve", string(r.Treasury.Curve))
	line(w, "Observed", observed(r.Treasury.Date, r.Treasury.Freshness))

	fmt.Fprintf(w, "\nInflation\n")
	line(w, "CPI YoY", ratioPct(r.Inflation.CPIYoY))
	line(w, "Core CPI YoY", ratioPct(r.Inflation.CoreCPIYoY))
	line(w, "Trend", string(r.Inflation.Trend))
	line(w, "Observed", observed(r.Inflation.Date, r.Inflation.Freshness))

	fmt.Fprintf(w, "\nLabor\n")
	line(w, "Unemployment", pct(r.Labor.Unemployment))
	line(w, "NFP change", thousands(r.Labor.PayrollChange))
	line(w, "State", string(r.Labor.State))
	line(w, "Observed", observed(r.Labor.Date, r.Labor.Freshness))

	fmt.Fprintf(w, "\nVolatility\n")
	line(w, "VIX", num(r.Volatility.VIX))
	line(w, "State", string(r.Volatility.State))
	line(w, "Observed", observed(r.Volatility.Date, r.Volatility.Freshness))

	fmt.Fprintf(w, "\nNot implemented\n")
	line(w, "PCE", string(r.PCEStatus))
	line(w, "FOMC calendar", string(r.FOMCCalendarStatus))

	if len(r.RiskFlags) > 0 {
		fmt.Fprintf(w, "\nFlags\n")
		for _, f := range r.RiskFlags {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
}

func line(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-15s %s\n", label+":", value)
}

// unavailableText is what a missing value prints as. Never "0", never "-": both read as data.
const unavailableText = "UNAVAILABLE"

func pct(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%.2f%%", *v)
}

// ratioPct renders a RATIO as a percentage: the domain stores 0.027, the reader sees +2.7%.
func ratioPct(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%+.1f%%", *v*100)
}

func pp(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%+.2f pp", *v)
}

func bps(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%+.0f bps", *v)
}

func num(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%.2f", *v)
}

// thousands renders a payroll change, which BLS reports in thousands of persons: 73 → "+73K".
func thousands(v *float64) string {
	if v == nil {
		return unavailableText
	}
	return fmt.Sprintf("%+.0fK", *v)
}

func targetRange(lo, hi *float64) string {
	if lo == nil || hi == nil {
		return unavailableText
	}
	return fmt.Sprintf("%.2f–%.2f%%", *lo, *hi)
}

// observed shows the date and marks a stale reading without hiding its value — a reader needs
// both "what was it" and "how old is that".
func observed(date string, f Freshness) string {
	if date == "" {
		return unavailableText
	}
	if f == Stale {
		return date + "  (STALE)"
	}
	return date
}

var _ = strings.TrimSpace
