package institutional

import (
	"fmt"
	"math"
	"sort"
)

// Alignment: whether the three semantics and the four horizons point the same way — reported as
// EVIDENCE, with its components named, not collapsed into a score.
//
// §10.4 states the case this file is written around: POSITION bearish, FLOW bullish, CHANGE
// improving is a short-covering shape, and "compressing that to bullish or bearish throws away
// the only thing the three semantics were separated to show". So MIXED is a real answer here,
// not a failure to decide, and the components survive whatever the verdict is.

// AlignmentRuleVersion labels the rule, so a stored verdict stays interpretable after the rule
// moves. Separate from FeatureVersion because this is the piece most likely to be revised.
const AlignmentRuleVersion = "R15-M4-align-v1"

// Direction is one component's sign.
type Direction string

const (
	// DirectionBullish — a net long reading beyond the significance threshold.
	DirectionBullish Direction = "BULLISH"
	// DirectionBearish — a net short reading beyond the threshold.
	DirectionBearish Direction = "BEARISH"
	// DirectionNeutral — OBSERVED and within the threshold, including an exact zero. This is
	// a conclusion drawn from data and is nothing like an excluded component.
	DirectionNeutral Direction = "NEUTRAL"
	// DirectionUnknown — the component was excluded; it has no direction. Distinct from
	// NEUTRAL, which is the whole reason it exists.
	DirectionUnknown Direction = "UNKNOWN"
)

// AlignmentVerdict is §10.4's five-value answer.
type AlignmentVerdict string

const (
	// AlignedBullish — at least one bullish component and no bearish one.
	AlignedBullish AlignmentVerdict = "ALIGNED_BULLISH"
	// AlignedBearish — at least one bearish component and no bullish one.
	AlignedBearish AlignmentVerdict = "ALIGNED_BEARISH"
	// AlignmentMixed — bullish AND bearish components both present. The short-covering shape
	// lands here, with its components intact.
	AlignmentMixed AlignmentVerdict = "MIXED"
	// AlignmentNeutral — every participating component observed and every one within the
	// significance threshold. A reading, not an absence.
	AlignmentNeutral AlignmentVerdict = "NEUTRAL"
	// AlignmentInsufficientData — fewer than MinAlignmentComponents observed. Never
	// NEUTRAL: §6's "MISSING is never NEUTRAL" applied to a verdict.
	AlignmentInsufficientData AlignmentVerdict = "INSUFFICIENT_DATA"
)

// Shape names a configuration worth preserving under a MIXED verdict. It is a LABEL on the
// components, never a substitute for them, and never an override of the verdict.
type Shape string

const (
	// ShapeNone — no named configuration.
	ShapeNone Shape = "NONE"
	// ShapeShortCovering — POSITION bearish, FLOW bullish, CHANGE improving. §10.4's
	// example, and the reason MIXED must not be compressed: the position is still net short,
	// the session's trading was net long, and the exposure is moving towards flat.
	ShapeShortCovering Shape = "SHORT_COVERING"
	// ShapeLongLiquidation — the mirror image: POSITION bullish, FLOW bearish, CHANGE
	// falling. Still net long, selling into it, exposure coming down.
	ShapeLongLiquidation Shape = "LONG_LIQUIDATION"
)

// Component names, in the fixed order they are always reported.
const (
	ComponentFlow     = "FLOW"
	ComponentPosition = "POSITION"
)

// AlignmentComponent is one input to the verdict, present whether or not it participated.
type AlignmentComponent struct {
	Name      string         `json:"name"`
	Semantics ValueSemantics `json:"semantics"`
	Status    MetricStatus   `json:"status"`
	// Value is the lot count, nil when the component was excluded. An observed zero is a
	// non-nil 0 with Included true and Direction NEUTRAL.
	Value     *float64  `json:"value,omitempty"`
	Display   string    `json:"display"`
	Direction Direction `json:"direction"`
	Included  bool      `json:"included"`
	// ExcludedReason is why a component did not participate, in one line. Empty only when
	// Included.
	ExcludedReason string `json:"excluded_reason,omitempty"`
	// BelowSignificance marks a participating component whose magnitude was under the
	// threshold. It participated and it is NEUTRAL; it was not thrown away.
	BelowSignificance bool `json:"below_significance"`
}

// Alignment is the verdict with everything it was computed from.
type Alignment struct {
	Verdict    AlignmentVerdict     `json:"verdict"`
	Shape      Shape                `json:"shape"`
	Components []AlignmentComponent `json:"components"`
	// Included and Excluded name the components, so a reader never has to filter the list to
	// answer "what was this based on".
	Included []string `json:"included"`
	Excluded []string `json:"excluded"`
	Bullish  int      `json:"bullish"`
	Bearish  int      `json:"bearish"`
	Neutral  int      `json:"neutral"`
	// MinimumSignificanceLots and MinComponents are the policy the verdict was computed
	// under, copied in so a stored verdict can be reproduced.
	MinimumSignificanceLots float64 `json:"minimum_significance_lots"`
	MinComponents           int     `json:"min_components"`
	RuleVersion             string  `json:"rule_version"`
	Reason                  string  `json:"reason,omitempty"`
}

