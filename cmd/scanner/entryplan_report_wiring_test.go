package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// EP-8. STRUCTURAL: the report's ⑲ display flag is wired from the config's show_entry_plan and
// from nothing else. main() builds report.GuardrailViewOptions inline, so this parses main.go and
// checks the composite literal rather than calling main. Behaviour of the section itself is
// pinned in internal/report/report_entryplan_test.go.
func TestReportShowEntryPlanIsWiredFromConfig(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals, wired := 0, 0
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := cl.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "GuardrailViewOptions" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "report" {
			return true
		}
		literals++
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "ShowEntryPlan" {
				continue
			}
			// want cfg.Scanner.ShowEntryPlan
			outer, ok := kv.Value.(*ast.SelectorExpr)
			if !ok {
				t.Errorf("ShowEntryPlan is wired from a %T, want cfg.Scanner.ShowEntryPlan", kv.Value)
				continue
			}
			if outer.Sel.Name != "ShowEntryPlan" {
				t.Errorf("ShowEntryPlan is wired from field %s, want cfg.Scanner.ShowEntryPlan", outer.Sel.Name)
				continue
			}
			inner, ok := outer.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "Scanner" {
				t.Errorf("ShowEntryPlan is not read from cfg.Scanner")
				continue
			}
			if id, ok := inner.X.(*ast.Ident); !ok || id.Name != "cfg" {
				t.Errorf("ShowEntryPlan is not read from cfg")
				continue
			}
			wired++
		}
		return true
	})
	if literals != 1 {
		t.Fatalf("found %d report.GuardrailViewOptions literals in main.go, want 1", literals)
	}
	if wired != 1 {
		t.Fatalf("report.GuardrailViewOptions sets ShowEntryPlan from cfg.Scanner.ShowEntryPlan %d times, want 1", wired)
	}
}
