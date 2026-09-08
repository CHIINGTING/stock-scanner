package fetcher

import (
	"math"
	"time"
)

// Detecting dividend / split adjustment events from the Close ÷ AdjClose ratio.
//
// WHY THIS EXISTS
//
// Entry Planning prices supports and stops off MA20 / MA60 / ATR, and
// internal/indicator/calculator.go builds every one of those from the RAW Close
// (closes[i] = c.Close, unconditionally). A raw close series is DISCONTINUOUS on
// an ex-dividend day: the quote drops by the dividend with no economic event
// behind it. Measured over the cached history (1,987 symbols with >=120 bars,
// ~2 years, 954,967 bar pairs):
//
//	adjustment events found              3,141
//	|gap| median                         3.18%   (p75 4.85%, p90 6.13%, max 23.15%)
//	events with |gap| >= 3%              53.1%
//	events with |gap| >= 5%              22.7%
//	symbols whose newest bar IS the event      9
//
// and, per trailing window — counted as "the event bar falls inside the last
// barsRead bars", which is exactly what WindowIsAdjustmentClean suppresses on:
//
//	barsRead 20 (MA20)      213 symbols (10.7%)
//	barsRead 21 (20 TRs)    220 symbols (11.1%)   ← one extra bar, 7 more symbols
//	barsRead 60 (MA60)    1,125 symbols (56.6%)
//	barsRead 61 (60 TRs)  1,142 symbols (57.5%)   ← 17 more symbols
//
// The 21- and 61-bar rows are the windows in which 20 / 60 TRUE RANGES are
// FORMED. They are NOT "ATR20" and "ATR60": this repo's ATR is Wilder-smoothed
// and reads the whole series, so no bar count describes it — see
// WindowIsAdjustmentClean.
//
// (The subset where the window also contains a PRE-event bar, i.e. where a plain
// average of barsRead closes truly mixes two bases, is 209 and 1,100 for MA20 /
// MA60 — see WindowIsAdjustmentClean for why the wider criterion is used.)
//
// The dangerous direction is that a mechanical gap MANUFACTURES an entry: a
// 3.18% raw drop is easily enough to turn "price is extended above MA20" into
// "price has pulled back to MA20", i.e. a pullback signal with no economic
// content. So this detector exists to let a caller SUPPRESS or ANNOTATE such an
// entry.
//
// SCOPE: this file only reports. It deliberately does NOT change how indicators
// are computed. The repo is knowingly mixed today — internal/scanner (relstrength,
// vcp) routes through PriceForCalc while indicator.Calculate does not — and
// changing that would move existing strategy scores. Not this file's job.
//
// PURITY: every function here is pure. No clock, no network, no file I/O, no
// randomness, no mutation of the input slice. Same input → same output.

// AdjustmentStatus says how much the scan was able to conclude. Read it BEFORE
// reading Events: an empty Events slice means something different per status.
type AdjustmentStatus string

const (
	// AdjustmentOK means an adjusted series is genuinely present (AdjClose differs
	// from Close somewhere in the window) and Events lists every adjustment the
	// SOURCE has already applied. Events may legitimately be empty: that is "an
	// adjusted series exists and nothing was adjusted inside this window".
	//
	// BLIND SPOT — "OK and zero events" does NOT mean "the raw Close series is
	// continuous here". This detector can only see adjustments the upstream has
	// already folded into AdjClose. If a stock went ex-dividend and Yahoo has not
	// yet restated the series, the raw Close has ALREADY gapped while the ratio
	// staircase has not moved yet, so the scan reports OK with no events while
	// MA20 is already contaminated. That is the only path in this file which
	// returns a clean-LOOKING conclusion that can be wrong; every other failure
	// mode returns "unknown" instead. It cannot be closed from candles alone — it
	// needs a corporate-action feed this package does not have.
	//
	// Empirically the lag looks short rather than long: 9 cached symbols carry the
	// event ON their newest bar, i.e. the restatement usually lands the same day
	// the raw quote gaps. So read "OK, 0 events" as "the source knows of no
	// adjustment in this window", not as "there was none".
	AdjustmentOK AdjustmentStatus = "OK"

	// AdjustmentNoAdjustedSeries means Close ÷ AdjClose was 1.0 on every usable bar.
	//
	// THIS IS NOT "this stock pays no dividend". It is "WE CANNOT TELL".
	//
	// The cause is in this package: yahoo.go's parse loop does, PER BAR,
	//
	//	c.AdjClose = c.Close
	//	if i < len(adj) && adj[i] != nil && ... { c.AdjClose = *adj[i] }
	//
	// so whenever an adjusted close is missing or invalid, AdjClose is filled with
	// Close (a deliberate choice, so AdjClose is never a misleading zero — see
	// types.go). When the source ships no adjusted array at all, EVERY bar takes
	// that path and the ratio is a flat 1.0, which makes these two situations
	// bit-for-bit identical downstream and impossible to separate from candles
	// alone:
	//
	//	(a) a stock that has never paid a dividend or split, and
	//	(b) a stock for which we simply have no adjusted series.
	//
	// 430 of the 1,987 cached symbols (21.6%) land in this bucket. Treating it as
	// (a) would silently assert "no ex-dividend gap here" for every symbol in (b).
	// A caller that needs the guarantee must treat this status as UNKNOWN — the
	// conservative reading is "an adjustment may have happened and we would not
	// have seen it". Separating (a) from (b) requires a dividend/corporate-action
	// source this package does not have.
	//
	// Because the fallback is per-bar, a SINGLE missing adjusted close inside an
	// otherwise adjusted series does not produce this status: it drops that one
	// bar's ratio to exactly 1.0 and therefore fabricates TWO events, one stepping
	// down onto 1.0 and one stepping back up — and the second has a POSITIVE
	// GapPct. No such hole exists anywhere in the cache today (0 positive gaps in
	// 954,967 pairs), but it is one of the two known mechanisms that can produce
	// one; see AdjustmentEvent.RatioBefore for the other.
	AdjustmentNoAdjustedSeries AdjustmentStatus = "NO_ADJUSTED_SERIES"

	// AdjustmentInsufficientBars means there were fewer than MinAdjustmentBars
	// usable bars, so not even one ratio change could be formed. Events is empty
	// and that emptiness carries no information at all.
	AdjustmentInsufficientBars AdjustmentStatus = "INSUFFICIENT_BARS"
)

