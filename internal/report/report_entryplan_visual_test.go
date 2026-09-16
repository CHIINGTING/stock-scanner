package report

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-10 WORKSTREAM A — the fixtures ⑲ was VISUALLY inspected on.
//
// EP-8 proved the ⑲ contract on HTML strings. A string assertion cannot see a broken card, a
// column that wraps into its neighbour, an overlapping label or a dark-on-dark status chip, and
// ⑲ had never been opened in a browser. This file is the fixture set that was rendered and read
// as images (headless Chrome, 1440px) during EP-10.
//
// It is a NORMAL TEST FIRST: every case asserts the number of ⑲ sections the EP-8 contract
// requires for it, so the fixtures cannot rot into decoration. Setting EP10_VISUAL_DIR
// additionally writes each case's full report next to the assertions, which is how the PNGs were
// produced. Nothing is skipped when the variable is unset.
//
// The cases are the states the EP-10 brief names A..J plus the adversarial presentation inputs.
// ──────────────────────────────────────────────────────────────────────────────

// epVisualCase is one report to render. wantSections is the EP-8 contract for it.
type epVisualCase struct {
	id           string
	title        string
	entries      []scanner.WatchlistEntry
	gv           GuardrailViewOptions
	wantSections int
}

// epNamed stamps a symbol and a name on an entry so a screenshot says which case it is.
func epNamed(e scanner.WatchlistEntry, symbol, name string) scanner.WatchlistEntry {
	e.A.Symbol, e.A.Name = symbol, name
	return e
}

// epShow is the only view option these fixtures need.
var epShow = GuardrailViewOptions{ShowEntryPlan: true}

// epAdversarialPlan is the presentation torture test: every optional level absent, a very long
// reason, a very long caveat, HTML-sensitive text, a non-finite number, a genuine zero that must
// not masquerade as missing, and an unknown reason code.
func epAdversarialPlan() *entryplan.Plan {
	return &entryplan.Plan{
		Symbol: "9999", AsOf: "2026-06-05", Status: entryplan.StatusWaitPullback,
		RuleVersion: epSynthVersion,
		// IdealEntry, MaxChasePrice, Invalidation, Target1, Target2 and RiskReward are all nil.
		Confidence: entryplan.ConfidenceLow,
		Reasons: []entryplan.Reason{
			entryplan.ReasonStatusPriceBelowEntryZone,
			entryplan.Reason(strings.Repeat("VERY_LONG_UNKNOWN_REASON_CODE_", 12)),
			"<script>alert('x')</script>",
		},
		Caveats: []string{
			strings.Repeat("這是一則刻意寫得非常長的注意事項，用來檢查排版是否會溢出或與相鄰欄位重疊。", 8),
			"含有 HTML 敏感字元 <b>&</b> \"引號\" 與 <img src=x onerror=1>",
		},
		Evidence: []entryplan.Evidence{
			// A genuine ZERO current price: not a price, so it must render "—" rather than 0.00,
			// and the relation must be UNAVAILABLE rather than BELOW_ZONE.
			{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Available, Value: epF(0)},
			{Key: entryplan.EvidenceEntrySemantic, Status: entryplan.Available,
				Text: string(entryplan.EntrySemanticPullback)},
		},
	}
}

// epNonFinitePlan carries NaN and ±Inf in every numeric slot that has one.
func epNonFinitePlan() *entryplan.Plan {
	p := epSynthPlan(entryplan.StatusBuyNow, math.NaN())
	p.IdealEntry.Low = math.Inf(-1)
	p.IdealEntry.High = math.NaN()
	p.MaxChasePrice = epF(math.Inf(1))
	p.Invalidation.Price = math.NaN()
	p.Target1.Price = math.Inf(1)
	p.Target1.RMultiple = epF(math.NaN())
	p.Target2.RMultiple = epF(math.Inf(-1))
	p.RiskReward.Ratio = math.NaN()
	return p
}

