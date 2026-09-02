package r6backtest

import (
	"math"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dumprisk"
)

// ──────────────────────────────────────────────────────────────────────────────
// Next-Day Dump Risk — measured on the bar AFTER the signal, from the signal close.
//
// # Why this study does not use the trade engine
//
// Every setup in this package enters at the NEXT OPEN and holds for 5–60 days under a stop.
// That shape cannot answer this question, and would quietly answer a different one: entering
// at the T+1 open prices the overnight gap INTO the entry, which is precisely the risk being
// studied. A holder who bought during the ramp on day T owns the gap. So the reference price
// here is the SIGNAL CLOSE, and the horizon is T+1 / T+2 rather than 5/10/20/60 days.
//
// The unit of observation is one stock-bar. Unlike the FX study (one row per trading date)
// the conditions here are genuinely per-stock, but the observations are NOT independent —
// on a strong day hundreds of stocks trigger together. Every result therefore reports
// unique_dates and unique_stocks beside the raw count, and the interpretation must lean on
// the smaller number.
//
// # Causality
//
// Features at bar i read bars <= i only (dumprisk.Compute takes one bar plus trailing
// context). Outcomes read bars i+1 and i+2 only. The two never meet: an outcome is never an
// input to a bucket, and a bucket is never assigned using a bar the scanner could not have
// seen at the close of day i. TestDumpRiskIsCausal holds this.
// ──────────────────────────────────────────────────────────────────────────────

// DumpObs is one stock-bar: what was observable at the close of T, and what the next two
// sessions did to a holder who was long into that close.
type DumpObs struct {
	Symbol string
	Name   string
	Date   time.Time

	Feat dumprisk.Features

	// ── Stock-level context, carried so selection bias is measurable rather than assumed ──
	// A bucket that quietly selects cheap, thin, volatile names would show a downside edge
	// that belongs to those attributes and not to the pattern. These three travel with every
	// observation so each bucket can publish its own profile beside its returns.
	Price       float64 // the signal close, in NTD — the price-level axis
	AvgVol20    float64 // trailing 20-session mean volume, in 股 — the liquidity axis
	ATRPct      float64 // ATR(14) as a % of the close — the volatility axis
	TurnoverNTD float64 // close x volume — a market-participation proxy that needs no float

	// ── Outcomes, all measured from the SIGNAL CLOSE (day T) ─────────────────
	// T1OpenRet is the overnight gap: the part of the risk a holder cannot trade out of.
	T1OpenRet  float64 // (Open[T+1]/Close[T] - 1) * 100
	T1CloseRet float64 // (Close[T+1]/Close[T] - 1) * 100
	T1LowRet   float64 // (Low[T+1]/Close[T] - 1) * 100 — worst point reachable on T+1
	T1HighRet  float64 // (High[T+1]/Close[T] - 1) * 100 — the other tail, for symmetry

	// T2 fields are NaN when a T+2 bar does not exist (end of data, halt, delisting).
	T2CloseRet float64
	T2LowRet   float64
	HasT2      bool

	// MAE in ATR multiples. A −3% day is a routine wiggle on a volatile small-cap and a
	// serious break on a quiet large-cap; the percentage columns cannot tell them apart, so
	// the volatility-normalised excursion is carried alongside. NaN when ATR was unavailable.
	T1MAEATR float64 // (Low[T+1] - Close[T]) / ATR[T]
	T2MAEATR float64 // (min(Low[T+1],Low[T+2]) - Close[T]) / ATR[T]
	ATROK    bool
}

// GapDown reports whether T+1 opened below the signal close.
func (o DumpObs) GapDown() bool { return o.T1OpenRet < 0 }

// NegativeClose reports whether T+1 closed below the signal close.
func (o DumpObs) NegativeClose() bool { return o.T1CloseRet < 0 }

// dumpTrailingVolMA computes the trailing 20-session mean volume ending at bar i inclusive.
// The Stock's own VolMA20 slice is reused when populated; this exists so the study cannot
// silently disagree with the live scanner about what "20-day average volume" means.
func dumpVolMA20(s *Stock, i int) float64 {
	if i >= 0 && i < len(s.VolMA20) {
		return s.VolMA20[i]
	}
	return 0
}

