package provider

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The TXO/TXU trap of §7.2, proved rather than described.
//
// One series, three namings:
//
//	DailyMarketReportOpt              TXO / 202609F1
//	FinalSettlementPriceIndexOptions  TXO / 202609F1
//	SettledPositionsIndexOptions      TXU / 202609 / 臺指選擇權F1
//
// A join keyed on (contract, expiry) matches NOTHING across the first and the third, and it
// matches nothing silently — no error, no empty-result complaint, just a settled expiry that
// never reconciles. derivative_contracts is where that is fixed, and this is the pair §7.2
// says a test must pin.
func TestSettledPositionsDoNotJoinOnContractAndExpiry(t *testing.T) {
	settled, err := ParseSettledPositions(readFixture(t, "settled_positions_20260904.json"))
	if err != nil {
		t.Fatalf("settled positions: %v", err)
	}
	settlement, err := ParseFinalSettlement(readFixture(t, "final_settlement_20260904.json"))
	if err != nil {
		t.Fatalf("final settlement: %v", err)
	}
	byStrike := mustParseOptionsByStrike(t, "options_by_strike_20260904.json")

	// ── the naive join, which is what an implementer writes first ──
	series := map[string]bool{}
	for _, r := range byStrike.Rows {
		series[r.Product+"|"+r.ExpiryCode] = true
	}
	if !series["TXO|202609F1"] {
		t.Fatal("the by-strike fixture lost TXO/202609F1")
	}
	for _, p := range settled.Rows {
		key := p.Product + "|" + p.DeliveryMonth
		if series[key] {
			t.Fatalf("(contract, expiry) join on %q matched — the exchange stopped "+
				"disagreeing with itself and this test no longer describes reality", key)
		}
	}
	// And it fails while looking perfectly healthy: both sides parsed AVAILABLE.
	if settled.Report.Status() != "AVAILABLE" || byStrike.Report.Status() != "AVAILABLE" {
		t.Fatal("both responses must be clean — the point is that a clean join still matches nothing")
	}

	// ── the reconciliation ──
	aliases, unmatched, err := ReconcileSettledPositions(settled.Rows, settlement.Rows)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(unmatched) != 0 {
		t.Fatalf("unmatched settled rows: %+v", unmatched)
	}
	if len(aliases) != 1 {
		t.Fatalf("%d aliases, want 1 (both call and put rows name one series)", len(aliases))
	}
	got := aliases[0]
	want := ContractAlias{
		Product: "TXO", ExpiryCode: "202609F1", ExpiryKind: ExpiryWeekly,
		AltProduct: "TXU", AltExpiryCode: "202609", ContractName: "臺指選擇權F1",
	}
	if got != want {
		t.Fatalf("alias\n got %+v\nwant %+v", got, want)
	}

	// The canonical side of the alias joins to the by-strike report, which is the whole
	// point of storing it.
	if !series[got.Product+"|"+got.ExpiryCode] {
		t.Fatalf("the reconciled key %s|%s still does not join", got.Product, got.ExpiryCode)
	}
}

// The suffix is not lost, it is MOVED into ContractName. Recombining is what makes the join
// possible at all.
func TestSeriesExpiryCode(t *testing.T) {
	for _, tc := range []struct{ month, name, want string }{
		{"202609", "臺指選擇權F1", "202609F1"},
		{"202609", "臺指選擇權W2", "202609W2"},
		{"202609", "臺指選擇權F12", "202609F12"},
		// A genuine monthly carries no suffix, and "" is the honest answer rather than an
		// invented join.
		{"202609", "臺指選擇權", ""},
		{"202609", "", ""},
	} {
		if got := seriesExpiryCode(tc.month, tc.name); got != tc.want {
			t.Errorf("seriesExpiryCode(%q, %q) = %q, want %q", tc.month, tc.name, got, tc.want)
		}
	}
}

// R15 is a leaf. The scanner's decisions must not be able to depend on it even by accident,
// and the guarantee is structural rather than a comment.
//
// Scoped to ./internal/scanner ONLY. It must NOT be copied onto ./cmd/scanner, because
// cmd/scanner -> internal/report -> internal/derivatives is the intended wiring for the
// report section and would fail by design.
func TestScannerDoesNotDependOnDerivativesProvider(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "./internal/scanner")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./internal/scanner: %v", err)
	}
	for _, pkg := range []string{
		"stock-scanner/internal/derivatives",
		"stock-scanner/internal/derivatives/provider",
	} {
		if strings.Contains(string(out), pkg) {
			t.Errorf("internal/scanner depends on %s — a default-off research layer has "+
				"reached the decision path", pkg)
		}
	}
}

// No HTTP in this package. M3 part B owns fetching; part A is pure, which is what lets every
// test here run off committed bytes and never download.
func TestParsersDoNotImportNetHTTP(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}",
		"./internal/derivatives/provider")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, imp := range strings.Fields(string(out)) {
		if imp == "net/http" || imp == "net" {
			t.Errorf("the parser package imports %s — parsing must not be able to fetch", imp)
		}
	}
}
