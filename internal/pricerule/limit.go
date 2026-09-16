package pricerule

import "strings"

// LimitRule says which daily price-limit regime a security trades under.
type LimitRule string

const (
	// LimitRule10Pct — the code's SHAPE is compatible with the ±10% daily band: four digits, not
	// in the "00" block, i.e. the shape TWSE and TPEX use for ordinary common shares.
	//
	// Read that literally. PCT_10 is a statement about the identifier, not about today. It does
	// NOT assert that ±10% of the previous close is the band in force for this security on this
	// date, because nothing in this package can know that — there is no calendar here, no
	// corporate-action feed and no listing history. See ClassifyLimitRule for what the shape does
	// and does not imply, and LimitUpPrice for the two things the CALLER must guarantee before
	// the returned number means anything.
	LimitRule10Pct LimitRule = "PCT_10"
	// LimitRuleUnknown — the regime could not be established from the information available.
	// No clamping is permitted against this value; see ClassifyLimitRule.
	LimitRuleUnknown LimitRule = "UNKNOWN"
)

// ClassifyLimitRule decides from the SHAPE OF THE CODE whether ±10% is even applicable.
//
// From the code, and only ever from the code — the same approach R15-M1 landed on for expiry
// classification (see internal/derivatives/provider.ClassifyExpiry, where a plausible
// date-shaped rule and a plausible missing-data rule were both rejected on evidence before the
// code-shape rule survived). The reason is the same in both places: the shape of an identifier
// is a stated convention, whereas anything inferred from a value is an inference that fails
// silently, still returning a number.
//
// The rules, in precedence order:
//
//	4 digits, not starting "00"  -> PCT_10. The code SHAPE is compatible with ±10%. This is not
//	                                a claim that ±10% applies today; see below.
//	starts "00"                  -> UNKNOWN. This is the honest answer, not a cop-out; see
//	                                below.
//	anything else                -> UNKNOWN. 6-digit warrants (which have their own limit
//	                                mechanics tied to the underlying, not ±10% of their own
//	                                previous close), codes containing letters, wrong lengths,
//	                                and the empty string.
//
// WHAT PCT_10 DOES NOT SAY. A four-digit non-"00" code is compatible with ±10%; several things
// that this function cannot see override it, and in each case a number computed from ±10% would
// be a fabrication with no error attached:
//
//   - Ex-dividend, ex-rights and capital-reduction days. The band is ±10% of the ADJUSTED
//     REFERENCE PRICE (除權息參考價 / 減資換發參考價), not of the raw previous close. This is the
//     high-frequency case, not an exotic one: it happens to hundreds of ordinary four-digit
//     shares every July–September. Feeding a raw cached prevClose in on such a day yields a
//     confident, plausible, wrong ceiling. Handling it is the caller's job (see LimitUpPrice).
//   - The first five trading days of a new listing (TWSE/TPEX) and of a security resuming
//     trading after suspension: no daily price limit at all. A four-digit code plus ±10%
//     invents a ceiling where the market has none.
//   - 興櫃 (emerging-stock board) securities carry four-digit codes and have NO daily price
//     limit whatsoever. That board is not reachable through this repo today — internal/
//     stockcatalog only holds TWSE and TPEX — which makes "the input universe is TWSE/TPEX" an
//     IMPLICIT PREMISE of the PCT_10 verdict rather than something the function checks. Written
//     down here because an implicit premise that nobody wrote down is how the next widening of
//     the catalogue silently fabricates prices.
//
// Why "00" is UNKNOWN rather than PCT_10. The "00" block is ETFs, and the ±10% band does not
// uniformly apply to them: an ETF tracking foreign constituents (原型國外成分 ETF) has NO
// daily price limit at all, while one tracking domestic constituents does have ±10%. Both live
// in the same "00" block — 0050 is domestic, 00646 is not — and the CODE CANNOT TELL THEM
// APART. Guessing PCT_10 would fabricate a ceiling for an instrument that has none, which in
// an entry-planning context means quietly refusing to chase a price the market is perfectly
// willing to trade at, with no error anywhere.
//
// The gap is in the repo, not in the market: internal/stockcatalog.Stock carries Code, Name
// and Market (TWSE/TPEX) and nothing that says what KIND of instrument it is — no ETF flag, no
// leverage/inverse flag, no constituent domicile. Until some authorized source states the
// instrument class per code, UNKNOWN is the only claim this function is entitled to make.
// Widening PCT_10 to cover "00" codes is not a fix; adding the missing classification is.
func ClassifyLimitRule(code string) LimitRule {
	c := strings.TrimSpace(code)
	// isDigits first, then the length, so that the "not digits at all" verdict is reached for
	// the empty string too rather than short-circuited away by the length test.
	if !isDigits(c) || len(c) != 4 {
		return LimitRuleUnknown
	}
	if strings.HasPrefix(c, "00") {
		return LimitRuleUnknown
	}
	return LimitRule10Pct
}

// isDigits reports whether s is non-empty and every byte is an ASCII digit.
//
// Byte-wise on purpose: a code containing a full-width digit or any other multi-byte rune is
// not a code this package recognises, and it must fall through to UNKNOWN rather than be
// normalised into something that looks recognisable.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// The two ends of the band, written once. limitUpFactor is also the WIDER of the two products,
// which is why the pair check in limitPrices can be stated in terms of it.
const (
	limitUpFactor   = 1.10
	limitDownFactor = 0.90
)