// DayTradingLookup returns 當沖成交股數 for a stock on a date, and whether a snapshot covered
// that stock-day. It is an interface so the study runs with or without the 當沖 archive: a nil
// lookup means the DayTradingRatio column is simply absent, never zero.
type DayTradingLookup interface {
	DayTradeShares(symbol string, date time.Time) (shares float64, ok bool)
}

// CollectDumpObs walks every stock-bar in the universe and produces one observation per bar
// that has BOTH a computable feature set and at least a T+1 bar to measure against.
//
// Bars dropped, and why:
//   - i < warmup            — ATR/VolMA20 not yet meaningful
//   - no bar at i+1         — the last bar of a series, a halt, or a delisting: the outcome
//     is unknowable, and assuming 0% would invent the very thing being measured
//   - candlestick-invalid   — a zero-range (limit-locked) or malformed bar carries no shape
func CollectDumpObs(u *Universe, warmup int, dt DayTradingLookup) []DumpObs {
	var out []DumpObs

	for _, s := range u.Stocks {
		n := len(s.Candles)
		// i+1 must exist for the primary outcome; T+2 is optional and flagged.
		for i := warmup; i+1 < n; i++ {
			cur := s.Candles[i]
			next := s.Candles[i+1]

			prevClose := 0.0
			if i >= 1 {
				prevClose = s.Candles[i-1].Close
			}
			atr := 0.0
			if i < len(s.ATR14) {
				atr = s.ATR14[i]
			}

			in := dumprisk.Input{
				Open:      cur.Open,
				High:      cur.High,
				Low:       cur.Low,
				Close:     cur.Close,
				Volume:    float64(cur.Volume),
				PrevClose: prevClose,
				VolMA20:   dumpVolMA20(s, i),
				ATR:       atr,
			}
			if dt != nil {
				if shares, ok := dt.DayTradeShares(s.Symbol, cur.Date); ok && cur.Volume > 0 {
					in.HasDayTrading = true
					in.DayTradeShares = shares
					in.TotalShares = float64(cur.Volume)
				}
			}

			f := dumprisk.Compute(in)
			if !f.Valid || !f.ChangeOK {
				continue
			}
			base := cur.Close
			if base <= 0 {
				continue
			}

			atrPct := math.NaN()
			if atr > 0 && base > 0 {
				atrPct = atr / base * 100
			}

			o := DumpObs{
				Symbol:      s.Symbol,
				Name:        s.Name,
				Date:        cur.Date,
				Feat:        f,
				Price:       base,
				AvgVol20:    dumpVolMA20(s, i),
				ATRPct:      atrPct,
				TurnoverNTD: base * float64(cur.Volume),
				T1OpenRet:   pctFrom(base, next.Open),
				T1CloseRet:  pctFrom(base, next.Close),
				T1LowRet:    pctFrom(base, next.Low),
				T1HighRet:   pctFrom(base, next.High),
				T2CloseRet:  math.NaN(),
				T2LowRet:    math.NaN(),
				T1MAEATR:    math.NaN(),
				T2MAEATR:    math.NaN(),
			}

			worstLow := next.Low
			if i+2 < n {
				t2 := s.Candles[i+2]
				o.HasT2 = true
				o.T2CloseRet = pctFrom(base, t2.Close)
				o.T2LowRet = pctFrom(base, t2.Low)
				worstLow = math.Min(worstLow, t2.Low)
			}

			if atr > 0 && !math.IsNaN(atr) && !math.IsInf(atr, 0) {
				o.ATROK = true
				o.T1MAEATR = (next.Low - base) / atr
				if o.HasT2 {
					o.T2MAEATR = (worstLow - base) / atr
				}
			}

			out = append(out, o)
		}
	}
	return out
}

// pctFrom is the percentage change from base to v; NaN when either side is unusable, so a
// missing price never becomes a 0% move.
func pctFrom(base, v float64) float64 {
	if base <= 0 || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return math.NaN()
	}
	return (v/base - 1) * 100
}

// ──────────────────────────────────────────────────────────────────────────────
// Thresholds derived from the data, not chosen
// ──────────────────────────────────────────────────────────────────────────────

