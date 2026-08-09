package r6backtest

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/fetcher"

	"github.com/deep-huang/stock-scanner/internal/indicator"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// R12 validation: do Slope / BIAS / SectorHeat actually separate forward returns?
//
// The question is NOT "is there a profitable rule here". It is narrower and falsifiable:
// PULLBACK_IN_UPTREND and EXTENDED are claimed to be different situations, so they must show
// different forward-return and downside profiles. If they do not, the state machine is
// describing a distinction the market does not make, and the thresholds are decoration.
//
// Three disciplines, because technical-rule backtests fail in predictable ways:
//
//   - LOOK-AHEAD: every condition at bar i reads bars <= i only, entry is i+1 (the engine's
//     existing next_open rule), and the sector panel for date d is built from bar indices
//     <= d for every member. Nothing consults a future bar;
//   - DATA SNOOPING: the thresholds come from trendext.go unchanged. This run MEASURES the
//     shipped hypothesis; it does not search for a better one. A sweep that reported the
//     best of many threshold sets would be selecting on the sample it reports;
//   - SURVIVORSHIP: the universe is whatever .cache holds today, so delisted names are
//     absent. That biases every setup here upward, including the baseline — which is why the
//     comparison is always against a baseline drawn from the SAME universe, never against an
//     absolute return.

// TrendExtCondition is one condition being compared.
type TrendExtCondition struct {
	Name string
	// Detect reports whether the condition holds for stock s at bar i, given the sector heat
	// panel for that date.
	Detect func(s *Stock, i int, heat *scanner.SectorHeatView) bool
}

// heatPanel holds, per date index, the heat reading of every sector on that date.
type heatPanel struct {
	byDate []map[string]*scanner.SectorHeatView
}

func (p *heatPanel) at(dateIdx int, sector string) *scanner.SectorHeatView {
	if p == nil || dateIdx < 0 || dateIdx >= len(p.byDate) {
		return nil
	}
	return p.byDate[dateIdx][sector]
}