// LimitUpPrice returns today's highest legal price: prevClose × 1.10, aligned DOWN to the tick.
//
// Down, because the aligned price must stay inside the band. prevClose 1245 gives 1369.5,
// which is not on the 5.00 grid that applies above 1000; the answer is 1365, not 1370, because
// 1370 would be above the +10% the rules allow.
//
// THE CALLER'S CONTRACT, and it is not optional. Two things must hold, and this package can
// verify neither of them — it is PURE, so it has no calendar, no corporate-action source and no
// listing history:
//
//  1. prevClose must be the ADJUSTED REFERENCE PRICE for today, not the raw previous close, on
//     any day the security goes ex-dividend, ex-rights, or trades after a capital reduction.
//     The exchange computes the band from 除權息參考價 / 減資換發參考價 on those days. Passing a
//     raw cached close instead does not produce an error, it produces a wrong ceiling that
//     looks exactly like a right one — and it will happen to hundreds of ordinary four-digit
//     shares every dividend season.
//  2. Today must not be within the first five trading days of a new TWSE/TPEX listing, nor of a
//     resumption after suspension. There is no daily limit in that window, so there is no
//     ceiling to compute and any number returned here is an invention.
//
// A caller that cannot establish both must treat the limit prices as unavailable rather than
// call this with whatever price it happens to have. See also ClassifyLimitRule, which explains
// why a PCT_10 verdict is a statement about the code's shape and not about today.
//
// ok=false when rule is not LimitRule10Pct, when prevClose is not finite / not positive, or
// when either end of the band falls outside the alignable domain. It does NOT return a computed
// price alongside a warning flag for the caller to weigh up: an UNKNOWN regime has no ceiling
// this package knows of, so there is no number to return. MISSING ≠ ZERO.
func LimitUpPrice(prevClose float64, rule LimitRule) (float64, bool) {
	up, _, ok := limitPrices(prevClose, rule)
	return up, ok
}

// LimitDownPrice returns today's lowest legal price: prevClose × 0.90, aligned UP to the tick.
//
// Up, for the mirror-image reason: prevClose 1245 gives 1120.5 and the answer is 1125, not
// 1120, because 1120 would be below the -10% the rules allow.
//
// Both directions therefore round INTO the band. The consequence is the property that makes
// these usable as a clamp: a clamped price is never more extreme than the regulation permits,
// only ever equal or less extreme. Stated exactly, because "exactly" is what a clamp needs:
// for every whole-cent prevClose the ceiling is <= the exact +10% and the floor is >= the exact
// -10%, compared against integer tenth-of-a-cent arithmetic that does not go through this
// package at all. That is asserted by a sweep over every whole-cent prevClose from 0.01 to
// 2000, not assumed.
//
// The related but WEAKER claim "floor < ceiling" is not universal and is not written here as
// though it were. For prevClose in [0.01, 0.09] the ±10% band is narrower than the 0.01 tick
// that applies there, both ends round onto prevClose itself, and floor == ceiling. That is
// arithmetically correct rather than fabricated — a band of ±0.9 cents cannot contain a
// different legal price — and it is pinned by test. Strict floor < ceiling holds from prevClose
// 0.10 upwards, which is every price any Taiwan board actually quotes.
//
// The caller's contract from LimitUpPrice applies here identically.
//
// ok=false on the same terms as LimitUpPrice — and on the SAME INPUTS: the two functions
// succeed and fail together, never one without the other.
func LimitDownPrice(prevClose float64, rule LimitRule) (float64, bool) {
	_, down, ok := limitPrices(prevClose, rule)
	return down, ok
}

// limitPrices is the shared body of the two limit functions, and it computes BOTH ends even
// when the caller asked for one.
//
// That is deliberate and it fixes a real inconsistency. maxAlignablePrice applies to the number
// being aligned, and the two ends are different numbers (× 1.10 and × 0.90), so a per-call
// domain check let one end succeed while the other failed: a security with a limit-down price
// and no limit-up price, which is not a state the market has. The same asymmetry appeared at
// the bottom, where a sub-cent prevClose rounds its ceiling off the bottom of the grid while
// its floor survives. Both ends now stand or fall together, so "these two prices bracket the
// legal range" is true whenever ok is true.
//
// The tick is taken from each LIMIT price, not from prevClose, because that is the price being
// aligned and the two can sit in different bands: prevClose 990 (tick 1) has a limit up of
// 1089, which is in the 5.00 band and aligns to 1085.
func limitPrices(prevClose float64, rule LimitRule) (up, down float64, ok bool) {
	if rule != LimitRule10Pct {
		return 0, 0, false
	}
	if !isFinitePositive(prevClose) {
		return 0, 0, false
	}

	up, upOK := RoundToTick(prevClose*limitUpFactor, RoundDown)
	down, downOK := RoundToTick(prevClose*limitDownFactor, RoundUp)
	if !upOK || !downOK {
		return 0, 0, false
	}
	return up, down, true
}
