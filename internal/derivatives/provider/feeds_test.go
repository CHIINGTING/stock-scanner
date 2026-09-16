package provider

import "testing"

// DailyMarketReportFut is the feed with the awkward characteristics: a column literally named
// "%", no Close at all, 271 spread codes, and 一般 rows carrying "-" open interest.
func TestParseFuturesDaily(t *testing.T) {
	got := mustParseFuturesDaily(t, "futures_daily_20260904.json")
	if got.Report.Status() != "AVAILABLE" {
		t.Fatalf("status %s: %v", got.Report.Status(), got.Report.Rejected)
	}
	if got.TradingDate != "2026-09-04" {
		t.Fatalf("trading date %q", got.TradingDate)
	}
	if len(got.Report.UnknownTokens) != 0 {
		t.Fatalf("unknown tokens in a real response: %v — every non-numeric token this feed "+
			"carries is a KNOWN absence", got.Report.UnknownTokens)
	}

	var realPct, absentPct, zeroPct, nullSettle, dayNoOI, sessions int
	seenSession := map[string]bool{}
	for _, r := range got.Rows {
		seenSession[r.Session] = true
		switch {
		case r.ChangePct == nil:
			absentPct++
		case *r.ChangePct == 0:
			zeroPct++
		default:
			realPct++
		}
		if r.Settlement == nil {
			nullSettle++
		}
		if r.Session == SessionDay && r.OI == nil {
			dayNoOI++
		}
	}
	sessions = len(seenSession)
	if sessions != 2 {
		t.Fatalf("fixture carries %d sessions, want both 一般 and 盤後", sessions)
	}
	// Without the percent strip every one of these would be ABSENT and logged unknown.
	if realPct == 0 {
		t.Error(`no real "%" value survived — 727 observations would be silently dropped`)
	}
	if zeroPct == 0 {
		t.Error(`"0.00%" must be an observed zero, not an absence`)
	}
	if absentPct == 0 {
		t.Error(`"-" in the "%" column must still be ABSENT`)
	}
	if nullSettle == 0 {
		t.Error(`the fixture lost its "NULL" SettlementPrice rows`)
	}
	// 252 一般 rows carry OpenInterest "-" in the live response. An earlier draft explained
	// "-" with "the night rows carry no OI"; these rows are why that story is false.
	if dayNoOI == 0 {
		t.Error("the fixture lost its 一般 rows carrying OpenInterest \"-\"")
	}

	// This feed has Last and no Close. Field names are read from each feed, not assumed.
	var withLast int
	for _, r := range got.Rows {
		if r.Last != nil {
			withLast++
		}
	}
	if withLast == 0 {
		t.Error("no row decoded Last — DailyMarketReportFut has no Close column at all")
	}
}