// BuildHeatPanel computes sector heat for every date on the axis, reusing the LIVE scanner's
// aggregation (scanner.ComputeSectorHeat / NormalizeSectorHeat) rather than a private copy.
// Validating a reimplementation would validate the wrong code.
//
// step lets a caller compute every Nth date; heat moves slowly and a full daily panel over
// ~2,000 symbols is the dominant cost of the run. Dates in between reuse the most recent
// computed panel, which is a stated approximation, not silent rounding.
func BuildHeatPanel(u *Universe, step int) *heatPanel {
	if step < 1 {
		step = 1
	}
	// Group once: sector → member stocks.
	bySector := map[string][]*Stock{}
	for _, s := range u.Stocks {
		if s.Sector == "" {
			continue
		}
		bySector[s.Sector] = append(bySector[s.Sector], s)
	}
	names := make([]string, 0, len(bySector))
	for n := range bySector {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic order

	panel := &heatPanel{byDate: make([]map[string]*scanner.SectorHeatView, len(u.Axis))}
	var last map[string]*scanner.SectorHeatView

	for d := range u.Axis {
		if d%step != 0 && last != nil {
			panel.byDate[d] = last
			continue
		}
		// NormalizeSectorHeat operates on []SectorRotation, so the rotations are built as
		// carriers — the same two-phase shape the live scanner uses.
		rots := make([]scanner.SectorRotation, 0, len(names))
		for _, name := range names {
			members := make([]scanner.HeatMemberInput, 0, len(bySector[name]))
			for _, s := range bySector[name] {
				i, ok := s.idxAt(u.Axis[d])
				if !ok {
					continue // no bar for this member on this date
				}
				m, ok := heatMemberAt(s, i)
				if !ok {
					continue
				}
				members = append(members, m)
			}
			h := scanner.ComputeSectorHeat(len(bySector[name]), members)
			rots = append(rots, scanner.SectorRotation{Name: name, Heat: &h})
		}
		scanner.NormalizeSectorHeat(rots)

		m := make(map[string]*scanner.SectorHeatView, len(rots))
		for i := range rots {
			m[rots[i].Name] = rots[i].Heat
		}
		panel.byDate[d] = m
		last = m
	}
	return panel
}

// heatMemberAt builds one member's heat input AS OF bar i. Reads bars <= i only.
func heatMemberAt(s *Stock, i int) (scanner.HeatMemberInput, bool) {
	if i < 21 || i >= len(s.Close) {
		return scanner.HeatMemberInput{}, false
	}
	m := scanner.HeatMemberInput{}
	if s.MA20[i] > 0 {
		m.MA20Valid = true
		m.AboveMA20 = s.Close[i] > s.MA20[i]
	}
	if s.Close[i-20] > 0 {
		m.Return20 = (s.Close[i]/s.Close[i-20] - 1) * 100
	}
	if s.VolMA20[i] > 0 {
		m.VolumeRatio = s.Vol[i] / s.VolMA20[i]
	}
	return m, true
}

// idxAt returns the bar index of a date key for this stock.
func (s *Stock) idxAt(dateKey string) (int, bool) {
	i, ok := s.idxOf[dateKey]
	return i, ok
}

// ── per-bar R12 metrics (bars <= i only) ─────────────────────────────────────────────

// ma20SlopeAt is the shipped slope definition applied to a historical bar.
func ma20SlopeAt(s *Stock, i int) (float64, bool) {
	return indicator.MASlopePerDay(s.MA20[:i+1], th.MA20SlopeLookback)
}

func ma60SlopeAt(s *Stock, i int) (float64, bool) {
	return indicator.MASlopePerDay(s.MA60[:i+1], th.MA60SlopeLookback)
}

func bias20At(s *Stock, i int) (float64, bool) {
	if i >= len(s.MA20) || s.MA20[i] <= 0 {
		return 0, false
	}
	return (s.Close[i] - s.MA20[i]) / s.MA20[i] * 100, true
}

// bias20PercentileAt ranks bar i's BIAS20 against the trailing window of the SAME stock.
//
// Counted rather than sorted: a sort per bar over ~2,000 symbols dominates the run, and the
// rank only needs a count of "how many are <= today".
func bias20PercentileAt(s *Stock, i int) (float64, bool) {
	start := i - th.BiasWindow + 1
	if start < 20 { // MA20 warm-up
		start = 20
	}
	if i-start+1 < th.BiasMinSample {
		return 0, false
	}
	today, ok := bias20At(s, i)
	if !ok {
		return 0, false
	}
	var n, le int
	for j := start; j <= i; j++ {
		b, ok := bias20At(s, j)
		if !ok {
			continue
		}
		n++
		if b <= today {
			le++
		}
	}
	if n < th.BiasMinSample {
		return 0, false
	}
	return float64(le) / float64(n) * 100, true
}

// th is the SHIPPED threshold set, read from the scanner rather than mirrored here. Mirrored
// constants would let the backtest and production drift apart silently, and this run would
// then be validating a hypothesis nobody ships.
var th = scanner.DefaultTrendExtThresholds()

// TrendExtConditions is the comparison set required by the R12 validation:
// baseline vs each leg alone vs the combined states.
func TrendExtConditions() []TrendExtCondition {
	slopeUp := func(s *Stock, i int) bool {
		v, ok := ma20SlopeAt(s, i)
		return ok && v > th.SlopeFlatPct
	}
	nearMA := func(s *Stock, i int) bool {
		b, ok := bias20At(s, i)
		return ok && absf(b) <= th.NearMAPct
	}
	extended := func(s *Stock, i int) bool {
		p, ok := bias20PercentileAt(s, i)
		return ok && p >= th.ExtendedPct
	}
	ma60NotDown := func(s *Stock, i int) bool {
		v, ok := ma60SlopeAt(s, i)
		return ok && v >= -th.SlopeFlatPct
	}
	heatAtLeast := func(h *scanner.SectorHeatView, min float64) bool {
		return h != nil && h.Status == scanner.SectorHeatOK && h.Heat >= min
	}
	heatIsWeak := func(h *scanner.SectorHeatView) bool {
		return h != nil && h.Status == scanner.SectorHeatOK && h.Heat <= th.HeatWeakMax
	}

	return []TrendExtCondition{
		{
			// Every bar that could be measured at all. This is the reference distribution:
			// it carries the same survivorship and regime bias as every other row, so the
			// only meaningful reading of any row below is its DIFFERENCE from this one.
			Name: "BASELINE_ALL_BARS",
			Detect: func(s *Stock, i int, _ *scanner.SectorHeatView) bool {
				_, ok := bias20At(s, i)
				return ok
			},
		},
		{
			Name: "SLOPE_ONLY_MA20_UP",
			Detect: func(s *Stock, i int, _ *scanner.SectorHeatView) bool {
				return slopeUp(s, i)
			},
		},
		{
			Name: "BIAS_ONLY_NEAR_MA20",
			Detect: func(s *Stock, i int, _ *scanner.SectorHeatView) bool {
				return nearMA(s, i)
			},
		},
		{
			Name: "BIAS_ONLY_EXTENDED_P90",
			Detect: func(s *Stock, i int, _ *scanner.SectorHeatView) bool {
				return extended(s, i)
			},
		},
		{
			Name: "SECTOR_ONLY_HEAT_STRONG",
			Detect: func(_ *Stock, _ int, h *scanner.SectorHeatView) bool {
				return heatAtLeast(h, th.HeatStrongMin)
			},
		},
		{
			// A technical pullback inside an uptrend. Measured, not promoted.
			Name: "STATE_PULLBACK_IN_UPTREND",
			Detect: func(s *Stock, i int, h *scanner.SectorHeatView) bool {
				return slopeUp(s, i) && ma60NotDown(s, i) && nearMA(s, i) &&
					!extended(s, i) && !heatIsWeak(h)
			},
		},
		{
			// The state that must NOT look like a better version of the one above.
			Name: "STATE_EXTENDED",
			Detect: func(s *Stock, i int, _ *scanner.SectorHeatView) bool {
				return slopeUp(s, i) && extended(s, i)
			},
		},
		{
			Name: "STATE_TREND_CONFIRMED",
			Detect: func(s *Stock, i int, h *scanner.SectorHeatView) bool {
				return slopeUp(s, i) && ma60NotDown(s, i) && !extended(s, i) &&
					heatAtLeast(h, th.HeatStrongMin)
			},
		},
		{
			Name: "STATE_SECTOR_DIVERGENCE",
			Detect: func(s *Stock, i int, h *scanner.SectorHeatView) bool {
				return slopeUp(s, i) && heatIsWeak(h)
			},
		},
	}
}

// trendExtSetup adapts a condition to the engine's Setup interface, so the existing entry,
// stop, horizon and drawdown machinery is reused verbatim.
type trendExtSetup struct {
	cond  TrendExtCondition
	panel *heatPanel
	axis  map[string]int // date key → axis index
}

func (t trendExtSetup) Name() string { return t.cond.Name }

func (t trendExtSetup) Detect(_ *Universe, _ *RSPanel, s *Stock, i int, _ Params) *Trigger {
	dateKey := s.Candles[i].Date.Format("2006-01-02")
	d, ok := t.axis[dateKey]
	if !ok {
		return nil
	}
	if !t.cond.Detect(s, i, t.panel.at(d, s.Sector)) {
		return nil
	}
	note := ""
	if b, ok := bias20At(s, i); ok {
		if sl, ok2 := ma20SlopeAt(s, i); ok2 {
			note = fmt.Sprintf("slope=%.3f bias=%.2f", sl, b)
		}
	}
	// Sector rides on the Trade (the engine copies it from the Stock), so the Trigger only
	// carries the note.
	return &Trigger{Note: note}
}

// TrendExtSetups wraps every condition for the engine.
func TrendExtSetups(u *Universe, panel *heatPanel) []Setup {
	axis := make(map[string]int, len(u.Axis))
	for i, d := range u.Axis {
		axis[d] = i
	}
	conds := TrendExtConditions()
	out := make([]Setup, 0, len(conds))
	for _, c := range conds {
		out = append(out, trendExtSetup{cond: c, panel: panel, axis: axis})
	}
	return out
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// LoadSectorMap reads symbol → sector from the SAME configs/sectors.yaml the scanner uses.
//
// Deliberately reuses fetcher.LoadSectors rather than parsing the file again: a second parser
// is a second taxonomy waiting to disagree with the first.
func LoadSectorMap(path string) (map[string]string, error) {
	list, err := fetcher.LoadSectorList(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, sec := range list.Sectors {
		for _, st := range sec.Stocks {
			// The cache stores a bare code ("1308") with the market in its own field, so
			// the bare form is the one that matches. The suffixed variants are mapped too
			// because the cache FILENAMES carry a suffix, and a future loader keying on the
			// filename would otherwise silently map nothing.
			out[st.Code] = sec.Name
			out[st.Code+"_TW"] = sec.Name
			out[st.Code+"_TWO"] = sec.Name
			out[st.Code+".TW"] = sec.Name
			out[st.Code+".TWO"] = sec.Name
		}
	}
	return out, nil
}
