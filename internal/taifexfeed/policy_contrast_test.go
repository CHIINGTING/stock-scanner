package taifexfeed_test

import (
	"fmt"
	"testing"

	dp "github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	mp "github.com/deep-huang/stock-scanner/internal/market/provider"
)

// The two policies, pinned SIDE BY SIDE on the SAME input bytes.
//
// internal/market/provider and internal/derivatives/provider now share one decode
// (internal/taifexfeed.Decode) and keep opposite answers about what an absence token means.
// That divergence is deliberate — see the table in this package's doc comment — but a
// deliberate divergence that nothing checks is a drift waiting to happen: it was previously
// invisible precisely because the two readers never appeared in the same file.
//
// This test is the one place both answers are stated at once. Changing either policy fails it,
// which makes the change a decision rather than an accident. It lives here, beside the shared
// decode, because it is the only package that may import both readers: internal/derivatives
// must stay a leaf of the decision path and internal/market/provider must not import it, so
// neither could host this test without creating exactly the dependency
// internal/derivatives/architecture_test.go forbids. (An external _test package importing both
// is not a dependency of either: `go list -deps` of a non-test build never sees it.)

// institutionalBody is ONE institutional-futures row — the response both readers consume —
// with the POSITION net token under test substituted in. Everything else is held constant, so
// any difference in the two answers is a difference in policy and nothing else.
func institutionalBody(netOI string) []byte {
	return []byte(fmt.Sprintf(`[{
	  "Date": "20260904",
	  "ContractCode": "臺股期貨",
	  "Item": "外資及陸資",
	  "TradingVolume(Long)": "1,234",
	  "TradingValue(Long)(Thousands)": "10",
	  "TradingVolume(Short)": "2",
	  "TradingValue(Short)(Thousands)": "20",
	  "TradingVolume(Net)": "-1",
	  "TradingValue(Net)(Thousands)": "-10",
	  "OpenInterest(Long)": "8321",
	  "ContractValueofOpenInterest(Long)(Thousands)": "100",
	  "OpenInterest(Short)": "96232",
	  "ContractValueofOpenInterest(Short)(Thousands)": "200",
	  "OpenInterest(Net)": %q,
	  "ContractValueofOpenInterest(Net)(Thousands)": "-100"
	}]`, netOI))
}

// marketNetOI is what internal/market/provider makes of that row.
func marketNetOI(t *testing.T, body []byte) (float64, error) {
	t.Helper()
	fut, _, err := mp.ParseTAIFEXDaily(body, "2026-09-04")
	if err != nil {
		return 0, err
	}
	return fut.NetOI, nil
}

// r15NetOI is what internal/derivatives/provider makes of the SAME row: the POSITION/NET
// observation, whose Lots is nil when the value is ABSENT.
func r15NetOI(t *testing.T, body []byte) (lots *float64, unknown []dp.UnknownToken, err error) {
	t.Helper()
	inst, err := dp.ParseInstitutionalFutures(body)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range inst.Rows {
		if r.Semantics == dp.SemanticsPosition && r.Side == dp.SideNet {
			return r.Lots, inst.Report.UnknownTokens, nil
		}
	}
	t.Fatalf("no POSITION/NET row in %d rows", len(inst.Rows))
	return nil, nil, nil
}

func TestTheTwoReadersAgreeOnEveryRealNumber(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  float64
		why   string
	}{
		{"-87911", -87911, "the ordinary case, and the value the TXF fixtures carry"},
		{"0", 0, "an observed zero is a number, not an absence, on both sides"},
		{"1,234", 1234, "the thousands separator is stripped by the SHARED normaliser, so " +
			"neither reader can lose only the numbers >= 1000"},
		{" 42 ", 42, "surrounding whitespace is cleanup, not meaning"},
	} {
		t.Run(tc.token, func(t *testing.T) {
			body := institutionalBody(tc.token)

			got, err := marketNetOI(t, body)
			if err != nil {
				t.Fatalf("market: %v", err)
			}
			if got != tc.want {
				t.Fatalf("market NetOI = %v, want %v (%s)", got, tc.want, tc.why)
			}

			lots, _, err := r15NetOI(t, body)
			if err != nil {
				t.Fatalf("r15: %v", err)
			}
			if lots == nil {
				t.Fatalf("r15 made %q ABSENT; the two readers must agree on every value that "+
					"is a value (%s)", tc.token, tc.why)
			}
			if *lots != tc.want {
				t.Fatalf("r15 lots = %v, want %v (%s)", *lots, tc.want, tc.why)
			}
		})
	}
}

