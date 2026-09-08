package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/service"
)

// mainFile is the file under assertion. Both groups of tests below read it as source rather
// than as a linked package, for reasons spelled out at TestMainConstructsOnlyTheCacheFeed.
const mainFile = "main.go"

// parseMain parses main.go from the package directory (the working directory of `go test`).
func parseMain(t *testing.T) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mainFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", mainFile, err)
	}
	return fset, f
}

// ---------------------------------------------------------------------------
// GROUP 1 — the -out guard, asserted AT THE POINT OF USE.
//
// internal/market/service.TestDefaultReplayDirIsNotTheSnapshotDir already asserts the
// CONSTANT: DefaultReplayDir != "data/market". That is necessary and demonstrably not
// sufficient. Changing this command's flag default from service.DefaultReplayDir to the
// literal "data/market" compiles, leaves that test green, leaves the whole suite green — and
// dumps 364 regime_*.json files into data/market/, which IS version-controlled and which no
// market_*.json glob would ever show you. So the default is asserted where the CLI reads it,
// and the guard function is asserted on the inputs that reach it.
// ---------------------------------------------------------------------------

// outFlagDefault returns the expression main() passes as the default value of -out, together
// with the string it resolves to.
//
// The default is registered inside main(), which a test cannot call, so it is read out of the
// syntax tree instead of out of a flag.FlagSet. Only two shapes are resolvable — a string
// literal, or a selector on the service package — which is exactly the distinction this test
// needs to make.
func outFlagDefault(t *testing.T, f *ast.File) (expr ast.Expr, value string, isLiteral bool) {
	t.Helper()

	// The constants of internal/market/service that a default could legitimately name. Values
	// come from the real package, so a rename there fails compilation here rather than
	// silently un-asserting the test.
	known := map[string]string{
		"DefaultReplayDir":   service.DefaultReplayDir,
		"DefaultSnapshotDir": service.DefaultSnapshotDir,
	}

	var found ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "String" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "flag" {
			return true
		}
		name, ok := call.Args[0].(*ast.BasicLit)
		if !ok || name.Kind != token.STRING {
			return true
		}
		if unquote(t, name.Value) == "out" {
			found = call.Args[1]
			return false
		}
		return true
	})
	if found == nil {
		t.Fatalf("%s registers no flag.String(\"out\", ...): the -out flag this test guards is gone "+
			"or was renamed; re-point the test at whatever replaced it before deleting it", mainFile)
	}

	switch v := found.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			t.Fatalf("-out default is a %s literal, not a string", v.Kind)
		}
		return found, unquote(t, v.Value), true
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		if !ok || pkg.Name != "service" {
			t.Fatalf("-out default is a selector on %s, which this test cannot resolve; it only "+
				"knows the internal/market/service constants", exprString(v.X))
		}
		val, ok := known[v.Sel.Name]
		if !ok {
			t.Fatalf("-out default is service.%s, which this test does not know; add it to `known` "+
				"(with its real value) so the assertions below stay meaningful", v.Sel.Name)
		}
		return found, val, false
	default:
		t.Fatalf("-out default is a %T this test cannot resolve; it must be a string literal or a "+
			"constant of internal/market/service", found)
		return nil, "", false
	}
}

// The default -out must be the replay archive, and must not be the live one.
//
// This is the assertion that kills the one mutation that survived the review round: replacing
// service.DefaultReplayDir with the literal "data/market" at the flag registration.
func TestOutFlagDefaultIsTheReplayDir(t *testing.T) {
	_, f := parseMain(t)
	expr, value, _ := outFlagDefault(t, f)

	if value != service.DefaultReplayDir {
		t.Errorf("-out defaults to %q (source: %s); want service.DefaultReplayDir = %q",
			value, exprString(expr), service.DefaultReplayDir)
	}
	if value == service.DefaultSnapshotDir {
		t.Fatalf("-out DEFAULTS TO THE LIVE SNAPSHOT ARCHIVE %q. Running the command with no "+
			"flags would write every replayed session into the version-controlled live archive, "+
			"where a market_*.json glob will never show them to you", service.DefaultSnapshotDir)
	}
	// Same comparison the guard makes, so a default of "data/market/" or "./data/market" —
	// which is not string-equal to DefaultSnapshotDir but resolves to it — is caught too.
	if filepath.Clean(value) == filepath.Clean(service.DefaultSnapshotDir) {
		t.Fatalf("-out default %q resolves to the live snapshot archive %q",
			value, service.DefaultSnapshotDir)
	}
}

