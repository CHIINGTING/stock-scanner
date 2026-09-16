package provider

import "testing"

// aggregate applies §3.1's asymmetric rule to a set of by-strike rows.
//
// It lives in the test, not in the package: M3 part A parses and normalises, and the
// published aggregate with its status and its reconciliation gate is M5's. What this function
// is for is proving here, at the parser boundary, that the rows the parser produces support
// the rule — that "-" did not become 0, that the night session survived, and that the expiry
// codes are intact enough to exclude one.
type optAgg struct{ putVol, callVol, putOI, callOI float64 }

func aggregate(rows []OptionStrikeRow, product, excludeExpiry string, sessions ...string) optAgg {
	allow := map[string]bool{}
	for _, s := range sessions {
		allow[s] = true
	}
	var a optAgg
	for _, r := range rows {
		if r.Product != product {
			continue // §3.1: "sum every row" means every TXO row
		}
		if len(allow) > 0 && !allow[r.Session] {
			continue
		}
		// Volume: EVERY row, both sessions, every expiry.
		if r.Volume != nil {
			if r.CallPut == CallSide {
				a.callVol += *r.Volume
			} else {
				a.putVol += *r.Volume
			}
		}
		// Open interest: every row EXCEPT the expiry settling on this session. Its lots
		// are still printed but are no longer live exposure.
		if r.ExpiryCode == excludeExpiry {
			continue
		}
		if r.OI != nil {
			if r.CallPut == CallSide {
				a.callOI += *r.OI
			} else {
				a.putOI += *r.OI
			}
		}
	}
	return a
}

// THE reconciliation of §3.1, against the committed fixture.
//
// The rule is asymmetric and each half matched the exchange exactly on the full 2026-09-04
// capture (see testdata/README.md):
//
//	volume = every TXO row, both sessions, every expiry     356,219 / 361,817  ✅
//	OI     = the same minus the expiry settling that day      48,162 /  48,258  ✅
//
// The trimmed fixture cannot carry the exchange's own totals — dropping 95% of the rows drops
// 95% of the lots — so it ships with a derived companion pinning the same two rules over the
// rows that are here. What makes the test worth running is the other half: the three RULES
// the spec rejected produce visibly different numbers on the trimmed rows too, and each one
// is asserted to fail.
func TestPutCallReconciliation(t *testing.T) {
	opt := mustParseOptionsByStrike(t, "options_by_strike_20260904.json")
	if opt.Report.Status() != "AVAILABLE" {
		t.Fatalf("fixture parses %s: %v", opt.Report.Status(), opt.Report.Rejected)
	}

	// Which expiry stops being live exposure at this session's close comes from the
	// exchange, compared against the SESSION BEING AGGREGATED — never against "today", and
	// never inferred from the code's shape.
	settle, err := ParseFinalSettlement(readFixture(t, "final_settlement_20260904.json"))
	if err != nil {
		t.Fatalf("settlement: %v", err)
	}
	settling := SettlingExpiry(settle.Rows, ProductTXO, opt.TradingDate)
	if settling != "202609F1" {
		t.Fatalf("settling TXO expiry on %s = %q, want 202609F1", opt.TradingDate, settling)
	}

	official, err := ParsePutCallRatio(readFixture(t, "put_call_ratio_trimmed_reconcile_20260904.json"))
	if err != nil {
		t.Fatalf("derived ratio: %v", err)
	}
	want := official.ByDate("2026-09-04")
	if want == nil {
		t.Fatal("derived ratio has no row for 2026-09-04")
	}

	got := aggregate(opt.Rows, ProductTXO, settling)
	if got.putVol != *want.PutVolume || got.callVol != *want.CallVolume {
		t.Errorf("volume: got put=%v call=%v, want put=%v call=%v",
			got.putVol, got.callVol, *want.PutVolume, *want.CallVolume)
	}
	if got.putOI != *want.PutOI || got.callOI != *want.CallOI {
		t.Errorf("open interest: got put=%v call=%v, want put=%v call=%v",
			got.putOI, got.callOI, *want.PutOI, *want.CallOI)
	}

	// ── the three rejected rules, each of which still produces a plausible number ──

	// 1. Not excluding the settling expiry. On the full capture this is 223,014 against an
	//    official 96,420 — 2.31x — with the status staying AVAILABLE.
	if incl := aggregate(opt.Rows, ProductTXO, ""); incl.putOI+incl.callOI <= got.putOI+got.callOI {
		t.Fatal("the fixture no longer carries the settling expiry's open interest, so the " +
			"exclusion rule is untested")
	} else {
		ratio := (incl.putOI + incl.callOI) / (got.putOI + got.callOI)
		if ratio < 2 {
			t.Errorf("including 202609F1 inflates OI only %.2fx — the fixture's premise weakened", ratio)
		}
		if incl.putOI == *want.PutOI {
			t.Error("including the settling expiry produced the correct PutOI — impossible")
		}
		// The volume half is UNAFFECTED by the exclusion. That asymmetry is the rule.
		if incl.putVol != got.putVol || incl.callVol != got.callVol {
			t.Error("excluding the settling expiry changed volume — the rule is asymmetric " +
				"and volume sums every expiry")
		}
	}

	// 2. Excluding every F* series, the draft rule §3.1 rejects. It is wrong in the most
	//    dangerous available way: it produces a number and the status stays AVAILABLE.
	noF := aggregateExcludingFSeries(opt.Rows)
	if noF.putOI == *want.PutOI || noF.callOI == *want.CallOI {
		t.Error("excluding every F* reproduced the official OI — the fixture can no longer " +
			"distinguish the rejected rule")
	}
	if noF.putOI >= got.putOI {
		t.Errorf("excluding every F* gave putOI %v, not below the correct %v", noF.putOI, got.putOI)
	}

	// 3. Day session only. The night rows are not optional for volume: 一般 alone gave
	//    214,161 against an official 356,219 on the full capture.
	day := aggregate(opt.Rows, ProductTXO, settling, SessionDay)
	if day.putVol >= got.putVol || day.callVol >= got.callVol {
		t.Errorf("一般 alone gave put=%v call=%v, not below the both-sessions %v/%v — the "+
			"night session is not optional", day.putVol, day.callVol, got.putVol, got.callVol)
	}

	// Summing OI across both sessions does not double-count, and the reason is data rather
	// than a story: every 盤後 row carries OpenInterest "-", which §3.1 makes ABSENT, and
	// no 一般 row does. (An earlier draft explained "-" with "the night rows carry no OI";
	// DailyMarketReportFut has 252 一般 rows carrying it, so the story is false and the
	// rule survives without it.)
	if day.putOI != got.putOI || day.callOI != got.callOI {
		t.Errorf("OI over both sessions (%v/%v) differs from 一般 alone (%v/%v) — a 盤後 row "+
			"has started carrying open interest",
			got.putOI, got.callOI, day.putOI, day.callOI)
	}

	// The contract-selection rule: "sum every row" means every TXO row. The fixture carries
	// TEO/CAO rows precisely so that dropping the filter would be visible.
	if all := aggregateAllProducts(opt.Rows, settling); all.putVol == got.putVol {
		t.Error("the fixture lost its non-TXO rows, so the TXO-only rule is untested")
	}
}

