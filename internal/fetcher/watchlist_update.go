package fetcher

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WatchCandidate is a stock proposed for the watchlist (typically a BUY/WATCH
// result from a scan). Only Code and Name are written.
type WatchCandidate struct {
	Code string
	Name string
}

// UpdateWatchlistFile REBUILDS the machine watchlist in stocks.yaml from one scan's
// candidates, and returns the resulting entries.
//
// Rebuild, not append. The previous append-only behaviour turned the watchlist into a
// ratchet: it had reached 748 entries against a 66-entry daily signal, so 92% of the
// "shortlist" was sediment from earlier scans that no longer qualified. A shortlist nobody
// can read through is the same as no shortlist.
//
// What this function will NOT touch, by design:
//
//	positions / portfolio  Real money. Human-owned. Never written, and their codes are
//	                       excluded from the watchlist so one stock is never reported twice.
//	watchlist_pinned       Your own ideas. Human-owned. Survives every rebuild; its codes
//	                       are excluded from the machine list to avoid duplication.
//	comments, key order    Preserved by editing the yaml.Node tree rather than re-encoding
//	                       a typed struct.
//
// The file is rewritten only when the resulting watchlist differs from the existing one, so
// a re-run with the same candidates is a no-op and leaves no diff.
func UpdateWatchlistFile(path string, candidates []WatchCandidate) ([]WatchCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Locate (or create) the root mapping node.
	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	if root == nil || root.Kind != yaml.MappingNode {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
	}

	// Codes that must never gain a watchlist entry: existing positions.
	blocked := map[string]bool{}
	collectCodes(mapValue(root, "positions"), blocked)
	collectCodes(mapValue(root, "portfolio"), blocked) // legacy alias

	// Locate (or create) the watchlist sequence node.
	watch := mapValue(root, "watchlist")
	if watch == nil || watch.Kind != yaml.SequenceNode {
		watch = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "watchlist"}
		root.Content = append(root.Content, key, watch)
	}
	// An empty candidate set is NOT an instruction to empty the watchlist. A scan that
	// produced nothing and a scan that failed to produce anything look identical from here
	// (a partial fetch, a network problem, an exchange holiday), and wiping the shortlist on
	// an ambiguous signal is destructive. An empty watchlist is never useful information to
	// persist, so the file is left exactly as it was.
	if len(candidates) == 0 {
		return nil, nil
	}

	// Pinned entries are human-owned and stay out of the machine list, so a stock you pinned
	// is never duplicated here and never silently deleted from there.
	collectCodes(mapValue(root, "watchlist_pinned"), blocked)

	// Snapshot what is there now, purely to decide whether a write is needed.
	before := map[string]bool{}
	collectCodes(watch, before)

	kept := make([]*yaml.Node, 0, len(candidates))
	seen := map[string]bool{}
	var result []WatchCandidate
	for _, c := range candidates {
		if c.Code == "" || blocked[c.Code] || seen[c.Code] {
			continue
		}
		seen[c.Code] = true
		kept = append(kept, newWatchEntryNode(c.Code, c.Name))
		result = append(result, c)
	}
	// REBUILD: the sequence becomes exactly today's candidates. Anything that no longer
	// qualifies is dropped rather than accumulated.
	watch.Content = kept

	if sameCodes(before, seen) {
		return result, nil // identical set — leave the file untouched
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) // match the 2-space indentation used in stocks.yaml
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}

	// Atomic write: temp file in the same dir, then rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("replace %s: %w", path, err)
	}
	return result, nil
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// collectCodes records the "code" value of every mapping item in a sequence node.
func collectCodes(seq *yaml.Node, into map[string]bool) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "code" {
				into[item.Content[i+1].Value] = true
			}
		}
	}
}

// newWatchEntryNode builds a `{code, name}` mapping node, quoting both values to
// match the style used throughout stocks.yaml.
func newWatchEntryNode(code, name string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "code"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: code, Style: yaml.DoubleQuotedStyle},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name, Style: yaml.DoubleQuotedStyle},
	)
	return m
}

// sameCodes reports whether two code sets are identical, so an unchanged rebuild produces no
// write and therefore no git diff.
func sameCodes(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