// The default must NAME the constant rather than repeat its value.
//
// A literal "data/market_replay" would pass the value check above and still be wrong: it is
// the shape that let the mutation in, because a literal is one keystroke away from a
// different literal and nothing in the type system objects. replay_store.go says the same
// thing about the guard itself — "so that the guard in cmd/market-regime-replay compares
// against a named thing instead of a string literal" — and the default deserves the same
// treatment, since it is the value 99% of runs actually use.
func TestOutFlagDefaultNamesTheConstant(t *testing.T) {
	_, f := parseMain(t)
	expr, _, isLiteral := outFlagDefault(t, f)
	if isLiteral {
		t.Errorf("-out default is the string literal %s; use service.DefaultReplayDir so the "+
			"CLI and the archive writer cannot drift apart", exprString(expr))
	}
}

// refuseSnapshotDir must reject every spelling of the live archive and accept everything else.
//
// The accepted half is not filler. A guard written as strings.HasPrefix(clean, live) rejects
// the live directory AND "data/market_replay", i.e. it breaks the command's own default while
// looking like a stricter version of the right check. Both halves have to be asserted for the
// test to tell a correct equality check apart from an over-eager prefix check.
func TestRefuseSnapshotDir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	rejected := []struct{ in, why string }{
		{service.DefaultSnapshotDir, "the live archive, spelled exactly"},
		{"data/market", "the live archive, spelled literally"},
		{"data/market/", "trailing separator: filepath.Clean removes it"},
		{"./data/market", "leading ./ : filepath.Clean removes it"},
		{"data/market/../market", "a detour through .. that Clean collapses"},
		{"data/market_replay/../market", "the default's parent, then back into the live archive"},
		// The absolute branch: -out is absolute, the constant is relative, and both resolve
		// against this process's working directory.
		{filepath.Join(wd, "data", "market"), "an absolute path to the same directory"},
	}
	for _, tc := range rejected {
		if err := refuseSnapshotDir(tc.in); err == nil {
			t.Errorf("refuseSnapshotDir(%q) = nil, want an error (%s)", tc.in, tc.why)
		} else if !strings.Contains(err.Error(), service.DefaultReplayDir) {
			// The error is the only thing the caller sees, so it has to say where to write
			// instead, not just that this was wrong.
			t.Errorf("refuseSnapshotDir(%q) error does not name the replay dir %q: %v",
				tc.in, service.DefaultReplayDir, err)
		}
	}

	accepted := []struct{ in, why string }{
		{service.DefaultReplayDir, "the command's own default"},
		{"data/market_replay", "the default, spelled literally"},
		{"data/market_replay/", "the default with a trailing separator"},
		{"./data/market_replay", "the default with a leading ./"},
		{"data/market-replay", "a neighbouring name that shares the live prefix"},
		{"data/marketing", "a name that HasPrefix would also reject"},
		{t.TempDir(), "an absolute scratch directory"},
		{"data", "the live archive's PARENT is not the live archive"},
	}
	for _, tc := range accepted {
		if err := refuseSnapshotDir(tc.in); err != nil {
			t.Errorf("refuseSnapshotDir(%q) = %v, want nil (%s); a prefix comparison instead of "+
				"an equality comparison would produce exactly this failure", tc.in, err, tc.why)
		}
	}
}