// MinAdjustmentBars is the smallest number of USABLE bars (Close > 0 and
// AdjClose > 0) from which a conclusion can be drawn. Detection compares the
// ratio of bar k against bar k-1, so two usable bars are needed for one
// comparison. This is a hard arithmetic floor, not a quality bar: the >=120-bar
// filter used in the measurements above was a sampling choice for the study, not
// a requirement of the algorithm.
const MinAdjustmentBars = 2

// adjustmentEpsilon is the relative change in Close ÷ AdjClose above which a
// ratio step counts as a real adjustment rather than floating-point noise.
//
// CHOSEN FROM MEASUREMENT, not taste. Over all 954,967 consecutive usable bar
// pairs in .cache, the distribution of |r[k]/r[k-1] - 1| (582,873 of them
// nonzero) is bimodal with an EMPTY BAND in between:
//
//	1e-13 .. 1e-12        6        \
//	1e-12 .. 1e-11      105         |
//	1e-11 .. 1e-10    1,190         | noise mode
//	1e-10 .. 1e-9    10,247         | (largest noise sample: 2.912194e-07)
//	1e-9  .. 1e-8   101,173         |
//	1e-8  .. 1e-7   452,719         |
//	1e-7  .. 1e-6    14,292        /
//	1e-6  .. 1e-5         0   ← nothing at all lives here
//	1e-5  .. 1e-4         1        \
//	1e-4  .. 1e-3         7         | real-event mode
//	1e-3  .. 1e-2       423         | (smallest real sample: 7.496447e-05,
//	1e-2  .. 1e-1     2,692         |  symbol 8932 on 2024-10-28)
//	1e-1  .. 1e+0        18        /
//
// The gap between the top of the noise mode and the bottom of the event mode is
// a factor of 257.4 (2.912194e-07 → 7.496447e-05) — the widest consecutive-value
// jump anywhere below 1e-2. EVERY threshold inside that band yields the identical
// 3,141 events, so the choice is not delicately balanced.
//
// WHERE THE NOISE CEILING COMES FROM — and why the obvious first-order model is
// NOT a bound. Yahoo ships these prices at float32 precision (relative half-ULP
// at most 2^-24 = 5.960464e-08, visible in cached values such as
// 45.474998474121094). A ratio step reads four such values (Close and AdjClose,
// on bars k and k-1), which suggests a ceiling of 4 × 2^-24 = 2.384186e-07.
// MEASURED, that ceiling is CROSSED 22 times: 124 pairs exceed 2.0e-07, 22
// exceed 2.384186e-07 and 3 exceed 2.9e-07. The largest, 2.9121938e-07 (symbol
// 7780, 2024-12-23 → 24), is 1.22× the 4-rounding model; and computing the four
// values' OWN exact half-ULPs for that pair — 5.4033e-08 + 5.9447e-08 +
// 5.3728e-08 + 5.9112e-08 = 2.2632e-07 — the observation is still 1.29× the
// roundings the model accounts for. So upstream carries at least one rounding
// this model does not (the adjustment factor is itself quantised), and the model
// must be read as an order-of-magnitude explanation, not a proof.
//
// What IS true of the cache: nothing exceeds FIVE half-ULPs, 5 × 2^-24 =
// 2.980232e-07, and the tail stops just under it (max ÷ ceiling = 0.977, zero
// pairs above). So the honest statement of the margin is that 1e-6 sits
//
//	3.43× above the largest OBSERVED noise sample (2.9121938e-07), and
//	3.36× above the 5-half-ULP ceiling  (2.980232e-07), and
//	75×  below the smallest real event  (7.496447e-05).
//
// 3.4× is comfortable but it is NOT an order of magnitude, and it is measured
// rather than proven — so a re-fetched cache, a different provider, or an
// upstream precision change REQUIRES re-measuring this distribution before
// trusting the threshold. TestScanAdjustmentsRealCache0050 is a canary, and a
// deliberately limited one: it fails with missing dates the moment the threshold
// rises above a real event (0050's smallest gap is 0.6048%), and with extra
// dates once the threshold sinks into 0050's OWN noise — but that noise ceiling is
// 1.408868e-07, well under the universe's 2.9121938e-07. Measured, 0050 gains no
// spurious event at 3e-07 or 2.5e-07 and only lights up at 1e-07 (31 extra),
// while the universe already gains 14 false events at 2.5e-07 and 124 at
// 2.0e-07. Reading one symbol therefore catches "collapsed to the 1e-7 decade"
// and "climbed above real events", NOT a threshold parked in the band between.
// Re-measuring the full distribution is the only check that covers that band.
//
// Thresholds a decimal either way are NOT interchangeable: 1e-7 admits 17,433
// "events" (14,292 of them pure noise), while 1e-2 keeps only 2,710 and drops
// 431 real adjustments including every gap under ~1%.
const adjustmentEpsilon = 1e-6

