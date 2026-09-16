package study

import (
	"encoding/json"
	"math"
	"sort"
)

// ── The distribution summary ──────────────────────────────────────────────────────────────

// Dist is a sample summarised the way EP-9 requires: NEVER THE MEAN ALONE.
//
// A mean on its own is the single easiest way to publish a misleading backtest — one 300%
// outlier in a thousand rows moves it by 0.3pp with nothing in the number to say so — which
// is why Median, P25, P75 and WinRate are on the same struct and every printer emits all of
// them together.
type Dist struct {
	N       int     `json:"n"`
	Mean    float64 `json:"mean"`
	Median  float64 `json:"median"`
	P25     float64 `json:"p25"`
	P75     float64 `json:"p75"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	WinRate float64 `json:"win_rate"` // share of samples strictly greater than 0, percent
}

// Summarise computes the distribution of x. An empty sample returns N=0 and zeroes, which the
// printers render as "-" rather than as 0.00 — a zero mean over no observations is not a zero
// mean.
//
// PERCENTILE DEFINITION, fixed here and pinned by a hand-computed test: NEAREST-RANK on the
// ascending sample, rank = ceil(p*N), 1-based, clamped to [1,N]. No interpolation.
//
// Interpolation is not wrong in general; it is wrong HERE, because every value in these
// samples is a real observed return and an interpolated p25 is a number no stock ever printed.
// Nearest-rank always returns an actual observation, which is what a reader comparing "p25 of
// the zone arm" against a specific trade needs. Median is the ONE exception and is the mean of
// the two middle values on an even sample, because that is what every reader means by median
// and pretending otherwise for consistency would be a worse surprise than the inconsistency.
func Summarise(x []float64) Dist {
	d := Dist{N: len(x)}
	if len(x) == 0 {
		return d
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)

	var sum, wins float64
	for _, v := range s {
		sum += v
		if v > 0 {
			wins++
		}
	}
	d.Mean = sum / float64(len(s))
	d.Median = median(s)
	d.P25 = nearestRank(s, 0.25)
	d.P75 = nearestRank(s, 0.75)
	d.Min, d.Max = s[0], s[len(s)-1]
	d.WinRate = wins / float64(len(s)) * 100
	return d
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func nearestRank(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	r := int(math.Ceil(p * float64(n)))
	if r < 1 {
		r = 1
	}
	if r > n {
		r = n
	}
	return sorted[r-1]
}

// ── Rates, with their denominators attached ───────────────────────────────────────────────

// Rate is a proportion that CARRIES ITS OWN DENOMINATOR.
//
// A bare percentage is how a denominator gets inflated without anybody noticing: an
// invalidation hit rate over "every filled observation" and one over "every filled
// observation whose plan published an invalidation" are different numbers with the same name,
// and the first is always the smaller one. Every rate in this package names the population it
// was computed over, and Pct returns NaN rather than 0 when that population is empty.
type Rate struct {
	Hits        int     `json:"hits"`
	Denominator int     `json:"denominator"`
	Pct         float64 `json:"pct"`
	// Population names what the denominator IS, in words, so a table can be read without the
	// code beside it.
	Population string `json:"population"`
}

// MarshalJSON emits Pct as JSON null when the denominator was empty.
//
// It exists because encoding/json REFUSES NaN outright — "json: unsupported value: NaN" — so
// without this the whole machine-readable run file fails to write the moment any segment has
// an empty denominator, and a study that cannot serialize its own result is a study nobody
// can check. EP-9 §24 found exactly that on the first full-scale run.
//
// null, NOT 0. The reason NewRate produces NaN in the first place is that 0.0% asserts
// "nothing was hit out of something"; writing 0 here would reintroduce that claim at the
// output boundary, where it is hardest to notice.
func (r Rate) MarshalJSON() ([]byte, error) {
	type alias struct {
		Hits        int      `json:"hits"`
		Denominator int      `json:"denominator"`
		Pct         *float64 `json:"pct"`
		Population  string   `json:"population"`
	}
	a := alias{Hits: r.Hits, Denominator: r.Denominator, Population: r.Population}
	if !math.IsNaN(r.Pct) && !math.IsInf(r.Pct, 0) {
		v := r.Pct
		a.Pct = &v
	}
	return json.Marshal(a)
}

// NewRate builds a rate. An empty denominator yields Pct = NaN, deliberately: the printers
// render NaN as "-", and 0.0% would assert that nothing was hit out of something.
func NewRate(hits, denom int, population string) Rate {
	r := Rate{Hits: hits, Denominator: denom, Population: population, Pct: math.NaN()}
	if denom > 0 {
		r.Pct = float64(hits) / float64(denom) * 100
	}
	return r
}

// ── One arm x one wait window ─────────────────────────────────────────────────────────────

// Metrics is the EP-9 metric block for one (arm, wait window) cell, or for one segment of it.
type Metrics struct {
	Arm  Arm `json:"arm"`
	Wait int `json:"wait_sessions"`

	// Label and N are the segment's identity. Label is "" for the whole cell.
	Segment string `json:"segment,omitempty"`

	// NCandidate is every observation an order COULD have been placed for — i.e. the plan
	// published the price this arm needs. NotApplicable observations are NOT in it.
	NCandidate int `json:"n_candidate"`
	// NFilled / NMissed partition NCandidate together with NUnavailable.
	NFilled      int `json:"n_filled"`
	NMissed      int `json:"n_missed"`
	NUnavailable int `json:"n_unavailable"`
	// NNotApplicable is counted OUTSIDE NCandidate and reported beside it, so a reader can
	// see how many plans this arm had no instruction for.
	NNotApplicable int `json:"n_not_applicable"`
	// NExcludedByStatus is how many observations the EP-9 §18 population filter kept OUT of
	// this block. Reported beside the counts so the excluded population is visible rather
	// than merely absent.
	NExcludedByStatus int `json:"n_excluded_by_status"`
	// Population names which of the two §18 populations this block describes, in words, so a
	// detached row still says what it is a row of.
	Population string `json:"population"`

	FillRate   Rate `json:"fill_rate"`
	MissedRate Rate `json:"missed_rate"`

	// Returns are keyed by horizon. A horizon the data does not reach for an observation is
	// absent from that observation and therefore from this sample; N differs per horizon and
	// each Dist carries its own.
	Returns map[int]Dist `json:"returns"`
	MFE20   Dist         `json:"mfe_20"`
	MAE20   Dist         `json:"mae_20"`

	InvalidationHitRate Rate `json:"invalidation_hit_rate"`
	Target1HitRate      Rate `json:"target_1_hit_rate"`
	Target2HitRate      Rate `json:"target_2_hit_rate"`

	// FirstTouch counts. Ambiguous is reported, never resolved; the two bounds below are how
	// a reader gets a range instead of a fabricated point estimate.
	FirstTouchStop      int `json:"first_touch_stop"`
	FirstTouchTarget    int `json:"first_touch_target"`
	FirstTouchAmbiguous int `json:"first_touch_ambiguous"`
	FirstTouchNeither   int `json:"first_touch_neither"`
	// StopFirstRateConservative counts every AMBIGUOUS bar as a stop-first; Optimistic counts
	// none of them. The truth is between the two and daily OHLC cannot narrow it.
	StopFirstRateConservative Rate `json:"stop_first_rate_conservative"`
	StopFirstRateOptimistic   Rate `json:"stop_first_rate_optimistic"`

	FillVsSignalCloseBps Dist `json:"fill_vs_signal_close_bps"`

	// PendingTruncated is how many filled observations had a TRUNCATED 20-session window.
	// Reported, never silently dropped and never counted as a loss.
	PendingTruncated int `json:"pending_truncated"`
}

// Executable restates Arm.Executable on the block, so a serialized metric row says on its own
// face whether it describes an order anybody could have placed.
func (m Metrics) Executable() bool { return m.Arm.Executable() }
