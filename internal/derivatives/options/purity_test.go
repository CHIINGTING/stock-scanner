package options_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheOptionsLayerStaysPure — M5's domain layer is computable from a slice in memory.
//
// The same check M4 has, for the same reason: internal/derivatives pulls in modernc.org/sqlite
// through internal/store, and one convenience import would put a SQL driver behind every pure
// function here. The persistence-backed reader lives in internal/derivatives/optionshistory,
// which imports BOTH and is therefore the only place the two may meet.
func TestTheOptionsLayerStaysPure(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "./internal/derivatives/options")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}
	for _, pkg := range []string{
		"database/sql",
		"net/http",
		"github.com/deep-huang/stock-scanner/internal/store",
		"github.com/deep-huang/stock-scanner/internal/derivatives",
		"github.com/deep-huang/stock-scanner/internal/derivatives/acquire",
		"github.com/deep-huang/stock-scanner/internal/derivatives/history",
	} {
		if deps[pkg] {
			t.Errorf("internal/derivatives/options depends on %s — M5's domain layer is pure "+
				"and this is how it stops being one", pkg)
		}
	}
}