// AdjustmentEvent is one detected discontinuity in the raw Close series.
//
// It is one STEP in the ratio staircase, which is not always one corporate
// action: two actions taking effect on the same bar (say a cash dividend and a
// split) show up as a SINGLE event whose GapPct is their compounded effect.
// That is the right number for "how much of this bar's raw move was mechanical",
// but it must not be read as attributable to either action alone.
type AdjustmentEvent struct {
	// Index is the position IN THE INPUT SLICE of the first bar quoted on the NEW
	// adjustment basis — i.e. the ex-dividend bar itself, the bar whose raw Close
	// already contains the drop. Bars at Index-1 and earlier are on the old basis.
	Index int
	// Date is candles[Index].Date, copied so a caller need not re-index.
	Date time.Time
	// RatioBefore / RatioAfter are Close ÷ AdjClose on the previous usable bar and
	// on this bar. Both are always > 0 (bars with a non-positive Close or AdjClose
	// never become an event endpoint).
	//
	// MEASURED: all 3,141 events in .cache have RatioAfter < RatioBefore, because
	// a cash dividend or a forward split scales the stored history DOWN and the
	// factor unwinds as you walk forward. That is a measurement, NOT a mechanical
	// invariant, and two known mechanisms invert it:
	//
	//	a reverse split or a capital reduction (減資, common in Taiwan) scales
	//	historical AdjClose UP, so RatioAfter > RatioBefore and GapPct is POSITIVE;
	//
	//	a per-bar hole in the upstream adjusted array (see
	//	AdjustmentNoAdjustedSeries) drops one bar's ratio to 1.0 and the step back
	//	off it is likewise POSITIVE.
	//
	// Neither has occurred in the cached window, so nothing here is ordered or
	// sign-tested — detection uses math.Abs and so must any caller.
	RatioBefore float64
	RatioAfter  float64
	// GapPct is the percentage of the raw-close move at Index that is MECHANICAL —
	// i.e. how much the raw series jumped for adjustment reasons alone, with the
	// economic return removed:
	//
	//	GapPct = (RatioAfter/RatioBefore - 1) * 100
	//
	// It is SIGNED, and both signs are possible (see RatioBefore): a dividend or
	// forward split makes it negative, a reverse split or capital reduction
	// positive. Magnitude is what matters for "is this big enough to matter", so
	// compare math.Abs(GapPct) and never GapPct < -x.
	//
	// Equivalent, and the way to hand-check it: with r = Close/AdjClose,
	//
	//	Close[k]/Close[k-1] = (AdjClose[k]/AdjClose[k-1]) × (r[k]/r[k-1])
	//
	// so GapPct is exactly the factor by which the RAW return differs from the
	// ADJUSTED (economically real) return, in percent.
	//
	// Worked example, hand-checkable against .cache/0050_TW.json — 0050's
	// ex-dividend bar of 2025-01-17 (Index 93):
	//
	//	2025-01-16  Close 49.51250076293945  AdjClose 47.531585693359375
	//	2025-01-17  Close 49.07500076293945  AdjClose 47.762733459472656
	//
	//	raw return       49.075000/49.512501 - 1 = -0.8836%
	//	adjusted return  47.762733/47.531586 - 1 = +0.4863%
	//	GapPct           0.991164/1.004863 - 1   = -1.3633%
	//
	// The holder was UP 0.49% that day; the raw quote showed DOWN 0.88%. The
	// -1.36% difference is the dividend, and it is exactly the GapPct reported.
	// Via the ratios it is the same number: 1.0274747111067308 /
	// 1.0416757623522084 - 1 = -1.3633%.
	GapPct float64
	// SpansSkippedBar is true when the bar immediately before Index was unusable
	// (non-positive Close or AdjClose) and therefore skipped. The adjustment is
	// real, but it could have taken effect on any of the skipped bars rather than
	// on Index: the DATE is then approximate (not later than Date, possibly
	// earlier). False means Index-1 was compared directly and the date is exact.
	SpansSkippedBar bool
}

