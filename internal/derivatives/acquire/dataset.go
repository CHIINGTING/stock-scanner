package acquire

import "github.com/deep-huang/stock-scanner/internal/derivatives"

// The datasets R15 acquires, and the three things about each that decide how it is stored.
//
// This table is the single place those decisions live. Spreading them across the fetch, the
// parser and the writer is how one of them ends up disagreeing with the others — and two of
// the three are invisible when wrong: a feed archived under the wrong trading date, or a
// response stored as one snapshot when it holds two sessions, both produce a database that
// looks complete.

// DateSemantics says whether a feed's own date is an OBSERVATION date.
//
// Every one of these feeds carries a date field. Only some of them carry a TRADING date, and
// §3.1 records what happens when that distinction is missed: margin's `Date` is an EFFECTIVE
// date that changes only when margins change, so validating it against the session would raise
// ErrDateMismatch on every ordinary day and leave M7 permanently at margin_source: NONE.
type DateSemantics int

const (
	// DateFromFeed — the feed reports the trading day it describes, and it is validated
	// against the requested session (§2.2). A mismatch is an error, never today's data
	// wearing yesterday's label.
	DateFromFeed DateSemantics = iota
	// DateFromRequest — the feed carries no trading date, or carries a date that is not one:
	// a settlement day, a per-contract settlement day, a margin effective date. The target
	// session is used and the snapshot records date_source ASSIGNED, so a reader can always
	// tell an observed date from an assigned one.
	DateFromRequest
)

// Dataset is one acquirable feed.
type Dataset struct {
	// Name is the stable identity used in data health, errors and the archive path. It is
	// not the endpoint: an endpoint can be renamed, and a health history keyed on the URL
	// would then look like a new dataset appearing beside a dead one.
	Name string
	// Endpoint is the path under BaseURL.
	Endpoint string
	// Dates decides whether the feed's own date is trusted as the trading date.
	Dates DateSemantics
	// SplitSessions is true for the one response that carries both trading sessions.
	//
	// It exists because options_oi_by_strike is keyed without a session column (§10.2b): the
	// session lives on the SNAPSHOT, so a two-session response must become two snapshots or
	// half its rows collide on the UNIQUE index. See provider.SplitBySession.
	SplitSessions bool
	// Session is the session recorded when SplitSessions is false. COMBINED for every feed
	// with no session dimension — a sentinel, never NULL, because SQLite treats NULLs as
	// distinct in a UNIQUE index and a nullable key column silently disables idempotency for
	// exactly the rows it covers.
	Session string
	// ExpectedFreshness describes when this dataset should exist for a session, in the words
	// the data-health panel shows a reader. TAIFEX publishes these at different times, so a
	// 15:00 run legitimately sees some and not others — that is NOT_PUBLISHED, which is a
	// different claim from MISSING (§6.1).
	ExpectedFreshness string
}

// The v1 catalogue.
//
// Deliberately absent: OpenInterestOfLargeTradersFutures / …Options. §11.1 shows the net
// residual is Σ instShort − Σ instLong, in which the whole-market total cancels, so v1 has no
// consumer for them — and their only other candidate use is a market share whose calibre does
// not match the institutional feed (117,552 is 臺股期貨(TX+MTX/4), a composite; against the
// pure-TX 107,437 that is a 61% error with no official cross-check to catch it). They are also
// the only R15 endpoint served with a UTF-8 BOM, which Go's json.Unmarshal rejects outright.
// §11.3 keeps the row-selection rule for the day a total is genuinely needed.
var (
	InstitutionalFutures = Dataset{
		Name:     "institutional_futures",
		Endpoint: "MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate",
		Dates:    DateFromFeed,
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "published after the close, typically 15:00–16:00 Asia/Taipei; " +
			"absent before that is NOT_PUBLISHED",
	}
	InstitutionalCallsPuts = Dataset{
		Name:     "institutional_calls_puts",
		Endpoint: "MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate",
		Dates:    DateFromFeed,
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "published after the close, typically 15:00–16:00 Asia/Taipei; " +
			"absent before that is NOT_PUBLISHED",
	}
	OptionsByStrike = Dataset{
		Name:          "options_by_strike",
		Endpoint:      "DailyMarketReportOpt",
		Dates:         DateFromFeed,
		SplitSessions: true,
		ExpectedFreshness: "day rows after the 13:45 close; night rows only after the night " +
			"session settles, so a run before then sees DAY and NOT_PUBLISHED for AFTER_HOURS",
	}
	FuturesDaily = Dataset{
		Name:              "futures_daily",
		Endpoint:          "DailyMarketReportFut",
		Dates:             DateFromFeed,
		SplitSessions:     true,
		ExpectedFreshness: "as options_by_strike",
	}
	PutCallRatio = Dataset{
		Name:     "put_call_ratio",
		Endpoint: "PutCallRatio",
		Dates:    DateFromFeed,
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "carries ~23 sessions of history rather than one, so a gap here is " +
			"visible immediately rather than after a day",
	}
	Margin = Dataset{
		Name:     "margin",
		Endpoint: "IndexFuturesAndOptionsMargining",
		Dates:    DateFromRequest, // Date is an EFFECTIVE date — see DateSemantics
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "changes only when the exchange revises margins; an unchanged value " +
			"is current, not stale",
	}
	FinalSettlement = Dataset{
		Name:     "final_settlement",
		Endpoint: "FinalSettlementPriceIndexOptions",
		Dates:    DateFromRequest, // TheFinalSettlementDay is a settlement day, not a trading day
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "one row, the expiry settling that day; on a non-settlement day it " +
			"names an earlier expiry, which is how §3.1 tells the two apart",
	}
	SettledPositions = Dataset{
		Name:     "settled_positions",
		Endpoint: "SettledPositionsIndexOptions",
		Dates:    DateFromRequest,
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "settlement days only; this is the feed that names the same series " +
			"TXU/202609/臺指選擇權F1 where the others say TXO/202609F1 (§7.2)",
	}
	OptionsDelta = Dataset{
		Name:     "options_delta",
		Endpoint: "DailyOptionsDelta",
		Dates:    DateFromRequest, // ContractSettlementDay is a per-contract future date
		Session:  derivatives.SessionCombined,
		ExpectedFreshness: "carries a settlement day for every LIVE expiry, and omits the one " +
			"settling today — absence from it means nothing (§3.1)",
	}
)

// All is the acquisition order. Deterministic so a partial run fails the same way twice.
var All = []Dataset{
	OptionsByStrike,
	FuturesDaily,
	InstitutionalFutures,
	InstitutionalCallsPuts,
	PutCallRatio,
	Margin,
	FinalSettlement,
	SettledPositions,
	OptionsDelta,
}
