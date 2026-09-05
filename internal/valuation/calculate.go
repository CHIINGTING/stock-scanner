package valuation

import (
	"math"
	"sort"
)

// The pure calculation layer.
//
// Nothing here reads a file, makes a request or looks at the clock. Everything it needs
// arrives as an argument, which is what makes a valuation reproducible from its inputs alone
// — and what lets every case below be tested without a fixture server.

// MinHistoricalPESamples is the fewest observations a percentile may be computed from.
//
// Below this the statistic is arithmetically defined and practically meaningless: a median of
// three points describes those three points, not a distribution. Reporting it anyway would
// give a brand-new archive the same apparent authority as a five-year one.
const MinHistoricalPESamples = 20

// DefaultWindowDays is the lookback for the P/E distribution.
//
// One window, one constant. Offering 1Y/3Y/5Y at once would multiply every statistic by three
// before there is enough history to fill even one of them.
const DefaultWindowDays = 365 * 5

// Input is everything Calculate needs. Assembled by the caller from archived snapshots.
type Input struct {
	Symbol string
	// Current is the latest published ratios, or nil when none are archived.
	Current *Ratios
	// History is the archived P/E observations, in any order. Only positive values count as
	// samples — see filterSamples.
	History []Ratios
	// WindowDays bounds the history. Zero uses DefaultWindowDays.
	WindowDays int
	// MinSamples overrides MinHistoricalPESamples. Zero uses the constant.
	MinSamples int
	// AsOfDate bounds the history so a historical run cannot see later observations. Empty
	// means no bound — acceptable only for a current run.
	AsOfDate string
}

// Calculate produces a valuation from archived observations.
//
// A pure function: same inputs, same output, no I/O. Every metric it cannot support says so
// through a status rather than through a zero.
func Calculate(in Input) Valuation {
	v := Valuation{
		Symbol:         in.Symbol,
		Source:         SourceName,
		TrailingStatus: Unavailable,
		// These three have no source. Stated as a status so a reader learns there is nothing
		// to wait for, rather than assuming the computation failed.
		ForwardPEStatus: NotImplemented,
		PEGStatus:       NotImplemented,
		FairValueStatus: NotImplemented,
	}

	if in.Current != nil {
		v.TrailingDate = in.Current.Date
		v.PBRatio = in.Current.PBRatio
		v.DividendYield = in.Current.DividendYield
		if in.Current.PERatio != nil && *in.Current.PERatio > 0 {
			pe := *in.Current.PERatio
			v.TrailingPE = &pe
			v.TrailingStatus = Available
		} else {
			// The exchange publishes no P/E for a company with non-positive earnings. That
			// is a real state — not a missing feed, and not a P/E of zero.
			v.TrailingStatus = Unavailable
			v.Reason = "the exchange published no P/E, which it omits for non-positive earnings"
		}
	} else {
		v.Reason = "no archived ratios"
	}

	v.HistoricalPE = historicalStats(in, v.TrailingPE)
	v.Status = aggregate(v.TrailingStatus, v.HistoricalPE.Status)
	return v
}

// historicalStats builds the P/E distribution.
func historicalStats(in Input, currentPE *float64) HistoricalPEStats {
	window := in.WindowDays
	if window <= 0 {
		window = DefaultWindowDays
	}
	minSamples := in.MinSamples
	if minSamples <= 0 {
		minSamples = MinHistoricalPESamples
	}
	stats := HistoricalPEStats{Status: InsufficientData, WindowDays: window}

	sessions := eligibleSessions(in.History, in.AsOfDate, window)
	samples := validPEs(sessions)
	stats.SampleCount = len(samples)
	// The quality metrics describe the EVIDENCE, so they are measured whether or not the
	// distribution clears the availability floor. A reader looking at INSUFFICIENT_DATA is
	// exactly the reader who wants to know that 225 sessions were archived and 4 of them had
	// a P/E — that is the difference between "keep waiting" and "this company has no
	// earnings to wait for".
	stats.Quality = qualityOf(sessions, false)
	// The availability SHAPE is evidence about the earnings denominator, so it is read
	// whether or not the distribution itself is usable — a stock with no P/E at all is
	// exactly the case FU-11 most needs it for.
	stats.Persistence = PEAvailabilityShape(sessions)
	if len(samples) < minSamples {
		// Deliberately reports the count anyway: "3 of the 20 needed" is more useful than a
		// bare INSUFFICIENT_DATA, and it shows the archive is filling up.
		//
		// Dispersion is set here too, rather than left at its zero value. An empty status
		// reaching the presentation layer maps to a generic UNAVAILABLE, which says "we
		// could not measure the width" when the truth is the more specific "there is no
		// distribution to measure yet".
		stats.Dispersion = ComputeDispersion(stats)
		return stats
	}

	sort.Float64s(samples)
	stats.Status = Available
	stats.Min = ptr(samples[0])
	stats.Max = ptr(samples[len(samples)-1])
	stats.Median = ptr(quantile(samples, 0.50))
	stats.P25 = ptr(quantile(samples, 0.25))
	stats.P75 = ptr(quantile(samples, 0.75))

	if currentPE != nil {
		stats.CurrentPercentile = ptr(percentileOf(samples, *currentPE))
	}
	// Re-graded now that availability is known. The metrics are identical; only the
	// availability input changed, and it is what separates LOW from INSUFFICIENT.
	stats.Quality = qualityOf(sessions, true)
	stats.Dispersion = ComputeDispersion(stats)
	return stats
}

