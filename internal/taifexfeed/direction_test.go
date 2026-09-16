package taifexfeed_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The dependency direction that makes ONE decode possible at all.
//
// internal/derivatives must stay a leaf of the decision path — that is
// internal/derivatives/architecture_test.go, and it is what keeps a default-off research layer
// (and the only package in R15 that opens sockets) out of internal/scanner, internal/report,
// internal/candidate and internal/ai.
//
// That test cannot see this one's failure mode. It checks the four DECISION packages, and none
// of them depends on internal/market/provider today, so `internal/market/provider` importing
// `internal/derivatives/provider` compiles, creates no cycle, and leaves the whole suite green
// — verified by mutation. It would nonetheless be the wrong fix for FU-10: it puts a networked,
// default-off research package one import away from the market snapshot, and the day something
// on the decision path starts reading a market provider (cmd/market-fetch and
// internal/market/service already do) the isolation is gone with no test to say so.
//
// So the direction is asserted HERE, at the shared package, where the constraint actually
// lives: a neutral package importable by both and importing neither is the ONLY shape that
// lets the two readers share a decode without one depending on the other.
const (
	marketProvider = "github.com/deep-huang/stock-scanner/internal/market/provider"
	sharedDecode   = "github.com/deep-huang/stock-scanner/internal/taifexfeed"
	marketPrefix   = "github.com/deep-huang/stock-scanner/internal/market/"
	r15Prefix      = "github.com/deep-huang/stock-scanner/internal/derivatives"
)

func deps(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			set[s] = true
		}
	}
	return set
}

func TestTheMarketProviderDoesNotDependOnR15(t *testing.T) {
	for dep := range deps(t, marketProvider) {
		if strings.HasPrefix(dep, r15Prefix) {
			t.Errorf("%s depends on %s — sharing a decode must not be done by making the "+
				"market snapshot import a default-off research layer whose acquire package "+
				"makes live HTTP calls. The shared decode is %s, which imports neither.",
				marketProvider, dep, sharedDecode)
		}
	}
}

// The other direction, for the package where it means something.
//
// Scoped to internal/derivatives/provider — the R15 READER — and not to internal/derivatives
// or internal/derivatives/acquire, which reach internal/market transitively through
// internal/dailydata (§1.4: R15 reuses its session logic, and that predates this change). That
// transitive edge is a scheduling dependency and carries no token policy with it; what must
// never happen is the PARSE path acquiring internal/market's ""/"-" -> 0 answer, which would
// erase the absence/zero distinction the whole layer rests on.
func TestTheR15ReaderDoesNotDependOnTheMarketProvider(t *testing.T) {
	const pkg = "github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	for dep := range deps(t, pkg) {
		if strings.HasPrefix(dep, marketPrefix) {
			t.Errorf("%s depends on %s — the other direction is no better: it would drag "+
				"internal/market's absence policy (\"\"/\"-\" -> 0) into a layer whose "+
				"whole point is that \"-\" and 0 are different observations", pkg, dep)
		}
	}
}

// The shared package is NEUTRAL, which is the property that makes both of the above
// satisfiable at once. If it ever imports either reader, one of them acquires the other
// transitively and the two tests above start failing for a reason no one will look for here.
func TestTheSharedDecodeIsNeutral(t *testing.T) {
	for dep := range deps(t, sharedDecode) {
		if strings.HasPrefix(dep, marketPrefix) || strings.HasPrefix(dep, r15Prefix) {
			t.Errorf("%s depends on %s — a shared package that imports one of its consumers "+
				"is not shared, it is a cycle waiting to be discovered", sharedDecode, dep)
		}
	}
}

// The converse: both readers really do go through the shared decode. Without this the three
// tests above pass perfectly for two packages that have nothing to do with each other, which
// is the state this change was made to leave behind.
func TestBothReadersGoThroughTheSharedDecode(t *testing.T) {
	for _, pkg := range []string{
		marketProvider,
		"github.com/deep-huang/stock-scanner/internal/derivatives/provider",
	} {
		if !deps(t, pkg)[sharedDecode] {
			t.Errorf("%s does not depend on %s — it has stopped using the one decode, so the "+
				"direction assertions above are guarding a relationship that no longer exists",
				pkg, sharedDecode)
		}
	}
}