// ---------------------------------------------------------------------------
// GROUP 2 — the offline claim, executable.
//
// main.go's package comment asserts that the only provider this file constructs is
// provider.CacheFeed, and told the reader to "verify by reading the imports below and run()".
// internal/derivatives/architecture_test.go states this repo's position on that arrangement:
// "A comment describing a test that does not exist is worse than no comment: it is a claim a
// reader will not re-derive." So the claim is re-derived here, mechanically.
//
// WHAT THIS TEST PROVES, EXACTLY: that the source of cmd/market-regime-replay/main.go
// constructs no networked provider and imports neither net/http nor internal/fetcher.
//
// WHAT IT DOES NOT PROVE: that the binary cannot reach the network. It certainly can —
// internal/market/provider holds the TWSE and TAIFEX HTTP clients alongside CacheFeed, so
// importing the package for CacheFeed links net/http (and, transitively, internal/fetcher)
// into the executable regardless of what this file does. A `go list -deps` assertion of the
// kind internal/derivatives/architecture_test.go uses is therefore IMPOSSIBLE to write for
// this command as the packages are laid out today: the dependency is real, only the call is
// absent.
//
// The root fix is to split CacheFeed into its own package with no HTTP client in it, at which
// point the guarantee becomes a dependency fact and `go list -deps ./cmd/market-regime-replay`
// can be asserted directly. That is a follow-up and deliberately out of scope for this round;
// until it happens, the assertion below is file-scoped syntax and should be read as such.
//
// One more limit worth stating: this checks main.go only. If this command ever grows a second
// file, that file is unasserted until someone extends `mainFile` here.
// ---------------------------------------------------------------------------

// networkedIdents are identifiers whose appearance anywhere in main.go would mean the file
// reaches an exchange. Matched against ast.Ident names, so the package comment's prose
// mention of "provider.TWSE or provider.TAIFEX" is not a false positive.
var networkedIdents = map[string]string{
	"TWSEProvider":      "the TWSE HTTP client",
	"NewTWSEProvider":   "the TWSE HTTP client constructor",
	"TAIFEXProvider":    "the TAIFEX HTTP client",
	"NewTAIFEXProvider": "the TAIFEX HTTP client constructor",
	"TAIFEXBackfill":    "the TAIFEX backfill HTTP client",
	"NewTAIFEXBackfill": "the TAIFEX backfill HTTP client constructor",
	"NetProvider":       "a networked provider",
}

// The only provider constructed here is provider.CacheFeed.
//
// Asserted as an ALLOWLIST rather than a denylist of known-bad names: a provider added to
// internal/market/provider tomorrow would not be in any denylist written today, and the whole
// point of the offline claim is that it holds for providers nobody has written yet.
func TestMainConstructsOnlyTheCacheFeed(t *testing.T) {
	_, f := parseMain(t)

	const allowed = "CacheFeed"
	var used []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "provider" {
			return true
		}
		used = append(used, sel.Sel.Name)
		if sel.Sel.Name != allowed {
			t.Errorf("main.go references provider.%s. The only provider this command may use is "+
				"provider.%s, which reads local JSON; anything else makes a replay of past "+
				"sessions depend on a live exchange", sel.Sel.Name, allowed)
		}
		return true
	})
	if len(used) == 0 {
		t.Fatalf("main.go references no provider.* symbol at all. Either the command stopped "+
			"reading the cache through internal/market/provider, or this test is looking at the "+
			"wrong file (%s) and is now asserting nothing", mainFile)
	}

	// Belt and braces for shapes the allowlist above cannot see: a dot-import, an aliased
	// import, or a bare identifier copied in from elsewhere.
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if why, bad := networkedIdents[id.Name]; bad {
			t.Errorf("main.go mentions %s (%s); this command must not construct it", id.Name, why)
		}
		return true
	})
}

