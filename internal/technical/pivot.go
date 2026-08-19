package technical

import "math"

// PivotResult is the classic (floor-trader) pivot set for the CURRENT session, derived from
// the PREVIOUS complete session's OHLC.
//
// The derivation window is the part that matters and is easy to get wrong. A daily scanner
// runs after the close, so its last bar is today; today's pivot levels are the ones that were
// knowable this morning, which means they come from YESTERDAY's high, low and close. Feeding
// today's own high/low back in would produce levels that could not have been traded against
// and would make every backtest of them look better than reality.
type PivotResult struct {
	Status Status `json:"status"`

	// BasisDate is the session the levels were derived from — the previous complete
	// session, never the one being evaluated. Recorded so the derivation is auditable.
	BasisDate string `json:"basis_date,omitempty"`

	Pivot float64 `json:"pivot"`

	R1 float64 `json:"r1"`
	R2 float64 `json:"r2"`
	R3 float64 `json:"r3"`

	S1 float64 `json:"s1"`
	S2 float64 `json:"s2"`
	S3 float64 `json:"s3"`

	// NearestLevel names the closest level to the current close ("P", "R1" … "S3").
	NearestLevel string `json:"nearest_level"`
	// DistancePct is where that level sits RELATIVE TO PRICE: positive means the level is
	// above the current close, negative means below. So price 100 with R1 at 101 gives +1%.
	DistancePct float64 `json:"distance_pct"`

	// Location classifies the close against the level set.
	Location string `json:"location"` // LocNearSupport … LocMidRange
}

// pivotLevel pairs a name with its price, so the nearest-level search stays data-driven
// instead of an unrolled chain of comparisons.
type pivotLevel struct {
	name  string
	price float64
	// resistance is true for P-and-above levels, false for supports. P itself is treated as
	// neither: it is the axis, and calling it support or resistance depends on which side
	// price is on.
	kind string
}

const (
	pivotKindResistance = "R"
	pivotKindSupport    = "S"
	pivotKindAxis       = "P"
)

// ComputePivot derives the classic pivot set.
//
// It needs at least two bars: one to derive from and one to measure against. With a single
// bar the only available basis would be the bar being evaluated, which is precisely the
// look-ahead this construction exists to avoid — so it reports INSUFFICIENT_DATA rather than
// quietly using it.
func ComputePivot(bars []Bar, cfg Config) PivotResult {
	cfg = cfg.Defaulted()
	out := PivotResult{NearestLevel: "", Location: LocationUnknown}

	s, err := newSeries(bars)
	if err != nil {
		out.Status = statusFor(err)
		return out
	}
	if s.n < 2 {
		out.Status = StatusInsufficientData
		return out
	}

	prev := s.n - 2 // the previous COMPLETE session
	cur := s.n - 1
	ph, pl, pc := s.highs[prev], s.lows[prev], s.closes[prev]
	rng := ph - pl

	p := (ph + pl + pc) / 3
	r1 := 2*p - pl
	s1 := 2*p - ph
	r2 := p + rng
	s2 := p - rng
	r3 := ph + 2*(p-pl)
	s3 := pl - 2*(ph-p)

	if !allFinite(p, r1, r2, r3, s1, s2, s3) {
		out.Status = StatusInvalidBar
		return out
	}

	out.Status = StatusOK
	if d := bars[prev].Date; !d.IsZero() {
		out.BasisDate = d.Format("2006-01-02")
	}
	out.Pivot, out.R1, out.R2, out.R3, out.S1, out.S2, out.S3 = p, r1, r2, r3, s1, s2, s3

	closePx := s.closes[cur]
	levels := []pivotLevel{
		{"S3", s3, pivotKindSupport}, {"S2", s2, pivotKindSupport}, {"S1", s1, pivotKindSupport},
		{"P", p, pivotKindAxis},
		{"R1", r1, pivotKindResistance}, {"R2", r2, pivotKindResistance}, {"R3", r3, pivotKindResistance},
	}
	nearest, distPct, ok := nearestLevel(closePx, levels)
	if !ok {
		// The levels are finite but price cannot be expressed against them; say so rather
		// than publish a zero distance that reads as "exactly on the level".
		out.Status = StatusInvalidBar
		return out
	}
	out.NearestLevel = nearest.name
	out.DistancePct = distPct
	out.Location = pivotLocation(closePx, nearest, distPct, r3, s3, cfg.PivotNearPct)
	return out
}

// nearestLevel finds the level closest to price and its signed distance FROM price.
//
// Ties resolve toward the lower level because the slice is ordered S3→R3 and the comparison
// is strict; that only matters for exactly-equidistant prices, and a deterministic answer
// matters more than which one it is.
func nearestLevel(price float64, levels []pivotLevel) (pivotLevel, float64, bool) {
	best := pivotLevel{}
	bestDist := math.Inf(1)
	var bestPct float64
	var found bool

	for _, l := range levels {
		pct, ok := pctDiff(l.price, price)
		if !ok {
			continue
		}
		if d := math.Abs(l.price - price); d < bestDist {
			best, bestDist, bestPct, found = l, d, pct, true
		}
	}
	return best, bestPct, found
}

// pivotLocation classifies the close against the level set.
//
// Outside the outermost levels is unambiguous. Inside them, "near" is decided by the
// configured tolerance — a tolerance, not a signal threshold, and the only place that number
// appears.
func pivotLocation(price float64, nearest pivotLevel, distPct, r3, s3, nearPct float64) string {
	switch {
	case price > r3:
		return LocAboveResistance
	case price < s3:
		return LocBelowSupport
	}
	if math.Abs(distPct) > nearPct {
		return LocMidRange
	}
	switch nearest.kind {
	case pivotKindResistance:
		return LocNearResistance
	case pivotKindSupport:
		return LocNearSupport
	default:
		// Sitting on the pivot axis: which side of it price is on decides whether the axis
		// is currently acting as the floor or the ceiling.
		if price >= nearest.price {
			return LocNearSupport
		}
		return LocNearResistance
	}
}