// Margin exists, and finding it meant reading the 135-endpoint catalogue rather than guessing
// URLs: an earlier draft probed a dozen invented names, collected HTTP 302s, and concluded
// there was no official source. M7 would then have hand-maintained a table with no staleness
// rule at all.
func TestParseMargin(t *testing.T) {
	got, err := ParseMargin(readFixture(t, "margin_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Report.Status() != "AVAILABLE" || len(got.Rows) != 31 {
		t.Fatalf("status %s with %d rows: %v", got.Report.Status(), len(got.Rows), got.Report.Rejected)
	}
	var tx *MarginRow
	for i := range got.Rows {
		if got.Rows[i].Product == "臺股期貨" {
			tx = &got.Rows[i]
		}
	}
	if tx == nil {
		t.Fatal("no 臺股期貨 margin row")
	}
	if *tx.ClearingMargin != 519000 || *tx.MaintenanceMargin != 538000 || *tx.InitialMargin != 701000 {
		t.Errorf("臺股期貨 margins %v/%v/%v, want 519000/538000/701000",
			*tx.ClearingMargin, *tx.MaintenanceMargin, *tx.InitialMargin)
	}
	// The exchange's EFFECTIVE date, converted but never validated against the requested
	// session: it changes only when margins change, so validating it would raise
	// ErrDateMismatch on every ordinary day and leave M7 permanently at margin_source NONE.
	if tx.EffectiveDate != "2026-09-04" {
		t.Errorf("effective date %q, want 2026-09-04", tx.EffectiveDate)
	}
}

// The settlement feed is not an optional input: without it, open interest silently includes a
// settled expiry and the status stays AVAILABLE — a 2.3x error on 2026-09-04.
func TestParseFinalSettlement(t *testing.T) {
	got, err := ParseFinalSettlement(readFixture(t, "final_settlement_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("%d rows — this feed returns the most recent settlement event, one row", len(got.Rows))
	}
	r := got.Rows[0]
	if r.Product != ProductTXO || r.ExpiryCode != "202609F1" || r.FinalSettlementDay != "2026-09-04" {
		t.Fatalf("got %s/%s on %s, want TXO/202609F1 on 2026-09-04",
			r.Product, r.ExpiryCode, r.FinalSettlementDay)
	}
	// Here ContractDeliveryMonth IS a series code, so classifying it is legitimate — unlike
	// in SettledPositionsIndexOptions, where the same series appears as a bare 202609.
	if r.ExpiryKind != ExpiryWeekly {
		t.Errorf("202609F1 = %s, want WEEKLY", r.ExpiryKind)
	}

	// The comparison is against the SESSION BEING AGGREGATED. Recomputing 2026-09-04 on or
	// after 2026-09-09 — TXO's next settlement — must not find a match against a settlement
	// snapshot for a different day.
	if got := SettlingExpiry(got.Rows, ProductTXO, "2026-09-03"); got != "" {
		t.Errorf("a non-settlement session resolved to %q — anchoring to anything but the "+
			"session being aggregated re-introduces the 2.31x error", got)
	}
	if got := SettlingExpiry([]SettlementRow{{
		Product: ProductTXO, ExpiryCode: "202609W2", FinalSettlementDay: "2026-09-09",
	}}, ProductTXO, "2026-09-04"); got != "" {
		t.Error("a LATER settlement snapshot must not be applied to an earlier session — " +
			"this is the same bug from the other side")
	}
}

// Absence from DailyOptionsDelta means one thing only: no Delta is available for that
// contract. It never makes a contract UNRESOLVED, and it never affects open interest.
func TestParseOptionsDelta(t *testing.T) {
	got := mustParseDelta(t, "options_delta_20260904.json")
	if got.Report.Status() != "AVAILABLE" {
		t.Fatalf("status %s: %v", got.Report.Status(), got.Report.Rejected)
	}
	if len(got.Rows) == 0 {
		t.Fatal("no rows")
	}
	// ContractSettlementDay is a FUTURE date, per row, and is never validated against the
	// session: the feed carries no observation date at all, so its snapshot is ASSIGNED.
	for _, r := range got.Rows {
		if r.ContractSettlementDay <= "2026-09-04" {
			t.Fatalf("%s %s settles %s — the live feed carries only future settlement days",
				r.Product, r.ExpiryCode, r.ContractSettlementDay)
		}
	}
	if got.HasExpiry(ProductTXO, "202609F1") {
		t.Error("the settling expiry must be absent from this feed — that is the live " +
			"characteristic the fixture exists to preserve")
	}
	var withDelta int
	for _, r := range got.Rows {
		if r.Delta != nil {
			withDelta++
		}
	}
	if withDelta == 0 {
		t.Error("no delta decoded")
	}
}

// Both spellings of the call/put dimension, in the same test, because they are in the same
// layer: 買權/賣權 here, CALL/PUT in the institutional feed.
func TestParseSettledPositions(t *testing.T) {
	got, err := ParseSettledPositions(readFixture(t, "settled_positions_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Rows) != 2 || got.Report.Status() != "AVAILABLE" {
		t.Fatalf("%d rows, status %s: %v", len(got.Rows), got.Report.Status(), got.Report.Rejected)
	}
	sides := map[string]bool{}
	for _, r := range got.Rows {
		sides[r.CallPut] = true
		// The delivery month is NOT classified here, deliberately: applied to this feed
		// the rule would call 202609 MONTHLY while DailyMarketReportOpt calls the same
		// series (202609F1) WEEKLY — one contract, two kinds.
		if r.DeliveryMonth != "202609" {
			t.Errorf("delivery month %q, want 202609", r.DeliveryMonth)
		}
	}
	if !sides[CallSide] || !sides[PutSide] {
		t.Errorf("call/put sides %v, want both normalised from 買權/賣權", sides)
	}
}