func aggregateExcludingFSeries(rows []OptionStrikeRow) optAgg {
	var kept []OptionStrikeRow
	for _, r := range rows {
		if len(r.ExpiryCode) > 6 && r.ExpiryCode[6] == 'F' {
			continue
		}
		kept = append(kept, r)
	}
	return aggregate(kept, ProductTXO, "")
}

func aggregateAllProducts(rows []OptionStrikeRow, settling string) optAgg {
	relabelled := make([]OptionStrikeRow, len(rows))
	copy(relabelled, rows)
	for i := range relabelled {
		relabelled[i].Product = ProductTXO
	}
	return aggregate(relabelled, ProductTXO, settling)
}

// The official ratio is the one endpoint that answers with history rather than a session, and
// that history is the only one R15 gets without a backfill.
func TestPutCallRatioCarriesHistory(t *testing.T) {
	got, err := ParsePutCallRatio(readFixture(t, "put_call_ratio_20260904.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Report.Status() != "AVAILABLE" {
		t.Fatalf("status %s: %v", got.Report.Status(), got.Report.Rejected)
	}
	if len(got.Rows) < 20 {
		t.Fatalf("%d sessions — every other R15 endpoint returns one, this one returns ~23; "+
			"reducing it here would throw away the only history R15 gets", len(got.Rows))
	}
	seen := map[string]bool{}
	for _, r := range got.Rows {
		if seen[r.TradingDate] {
			t.Fatalf("duplicate session %s", r.TradingDate)
		}
		seen[r.TradingDate] = true
		if len(r.TradingDate) != 10 {
			t.Fatalf("session date %q is not YYYY-MM-DD — trading_date is compared as a "+
				"bare string against an as-of date", r.TradingDate)
		}
	}

	// The exchange's own figures for the session, unmodified.
	latest := got.ByDate("2026-09-04")
	if latest == nil {
		t.Fatal("no row for 2026-09-04")
	}
	for _, c := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"PutVolume", latest.PutVolume, 356219},
		{"CallVolume", latest.CallVolume, 361817},
		{"PutOI", latest.PutOI, 48162},
		{"CallOI", latest.CallOI, 48258},
		{"PutCallVolumeRatio%", latest.VolumeRatioPct, 98.45},
		{"PutCallOIRatio%", latest.OIRatioPct, 99.80},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}