// AdjustmentScan is the result of ScanAdjustments. Each field's meaning when data
// is missing is stated explicitly, because "empty" is otherwise ambiguous.
type AdjustmentScan struct {
	// Status is the only field that is meaningful unconditionally. Read it first.
	Status AdjustmentStatus
	// Events is ordered oldest-first. It is empty (nil) for every status except
	// AdjustmentOK, and under AdjustmentOK an empty slice is a real answer: no
	// adjustment inside the window — subject to the upstream-lag blind spot
	// documented on AdjustmentOK. Under the other two statuses emptiness means
	// "not determined" and must not be read as "no adjustment".
	Events []AdjustmentEvent
	// UsableBars counts bars with Close > 0 AND AdjClose > 0. Always filled, for
	// every status; 0 means the input was empty or entirely unusable.
	UsableBars int
	// SkippedBars counts bars dropped because Close <= 0 or AdjClose <= 0. Always
	// filled, for every status. A bar with no price cannot be shown to be free of
	// an adjustment, so this is reported rather than absorbed silently: when it is
	// nonzero, the window contains blind spots, an adjustment landing entirely
	// inside a skipped run is invisible, and any event with SpansSkippedBar has an
	// approximate date. UsableBars + SkippedBars == len(candles), always.
	//
	// It is a WHOLE-SERIES count and says nothing about where the holes are, so
	// it cannot answer "is this window complete". WindowIsAdjustmentClean
	// re-derives usability over its own trailing window for that reason.
	SkippedBars int
}

// ScanAdjustments finds dividend / split adjustment events in a candle series.
//
// PASS THE FULL SERIES. DO NOT PRE-SLICE.
//
// ScanAdjustments(candles[len(candles)-20:]) is the obvious call and it is
// wrong. The ratio staircase is only evidence while its whole history is
// present: after the newest adjustment has unwound, Close ÷ AdjClose sits at
// EXACTLY 1.0 on every later bar, so a short trailing window of a
// dividend-paying stock is bit-identical to yahoo.go's fallback and comes back
// NO_ADJUSTED_SERIES — "unknown". Measured over the 1,987 cached symbols with
// >=120 bars:
//
//	series passed in   NO_ADJUSTED_SERIES   of which the FULL series scans OK
//	last 20 bars       1,778 (89.5%)        1,348  ← lost purely to the slicing
//	last 60 bars         887 (44.6%)          457  ← lost purely to the slicing
//	full series          430 (21.6%)            0
//
// 0050 itself is in the 1,348: its 2026-07-21 dividend was the last one, the
// ratio has been exactly 1.0 ever since, and its final 20 bars alone therefore
// report NO_ADJUSTED_SERIES. Combined with the conservative policy that unknown
// means not-clean, pre-slicing to 20 bars would suppress ~89% of the universe on
// no evidence at all. Hand in everything you have and express the WINDOW as a
// bar count instead — see WindowIsAdjustmentClean.
//
// INPUT CONTRACT, unchecked: bars oldest-first (as StockData.Candles always is)
// and no duplicate dates. This function verifies neither — dates are only copied
// into events, so an out-of-order or duplicated bar yields a correspondingly
// meaningless Date while the ratio comparisons still run pairwise down the slice.
// The slice is read, never modified, and no bar is reordered. Indices in the
// result refer to the input slice, including bars skipped as unusable, so they
// line up with the same slice an indicator window is measured over.
//
// POINT-IN-TIME HONESTY — read this before using it in a backtest.
//
// Yahoo's AdjClose is RETROACTIVELY RESTATED. One ex-dividend event rewrites the
// AdjClose of EVERY bar before it, so the ratio staircase we read today is built
// from information that did not exist on the dates it describes. Concretely:
//
//   - the DATE of a step is correct — it is the real ex-dividend date, verified
//     against 0050's 2025-01-17 / 2025-07-21 / 2026-01-22 / 2026-07-21; and
//   - the KNOWLEDGE that the step is there is hindsight. Scanning a series as it
//     stood before that ex-date would have shown a flat ratio there, because the
//     restatement had not happened yet.
//
// For LIVE use this is a non-issue: today's snapshot already contains today's
// restatement, which is exactly the information a live decision has.
//
// For BACKTESTS it is lookahead, and it must be labelled as such. It is
// DIRECTIONALLY SAFE here only because of how the result is meant to be used: to
// SUPPRESS or ANNOTATE an entry, never to create one. Hindsight that removes
// trades cannot inflate a backtest with trades the strategy could not have taken;
// it can only make the backtest more pessimistic than reality (it may also remove
// a trade that live trading would have taken, so the trade COUNT is not
// faithful). Wire this into anything that GENERATES a signal and that safety
// argument is void.
func ScanAdjustments(candles []Candle) AdjustmentScan {
	usable := make([]adjRatioBar, 0, len(candles))
	skipped := 0
	for i, c := range candles {
		// A bar without a positive price on both series carries no ratio. It is
		// dropped, but counted into SkippedBars — never treated as "unchanged".
		if !adjBarUsable(c) {
			skipped++
			continue
		}
		usable = append(usable, adjRatioBar{index: i, date: c.Date, ratio: c.Close / c.AdjClose})
	}

	scan := AdjustmentScan{UsableBars: len(usable), SkippedBars: skipped}

	if len(usable) < MinAdjustmentBars {
		scan.Status = AdjustmentInsufficientBars
		return scan
	}

	for k := 1; k < len(usable); k++ {
		before, after := usable[k-1].ratio, usable[k].ratio
		if math.Abs(after/before-1) <= adjustmentEpsilon {
			continue
		}
		scan.Events = append(scan.Events, AdjustmentEvent{
			Index:           usable[k].index,
			Date:            usable[k].date,
			RatioBefore:     before,
			RatioAfter:      after,
			GapPct:          (after/before - 1) * 100,
			SpansSkippedBar: usable[k].index != usable[k-1].index+1,
		})
	}

	if len(scan.Events) == 0 && ratioFlatAtOne(usable) {
		// No step anywhere AND the ratio never left 1.0 → AdjClose is a copy of
		// Close on every bar, which is what yahoo.go's fallback produces. Cannot
		// distinguish "never paid a dividend" from "no adjusted series". Same
		// epsilon as detection, so a series is never judged flat by a margin
		// finer than the one used to find steps.
		scan.Status = AdjustmentNoAdjustedSeries
		return scan
	}

	// Either steps were found, or the ratio sits at some value other than 1.0 —
	// both prove an adjusted series distinct from Close is present, so an empty
	// Events list here really does mean "nothing adjusted in this window".
	scan.Status = AdjustmentOK
	return scan
}