// DumpThresholds are the cut points the buckets use. They are COMPUTED from the observed
// distribution of the trigger population rather than written down in advance: the question
// "what counts as a weak close" has an empirical answer on this market and this period, and
// picking 0.3 by hand would be the first step toward tuning until the table looks good.
type DumpThresholds struct {
	// LargeGainPct is the shipped scanner band, NOT a fitted value — the study measures the
	// classification that actually runs. See scanner.DefaultPriceMoveThresholds.
	LargeGainPct float64
	// VolSpikeRatio is likewise the shipped volume-expansion band.
	VolSpikeRatio float64

	// WeakCloseCLV / StrongCloseCLV are the lower and upper terciles of CloseLocationValue
	// WITHIN the trigger population (large-gain, volume-spike bars) — the population the
	// buckets actually split, so the cuts land where that population's mass is.
	WeakCloseCLV   float64
	StrongCloseCLV float64
	// LongUpperShadowPct is the upper tercile of UpperShadowPct in the same population.
	LongUpperShadowPct float64
	// HighDayTradingRatio is the upper tercile of DayTradingRatio, computed only over
	// observations that HAVE day-trading data.
	HighDayTradingRatio float64
	DayTradingAvailable bool

	// SelloffMAEATR is the T+1 MAE-in-ATR level that the BASELINE population reaches only
	// SelloffPctile of the time. Defining "a selloff" as a fixed −3% would call a routine
	// session on a volatile stock a dump; this makes the label mean "worse than all but
	// SelloffPctile of ordinary days".
	SelloffMAEATR float64
	SelloffPctile float64
}

// DeriveDumpThresholds computes the cut points from the observations themselves.
//
// trigger is the population the buckets split (large gain + volume spike); baseline is every
// observation, used only for the selloff definition. Percentile cuts on an empty slice return
// NaN, and callers must treat a NaN threshold as "this dimension is unavailable" rather than
// comparing against it.
func DeriveDumpThresholds(all []DumpObs, largeGainPct, volSpikeRatio, selloffPctile float64) DumpThresholds {
	th := DumpThresholds{
		LargeGainPct:  largeGainPct,
		VolSpikeRatio: volSpikeRatio,
		SelloffPctile: selloffPctile,
	}

	var clv, upper, dtr, baselineMAE []float64
	for _, o := range all {
		if o.ATROK && !math.IsNaN(o.T1MAEATR) {
			baselineMAE = append(baselineMAE, o.T1MAEATR)
		}
		if !isTrigger(o, largeGainPct, volSpikeRatio) {
			continue
		}
		clv = append(clv, o.Feat.CloseLocationValue)
		upper = append(upper, o.Feat.UpperShadowPct)
		if o.Feat.DayTradingOK {
			dtr = append(dtr, o.Feat.DayTradingRatio)
		}
	}

	th.WeakCloseCLV = pctlOf(clv, 33.3)
	th.StrongCloseCLV = pctlOf(clv, 66.7)
	th.LongUpperShadowPct = pctlOf(upper, 66.7)
	if len(dtr) > 0 {
		th.HighDayTradingRatio = pctlOf(dtr, 66.7)
		th.DayTradingAvailable = true
	}
	// The selloff cut is a LOWER tail: MAE is negative, so the p-th percentile of the raw
	// values is the level only p% of ordinary days fall below.
	th.SelloffMAEATR = pctlOf(baselineMAE, selloffPctile)

	return th
}

func isTrigger(o DumpObs, largeGainPct, volSpikeRatio float64) bool {
	return o.Feat.ChangeOK && o.Feat.PriceChangePct >= largeGainPct &&
		o.Feat.VolumeOK && o.Feat.VolumeRatio >= volSpikeRatio
}

// pctlOf returns the p-th percentile (0–100) of xs, or NaN when xs is empty.
func pctlOf(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	if len(cp) == 1 {
		return cp[0]
	}
	pos := p / 100 * float64(len(cp)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return cp[lo]
	}
	frac := pos - float64(lo)
	return cp[lo]*(1-frac) + cp[hi]*frac
}