// TestTheTwoReadersDivergeOnAbsence is the deliberate difference, stated once.
//
// Same bytes, two answers, both correct for their own rows:
//
//	internal/market       "-" -> 0        an empty cell on a TWSE cash row IS a zero, and
//	                                      twse_test.go has pinned that since before R15
//	internal/derivatives  "-" -> ABSENT   on a derivatives row "-" is "not carried" and "0"
//	                                      is "a strike nobody holds"; collapsing them erases
//	                                      the distinction the layer rests on and fails §10.1
//
// Making them agree is the tempting cleanup and it is a regression in whichever direction it
// is taken. That is why this test asserts a DIFFERENCE rather than a value.
func TestTheTwoReadersDivergeOnAbsence(t *testing.T) {
	for _, token := range []string{"-", ""} {
		t.Run(fmt.Sprintf("%q", token), func(t *testing.T) {
			body := institutionalBody(token)

			got, err := marketNetOI(t, body)
			if err != nil {
				t.Fatalf("market rejected %q: %v — parseAmount's shipped answer is 0, and "+
					"twse_test.go pins it", token, err)
			}
			if got != 0 {
				t.Fatalf("market NetOI = %v, want 0", got)
			}

			lots, unknown, err := r15NetOI(t, body)
			if err != nil {
				t.Fatalf("r15 rejected %q: %v — §3.1 makes an unparsable token ABSENT, not an "+
					"error", token, err)
			}
			if lots != nil {
				t.Fatalf("r15 decoded %q to %v; ABSENT and an observed 0 must stay "+
					"distinguishable (§3.1)", token, *lots)
			}
			// A KNOWN absence is silent. Only a sentinel nobody has seen before is reported,
			// or the day's log is 100,274 lines of normal data.
			if len(unknown) != 0 {
				t.Fatalf("r15 reported %q as an unknown token: %v", token, unknown)
			}
		})
	}
}

// TestTheTwoReadersDivergeOnGarbageToo — the same split, at the other end. Text that is
// neither a number nor a known sentinel is an ERROR to internal/market (a layout change must
// not look like a quiet market) and an ABSENT-plus-a-report to R15 (a nullable column exists
// to express exactly this, and the token is surfaced so a new sentinel becomes visible on the
// first session).
func TestTheTwoReadersDivergeOnGarbageToo(t *testing.T) {
	body := institutionalBody("暫停交易")

	if got, err := marketNetOI(t, body); err == nil {
		t.Fatalf("market accepted 暫停交易 as %v — unparsable text must not become a silent 0", got)
	}

	lots, unknown, err := r15NetOI(t, body)
	if err != nil {
		t.Fatalf("r15: %v — an unparsable token is ABSENT and explicitly not an error (§3.1)", err)
	}
	if lots != nil {
		t.Fatalf("r15 decoded 暫停交易 to %v", *lots)
	}
	var seen bool
	for _, u := range unknown {
		if u.Token == "暫停交易" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("r15 swallowed an unrecognised token silently: %v — the census is what makes "+
			"a new TAIFEX sentinel visible on day one instead of a column quietly vanishing",
			unknown)
	}
}

// TestBothReadersSeeTheSameLayoutChange — the half that IS shared. A renamed column is
// detected by the one decode, so neither reader can be the one that keeps working and quietly
// reports zeros.
//
// This is what the struct decode could not do: json.Unmarshal into a struct fills the missing
// key with "", which internal/market's own policy then turns into a perfectly plausible net OI
// of 0.
func TestBothReadersSeeTheSameLayoutChange(t *testing.T) {
	// OpenInterest(Net) renamed — the column both readers consume.
	body := []byte(`[{
	  "Date": "20260904", "ContractCode": "臺股期貨", "Item": "外資及陸資",
	  "TradingVolume(Long)": "1", "TradingVolume(Short)": "2", "TradingVolume(Net)": "-1",
	  "OpenInterest(Long)": "8321", "OpenInterest(Short)": "96232",
	  "OpenInterestNet": "-87911"
	}]`)

	if fut, _, err := mp.ParseTAIFEXDaily(body, "2026-09-04"); err == nil {
		t.Fatalf("market accepted a response with OpenInterest(Net) renamed away, and read "+
			"net OI as %v", fut.NetOI)
	}
	if got, err := dp.ParseInstitutionalFutures(body); err == nil {
		t.Fatalf("r15 accepted a response with OpenInterest(Net) renamed away (%d rows)",
			len(got.Rows))
	}
}
