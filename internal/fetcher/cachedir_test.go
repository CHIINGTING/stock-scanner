package fetcher

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The guard's own tests. It moved here from cmd/health-dashboard so two binaries could share
// one implementation; its coverage has to move with it rather than being borrowed from
// whichever caller happens to exercise it.

func TestResolveCacheDirRefusesTheReserved(t *testing.T) {
	abs, err := filepath.Abs(".cache")
	if err != nil {
		t.Fatal(err)
	}
	reject := []struct{ name, requested, reserved string }{
		{"identical", ".cache", ".cache"},
		{"single dot", "./.cache", ".cache"},
		{"double dot", "././.cache", ".cache"},
		{"trailing slash", ".cache/", ".cache"},
		{"relative vs absolute", ".cache", abs},
		{"absolute vs relative", abs, ".cache"},
		{"traversal", "foo/../.cache", ".cache"},
		// A blank reserved value resolves to DefaultCacheDir, which is the directory being
		// protected — treating blank as "nothing to protect" reopens the hole for exactly
		// the configurations that omit the key.
		{"blank reserved", ".cache", ""},
	}
	for _, tc := range reject {
		if _, err := ResolveCacheDir(tc.requested, tc.reserved, ".cache-other"); err == nil {
			t.Errorf("%s: (%q, %q) was accepted", tc.name, tc.requested, tc.reserved)
		}
	}
	accept := []struct{ name, requested, reserved string }{
		{"a sibling", ".cache-research", ".cache"},
		{"blank request takes the fallback", "", ".cache"},
		{"near miss", ".cache2", ".cache"},
		{"absolute elsewhere", "/tmp/x-cache", ".cache"},
	}
	for _, tc := range accept {
		got, err := ResolveCacheDir(tc.requested, tc.reserved, ".cache-research")
		if err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
			continue
		}
		if SameDir(got.Dir, got.Reserved) {
			t.Errorf("%s: resolved the reserved directory", tc.name)
		}
	}
}

// With no fallback and no request there is nothing to resolve, and defaulting to
// DefaultCacheDir would hand back the very directory the guard exists to refuse.
func TestResolveCacheDirNeedsAFallback(t *testing.T) {
	if _, err := ResolveCacheDir("", ".cache", ""); err == nil {
		t.Fatal("an empty request with no fallback was accepted")
	}
}

// os.SameFile is what closes the case-insensitive and symlink routes; string comparison alone
// lets both through.
func TestSameDirCatchesWhatStringsCannot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(real, link); err == nil {
		if !SameDir(link, real) {
			t.Error("a symlink to the directory was not recognised")
		}
	}
	upper := filepath.Join(dir, ".CACHE")
	if _, err := os.Stat(upper); err == nil {
		if !SameDir(upper, real) {
			t.Error("a case variant was not recognised on a case-insensitive filesystem")
		}
	} else if runtime.GOOS != "linux" {
		t.Logf("cannot reach %s: %v", upper, err)
	}
	other := filepath.Join(dir, ".cache-research")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if SameDir(other, real) {
		t.Error("two distinct directories were reported as the same")
	}
}
