package provider

import (
	"fmt"
	"strings"
	"time"
)

// expiry_kind (§3.1).
const (
	ExpiryMonthly     = "MONTHLY"
	ExpiryWeekly      = "WEEKLY"
	ExpiryCombination = "COMBINATION"
	ExpiryUnresolved  = "UNRESOLVED"
)

// ClassifyExpiry decides what kind of series an expiry CODE names.
//
// From the code, and only ever from the code. Not from a settlement date: TAIFEX defers a
// settlement to the next trading day when the third Wednesday is a holiday, so 202602 settled
// on a Monday while remaining a monthly, and a date-shaped rule would have called it weekly.
// Not from missing Delta data either: DailyOptionsDelta omits the expiry settling today, so a
// rule that demoted an absent contract to UNRESOLVED would have demoted 202609F1 — the single
// largest row in the file, 126,594 lots — and then "UNRESOLVED is counted in every total"
// would have put it back into open interest, landing on 223,014 against an official 96,420.
//
// The rules, in precedence order:
//
//	contains "/"          -> COMBINATION. Checked FIRST, because "202609/202610" is
//	                         YYYYMM-prefixed and the prefix rule below would happily call it
//	                         WEEKLY. It is neither: it is one spread order across two
//	                         expiries, 271 rows of DailyMarketReportFut, and it is excluded
//	                         from every per-series aggregate.
//	bare YYYYMM           -> MONTHLY
//	YYYYMM + any suffix   -> WEEKLY. W2, F1, F2, F3 today; an unseen suffix is a weekly too,
//	                         and that is deliberate. An earlier rule classified F1/F2/F3 as
//	                         UNCLASSIFIED and excluded them, which is wrong in the most
//	                         dangerous available way: it still produces a number, and the
//	                         status stays AVAILABLE. Against 2026-09-04 it gave PutOI 46,852
//	                         where the exchange said 48,162.
//	anything else         -> UNRESOLVED, and UNRESOLVED is COUNTED in every total, never
//	                         silently dropped.
func ClassifyExpiry(code string) string {
	c := strings.TrimSpace(code)
	if strings.Contains(c, "/") {
		return ExpiryCombination
	}
	if len(c) < 6 || !isSixDigits(c[:6]) {
		return ExpiryUnresolved
	}
	if len(c) == 6 {
		return ExpiryMonthly
	}
	return ExpiryWeekly
}

// isSixDigits is the whole of the "YYYYMM" test, and stops there on purpose.
//
// It does NOT check that the month is 01-12 or that the year is plausible, which means it
// calls 666666 and 999912 MONTHLY. Those are live values — sentinels in
// OpenInterestOfLargeTradersFutures/Options' SettlementMonth column, where 999912 is the
// all-contracts total and summing it with the real months would count the market roughly
// three times.
//
// Tightening this function is the wrong fix and the spec says so: SettlementMonth is never an
// expiry_kind input, that feed is out of v1's fetch scope entirely, and a validity check here
// would create a second, weaker line of defence that invites someone to rely on it. The three
// feeds that ARE classifier inputs — DailyMarketReportOpt, DailyMarketReportFut,
// DailyOptionsDelta — carry no such token.
func isSixDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ── session and call/put normalisation ────────────────────────────────────────────────

// NormalizeSession maps the feed's own session label to the stored vocabulary.
//
// The feed's label, never the clock at fetch time. The exchange's convention is the opposite
// of the intuitive one — the 盤後 rows printed in day D's report were traded the EVENING
// BEFORE D — and grouping by the feed's field means R15 never has to reason about which
// calendar day a night session spans.
func NormalizeSession(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case "一般":
		return SessionDay, nil
	case "盤後":
		return SessionAfterHours, nil
	default:
		return "", fmt.Errorf("unrecognised TradingSession %q", s)
	}
}

// NormalizeCallPut maps every spelling the exchange uses to CALL / PUT.
//
// An unrecognised value is an error, never a silent third category: a mis-mapped side inverts
// a put/call ratio without changing its magnitude, which no reconciliation gate can see.
//
// Both spellings are live and neither is optional (§7.2): DailyMarketReportOpt,
// SettledPositionsIndexOptions and DailyOptionsDelta say 買權/賣權, while
// MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate — the feed §2.1 selects
// for M5 — says CALL/PUT.
func NormalizeCallPut(s string) (string, error) {
	switch t := strings.TrimSpace(s); {
	case t == "買權" || strings.EqualFold(t, "CALL"):
		return CallSide, nil
	case t == "賣權" || strings.EqualFold(t, "PUT"):
		return PutSide, nil
	default:
		return "", fmt.Errorf("unrecognised CallPut %q", s)
	}
}

// isoDate converts the feed's native YYYYMMDD to the YYYY-MM-DD every stored date uses.
//
// Not cosmetic. derivative_snapshots.trading_date is compared as a BARE STRING against an
// as-of date, and "20260904" does not order together with "2026-09-04", so a decoder emitting
// the native form would make every point-in-time read answer about the wrong session.
// PutSnapshot rejects the native form outright; this is where the conversion happens.
func isoDate(s string) (string, error) {
	t, err := time.Parse("20060102", strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("not a YYYYMMDD date: %q", s)
	}
	return t.Format("2006-01-02"), nil
}
