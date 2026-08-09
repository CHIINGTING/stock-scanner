package analyzer

import (
	"fmt"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Render turns a Dashboard into a human-readable block for the offline dump CLI. Output is
// context only — it deliberately never contains a trade instruction.
func Render(d *model.Dashboard) string {
	if d == nil {
		return "(no dashboard)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "═══ Market Dashboard — %s ═══\n\n", d.Date)

	fmt.Fprintf(&b, "Market Score : %.0f / 100  (%d modules)\n", d.MarketScore.Total, d.MarketScore.AvailableCount)
	fmt.Fprintf(&b, "Regime       : %s\n", d.Regime.Regime)
	fmt.Fprintf(&b, "Confidence   : %s\n", d.Regime.Confidence)
	if d.Regime.Description != "" {
		fmt.Fprintf(&b, "  解讀       : %s\n", d.Regime.Description)
		fmt.Fprintf(&b, "  策略       : %s\n", d.Regime.Strategy)
		fmt.Fprintf(&b, "  風險       : %s\n", d.Regime.Risk)
	}
	for _, r := range d.Regime.Reasons {
		fmt.Fprintf(&b, "  · %s\n", r)
	}
	for _, c := range d.Regime.Caveats {
		fmt.Fprintf(&b, "  ⚠ %s\n", c)
	}

	b.WriteString("\n── Modules ──\n")
	for _, m := range d.MarketScore.Modules {
		if !m.Available {
			fmt.Fprintf(&b, "  %-14s  n/a (source unavailable)\n", displayName(m.Name))
			continue
		}
		fmt.Fprintf(&b, "  %-14s  %5.0f  %-16s %s\n", displayName(m.Name), m.Score, m.State, m.Detail)
	}

	b.WriteString("\n── Sector Ranking ──\n")
	for _, s := range d.Sectors {
		fmt.Fprintf(&b, "  %s %-10s  %.0f  (%s)\n", starBar(s.Stars), s.Name, s.Score, s.Reason)
	}

	if len(d.TopOpportunities) > 0 {
		b.WriteString("\n── Top Opportunities ──\n")
		for _, o := range d.TopOpportunities {
			fmt.Fprintf(&b, "  ▲ %s — %s\n", o.Sector, strings.Join(o.Reasons, "；"))
		}
	}
	if len(d.TopRisks) > 0 {
		b.WriteString("\n── Top Risks ──\n")
		for _, r := range d.TopRisks {
			fmt.Fprintf(&b, "  ▼ %s — %s\n", r.Sector, strings.Join(r.Reasons, "；"))
		}
	}

	b.WriteString("\n（僅為市場環境 context，非交易指令）\n")
	return b.String()
}

func starBar(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}