func epVisualCases() []epVisualCase {
	one := func(e scanner.WatchlistEntry) []scanner.WatchlistEntry {
		return []scanner.WatchlistEntry{e}
	}
	synth := func(status entryplan.EntryStatus, cur float64, sym, name string) []scanner.WatchlistEntry {
		return one(epNamed(epLegacyEntry(epSynthPlan(status, cur)), sym, name))
	}
	return []epVisualCase{
		{"A_pullback_thesis", "A：PULLBACK 想法（經真實橋接）",
			one(epNamed(epBridgeEntryWith(scanner.ActionBuy, scanner.ActPullbackBuy, nil), "2330", "A 拉回想法")),
			epShow, 1},
		{"B_breakout_thesis", "B：BREAKOUT 想法（經真實橋接）",
			one(epNamed(epBridgeEntryWith(scanner.ActionBuy, scanner.ActBreakoutBuy, nil), "2317", "B 突破想法")),
			epShow, 1},
		{"C_buy_now", "C：BUY_NOW",
			synth(entryplan.StatusBuyNow, 96.00, "1101", "C 現價符合"), epShow, 1},
		{"D_wait_pullback", "D：WAIT_PULLBACK",
			synth(entryplan.StatusWaitPullback, 98.50, "1102", "D 等待拉回"), epShow, 1},
		{"E_wait_breakout", "E：WAIT_BREAKOUT",
			synth(entryplan.StatusWaitBreakout, 93.10, "1103", "E 等待突破"), epShow, 1},
		{"F_too_extended", "F：TOO_EXTENDED",
			synth(entryplan.StatusTooExtended, 101.25, "1104", "F 已超過追價上限"), epShow, 1},
		{"G_insufficient_data", "G：INSUFFICIENT_DATA（想法已解析，價位全缺）",
			one(epNamed(epLegacyEntry(epInsufficientPlan()), "1105", "G 資料不足")), epShow, 1},
		{"H_sell_contradiction", "H：賣出動作與買進想法矛盾 → NO_VALID_ENTRY（無任何可執行價位）",
			one(epNamed(epBridgeEntryWith(scanner.ActionSell, scanner.ActPullbackBuy, nil), "2454", "H 想法矛盾")),
			epShow, 1},
		{"I_no_entry_thesis", "I：沒有進場想法 → 不顯示 ⑲",
			one(epNamed(epBridgeEntryWith(scanner.ActionBuy, scanner.ActPrepare, nil), "2308", "I 無進場想法")),
			epShow, 0},
		{"J_show_false", "J：show_entry_plan=false → 不顯示 ⑲",
			synth(entryplan.StatusBuyNow, 96.00, "1106", "J 開關關閉"), GuardrailViewOptions{}, 0},
		{"K_adversarial_missing", "K：對抗性 — 價位全 nil、超長理由／注意、HTML 字元、零現價",
			one(epNamed(epLegacyEntry(epAdversarialPlan()), "9999", "K 對抗性輸入")), epShow, 1},
		{"L_non_finite", "L：對抗性 — NaN／±Inf 出現在每一個數值欄位",
			one(epNamed(epLegacyEntry(epNonFinitePlan()), "9998", "L 非有限數")), epShow, 1},
		{"M_all_states_one_page", "M：同一份報告內的多檔（版面互相干擾檢查）",
			[]scanner.WatchlistEntry{
				epNamed(epLegacyEntry(epSynthPlan(entryplan.StatusBuyNow, 96.00)), "1101", "C 現價符合"),
				epNamed(epBridgeEntryWith(scanner.ActionBuy, scanner.ActPrepare, nil), "2308", "I 無進場想法"),
				epNamed(epLegacyEntry(epSynthPlan(entryplan.StatusTooExtended, 101.25)), "1104", "F 已超過追價上限"),
				epNamed(epBridgeEntryWith(scanner.ActionSell, scanner.ActPullbackBuy, nil), "2454", "H 想法矛盾"),
				epNamed(epLegacyEntry(epAdversarialPlan()), "9999", "K 對抗性輸入"),
			}, epShow, 4},
	}
}

// epInsufficientPlan is an INSUFFICIENT_DATA plan that DID resolve a thesis — state G. The EP-8
// contract makes it visible, and it must publish no price.
func epInsufficientPlan() *entryplan.Plan {
	return &entryplan.Plan{
		Symbol: "1105", AsOf: "2026-06-05", Status: entryplan.StatusInsufficientData,
		RuleVersion: epSynthVersion, Confidence: entryplan.ConfidenceInsufficientData,
		Reasons: []entryplan.Reason{entryplan.ReasonZoneATRUnavailable,
			entryplan.ReasonStatusEntryZoneUnavailable},
		Evidence: []entryplan.Evidence{
			{Key: entryplan.EvidenceEntrySemantic, Status: entryplan.Available,
				Text: string(entryplan.EntrySemanticBreakout)},
			{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Unavailable},
		},
	}
}

