package derivatives_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// §2.4 and §1.5, enforced rather than asserted.
//
// R15 is default-off and shadow-only, and the property that makes that true is a dependency
// fact: the decision path cannot reach the derivatives layer, so it cannot reach a network
// call either. Report generation, scanner decisions and the AI judge must answer from what is
// already archived.
//
// This test exists because the guarantee was previously written in a comment. `acquire/http.go`
// said "hence architecture_test.go, which fails if internal/report or internal/scanner ever
// reaches it" — and no such file existed. The only isolation test checked `./internal/scanner`
// against two of the three derivatives packages, and never checked `internal/report` at all, so
// `internal/report` importing `internal/derivatives/acquire` — R15's ONLY networked package —
// compiled and left the whole suite green.
//
// A comment describing a test that does not exist is worse than no comment: it is a claim a
// reader will not re-derive.

// consumers are the packages that must never reach R15.
//
// `./internal/scanner` and NOT `./cmd/scanner`: §1.5 is explicit that
// cmd/scanner → internal/report → internal/derivatives is the INTENDED wiring for the report
// section, so asserting it there would fail by design. The line is drawn at the decision
// packages themselves.
var consumers = []string{
	"./internal/scanner",
	"./internal/report",
	"./internal/candidate",
	"./internal/ai",
}

// forbidden is every R15 package. `acquire` matters most — it is the one that opens sockets —
// and it is exactly the one the previous check omitted.
var forbidden = []string{
	"github.com/deep-huang/stock-scanner/internal/derivatives",
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider",
	"github.com/deep-huang/stock-scanner/internal/derivatives/acquire",
	// M4's domain layer. It opens no sockets, which is exactly the argument that would
	// have kept it off this list — and the same argument was made for `provider` before
	// `acquire` turned out to be reachable through it. The list is every R15 package.
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional",
	// M4's point-in-time reader. It imports both the pure layer and the persistence layer,
	// which is the whole reason it exists as its own package — and which also makes it the
	// shortest path from a consumer to internal/store and, through internal/derivatives,
	// to everything R15 archives.
	"github.com/deep-huang/stock-scanner/internal/derivatives/history",
	// M5's pure domain layer: the PCR and options-structure contract. On the list for the
	// same reason `institutional` is — "it opens no sockets" is the argument that would
	// have kept `provider` off it, and `acquire` turned out to be reachable through
	// `provider`. The list is every R15 package, without exception, so that adding one is
	// a decision rather than an omission.
	"github.com/deep-huang/stock-scanner/internal/derivatives/options",
	// M5's point-in-time reader, the options-side sibling of `history` and the same
	// shortest path from a consumer to internal/store.
	"github.com/deep-huang/stock-scanner/internal/derivatives/optionshistory",
}

func TestDecisionPathCannotReachTheDerivativesLayer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range consumers {
		t.Run(strings.TrimPrefix(consumer, "./internal/"), func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", consumer)
			cmd.Dir = root
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps %s: %v", consumer, err)
			}
			deps := map[string]bool{}
			for _, line := range strings.Split(string(out), "\n") {
				deps[strings.TrimSpace(line)] = true
			}
			for _, pkg := range forbidden {
				if deps[pkg] {
					t.Errorf("%s depends on %s — a default-off research layer has reached "+
						"the decision path, and with it a package that makes live HTTP calls",
						consumer, pkg)
				}
			}
		})
	}
}

// The converse, so the test above cannot pass by naming packages that do not exist: every
// forbidden path must be a real, buildable package.
func TestTheForbiddenPackagesExist(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range forbidden {
		cmd := exec.Command("go", "list", pkg)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Errorf("%s does not build — the isolation check above is asserting against a "+
				"name that no longer exists, and would pass for the wrong reason", pkg)
		}
	}
}
