package r6backtest

import (
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// RSPanel holds as-of relative-strength percentiles: date → symbol → percentile.
// Built with the REAL scanner.CalculateRSRanks so it matches live RS semantics
// (ETF/DR exclusion, 120-day lookback, mid-rank percentile). No look-ahead: each
// date D ranks every stock using only candles up to and including D.
type RSPanel struct {
	byDate map[string]map[string]float64
}

// At returns the RS percentile for sym as-of dateKey (and whether it exists).
func (p *RSPanel) At(sym, dateKey string) (float64, bool) {
	if p == nil {
		return 0, false
	}
	if m, ok := p.byDate[dateKey]; ok {
		if v, ok2 := m[sym]; ok2 {
			return v, true
		}
	}
	return 0, false
}

// Dates returns the number of dates the panel covers.
func (p *RSPanel) Dates() int { return len(p.byDate) }

// BuildRSPanel computes the as-of RS percentile for every axis date that has
// enough cross-sectional history. Pure read of the universe; no mutation.
func BuildRSPanel(u *Universe, p Params) *RSPanel {
	cfg := scanner.RSConfig{
		Enable:           true,
		LookbackDays:     p.RSLookbackDays,
		MinHistoryDays:   p.RSMinHistory,
		ExcludeNonCommon: true,
		UseAdjustedClose: false,
	}
	panel := &RSPanel{byDate: make(map[string]map[string]float64)}
	for _, d := range u.Axis {
		items := make([]scanner.RSInput, 0, len(u.Stocks))
		for _, s := range u.Stocks {
			i, ok := s.IndexOf(d)
			if !ok || i+1 <= cfg.LookbackDays {
				continue
			}
			items = append(items, scanner.RSInput{
				Symbol:  s.Symbol,
				Name:    s.Name,
				Candles: s.Candles[:i+1], // as-of D, no future bars
			})
		}
		if len(items) < 50 { // too thin to rank meaningfully
			continue
		}
		res := scanner.CalculateRSRanks(items, cfg)
		m := make(map[string]float64, len(res))
		for _, r := range res {
			if r.Computed {
				m[r.Symbol] = r.RSRankPercentile
			}
		}
		if len(m) > 0 {
			panel.byDate[d] = m
		}
	}
	return panel
}
