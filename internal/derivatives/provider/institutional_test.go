package provider

import "testing"

// FLOW and POSITION both come out of ONE response, and they are different numbers.
//
// The existing market provider reads only the OpenInterest(*) half of these bytes. §3 exists
// because a single "net" field carrying whichever the caller assumed is unrecoverable after
// the fact: 外資 can sell lots on a day their net long position rises, because the sale closed
// shorts.
func TestParseInstitutionalFuturesCarriesBothSemantics(t *testing.T) {
	got, err := ParseInstitutionalFutures(readFixture(t, "institutional_futures_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.TradingDate != "2026-09-04" {
		t.Fatalf("trading date %q, want 2026-09-04 (converted from the feed's 20260904)", got.TradingDate)
	}
	if got.Report.Status() != "AVAILABLE" {
		t.Fatalf("status %s, rejected %v", got.Report.Status(), got.Report.Rejected)
	}
	if len(got.Report.UnknownTokens) != 0 {
		t.Fatalf("unknown tokens in a clean response: %v", got.Report.UnknownTokens)
	}
	// 9 source rows x {FLOW,POSITION} x {LONG,SHORT,NET}
	if got.Report.RowsAccepted != 9 || len(got.Rows) != 54 {
		t.Fatalf("accepted %d source rows -> %d observations, want 9 -> 54",
			got.Report.RowsAccepted, len(got.Rows))
	}

	// The three institutions, all present, all under the exchange's own labels. R15 never
	// renames them and never calls the remainder 散戶.
	for _, inst := range []string{InstitutionDealer, InstitutionTrust, InstitutionForeign} {
		if find(got.Rows, "臺股期貨", inst, SemanticsPosition, SideNet) == nil {
			t.Errorf("no 臺股期貨 POSITION/NET row for %s", inst)
		}
	}

	// 自營商 / 臺股期貨 on 2026-09-04, straight off the feed:
	//   TradingVolume  long 2970  short 4826  net -1856     <- FLOW
	//   OpenInterest   long 3338  short 4035  net  -697     <- POSITION
	// Two different nets on one row. Either one alone would be a defensible "自營商淨額"
	// and they disagree on both magnitude and meaning.
	flowNet := find(got.Rows, "臺股期貨", InstitutionDealer, SemanticsFlow, SideNet)
	posNet := find(got.Rows, "臺股期貨", InstitutionDealer, SemanticsPosition, SideNet)
	if flowNet == nil || posNet == nil {
		t.Fatal("自營商 rows missing")
	}
	if *flowNet.Lots != -1856 {
		t.Errorf("FLOW net = %v, want -1856", *flowNet.Lots)
	}
	if *posNet.Lots != -697 {
		t.Errorf("POSITION net = %v, want -697", *posNet.Lots)
	}
	if *flowNet.Lots == *posNet.Lots {
		t.Error("the fixture no longer distinguishes FLOW from POSITION")
	}
	if *flowNet.ValueThousands != -17295069 {
		t.Errorf("FLOW net value = %v, want -17295069", *flowNet.ValueThousands)
	}

	// NET is the feed's OWN column, never long - short. Deriving it would paper over a
	// column-layout change that should surface as a mismatch. On this row the arithmetic
	// happens to agree, and that is exactly the point: the test asserts the source, so the
	// day it stops agreeing the parser is not what changed.
	flowLong := find(got.Rows, "臺股期貨", InstitutionDealer, SemanticsFlow, SideLong)
	flowShort := find(got.Rows, "臺股期貨", InstitutionDealer, SemanticsFlow, SideShort)
	if *flowLong.Lots != 2970 || *flowShort.Lots != 4826 {
		t.Errorf("long/short = %v/%v, want 2970/4826", *flowLong.Lots, *flowShort.Lots)
	}

	// Futures have no call/put dimension, and it is a '' sentinel rather than a NULL:
	// SQLite treats every NULL as distinct in a UNIQUE index, so a nullable key column
	// silently disables the constraint for exactly the rows it covers.
	for _, r := range got.Rows {
		if r.CallPut != "" {
			t.Fatalf("futures row carries call_put %q", r.CallPut)
		}
	}
}

// The call/put split feed, chosen over the 15-row combined one because
// institutional_derivatives keys on call_put.
func TestParseInstitutionalCallsPutsNormalisesTheAsciiSpelling(t *testing.T) {
	got, err := ParseInstitutionalCallsPuts(readFixture(t, "institutional_callsputs_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Report.Status() != "AVAILABLE" || got.Report.RowsAccepted != 30 {
		t.Fatalf("status %s, accepted %d, rejected %v",
			got.Report.Status(), got.Report.RowsAccepted, got.Report.Rejected)
	}
	if len(got.Rows) != 180 { // 30 x 6
		t.Fatalf("%d observations, want 180", len(got.Rows))
	}

	// This feed says CALL / PUT in ASCII where every other options feed says 買權 / 賣權.
	// An earlier draft of §7.2 asserted the values are "never Call/Put" — false for exactly
	// the feed §2.1 selects.
	seen := map[string]int{}
	for _, r := range got.Rows {
		seen[r.CallPut]++
	}
	if seen[CallSide] != 90 || seen[PutSide] != 90 || len(seen) != 2 {
		t.Fatalf("call/put census %v, want 90 CALL and 90 PUT only", seen)
	}

	// 臺指選擇權 / 自營商 / CALL, straight off the feed:
	//   TradingVolume net -6786   OpenInterest net 6652
	// Opposite signs on one row: the desk sold call volume while its net long call position
	// stayed positive. Collapsing them into one "net" would report a number that is true of
	// neither.
	flow := find(got.Rows, "臺指選擇權", InstitutionDealer, SemanticsFlow, SideNet)
	pos := find(got.Rows, "臺指選擇權", InstitutionDealer, SemanticsPosition, SideNet)
	if flow == nil || pos == nil {
		t.Fatal("臺指選擇權 自營商 rows missing")
	}
	// find() ignores call/put, so pin the pair explicitly.
	flow = findCP(got.Rows, "臺指選擇權", InstitutionDealer, CallSide, SemanticsFlow, SideNet)
	pos = findCP(got.Rows, "臺指選擇權", InstitutionDealer, CallSide, SemanticsPosition, SideNet)
	if *flow.Lots != -6786 || *pos.Lots != 6652 {
		t.Fatalf("FLOW/POSITION net = %v/%v, want -6786/6652", *flow.Lots, *pos.Lots)
	}

	// 投信 CALL FLOW long is a real "0" — an observed zero, present, not absent.
	trust := findCP(got.Rows, "臺指選擇權", InstitutionTrust, CallSide, SemanticsFlow, SideLong)
	if trust == nil || trust.Lots == nil {
		t.Fatal("投信 CALL FLOW LONG must be PRESENT")
	}
	if *trust.Lots != 0 {
		t.Fatalf("投信 CALL FLOW LONG = %v, want an observed 0", *trust.Lots)
	}
}

func find(rows []InstitutionalRow, product, inst, semantics, side string) *InstitutionalRow {
	for i := range rows {
		r := &rows[i]
		if r.Product == product && r.Institution == inst &&
			r.Semantics == semantics && r.Side == side {
			return r
		}
	}
	return nil
}

func findCP(rows []InstitutionalRow, product, inst, cp, semantics, side string) *InstitutionalRow {
	for i := range rows {
		r := &rows[i]
		if r.Product == product && r.Institution == inst && r.CallPut == cp &&
			r.Semantics == semantics && r.Side == side {
			return r
		}
	}
	return nil
}
