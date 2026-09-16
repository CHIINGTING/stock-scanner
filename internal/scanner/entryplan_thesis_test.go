package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6D — THE "EXISTING BUY THESIS" GUARD, ON THE SCANNER SIDE OF THE BRIDGE
//
// The user decided (2026-09-14) that WatchAction PULLBACK_BUY / BREAKOUT_BUY IS the existing
// BUY thesis, and added exactly one protection: a stock whose primary Action is SELL, REDUCE,
// TAKE PROFIT or STOP LOSS (the last two added at the EP-6D review)
// must not be BUY_NOW. entryplan decides what a thesis standing does (its own suite); this file
// pins what the BRIDGE hands over for every Action, and that the answer reaches the plan.
//
// WHAT THIS FILE DOES NOT COVER:
//   - The Action list is read off types.go's SOURCE. An Action constant declared in another
//     file would not be enumerated here.
//   - The behavioural rows run AttachEntryPlan directly on a hand-built entry. They do not prove
//     that production analysis ever produces a given Action alongside an entry WatchAction.
// ──────────────────────────────────────────────────────────────────────────────

// epExpectedThesis is the classification of every Action the scanner declares, with the reason.
var epExpectedThesis = map[Action]struct {
	standing entryplan.ThesisStanding
	why      string
}{
	ActionStrongBuy:  {entryplan.ThesisNotContradicted, "a buy recommendation does not contradict a buy thesis"},
	ActionBuy:        {entryplan.ThesisNotContradicted, "a buy recommendation does not contradict a buy thesis"},
	ActionWatch:      {entryplan.ThesisNotContradicted, "the user's decision: only the exit-side Actions contradict"},
	ActionHold:       {entryplan.ThesisNotContradicted, "the user's decision: only the exit-side Actions contradict"},
	ActionReduce:     {entryplan.ThesisContradicted, "the user's decision (2026-09-14)"},
	ActionSell:       {entryplan.ThesisContradicted, "the user's decision (2026-09-14)"},
	ActionTakeProfit: {entryplan.ThesisContradicted, "the user's decision at the EP-6D review: an exit-side recommendation, same set as SELL / REDUCE"},
	ActionStopLoss:   {entryplan.ThesisContradicted, "the user's decision at the EP-6D review: same as TAKE PROFIT"},
}

// actionsDeclaredInSource reads every `X Action = "..."` constant out of types.go.
func actionsDeclaredInSource(t *testing.T) []Action {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v — repoint this sweep at wherever Action moved", err)
	}
	var out []Action
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "Action" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out = append(out, Action(lit.Value[1:len(lit.Value)-1]))
			}
		}
	}
	return out
}

// Every declared Action has an EXPLICIT, pinned classification, in BOTH directions.
func TestEntryPlanThesisCoversEveryDeclaredAction(t *testing.T) {
	declared := actionsDeclaredInSource(t)
	// ANTI-VACUITY: eight Actions are declared today.
	if len(declared) != 8 {
		t.Fatalf("found %d Action constants in types.go (%v), want 8 — either the sweep is "+
			"blind or an Action was added/removed, and the table below must be re-decided",
			len(declared), declared)
	}
	// Cross-check against the other in-package enumeration so a constant declared outside the
	// const block the sweep reads is at least noticed when it gets a CSS class.
	if len(ActionCSS) != len(declared) {
		t.Errorf("ActionCSS has %d keys and types.go declares %d Actions", len(ActionCSS), len(declared))
	}

	var classified int
	for _, a := range declared {
		want, ok := epExpectedThesis[a]
		if !ok {
			t.Errorf("Action %q is declared and has no row in epExpectedThesis. Whoever adds an "+
				"Action classifies it in entryPlanThesis: CONTRADICTED only if the user's "+
				"decision says so, otherwise say explicitly why it is NOT_CONTRADICTED or left "+
				"unclassified", a)
			continue
		}
		if got := entryPlanThesis(a); got != want.standing {
			t.Errorf("%q → %q, want %q — %s", a, got, want.standing, want.why)
		}
		classified++
	}
	if classified != len(declared) {
		t.Errorf("classified %d of %d declared Actions", classified, len(declared))
	}
	for a := range epExpectedThesis {
		var found bool
		for _, d := range declared {
			if d == a {
				found = true
			}
		}
		if !found {
			t.Errorf("epExpectedThesis holds %q, which types.go no longer declares", a)
		}
	}

	// The CONTRADICTED set is EXACTLY the user's two, derived from the production mapping.
	var contradicted []string
	for _, a := range declared {
		if entryPlanThesis(a) == entryplan.ThesisContradicted {
			contradicted = append(contradicted, string(a))
		}
	}
	sort.Strings(contradicted)
	if !reflect.DeepEqual(contradicted, []string{"REDUCE", "SELL", "STOP LOSS", "TAKE PROFIT"}) {
		t.Errorf("the CONTRADICTED Actions are %v, want exactly [REDUCE SELL STOP LOSS TAKE PROFIT] — the user's "+
			"decision names those four and no others", contradicted)
	}
}