// qualityOf assembles the metrics and grades them.
//
// Kept next to the statistics it describes so the two can never be built from different
// samples: both come from the same eligibleSessions slice, which has already applied the asOf
// bound, the window cutoff and the one-record-per-session rule.
func qualityOf(sessions []sessionPE, available bool) QualityBlock {
	m := QualityMetrics{WindowSessions: len(sessions)}
	if len(sessions) > 0 {
		m.OldestSession = sessions[0].date
		m.NewestSession = sessions[len(sessions)-1].date
	}
	firstValid, lastValid := -1, -1
	for i, s := range sessions {
		if s.pe == nil {
			continue
		}
		m.ValidSamples++
		if firstValid < 0 {
			firstValid = i
		}
		lastValid = i
	}
	if firstValid >= 0 {
		m.OldestValidSession = sessions[firstValid].date
		m.NewestValidSession = sessions[lastValid].date
		// Measured in ARCHIVED SESSIONS, not in index arithmetic over some assumed calendar:
		// the archive is the only record of which days actually traded, and a span counted in
		// weekdays would silently include holidays this repo has no list of.
		m.SampleSpanSessions = lastValid - firstValid + 1
		m.SampleSpanDays = inclusiveDays(m.OldestValidSession, m.NewestValidSession)
	}
	return QualityBlock{
		Quality:         ClassifyQuality(m, available),
		RuleVersion:     QualityRuleVersion,
		QualityMetrics:  m,
		CoverageRatio:   m.CoverageRatio(),
		SampleSpanRatio: m.SampleConcentration(),
	}
}

// validPEs pulls the usable values out, preserving session order.
func validPEs(sessions []sessionPE) []float64 {
	out := make([]float64, 0, len(sessions))
	for _, s := range sessions {
		if s.pe != nil {
			out = append(out, *s.pe)
		}
	}
	return out
}

// sessionSamples reduces the archived records to ONE usable P/E per trading session.
//
// # Why deduplication is required at all
//
// The exchanges publish these ratios with a lag of two to four days — verified: a fetch on
// 2026-09-01 returned the 2026-08-28 session. The daily archive therefore stores the SAME
// session over and over:
//
//	archive 2026-09-01 → session 2026-08-28  P/E 28.05
//	archive 2026-09-02 → session 2026-08-28  P/E 28.05
//	archive 2026-09-03 → session 2026-08-28  P/E 28.05
//
// Counting those as three samples does not shift the median much — the duplication is
// roughly uniform — but it inflates the sample COUNT, so a distribution built from a week of
// real sessions would announce itself as having twenty and unlock a percentile that rests on
// almost nothing. The identity of an observation is the session, not the file.
//
// # Why the AsOf bound must come first
//
// A later archive of the same session is usually a correction, so the latest copy wins. But
// "latest" must mean the latest the caller is ALLOWED to see: a run dated 2026-09-02 must not
// pick up a correction archived on 2026-09-03, even though that correction is about a session
// it can legitimately see. So the order is
//
//	filter by archive date → group by session → latest eligible archive wins
//
// and never the reverse, which would let a future correction reach a past run.
// sessionPE is one trading session's outcome: the winning P/E, or nil when the exchange
// published none for it.
//
// The nil case is the whole reason this type exists. sessionSamples used to drop those records
// on the floor, which was right for the statistics — a company with no earnings contributes no
// P/E — and wrong for everything FU-9 needs, because it made an ACQUIRED session with no ratio
// indistinguishable from a session nobody ever fetched. One is a fact about the company; the
// other is a gap in the archive.
// # Which record wins when a session is archived more than once
//
// The latest eligible ARCHIVE wins, and that rule is now applied to the record as a whole —
// including one that carries no P/E. Before FU-9 the usability check ran first, so a later
// re-publication with no ratio could not displace an earlier one that had it; a session could
// only ever gain a P/E, never lose one.
//
// Latest-wins is the intended semantics, because a re-publication of the same session is a
// CORRECTION: the exchange restating that the company had no positive earnings for that
// session is exactly as authoritative as its earlier statement that it did. Silently keeping
// the older figure would preserve a number the publisher has withdrawn.
//
// It matters beyond this file, so it is stated rather than left to be inferred: dropping a
// sample changes SampleCount, and SampleCount is the target price's availability gate. Over
// the current archive — 1,838 sessions archived more than once — no session exhibits a
// valid→nil correction, so nothing moves today. TestALaterCorrectionWithNoPERatioWins and
// TestALaterArchiveNeverResurrectsAWithdrawnRatio pin both directions.
type sessionPE struct {
	date string
	pe   *float64
}

