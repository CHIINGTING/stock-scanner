// Package numtoken holds the one numeric-token normaliser this repository has.
//
// It exists because two readers need the SAME textual cleanup and OPPOSITE policies about
// what a cleaned-but-unparsable token means, and a spec review (R15 §3.1) established that
// only the cleanup may be shared:
//
//   - internal/market/provider's parseAmount reads TWSE cash rows, where an empty cell
//     genuinely is a zero. It keeps ""/"-" -> 0 and unparsable -> error.
//   - internal/derivatives/provider reads TAIFEX derivatives rows, where "-" means "not
//     carried" and "0" means "a strike nobody holds". It maps ""/"-" -> ABSENT.
//
// Putting either policy inside the normaliser would push it onto the other reader: giving
// R15 the TWSE policy makes MISSING indistinguishable from ZERO across the whole derivatives
// layer, and giving TWSE the R15 policy silently changes a shipped behaviour that
// twse_test.go pins. So the normaliser makes NO decision about meaning; it only cleans.
//
// Two failures this prevents, both observed against live data:
//
//   - Thousands separators. The TAIFEX JSON feeds carry none, but the Big5 CSV backfill does.
//     Without the comma strip, "1,234" fails to parse while "999" succeeds — so only numbers
//     >= 1000 disappear, silently, on the historical path only.
//   - Trailing percent. DailyMarketReportFut's "%" column carries 727 real values ("9.98%")
//     next to 1,467 "-" and 56 "0.00%". Without the suffix strip, every real value decodes to
//     ABSENT and is logged as an unknown token: 783 lines of daily noise while 727
//     observations vanish.
package numtoken

import "strings"

// Normalize trims surrounding whitespace and removes thousands separators.
//
// That is the entire operation. It is deliberately not a parser and deliberately not a
// policy: "" and "-" come out of here as "" and "-", for the caller to interpret.
func Normalize(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
}

// NormalizePercent is Normalize plus one trailing '%'.
//
// Separate from Normalize rather than folded into it, because a bare '%' is meaningful in
// exactly the fields declared to be percentages; stripping it everywhere would make a token
// like "50%" parse as 50 on a lot-count column, where it should stay visible as unknown.
func NormalizePercent(s string) string {
	return strings.TrimSuffix(Normalize(s), "%")
}