// adjBarUsable reports whether a bar carries a Close ÷ AdjClose ratio at all.
// Both series must be strictly positive: a non-positive price is missing data,
// never a quote. ScanAdjustments and WindowIsAdjustmentClean share this one
// predicate so the scan's SkippedBars count and the window guard cannot drift
// apart into disagreeing about what "unusable" means.
func adjBarUsable(c Candle) bool { return c.Close > 0 && c.AdjClose > 0 }

// adjRatioBar is one bar reduced to what detection needs: where it sits in the
// caller's slice, its date, and its Close ÷ AdjClose ratio (always > 0).
type adjRatioBar struct {
	index int
	date  time.Time
	ratio float64
}

// ratioFlatAtOne reports whether Close ÷ AdjClose is 1.0 on every usable bar,
// within adjustmentEpsilon. True is the signature of yahoo.go's AdjClose = Close
// fallback — and equally of a stock that has genuinely never adjusted, which is
// precisely why it maps to AdjustmentNoAdjustedSeries ("unknown") and not to
// "no events".
func ratioFlatAtOne(bars []adjRatioBar) bool {
	for _, b := range bars {
		if math.Abs(b.ratio-1) > adjustmentEpsilon {
			return false
		}
	}
	return true
}

// LastAdjustmentAge reports how many bars ago the most recent adjustment event
// happened, counted in INPUT-SLICE bars: 0 means the newest bar is itself the
// adjustment (its raw Close already contains the gap), 1 means the bar before it,
// and so on. The count spans skipped bars.
//
// ok == false means THERE IS NO ANSWER, and a caller MUST branch on it rather
// than reading barsAgo (which is 0 then, a value that would otherwise read as the
// most alarming possible answer). Three different situations produce it:
//
//	AdjustmentInsufficientBars   too few usable bars to compare anything
//	AdjustmentNoAdjustedSeries   ratio flat at 1.0 — undetectable, NOT "none"
//	AdjustmentOK, no events      an adjusted series exists and nothing adjusted
//	                             inside the window
//
// Only the third is genuinely "no recent adjustment"; the first two are unknown.
// This function does not distinguish them on purpose — collapsing them into one
// bool keeps the "no answer" case impossible to miss. Call ScanAdjustments when
// the reason matters, which it does whenever the decision is a guarantee rather
// than a warning.
//
// DO NOT GATE ON THIS. `if ok && age <= N { suppress }` is the natural line to
// write and it is WRONG twice over:
//
//   - It reads ok == false as "nothing to suppress", and ok == false is mostly
//     UNKNOWN: the 430 flat-at-one symbols (21.6% of the cached universe) sail
//     straight through such a check with no evidence at all about their MA20. The
//     polarity has to be inverted — the caller must PROVE cleanliness, not fail
//     to disprove it. MEASURED: WindowIsAdjustmentClean refuses to certify 650 of
//     the 1,987 cached symbols at barsRead 21, while `ok && age <= 20` suppresses
//     220 of them — all 430 it waves through are unknowns, not clean windows.
//   - N is a PERIOD and barsRead is a BAR COUNT, and writing one where the other
//     belongs is only ever safe by accident. `age <= N` coincides EXACTLY with
//     WindowIsAdjustmentClean(candles, N+1) — both are dirty iff age <= N — so
//     for a calculation that reads N+1 bars it under-suppresses nothing (measured
//     at N = 20: 0 symbols under-suppressed, and 7 OVER-suppressed relative to
//     strict contamination, the ones with age exactly 20). Aim the same line at
//     MA_N, which reads only N bars, and it suppresses one bar beyond what this
//     function would (the 7 symbols at age exactly 20, 17 at age exactly 60) and
//     two bars beyond strict contamination (11 symbols at N = 20, 42 at N = 60):
//     safe direction, paid for in signals. And for the repo's Wilder ATR / RSI
//     or its EMA-based KDJ, NO value of N is right at all, because those read
//     the entire series — see WindowIsAdjustmentClean.
//
// Use WindowIsAdjustmentClean for gating, and read this age directly only to
// ANNOTATE ("last adjustment 12 bars ago").
func LastAdjustmentAge(candles []Candle) (barsAgo int, ok bool) {
	scan := ScanAdjustments(candles)
	if scan.Status != AdjustmentOK || len(scan.Events) == 0 {
		return 0, false
	}
	last := scan.Events[len(scan.Events)-1]
	return len(candles) - 1 - last.Index, true
}