// MISSING ≠ ZERO: an Action nobody set, or one this switch has never seen, is UNCLASSIFIED —
// never NOT_CONTRADICTED.
func TestEntryPlanThesisRefusesToGuessAboutUnnamedActions(t *testing.T) {
	for _, a := range []Action{"", Action("LIQUIDATE"), Action("buy")} {
		if got := entryPlanThesis(a); got != "" {
			t.Errorf("Action %q → %q, want \"\" (unclassified) — an Action this bridge cannot "+
				"read is not evidence that the scanner does not want out", a, got)
		}
	}
}

// THE ACTION REACHES THE PLAN, through the exported attach.
//
// The fixture is epEntry with its pivot lowered to the last close, so a BULL breakout plan has
// the market standing on the floor of its own band: a priced BUY_NOW when the Action does not
// contradict. Every declared Action (plus "" and an unknown one) is then run through the same
// attach:
//
//	NOT_CONTRADICTED  → BUY_NOW, prices identical to the control
//	CONTRADICTED      → NO_VALID_ENTRY with NO executable price
//	unclassified      → INSUFFICIENT_DATA, prices identical to the control
func TestEntryPlanActionDecidesTheThesisOutcome(t *testing.T) {
	bull := EntryPlanMarket{Available: true, Regime: string(model.RegimeBull)}
	fixture := func(a Action) *entryplan.Plan {
		e := epEntry("2330")
		e.Consol.PivotHigh = 100
		e.A.Action = a
		entries := []WatchlistEntry{e}
		AttachEntryPlan(entries, epNoSeries, bull, true)
		return entries[0].EntryPlan
	}

	control := fixture(ActionHold)
	if control == nil || control.Status != entryplan.StatusBuyNow || control.IdealEntry == nil {
		t.Fatalf("control (HOLD) plan = %+v, want a priced BUY_NOW — every row below would "+
			"compare against the wrong baseline", control)
	}

	rows := map[Action]entryplan.ThesisStanding{"": "", Action("LIQUIDATE"): ""}
	for _, a := range actionsDeclaredInSource(t) {
		rows[a] = epExpectedThesis[a].standing
	}

	seen := map[Action]bool{}
	for a, standing := range rows {
		p := fixture(a)
		if p == nil {
			t.Fatalf("%q: no plan", a)
		}
		seen[a] = true
		samePrices := reflect.DeepEqual(p.IdealEntry, control.IdealEntry) &&
			reflect.DeepEqual(p.MaxChasePrice, control.MaxChasePrice) &&
			reflect.DeepEqual(p.Invalidation, control.Invalidation) &&
			reflect.DeepEqual(p.Target1, control.Target1) &&
			reflect.DeepEqual(p.Target2, control.Target2) &&
			reflect.DeepEqual(p.RiskReward, control.RiskReward)
		switch standing {
		case entryplan.ThesisContradicted:
			if p.Status != entryplan.StatusNoValidEntry {
				t.Errorf("Action %q → %q, want NO_VALID_ENTRY", a, p.Status)
			}
			if p.IdealEntry != nil || p.MaxChasePrice != nil || p.Invalidation != nil ||
				p.Target1 != nil || p.Target2 != nil || p.RiskReward != nil {
				t.Errorf("Action %q: a stock the scanner wants out of still publishes a price "+
					"(zone %v chase %v stop %v t1 %v)", a, p.IdealEntry, p.MaxChasePrice,
					p.Invalidation, p.Target1)
			}
		case entryplan.ThesisNotContradicted:
			if p.Status != entryplan.StatusBuyNow || !samePrices {
				t.Errorf("Action %q → %q (prices same as control: %v), want BUY_NOW with the "+
					"control's prices", a, p.Status, samePrices)
			}
		default:
			if p.Status != entryplan.StatusInsufficientData || !samePrices {
				t.Errorf("Action %q → %q (prices same as control: %v), want INSUFFICIENT_DATA "+
					"with the control's prices — unclassified only removes BUY_NOW", a, p.Status,
					samePrices)
			}
		}
	}
	for _, a := range []Action{ActionSell, ActionReduce, ActionTakeProfit, ActionStopLoss} {
		if !seen[a] {
			t.Fatalf("the sweep did not run %q — the protection was never exercised", a)
		}
	}
}
