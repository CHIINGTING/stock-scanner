package provider

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Every expiry-code shape the live session carried, classified from the CODE.
func TestClassifyExpiry(t *testing.T) {
	for code, want := range map[string]string{
		// bare YYYYMM: the only monthlies
		"202609": ExpiryMonthly,
		"202610": ExpiryMonthly,
		"202611": ExpiryMonthly,
		"202612": ExpiryMonthly,
		"202703": ExpiryMonthly,
		// any suffix is a weekly. F1/F2/F3 are TXO weekly series and the bulk of the
		// market; an earlier rule called them UNCLASSIFIED and excluded them, which put
		// PutOI at 46,852 against an official 48,162 while the status stayed AVAILABLE.
		"202609W2": ExpiryWeekly,
		"202609F1": ExpiryWeekly,
		"202609F2": ExpiryWeekly,
		"202609F3": ExpiryWeekly,
		// an unseen suffix is a weekly too, deliberately: the alternative is a silent
		// exclusion the day the exchange adds a series.
		"202609W5": ExpiryWeekly,
		"202609X9": ExpiryWeekly,
		// "/" wins over the YYYYMM prefix. 202609/202610 IS YYYYMM-prefixed, so checking
		// the prefix first would call a two-leg spread order a weekly series.
		"202609/202610":    ExpiryCombination,
		"202609W2/202609":  ExpiryCombination,
		"TX 202609/202612": ExpiryCombination,
		// not YYYYMM-prefixed: counted in every total, never silently dropped
		"":       ExpiryUnresolved,
		"-":      ExpiryUnresolved,
		"20260":  ExpiryUnresolved,
		"2026Q3": ExpiryUnresolved,
		"ABCDEF": ExpiryUnresolved,
		// Six digits that are not a year and a month, and the rule calls them MONTHLY
		// anyway. That is not an oversight being pinned, it is the spec's own statement
		// about its own rule: 666666 and 999912 are live SettlementMonth sentinels in
		// OpenInterestOfLargeTradersFutures/Options, 999912 being the all-contracts total,
		// and the defence against them is that SettlementMonth is NEVER an expiry_kind
		// input and v1 does not fetch those feeds at all. A validity check here would be a
		// weaker second line someone would then rely on.
		"666666": ExpiryMonthly,
		"999912": ExpiryMonthly,
	} {
		if got := ClassifyExpiry(code); got != want {
			t.Errorf("ClassifyExpiry(%q) = %s, want %s", code, got, want)
		}
	}
}

