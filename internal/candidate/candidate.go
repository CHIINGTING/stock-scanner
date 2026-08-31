// Package candidate is the Candidate Research universe: a small, hand-maintained list of
// stocks worth studying closely.
//
// # Why this is not the watchlist
//
// stocks.yaml already has two lists, and neither answers this question. `positions` is real
// money. `watchlist` is a MACHINE snapshot — rebuilt every scan, so anything that stops
// qualifying today is gone tomorrow. Both are about what the scanner found.
//
// A research universe is the opposite: it is what a HUMAN decided to keep looking at, and it
// must survive a scan that did not surface the stock. Folding it into the watchlist would
// make the machine's daily rebuild delete the human's list, which is exactly the failure the
// watchlist/watchlist_pinned split was created to stop.
//
// So candidate.yaml is a separate file, read by a separate command, and the scanner does not
// know it exists. Nothing here can reach a score, an action, a ranking, or stocks.yaml.
//
// # Read-only
//
// This package never writes. Normalisation happens in memory; the file on disk is exactly
// what the human typed. A tool that quietly rewrote a config a human maintains would make
// every later diff untrustworthy.
package candidate

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"gopkg.in/yaml.v3"
)

// Candidate is one stock in the research universe.
//
// Two fields, on purpose. Anything a tool can derive — a score, a stage, a target — belongs
// to the analysis, not to the config: a number typed here would be an opinion frozen at the
// moment of typing, and would then have to be maintained by hand forever.
type Candidate struct {
	// Symbol is the ticker as written, e.g. "2330.TW". Kept verbatim so an error message can
	// quote what the human actually typed.
	Symbol string `yaml:"symbol"`
	// Note is free-form user metadata. It is carried through to the research output and is
	// never read by any calculation.
	Note string `yaml:"note,omitempty"`

	// Code and Market are the canonical form every other package in this repo uses. Filled
	// by the loader; not read from YAML.
	Code   string `yaml:"-"`
	Market string `yaml:"-"`
}

// CanonicalSymbol is the normalised ticker, e.g. "2330.TW" for an input of "2330.tw".
func (c Candidate) CanonicalSymbol() string { return fetcher.YahooSymbol(c.Code, c.Market) }

// List is the parsed candidate.yaml.
type List struct {
	// Candidates keeps the file's order. That order is a human's priority statement and
	// nothing downstream may re-sort it — see the package doc for the ranking that is
	// deliberately absent.
	Candidates []Candidate `yaml:"candidates"`
}

// ErrNotFound reports that the candidate file does not exist.
//
// A distinct error because the caller's correct response differs: a missing file means the
// research universe was never configured (fine — say so and stop), while a malformed one
// means it was configured wrongly (fix it). Collapsing the two would make "you have no
// candidates" and "your config is broken" the same message.
type ErrNotFound struct {
	Path string
}

func (e *ErrNotFound) Error() string { return "candidate file not found: " + e.Path }

// Load reads and validates a candidate file.
//
// Validation order is deliberate: shape first, then canonical form, then duplicates. Checking
// duplicates on the RAW strings would let "2330.tw" and "2330.TW" through as two stocks; they
// are one, and the file has a mistake in it.
func Load(path string) (*List, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrNotFound{Path: path}
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// KnownFields makes a typo an error instead of a silent no-op. A file saying `sybmol:`
	// would otherwise load as a candidate with an empty symbol, and the human would be told
	// their symbol is empty rather than that they misspelled the key.
	var raw List
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	out := &List{Candidates: make([]Candidate, 0, len(raw.Candidates))}
	seen := make(map[string]int, len(raw.Candidates))

	for i, c := range raw.Candidates {
		if c.Symbol == "" {
			return nil, fmt.Errorf("%s: candidate %d has no symbol", path, i+1)
		}
		code, market, err := fetcher.ParseYahooSymbol(c.Symbol)
		if err != nil {
			return nil, fmt.Errorf("%s: candidate %d: %w", path, i+1, err)
		}
		c.Code, c.Market = code, market

		// Duplicates are an ERROR, not something to quietly collapse. This is a short list a
		// human maintains by hand; the same stock appearing twice is a mistake worth seeing,
		// and silently keeping one copy would hide it forever.
		canon := c.CanonicalSymbol()
		if first, dup := seen[canon]; dup {
			return nil, fmt.Errorf("%s: %s appears twice (candidate %d and candidate %d)",
				path, canon, first+1, i+1)
		}
		seen[canon] = i
		out.Candidates = append(out.Candidates, c)
	}
	return out, nil
}

// IsNotFound reports whether err is a missing-file error.
func IsNotFound(err error) bool {
	var nf *ErrNotFound
	return errors.As(err, &nf)
}