// WindowIsAdjustmentClean reports whether the trailing barsRead bars of candles
// can be SHOWN to sit entirely on one adjustment basis. It answers false for
// every case we cannot vouch for — NO_ADJUSTED_SERIES, INSUFFICIENT_BARS, a
// non-positive barsRead, barsRead > len(candles), and any UNUSABLE bar (Close or
// AdjClose not positive) inside the window — so "unknown" is never mistaken for
// "clean". (MISSING ≠ ZERO.)
//
// WHAT barsRead MEANS — AND WHERE THE WHOLE ABSTRACTION STOPS WORKING.
//
// barsRead is the number of TRAILING BARS THE CALLER'S CALCULATION ACTUALLY
// READS, not the indicator period. For a FINITE-WINDOW calculation that count is
// exact, a cut-off genuinely exists, and a true here is a real guarantee:
//
//	SMA_N        (MA20, MA60, volume MA)  reads N    → pass N
//	Bollinger_N  (mean and stddev)        reads N    → pass N
//	highest-high / lowest-low over N      reads N    → pass N
//	N-bar return / momentum               reads N+1  → pass N+1 (needs the base bar)
//
// THERE IS NO ROW FOR ATR, RSI OR KDJ, AND THAT IS NOT AN OVERSIGHT. An earlier
// version of this comment carried "ATR_N reads N+1 → pass N+1 (each true range
// needs the prior close)". That is FALSE for every ATR in this repo, and anyone
// wiring this up must not reintroduce it.
//
// internal/indicator/atr.go is the repo's only ATR and it is Wilder smoothing
// (atr.go:27-29, quoted verbatim):
//
//	for i := period + 1; i < n; i++ {
//		atr[i] = (atr[i-1]*float64(period-1) + tr(i)) / float64(period)
//	}
//
// atr[newest] is built from atr[newest-1], which is built from atr[newest-2],
// all the way back to the seed at index period: it READS THE ENTIRE SERIES. A
// true range enters at weight 1/N and every later bar multiplies it by (N-1)/N,
// so the true range from `age` bars ago still carries
//
//	((N-1)/N)^age of the weight it entered with — positive at every finite age.
//
// It never reaches zero, so NO barsRead exists that would make this function's
// true mean "the ATR does not contain that event":
//
//	residual weight of one true range       ATR(14)   ATR(20)
//	age = N+1 (the discarded "reads N+1")    0.329     0.341   ← a THIRD of it
//	age = 2N                                 0.126     0.129
//	age = 60                                 0.012     0.046
//	first age with residual below 1%           63        90
//
// (calculator.go:66 builds this repo's ATR with period 14; every consumer —
// scanner/holdinghorizon.go:132, technical/keltner.go:87,
// r6backtest/engine.go:83 — calls that same function.)
//
// Passing N+1 for ATR_N therefore certifies the TR-FORMATION WINDOW and nothing
// more: "the last N true ranges were each formed from two bars on one basis". It
// does NOT certify the ATR value. MEASURED over .cache, recomputing atr[newest]
// at period 14 against a counterfactual in which only the ex-day true range is
// de-gapped (the previous close restated onto the new basis), across the symbols
// whose most recent adjustment is old enough for the N+1 rule to call ATR(14)
// clean:
//
//	last event at age 15..20    41 symbols  median 1.01%  p90 3.80%  max 12.96%
//	last event at age 15..62   992 symbols  median 0.13%  p90 1.38%  max 12.96%
//	last event at age ≥ 63     386 symbols  median 0.00%  p90 0.04%  max  0.59%
//
// READ TWO THINGS OFF THAT TABLE CAREFULLY. The rows OVERLAP — it is not a
// partition: 15..20 is a subset of 15..62 (the same symbol, 8916, supplies both
// maxima) and the counts do not add to a universe; the disjoint bands are in the
// cost table further down. And the errors are ABSOLUTE VALUES, |raw/cf - 1|.
// Signed, the same three medians are +0.55%, +0.09% and +0.00%, because a
// dividend gap inflates the ex-day true range and so it is the RAW ATR that
// comes out too big — though not always: 7 / 136 / 67 of the three rows are
// negative, so a caller must not assume a sign either.
//
// So an ATR(14) this function would call clean can be overstated by 13% (8916,
// event 2026-08-12, gap -7.01%, age 19 — inside the "clean" band), and only past
// age 63, the 1% residual point, does the measured error collapse.
//
// These numbers are cache-derived, so they age exactly like adjustmentEpsilon's
// distribution does. TestATRDeGappingErrorRealCache is their canary: it re-runs
// this counterfactual over whatever .cache holds and fails if the ≥ 63 band's
// worst error climbs to 1%, or if the young band stops being an order of
// magnitude worse. A re-fetched cache REQUIRES re-measuring the table itself —
// the canary only proves it has not gone badly stale.
//
// The repo's other recursive indicators behave the same way at their own rates:
//
//	RSI (rsi.go:38-39)  avgGain / avgLoss are the identical Wilder recursion, so
//	                    ((N-1)/N)^age with N = 14 → age 63 for 1%. RSI is a
//	                    NONLINEAR function of the two, so read that as a memory
//	                    length, not an error bound. MEASURED on the real
//	                    indicator.RSI (central difference of RSI[newest] against
//	                    one close, alternating fixture so both averages stay
//	                    nonzero): the sensitivity shrinks by exactly 13/14 per
//	                    extra bar of age, i.e. the closed form survives the
//	                    nonlinearity as a SHAPE even though the magnitude
//	                    depends on where the series sits.
//	KDJ (kdj.go:48-49)  K and D are EMAs seeded at 50 from index 0 — recursive
//	                    from the first bar, no window at all, and CASCADED, which
//	                    makes D's tail strictly longer than K's. Detail below.
//
// KDJ IN DETAIL, because the obvious single-decay reading of it is wrong — an
// earlier version of this comment made both of the following mistakes. From
// kdj.go:22-23 and 48-49:
//
//	kWeight := 1.0 / float64(dSmooth)    k = (1-kWeight)*prevK + kWeight*rsv
//	dWeight := 1.0 / float64(jSmooth)    d = (1-dWeight)*prevD + dWeight*k
//
// FIRST: D's smoothing constant is jSmooth, NOT dSmooth. They merely default to
// the same 3 (configs/config.yaml:283-284 → scanner.go:256-257 →
// indicator.Config), and the two are configured independently, so writing
// dSmooth for D's decay is a claim about one default file, not about the code.
//
// SECOND: D is an EMA OF K, not of rsv. A shock reaches D only after passing
// through K, so D is a CASCADE of two EMAs and decays more slowly than either.
//
// MEASURED by finite difference on the real indicator.KDJ. Fixture: highs 100
// and lows 0 on every bar, so every kPeriod window has hi = 100, lo = 0 and rsv
// == close exactly; bumping one close therefore bumps exactly one rsv, and since
// K and D are linear in rsv the difference is exact rather than approximate.
//
// NORMALISATION — the number is meaningless without it. Each residual below is
// the sensitivity of TODAY's output to an rsv `age` bars back, divided by the
// sensitivity of that SAME output to TODAY's rsv. It reads "what fraction of the
// influence this input had on the day it entered is still in today's value",
// which is the same convention as the ATR table above (where that entry weight
// is 1/N). Normalising D against K's entry weight instead, or omitting D's
// same-bar response to K, gives visibly different numbers.
//
//	age    K residual = (1-1/dSmooth)^age    D residual (cascade)
//	  1                0.6667                       1.3333
//	  2                0.4444                       1.3333
//	  5                0.1317                       0.7901
//	 12                0.0077                       0.1002
//	 19                0.0005                       0.0090
//	first age < 1%       12                            19
//
// D's column is NOT monotone: two bars after a shock, D holds 1.33x the
// influence it held on entry day, because the cascade is still filling. Anything
// that calls this a "residual" and assumes decay from age 0 is wrong at the
// short end. The closed forms, with a = 1-1/dSmooth and b = 1-1/jSmooth, are
//
//	K:  a^age
//	D:  sum(m = 0..age) b^m · a^(age-m)
//	    = (a^(age+1) - b^(age+1)) / (a - b),   or (age+1)·a^age when a == b
//
// so at the defaults D is (age+1)·(2/3)^age and NOT (2/3)^age — it reaches 1%
// at age 19 rather than 12. Set j_smooth: 9 and leave d_smooth: 3 and K does not
// move at all while D's 1% point goes from age 19 to age 51 (measured, same
// fixture), which is what the parameter-name error above would hide. J = 3K - 2D
// inherits D's tail and ends up dominated by it: the 2D term passes the 3K term
// between ages 3 and 4 (so J's response to a shock CHANGES SIGN there), and by
// age 19 |dJ| is 10.3x |dK|. All of this is pinned by
// TestKDJCascadeResidualOnRealIndicator.
//
// WHAT A CALLER OF THOSE SHOULD DO INSTEAD — ANNOTATE, DO NOT GATE.
//
// Finite-window indicator: gate. WindowIsAdjustmentClean is a real guarantee for
// it and it is cheap.
//
// Recursive indicator (ATR, RSI, KDJ): this function cannot offer a guarantee at
// any barsRead, and the tempting substitute — demanding `ok && age >= 63` from
// LastAdjustmentAge so the residual is under 1% — is DELIBERATELY NOT
// RECOMMENDED here. Take the age, compute ((N-1)/N)^age, and ship it as an
// ANNOTATION on a signal you still emit:
//
//	"ATR(14) stop may be too wide: last adjustment 19 bars ago, gap -7.01%,
//	 so ((13/14)^19) = 25% of that gap's true range is still in the number"
//
// — which for 8916, the worst case in the cache, is an ATR 13.0% too big. That
// is a sentence a human can act on; a gate would simply have deleted the symbol.
//
// WHY, in one table. The cost of gating, over the same 1,987 cached symbols
// (these bands ARE disjoint, unlike the three-row table above):
//
//	WindowIsAdjustmentClean(candles, 15)     ATR(14)'s TR-formation window
//	                                         1,378 pass   69.4%
//	  ... AND ok AND age >= 63                 386 pass   19.4%
//	                                         → 992 further symbols excluded:
//	                                           49.9% of the universe, 72.0% of
//	                                           everything the window gate passed
//
//	(at barsRead 21 — the 20-true-range window — the same two rows are 1,337 /
//	67.3% and 386 / 19.4%, so the shape of the trade does not depend on which
//	of the two window counts is used.)
//
// and what that buys, from the same de-gapping counterfactual, ATR(14),
// |raw/cf - 1|:
//
//	age band     n    median     p90     p99     max
//	15..19      34     0.89%   4.78%  12.50%  12.96%
//	20..24      47     0.63%   5.06%   7.16%   7.49%
//	25..29      92     0.40%   2.77%   5.10%   7.77%
//	30..39     231     0.23%   1.79%   3.66%   4.43%
//	40..62     588     0.07%   0.72%   2.28%   5.43%
//	>= 63      386     0.00%   0.03%   0.38%   0.59%
//
// The damage is concentrated in ~173 symbols under age 30 (8.7% of the
// universe). Half the universe is refused in order to pull a MEDIAN 0.07% error
// out of the 588-symbol 40..62 band, which is not a trade worth making. And the
// error that remains points the safe way for EP-3's 1-4 week horizon: an
// inflated true range makes the ATR stop too WIDE, i.e. an early exit or a
// smaller position — not a fabricated entry. That is a different order of
// failure from the one this file opens with (a mechanical gap MANUFACTURING a
// pullback signal), and it is the manufactured signal, not the wide stop, that
// earns a gate.
//
// A caller who does want a threshold anyway has the formula:
//
//	age >= ceil(ln(tolerance) / ln((N-1)/N))
//
// which at a 1% tolerance is 63 for ATR(14) and RSI(14), 90 for ATR(20), and 12
// (K) / 19 (D) for KDJ at the defaults. The threshold belongs to the consumer's
// error budget — the table above is the price list — so no default is offered
// here for copying.
//
// Pass the FULL series. Do NOT pre-slice — see ScanAdjustments.
//
// WHAT A true IS WORTH.
//
// The one shape that answers true with no event evidence at all is status OK
// with an empty Events list: an adjusted series demonstrably exists and nothing
// in it adjusted, so no trailing window of it can straddle a basis change.
//
// EVERY true this function returns — that shape included — inherits
// AdjustmentOK's upstream-lag blind spot: an ex-dividend the source has not
// restated yet is invisible whether or not older events exist, so a
// corporate-action feed could contradict any of them, not merely the
// empty-Events one (this comment used to claim otherwise). And one false-clean
// path never needed a feed to expose: before the window-usability guard below, a
// window whose bars were ALL unpriced scanned OK with zero events and came back
// true — 30 bars where only the oldest 10 carry prices returned clean for
// barsRead 20, on 20 bars of nothing. scan.SkippedBars alone disproved it.
//
// That guard is deliberately the STRICT form — ANY unusable bar inside the
// trailing barsRead bars answers false — rather than the looser "enough usable
// bars in the window". Two reasons. An adjustment landing entirely inside a run
// of unpriced bars is invisible to the scan (see AdjustmentScan.SkippedBars), so
// such a bar can never be vouched for. And the caller's indicator reads the same
// SLICE POSITIONS being certified, where a Close of 0 is not a bar it skips but
// a zero it averages in. The guard is window-scoped on purpose: it re-derives
// usability over the trailing barsRead bars instead of reading the whole-series
// scan.SkippedBars, so a hole OUTSIDE the window does not disqualify a window
// that is genuinely complete. No cached symbol has a single unusable bar today
// (0 of 1,987), so this costs nothing now and exists for the day a feed ships a
// hole.
//
// The test is age >= barsRead, i.e. dirty whenever the event bar itself falls
// inside the window. That is deliberately ONE BAR more conservative than strict
// contamination: an average of barsRead closes only mixes two bases when the
// window also contains a PRE-event bar (age <= barsRead-2), because the event bar
// and everything newer already share the new basis. The extra bar costs 4 symbols
// at barsRead 20 and 25 at barsRead 60 in the cache, and in exchange the answer
// can never under-report for a calculation that reads barsRead bars — which is
// the direction this repo prefers to be wrong in. The parameter is named barsRead
// rather than n or period for the same reason: a caller who has to state a bar
// count cannot pass 20 for a calculation that reads 21 bars and believe the
// window was checked — and, for the recursive indicators above, is forced to
// notice that no honest bar count exists.
func WindowIsAdjustmentClean(candles []Candle, barsRead int) bool {
	if barsRead <= 0 || barsRead > len(candles) {
		// Nothing to vouch for, or a window longer than the evidence. Either way
		// the honest answer is "not shown to be clean".
		return false
	}
	for _, c := range candles[len(candles)-barsRead:] {
		// A bar with no price cannot be shown to be free of an adjustment: the
		// scan cannot see a step that lands inside a skipped run. MISSING ≠ ZERO
		// applies to the window, not only to the ratio.
		if !adjBarUsable(c) {
			return false
		}
	}
	scan := ScanAdjustments(candles)
	if scan.Status != AdjustmentOK {
		// INSUFFICIENT_BARS and NO_ADJUSTED_SERIES are both "we cannot tell".
		return false
	}
	if len(scan.Events) == 0 {
		return true
	}
	age := len(candles) - 1 - scan.Events[len(scan.Events)-1].Index
	return age >= barsRead
}