// TestEntryPlanVisualFixturesHoldTheirContract renders every visual fixture and checks the number
// of ⑲ sections the EP-8 contract requires. With EP10_VISUAL_DIR set it also writes each report,
// which is how the EP-10 screenshots were produced.
func TestEntryPlanVisualFixturesHoldTheirContract(t *testing.T) {
	outDir := os.Getenv("EP10_VISUAL_DIR")
	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := epVisualCases()
	if len(cases) < 13 {
		t.Fatalf("only %d visual fixtures — the EP-10 state list is A..J plus the adversarial ones", len(cases))
	}
	rendered := 0
	for _, c := range cases {
		html := genHTML(t, c.entries, c.gv)
		if got := len(epSections(html)); got != c.wantSections {
			t.Errorf("%s: %d ⑲ sections, want %d", c.id, got, c.wantSections)
		}
		// No fixture may ever put a non-finite number or a fabricated zero-price on screen.
		for _, sec := range epSections(html) {
			for _, bad := range []string{"NaN", "+Inf", "-Inf", "Inf"} {
				if strings.Contains(sec, bad) {
					t.Errorf("%s: %q reached the ⑲ section", c.id, bad)
				}
			}
		}
		rendered++
		if outDir == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(outDir, c.id+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rendered != len(cases) {
		t.Fatalf("rendered %d of %d fixtures", rendered, len(cases))
	}
}

// The H fixture is the one a reader is most likely to misread, so its contract is asserted
// separately rather than only counted: the section is VISIBLE, says NO_VALID_ENTRY, and carries
// no executable price anywhere.
func TestEntryPlanVisualSellContradictionShowsNoExecutablePrice(t *testing.T) {
	e := epBridgeEntryWith(scanner.ActionSell, scanner.ActPullbackBuy, nil)
	if e.EntryPlan == nil || e.EntryPlan.HasExecutablePrices() {
		t.Fatalf("fixture: want a price-less plan, got %+v", e.EntryPlan)
	}
	sec := epOneSection(t, e)
	epAssertContains(t, sec, "狀態 <b>NO_VALID_ENTRY</b>",
		epRow("理想進場區", "—"), epRow("追價上限", "—"), epRow("想法失效價", "—"),
		epRow("目標一", "—"), epRow("目標二", "—"), epRow("風險報酬比", "—"))
}

// ──────────────────────────────────────────────────────────────────────────────
// EP-10 REASON PRESENTATION — EXACT SET
//
// EP-8 mapped 28 of entryplan's 60 reason codes and said so; the other 32 rendered as bare
// SCREAMING_SNAKE. EP-10 completed the map, and completion is only worth anything with a guard
// that fails when the two lists drift. This one is exact in BOTH directions and reads the
// registry itself (entryplan.AllReasons), not a second list written here.
// ──────────────────────────────────────────────────────────────────────────────

func TestEntryPlanReasonLabelsCoverTheRegistryExactly(t *testing.T) {
	if len(entryplan.AllReasons) < 50 {
		t.Fatalf("entryplan.AllReasons has %d codes — this guard is not seeing the registry",
			len(entryplan.AllReasons))
	}
	registry := map[entryplan.Reason]bool{}
	for _, r := range entryplan.AllReasons {
		registry[r] = true
		if _, ok := epReasonLabels[r]; !ok {
			t.Errorf("entryplan registers %q and ⑲ has no presentation label for it", r)
		}
	}
	for r := range epReasonLabels {
		if !registry[r] {
			t.Errorf("⑲ maps %q, which entryplan does not register", r)
		}
	}
	if len(epReasonLabels) != len(entryplan.AllReasons) {
		t.Errorf("⑲ maps %d reasons, the registry has %d", len(epReasonLabels), len(entryplan.AllReasons))
	}
}

// Completing the map must not have made any label a lie or a duplicate, and the canonical code
// must still travel with every one of them.
func TestEntryPlanReasonLabelsAreDistinctAndKeepTheirCode(t *testing.T) {
	seen := map[string]entryplan.Reason{}
	for _, r := range entryplan.AllReasons {
		label := epReasonLabels[r]
		if label == "" {
			continue // reported by the exact-set guard
		}
		if prev, dup := seen[label]; dup {
			t.Errorf("%q and %q share the label %q — two different conditions would read the same",
				prev, r, label)
		}
		seen[label] = r
		out := epReasons([]entryplan.Reason{r})
		if len(out) != 1 || !strings.Contains(out[0], string(r)) {
			t.Errorf("%q renders as %v without its canonical code", r, out)
		}
	}
	// UNKNOWN is never spelled as a pass or a failure. The EP-9 population carrying this code
	// is EVALUATOR-BLOCKED, not "would-be BUY_NOW" and not "rejected".
	notEval := epReasonLabels[entryplan.ReasonStatusRequirementNotEvaluated]
	if !strings.Contains(notEval, "未評估") || !strings.Contains(notEval, "不等於通過") {
		t.Errorf("STATUS_REQUIREMENT_NOT_EVALUATED reads %q; it must say it was NOT EVALUATED and "+
			"that this is not a pass", notEval)
	}
	for _, bad := range []string{"通過（", "符合條件", "接近"} {
		if strings.Contains(notEval, bad) {
			t.Errorf("STATUS_REQUIREMENT_NOT_EVALUATED reads %q, which contains %q", notEval, bad)
		}
	}
}

// EP-10 D3, pinned EXACTLY rather than only on the nil-zone half.
//
// epZoneBasis delegates to epZone: the basis is a statement ABOUT a published zone, so it is
// withheld on exactly the condition the zone is. "Exactly" is the part that needs a test —
// a NARROWER predicate (`z == nil || !epIsPrice(z.Low)`) is correct for a nil zone and for a
// bad Low, and still prints a basis beside "—" for a HALF-ZONE whose High is not a price.
// Both halves and the mirror are asserted here, with a control that proves the basis renders
// at all.
func TestEntryPlanZoneBasisIsWithheldExactlyWhenTheZoneIs(t *testing.T) {
	// Control first: a fully published zone DOES carry its basis, so the cases below are
	// measuring a suppression rather than an absence.
	ctrl := epOneSection(t, epLegacyEntry(epSynthPlan(entryplan.StatusWaitPullback, 98.50)))
	epAssertContains(t, ctrl, epRow("理想進場區", `94.70 – 97.30 <span class="ep-code">MA20・RAW</span>`))

	cases := []struct {
		name      string
		low, high float64
	}{
		{"High is not a price (the half-zone a narrower predicate would let through)", 94.70, math.NaN()},
		{"High is +Inf", 94.70, math.Inf(1)},
		{"High is zero", 94.70, 0},
		{"Low is not a price (the mirror)", math.NaN(), 97.30},
		{"Low is zero", 0, 97.30},
		{"both bounds unusable", math.NaN(), math.Inf(-1)},
	}
	ran := 0
	for _, c := range cases {
		p := epSynthPlan(entryplan.StatusWaitPullback, 98.50)
		p.IdealEntry.Low, p.IdealEntry.High = c.low, c.high
		if len(p.IdealEntry.Basis) == 0 || p.IdealEntry.PriceBasis == "" {
			t.Fatalf("%s: fixture carries no basis, so nothing could be suppressed", c.name)
		}
		sec := epOneSection(t, epLegacyEntry(p))
		// No zone…
		epAssertContains(t, sec, epRow("理想進場區", "—"))
		// …and therefore no basis, in any form.
		for _, bad := range []string{"MA20", "RAW", `<span class="ep-code">MA20`, "・"} {
			if strings.Contains(sec, bad) {
				t.Errorf("%s: a zone basis (%q) was printed beside an unpublished zone\n%s",
					c.name, bad, sec)
			}
		}
		ran++
	}
	if ran != len(cases) {
		t.Fatalf("ran %d of %d half-zone cases", ran, len(cases))
	}
}

// The adversarial fixture must not panic, must not print a fabricated 0.00 for an absent level,
// and must escape every HTML-sensitive character it was handed.
func TestEntryPlanVisualAdversarialFixtureRendersSafely(t *testing.T) {
	sec := epOneSection(t, epLegacyEntry(epAdversarialPlan()))
	epAssertContains(t, sec,
		epRow("理想進場區", "—"), epRow("追價上限", "—"), epRow("想法失效價", "—"),
		epRow("目標一", "—"), epRow("目標二", "—"), epRow("風險報酬比", "—"),
		// A zero current price is NOT a price: "—", and the relation says so.
		epRow("計畫所見現價", "—"),
		`<span class="ep-code">UNAVAILABLE</span>`,
	)
	epAssertAbsent(t, sec, "0.00", "<script>", "<img src=x")
	if !strings.Contains(sec, "&lt;script&gt;") {
		t.Error("the HTML-sensitive reason code was not escaped")
	}

	// EP-10 browser finding D3, on the fixture that actually exercises it: a zone whose bounds
	// are NOT prices publishes no zone, so it must publish no BASIS either. The basis chip is a
	// statement about a zone that would, on this plan, not exist.
	nf := epNonFinitePlan()
	if nf.IdealEntry == nil || len(nf.IdealEntry.Basis) == 0 {
		t.Fatal("fixture: the non-finite plan must still carry a non-nil zone WITH a basis")
	}
	nfSec := epOneSection(t, epLegacyEntry(nf))
	epAssertContains(t, nfSec, epRow("理想進場區", "—"))
	epAssertAbsent(t, nfSec, "MA20", "RAW", `<span class="ep-code">MA20`)
}

// ──────────────────────────────────────────────────────────────────────────────
// EP-10 — THE PREMISE THAT MAKES ONE MUTANT EQUIVALENT, MACHINE-PINNED
//
// epBuySideSemantic reads `ev.Status == Available && EntrySemantic(ev.Text).Resolved()`.
// Dropping the second half is an EQUIVALENT mutation today (it is recorded as such in
// docs/EP10_MUTATION_AUDIT.md), and it is equivalent ONLY because of this chain:
//
//	P1  ComputePlan is the only producer of the ENTRY_SEMANTIC evidence row (plan.go:188-207).
//	P2  It sets that row's Status to Available ONLY inside the in.EntrySemantic.Usable()
//	    branch, and sets its Text from the same value in the same branch (plan.go:190-193).
//	P3  EntrySemanticEvidence.Usable() requires Status.OK() AND Semantic.Valid() AND
//	    Semantic.Resolved() (input.go:226-228).
//	P4  Every other arm sets the row to INSUFFICIENT_DATA or UNAVAILABLE (plan.go:194-205).
//
// So on any plan this repo can produce, `Status == Available` already IMPLIES `Resolved`.
// P3 is the fragile one — a future change that relaxed Usable() would silently turn the
// dropped half into a real defect. This test drives ComputePlan over the whole cross product
// of declared EntrySemantic values and declared Availability values and asserts the
// implication on the PUBLISHED ROW, so the equivalence claim fails here rather than being a
// sentence in an audit document.
// ──────────────────────────────────────────────────────────────────────────────
func TestTheEntrySemanticRowIsAvailableOnlyWhenResolved(t *testing.T) {
	semantics := epConstNames(t, "policy.go", "EntrySemantic")
	availabilities := epConstNames(t, "input.go", "Availability")
	if len(semantics) < 3 || len(availabilities) < 3 {
		t.Fatalf("parsed %d EntrySemantic and %d Availability constants — the census is not "+
			"seeing the declarations", len(semantics), len(availabilities))
	}

	rowOf := func(p entryplan.Plan) (entryplan.Evidence, bool) {
		for _, ev := range p.Evidence {
			if ev.Key == entryplan.EvidenceEntrySemantic {
				return ev, true
			}
		}
		return entryplan.Evidence{}, false
	}

	checked, available := 0, 0
	// The empty semantic and an unregistered one are included on purpose: both are values a
	// caller can hand over, and neither may reach an AVAILABLE row.
	semValues := []string{"", "EP10_NOT_A_SEMANTIC"}
	for _, v := range semantics {
		semValues = append(semValues, v)
	}
	availValues := []string{""}
	for _, v := range availabilities {
		availValues = append(availValues, v)
	}
	sort.Strings(semValues)
	sort.Strings(availValues)

	for _, sv := range semValues {
		for _, av := range availValues {
			p := entryplan.ComputePlan(entryplan.Snapshot{
				Symbol: "1111", AsOf: "2026-06-05",
				EntrySemantic: entryplan.EntrySemanticEvidence{
					Status:   entryplan.Availability(av),
					Semantic: entryplan.EntrySemantic(sv),
				},
			})
			row, found := rowOf(p)
			if !found {
				t.Fatalf("semantic=%q availability=%q: ComputePlan published no ENTRY_SEMANTIC row", sv, av)
			}
			checked++
			if row.Status != entryplan.Available {
				continue
			}
			available++
			if !entryplan.EntrySemantic(row.Text).Resolved() {
				t.Errorf("semantic=%q availability=%q: the published row is AVAILABLE with text %q, "+
					"which is NOT resolved. The equivalence premise recorded in "+
					"docs/EP10_MUTATION_AUDIT.md is broken and epBuySideSemantic's Resolved() half "+
					"is now load-bearing.", sv, av, row.Text)
			}
		}
	}
	// ANTI-VACUITY: the cross product really did reach the AVAILABLE arm, and really did reach
	// the other arms too, so "no counterexample" is not "no case".
	if checked != len(semValues)*len(availValues) {
		t.Fatalf("checked %d combinations, want %d", checked, len(semValues)*len(availValues))
	}
	if available == 0 {
		t.Fatal("no combination produced an AVAILABLE row — the implication was never tested")
	}
	if available == checked {
		t.Fatal("every combination produced an AVAILABLE row — the non-available arms were never reached")
	}
}