func eligibleSessions(history []Ratios, asOfDate string, windowDays int) []sessionPE {
	var cutoff string
	if asOfDate != "" && windowDays > 0 {
		cutoff = shiftDate(asOfDate, -windowDays)
	}

	// session date → the winning record so far.
	type winner struct {
		// pe is nil when the winning record for this session carried no usable P/E. The
		// session is still counted: it was archived.
		pe      *float64
		archive string
		// order is the record's position in the input, the tie-break when archive dates are
		// absent (a caller assembling history by hand). LoadHistory returns oldest-first, so
		// a later position is a later archive.
		order int
	}
	best := map[string]winner{}

	for i, r := range history {
		if r.Date == "" {
			continue // an observation with no session cannot be placed in time
		}
		// Usability of the VALUE, decided separately from presence of the session. Missing,
		// non-positive and non-finite values never become numbers — including them would drag
		// every percentile toward the cheap end and make a loss-making stretch look like a
		// bargain — but the session they belong to is still recorded as archived.
		var pe *float64
		if r.PERatio != nil && *r.PERatio > 0 &&
			!math.IsNaN(*r.PERatio) && !math.IsInf(*r.PERatio, 0) {
			v := *r.PERatio
			pe = &v
		}
		// The AsOf bound, applied to BOTH dates. The session must have happened by the
		// cutoff, and the archive we are reading it from must have existed by then too.
		if asOfDate != "" && r.Date > asOfDate {
			continue
		}
		if asOfDate != "" && r.ArchiveDate != "" && r.ArchiveDate > asOfDate {
			continue
		}
		if cutoff != "" && r.Date < cutoff {
			continue
		}

		cur, seen := best[r.Date]
		if !seen || laterArchive(r.ArchiveDate, i, cur.archive, cur.order) {
			best[r.Date] = winner{pe: pe, archive: r.ArchiveDate, order: i}
		}
	}

	// Sorted by session so the output is deterministic; the statistics sort again anyway,
	// but a stable order makes a failing test readable.
	sessions := make([]string, 0, len(best))
	for d := range best {
		sessions = append(sessions, d)
	}
	sort.Strings(sessions)

	out := make([]sessionPE, 0, len(sessions))
	for _, d := range sessions {
		out = append(out, sessionPE{date: d, pe: best[d].pe})
	}
	return out
}

// laterArchive reports whether a candidate record supersedes the one already held.
//
// A later ARCHIVE wins, because a re-publication of the same session is a correction. When
// archive dates are absent — a caller building history by hand — position stands in, and
// LoadHistory's oldest-first order makes that the same rule.
func laterArchive(archive string, order int, curArchive string, curOrder int) bool {
	if archive != "" && curArchive != "" {
		if archive != curArchive {
			return archive > curArchive
		}
		return order > curOrder
	}
	return order > curOrder
}

// quantile is the linear-interpolation quantile of a SORTED slice.
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// percentileOf is the share of samples at or below v, as a ratio in [0,1].
//
// Ties count as at-or-below, matching internal/indicator.PercentileRank. Stating the tie rule
// here matters: with "<" instead of "<=", a stock trading at exactly its historical median
// would report a percentile below 50, and two places in the repo would disagree about the
// same stock.
func percentileOf(sorted []float64, v float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	n := 0
	for _, s := range sorted {
		if s <= v {
			n++
		}
	}
	return float64(n) / float64(len(sorted))
}

func aggregate(trailing, historical Availability) Availability {
	switch {
	case trailing == Available && historical == Available:
		return Available
	case trailing == Available || historical == Available:
		return Partial
	default:
		return Unavailable
	}
}

func ptr(v float64) *float64 { return &v }