// classify is the ONE rule turning a lot count into a direction. Every component goes through
// it, so no component can be scored on a different scale from its neighbour.
func classify(v, minSignificance float64) (Direction, bool) {
	if v == 0 {
		// An observed zero. NEUTRAL because that is what it says, and not flagged as
		// "below significance", which would imply a magnitude too small to read.
		return DirectionNeutral, false
	}
	if math.Abs(v) < minSignificance {
		// Observed, directional in sign, too small to count under the caller's threshold.
		// It still participates, as NEUTRAL: dropping it would make a quiet day look like
		// a day with no data.
		return DirectionNeutral, true
	}
	if v > 0 {
		return DirectionBullish, false
	}
	return DirectionBearish, false
}

// Align builds the verdict from FLOW, POSITION and the horizons.
//
// The horizon list is expected to contain N=1 — CHANGE is ChangeN with N=1, and ChangeHorizons
// guarantees it. Components are reported in a fixed order (FLOW, POSITION, then horizons
// ascending) so two runs over the same data produce byte-identical output.
func Align(flow TradingFlowContracts, position OpenInterestPositionContracts, horizons []HorizonChange, p Policy) (Alignment, error) {
	np, err := p.Normalized()
	if err != nil {
		return Alignment{}, err
	}

	a := Alignment{
		MinimumSignificanceLots: np.MinimumSignificanceLots,
		MinComponents:           np.MinAlignmentComponents,
		RuleVersion:             AlignmentRuleVersion,
		Shape:                   ShapeNone,
	}

	sorted := append([]HorizonChange{}, horizons...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Horizon < sorted[j].Horizon })

	add := func(name string, m ObservedMetric) {
		c := AlignmentComponent{
			Name:      name,
			Semantics: m.Semantics,
			Status:    m.Status,
			Display:   m.Display,
			Direction: DirectionUnknown,
		}
		if v, ok := m.Lots(); ok {
			val := v
			dir, below := classify(v, np.MinimumSignificanceLots)
			c.Value = &val
			c.Direction = dir
			c.Included = true
			c.BelowSignificance = below
		} else {
			c.ExcludedReason = string(m.Status)
			if m.Reason != "" {
				c.ExcludedReason = string(m.Status) + ": " + m.Reason
			}
		}
		a.Components = append(a.Components, c)
	}

	add(ComponentFlow, flow.ObservedMetric)
	add(ComponentPosition, position.ObservedMetric)
	for _, h := range sorted {
		add(h.Label, h.Change.ObservedMetric)
	}

	for _, c := range a.Components {
		if !c.Included {
			a.Excluded = append(a.Excluded, c.Name)
			continue
		}
		a.Included = append(a.Included, c.Name)
		switch c.Direction {
		case DirectionBullish:
			a.Bullish++
		case DirectionBearish:
			a.Bearish++
		default:
			a.Neutral++
		}
	}

	switch {
	case len(a.Included) < np.MinAlignmentComponents:
		a.Verdict = AlignmentInsufficientData
		a.Reason = fmt.Sprintf("%d of %d components observed; %d required",
			len(a.Included), len(a.Components), np.MinAlignmentComponents)
	case a.Bullish > 0 && a.Bearish > 0:
		a.Verdict = AlignmentMixed
	case a.Bullish > 0:
		a.Verdict = AlignedBullish
	case a.Bearish > 0:
		a.Verdict = AlignedBearish
	default:
		a.Verdict = AlignmentNeutral
		a.Reason = "every observed component is within the significance threshold"
	}

	a.Shape = namedShape(a)
	return a, nil
}

// Direction returns one component's direction by name, and whether it participated.
func (a Alignment) Direction(name string) (Direction, bool) {
	for _, c := range a.Components {
		if c.Name == name {
			return c.Direction, c.Included
		}
	}
	return DirectionUnknown, false
}

// namedShape labels the configurations §10.4 says must stay visible. It reads the components
// that are already there and changes none of them.
func namedShape(a Alignment) Shape {
	pos, okP := a.Direction(ComponentPosition)
	flow, okF := a.Direction(ComponentFlow)
	chg, okC := a.Direction(HorizonLabel(1))
	if !okP || !okF || !okC {
		return ShapeNone
	}
	switch {
	case pos == DirectionBearish && flow == DirectionBullish && chg == DirectionBullish:
		return ShapeShortCovering
	case pos == DirectionBullish && flow == DirectionBearish && chg == DirectionBearish:
		return ShapeLongLiquidation
	}
	return ShapeNone
}