// main.go must import neither net/http nor internal/fetcher.
//
// This is weaker than it looks and the weakness is the point: net/http is in the linked binary
// anyway, via internal/market/provider's exchange clients. What this rules out is THIS file
// growing a direct HTTP call or a fetcher hand-off — the way the offline property would
// realistically be lost, one import at a time.
func TestMainImportsNoNetworkPackage(t *testing.T) {
	_, f := parseMain(t)

	banned := map[string]string{
		"net/http": "a direct HTTP client in the replay command",
		"github.com/deep-huang/stock-scanner/internal/fetcher": "the price fetcher; a replay reads " +
			".cache, it does not refill it",
	}
	var paths []string
	for _, imp := range f.Imports {
		p := unquote(t, imp.Path.Value)
		paths = append(paths, p)
		if why, bad := banned[p]; bad {
			t.Errorf("main.go imports %q: %s", p, why)
		}
	}
	if len(paths) == 0 {
		t.Fatalf("main.go has no imports; %s is almost certainly not the file this test means "+
			"to read", mainFile)
	}
}

// ---------------------------------------------------------------------------

func unquote(t *testing.T, lit string) string {
	t.Helper()
	s, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatalf("unquote %s: %v", lit, err)
	}
	return s
}

// exprString renders an expression for error messages. Only the shapes this test can
// encounter are handled; anything else falls back to the Go type, which is still enough for a
// reader to find the line.
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}

// TestRunRefusesTheSnapshotDirBeforeWritingAnything is the PRODUCTION WIRING test.
//
// Every other assertion in this file tests refuseSnapshotDir as a component. None of them
// tests that run() CALLS it: delete the `if err := refuseSnapshotDir(outDir)` block from
// main.go and TestRefuseSnapshotDir stays green, because the function it exercises is still
// there and still correct. That is the exact failure this repo has a standing rule about —
// removing production wiring must break a behavioural test, and testing the component alone
// is not sufficient.
//
// The mechanism: the guard is the FIRST statement in run(), ahead of every read and write.
// So pointing -out at the live archive while handing run() a cache directory that cannot
// possibly work must still fail with the GUARD's error, not with a cache error. Asserting on
// which error comes back is what makes this a wiring test rather than a "does it fail" test —
// an unwired run() reaches the cache and reports that instead.
func TestRunRefusesTheSnapshotDirBeforeWritingAnything(t *testing.T) {
	// A directory with no *.json in it: CacheFeed cannot load a benchmark from it, so if the
	// guard is ever removed this call fails for a DIFFERENT, recognisable reason.
	emptyCache := t.TempDir()

	for _, outDir := range []string{
		service.DefaultSnapshotDir,
		service.DefaultSnapshotDir + "/",
		"./" + service.DefaultSnapshotDir,
	} {
		t.Run(outDir, func(t *testing.T) {
			// dryRun=true so that even a fully unwired run() writes nothing to data/market
			// while this test runs. The guard must fire regardless of dryRun: a dry run that
			// accepted the live archive would still be one edit away from a real one.
			err := run(emptyCache, outDir, "", "", true)
			if err == nil {
				t.Fatalf("run() accepted -out %q, the live snapshot archive", outDir)
			}
			if !strings.Contains(err.Error(), service.DefaultReplayDir) {
				t.Fatalf("run() failed with %q, which does not name %s — the guard is not wired "+
					"into run(); this looks like a failure from the cache read further down, "+
					"which means -out was never checked at all",
					err, service.DefaultReplayDir)
			}
		})
	}
}

// The converse, so the test above cannot pass merely because run() rejects everything: a
// legitimate -out must get PAST the guard and fail later, on the cache.
func TestRunGetsPastTheGuardForALegitimateOutDir(t *testing.T) {
	emptyCache := t.TempDir()
	err := run(emptyCache, t.TempDir(), "", "", true)
	if err == nil {
		t.Fatal("run() succeeded against an empty cache directory")
	}
	if strings.Contains(err.Error(), service.DefaultReplayDir) {
		t.Fatalf("a temp -out was refused by the snapshot-dir guard: %v", err)
	}
}
