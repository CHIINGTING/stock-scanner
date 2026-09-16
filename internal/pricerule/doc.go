// Package pricerule is the price-grid arithmetic of the Taiwan equity market: the tiered tick
// size, alignment of an arbitrary number onto that grid, and the daily ±10% limit prices.
//
// It exists because the repo, until now, had no notion of a price that actually exists. Every
// derived price was produced by a round1() helper — math.Round(v*10)/10 — which for a stock
// quoted at 1245 happily prints 1245.3, a number no exchange will ever match. The scanner's
// limit-up detection is likewise a percentage proxy (>= 9.0% / >= 9.5% of the previous close),
// not a limit price. Anything a user is expected to type into an order box has to be a price
// the exchange can accept, and that is the whole job of this package.
//
// The governing rule, from which everything else follows:
//
//	This package states market mechanics. It decides nothing.
//
// Nothing here emits BUY / SELL / WATCH, nothing here scores, ranks or filters, and nothing
// here knows what an entry plan is. It answers "what is the smallest legal increment at this
// price?" and "what are today's legal extremes given yesterday's close?" — and refuses to
// answer when it cannot.
//
// Three properties are load-bearing and are asserted by tests rather than promised here:
//
//   - PURE. No clock, no randomness, no network, no database, no AI. The same inputs always
//     produce the same outputs, so a limit price computed in a report and the same limit price
//     recomputed in a backtest can never disagree.
//
//   - MISSING ≠ ZERO. Every function returns (value, ok) and an unusable input yields
//     ok=false with the value left at zero as a formality. There is no fallback tick and no
//     fallback limit rule; a caller that ignores ok gets 0.0, which is visibly wrong rather
//     than plausibly wrong.
//
//     With one asymmetry that callers must know about, because "visibly wrong" depends on the
//     context the 0 lands in. Used as a CEILING — a limit-up price, a maximum chase price — 0
//     is fail-safe: it refuses everything, and the worst outcome is a trade not taken. Used as
//     a FLOOR — a stop-loss level, a minimum acceptable sale price — the same 0 is
//     catastrophic: it permits everything, so a position with no stop looks like a position
//     with a stop at zero. Nothing in this package can tell which context it is being called
//     from, so the rule is on the caller: handle ok=false explicitly. `v, _ := LimitDownPrice(…)`
//     is not an abbreviation, it is a defect.
//
//   - NO NaN / Inf ESCAPES. Non-finite and non-positive inputs are rejected at the door, and
//     the grid arithmetic runs on int64 cents, so no non-finite value and no 1245.0000000000002
//     can leave this package.
//
// On floating point: prices are decimal, float64 is binary, and 0.05 and 0.5 are not binary
// friendly. math.Floor(price/tick)*tick is therefore not used anywhere in this package. Every
// tick in the Taiwan table is a whole number of cents, so alignment counts ticks as int64 and
// reassembles the answer from an integer number of cents. See RoundToTick.
package pricerule
