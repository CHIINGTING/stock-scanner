package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Big5 handling is allowed in exactly one file. This is an architectural boundary, not a
// style preference: the daily path must stay dependency-free, and an encoding concern
// leaking into an analyzer would make the domain logic care where a number came from.
//
// Enforced by a test rather than by a comment, because a comment cannot fail a build.
func TestBig5IsContainedToBackfill(t *testing.T) {
	const allowed = "taifex_backfill.go"
	root := filepath.Join("..", "..", "..", "internal")

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Test files are excluded: they do not shape the production dependency graph, and
		// this very file has to mention the import path in order to check for it.
		if filepath.Base(path) == allowed || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), "golang.org/x/text") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("x/text may only be imported by %s, found in: %v", allowed, offenders)
	}
}

// The analyzer and model layers must not import the provider layer either — data flows
// provider → raw → analyzer, never back.
func TestAnalyzerAndModelDoNotImportProvider(t *testing.T) {
	for _, dir := range []string{
		filepath.Join("..", "analyzer"),
		filepath.Join("..", "model"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(b), "market/provider") {
				t.Errorf("%s/%s imports the provider layer — data must flow one way", dir, e.Name())
			}
		}
	}
}
