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

// UpdateWatchlistFile appends new watchlist candidates to stocks.yaml in place.
//
// Guarantees:
//   - The positions: (and legacy portfolio:) section is never modified.
//   - A candidate already present in positions/portfolio is skipped.
//   - A candidate already present in watchlist is skipped (no duplicates).
//   - Comments are preserved by round-tripping through a yaml.Node tree rather
//     than the typed struct, so only the watchlist sequence gains new entries.
//
// It returns the candidates actually added. The file is rewritten (atomically)
// only when at least one entry is added; if nothing is added the file is left
// untouched and (nil, nil) is returned.
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
	existing := map[string]bool{}
	collectCodes(watch, existing)

	var added []WatchCandidate
	for _, c := range candidates {
		if c.Code == "" {
			continue
		}
		if blocked[c.Code] || existing[c.Code] {
			continue
		}
		watch.Content = append(watch.Content, newWatchEntryNode(c.Code, c.Name))
		existing[c.Code] = true
		added = append(added, c)
	}

	if len(added) == 0 {
		return nil, nil
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
	return added, nil
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