// The classification must hold over the real response, not just over hand-written codes.
func TestClassifyEveryExpiryInTheFixture(t *testing.T) {
	opt := mustParseOptionsByStrike(t, "options_by_strike_20260904.json")

	kinds := map[string]string{}
	for _, r := range opt.Rows {
		if r.Product != ProductTXO {
			continue
		}
		if prev, ok := kinds[r.ExpiryCode]; ok && prev != r.ExpiryKind {
			t.Fatalf("%s classified twice: %s and %s", r.ExpiryCode, prev, r.ExpiryKind)
		}
		kinds[r.ExpiryCode] = r.ExpiryKind
	}

	want := map[string]string{
		"202609": ExpiryMonthly, "202610": ExpiryMonthly, "202611": ExpiryMonthly,
		"202612": ExpiryMonthly, "202703": ExpiryMonthly,
		"202609W2": ExpiryWeekly, "202609F1": ExpiryWeekly,
		"202609F2": ExpiryWeekly, "202609F3": ExpiryWeekly,
	}
	if len(kinds) != len(want) {
		var got []string
		for k := range kinds {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf("fixture carries TXO expiries %v, want %d distinct", got, len(want))
	}
	for code, k := range want {
		if kinds[code] != k {
			t.Errorf("fixture %s = %s, want %s", code, kinds[code], k)
		}
	}

	// 202609F1 settles on this session, so DailyOptionsDelta omits it entirely. That
	// absence must change NOTHING about what kind of series it is. An earlier draft let a
	// missing delta row demote a contract to UNRESOLVED, and "UNRESOLVED is counted in
	// every total" then put the settling expiry's 126,594 lots straight back into open
	// interest: 223,014 against an official 96,420.
	delta := mustParseDelta(t, "options_delta_20260904.json")
	if delta.HasExpiry(ProductTXO, "202609F1") {
		t.Fatal("fixture no longer reproduces the live characteristic: DailyOptionsDelta " +
			"omits the expiry settling that day")
	}
	if !delta.HasExpiry(ProductTXO, "202609F2") {
		t.Fatal("fixture lost its non-settling weekly rows")
	}
	if got := ClassifyExpiry("202609F1"); got != ExpiryWeekly {
		t.Fatalf("202609F1 with no delta row = %s, want %s", got, ExpiryWeekly)
	}
}

// The combination codes really are in the futures fixture, and really do come back as
// COMBINATION rather than as a series.
func TestCombinationCodesInTheFuturesFixture(t *testing.T) {
	fut := mustParseFuturesDaily(t, "futures_daily_20260904.json")
	var seen int
	for _, r := range fut.Rows {
		if r.ExpiryKind != ExpiryCombination {
			continue
		}
		seen++
		if r.OI != nil {
			// Not a rule, an observation the spec records: combination rows carry
			// OpenInterest "-", so excluding them from OI is already a no-op and the
			// rule matters for volume.
			t.Errorf("%s %s carries OI %v — the fixture's premise changed",
				r.Product, r.ExpiryCode, *r.OI)
		}
	}
	if seen == 0 {
		t.Fatal("fixture lost its '/' spread rows")
	}
}

func TestNormalizeSession(t *testing.T) {
	if got, err := NormalizeSession("一般"); err != nil || got != SessionDay {
		t.Errorf("一般 -> %q, %v; want DAY", got, err)
	}
	if got, err := NormalizeSession("盤後"); err != nil || got != SessionAfterHours {
		t.Errorf("盤後 -> %q, %v; want AFTER_HOURS", got, err)
	}
	if _, err := NormalizeSession("夜盤"); err == nil {
		t.Error("an unrecognised session must not silently become a third session")
	}
}

func TestNormalizeCallPut(t *testing.T) {
	// Both spellings are live and neither is optional: 買權/賣權 in the by-strike, settled
	// positions and delta feeds; CALL/PUT in the institutional calls-and-puts feed §2.1
	// selects for M5.
	for _, in := range []string{"買權", "CALL", "call"} {
		if got, err := NormalizeCallPut(in); err != nil || got != CallSide {
			t.Errorf("%q -> %q, %v; want CALL", in, got, err)
		}
	}
	for _, in := range []string{"賣權", "PUT", "put"} {
		if got, err := NormalizeCallPut(in); err != nil || got != PutSide {
			t.Errorf("%q -> %q, %v; want PUT", in, got, err)
		}
	}
	// A mis-mapped side inverts a put/call ratio without changing its magnitude, which no
	// reconciliation gate can see. So it is an error, never a third category.
	for _, in := range []string{"認購", "C", "", "-"} {
		if _, err := NormalizeCallPut(in); err == nil {
			t.Errorf("%q was accepted as a call/put", in)
		}
	}
}

// ── fixture helpers ───────────────────────────────────────────────────────────────────

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func mustParseOptionsByStrike(t *testing.T, name string) *OptionsByStrike {
	t.Helper()
	got, err := ParseOptionsByStrike(readFixture(t, name))
	if err != nil {
		t.Fatalf("ParseOptionsByStrike(%s): %v", name, err)
	}
	return got
}

func mustParseFuturesDaily(t *testing.T, name string) *FuturesDaily {
	t.Helper()
	got, err := ParseFuturesDaily(readFixture(t, name))
	if err != nil {
		t.Fatalf("ParseFuturesDaily(%s): %v", name, err)
	}
	return got
}

func mustParseDelta(t *testing.T, name string) *OptionsDelta {
	t.Helper()
	got, err := ParseOptionsDelta(readFixture(t, name))
	if err != nil {
		t.Fatalf("ParseOptionsDelta(%s): %v", name, err)
	}
	return got
}
