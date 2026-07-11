package news

import (
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ResolveIndex maps free-text mentions to stock codes and sectors using an EXPLICIT
// dictionary only. It never fuzzy-guesses: a company name that is not in the dictionary
// stays unresolved and unbound (StockReference.Resolved=false), and unknown sector words
// are ignored. The stock/sector universe is injected by the caller (from fetcher data);
// the theme/alias layer comes from the news module's own aliases file.
type ResolveIndex struct {
	nameToCode    map[string]string // exact company name / alias → code
	codeToName    map[string]string // code → canonical name (first seen)
	sectorNames   []string          // known sector display names (e.g. 被動元件)
	themeToSector map[string]string // theme keyword (e.g. CCL) → sector (e.g. 銅箔基板)
	codeToSectors map[string][]string
}

// NewResolveIndex returns an empty index ready for AddStock/AddSector/ApplyAliases.
func NewResolveIndex() *ResolveIndex {
	return &ResolveIndex{
		nameToCode:    map[string]string{},
		codeToName:    map[string]string{},
		themeToSector: map[string]string{},
		codeToSectors: map[string][]string{},
	}
}

// AddStock registers a code↔name pair (from sectors.yaml / stocks.yaml). Names shorter
// than 2 runes are skipped to avoid spurious substring matches.
func (r *ResolveIndex) AddStock(code, name string) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	if code == "" {
		return
	}
	if _, ok := r.codeToName[code]; !ok && name != "" {
		r.codeToName[code] = name
	}
	if runeLen(name) >= 2 {
		r.nameToCode[name] = code
	}
}

// AddStockSector records that a code belongs to a sector (for divergence lookup).
func (r *ResolveIndex) AddStockSector(code, sector string) {
	code, sector = strings.TrimSpace(code), strings.TrimSpace(sector)
	if code == "" || sector == "" {
		return
	}
	for _, s := range r.codeToSectors[code] {
		if s == sector {
			return
		}
	}
	r.codeToSectors[code] = append(r.codeToSectors[code], sector)
}

// AddSectorName registers a sector display name for direct text matching.
func (r *ResolveIndex) AddSectorName(name string) {
	name = strings.TrimSpace(name)
	if runeLen(name) < 2 {
		return
	}
	for _, s := range r.sectorNames {
		if s == name {
			return
		}
	}
	r.sectorNames = append(r.sectorNames, name)
}

// AliasDict is the parsed news_aliases.yaml — the news module's own dictionary.
type AliasDict struct {
	StockAliases map[string]string `yaml:"stock_aliases"` // alias/name → code
	Themes       map[string]string `yaml:"themes"`        // theme keyword → sector
}

// LoadAliases reads the aliases file. A missing path is not an error (returns an empty
// dict) so the module runs with dictionary-from-fetcher only.
func LoadAliases(path string) (*AliasDict, error) {
	if path == "" {
		return &AliasDict{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AliasDict{}, nil
		}
		return nil, err
	}
	var d AliasDict
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ApplyAliases folds the alias dictionary into the index.
func (r *ResolveIndex) ApplyAliases(d *AliasDict) {
	if d == nil {
		return
	}
	for alias, code := range d.StockAliases {
		r.AddStock(code, alias)
	}
	for theme, sector := range d.Themes {
		theme, sector = strings.TrimSpace(theme), strings.TrimSpace(sector)
		if theme == "" || sector == "" {
			continue
		}
		r.themeToSector[theme] = sector
		r.AddSectorName(sector)
	}
}

// ResolveStocks returns the resolved stock references mentioned in text. Only
// dictionary hits are returned; unknown names are never invented. Deterministic order.
func (r *ResolveIndex) ResolveStocks(text string) []StockReference {
	seen := map[string]bool{}
	var out []StockReference
	names := make([]string, 0, len(r.nameToCode))
	for n := range r.nameToCode {
		names = append(names, n)
	}
	// Longer names first so 華新科 wins over a hypothetical 華新 substring.
	sort.Slice(names, func(i, j int) bool { return runeLen(names[i]) > runeLen(names[j]) })
	for _, n := range names {
		if strings.Contains(text, n) {
			code := r.nameToCode[n]
			if seen[code] {
				continue
			}
			seen[code] = true
			out = append(out, StockReference{Code: code, Name: r.canonicalName(code, n), Resolved: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ResolveSectors returns sector labels mentioned in text, via direct sector-name match
// or theme keyword (CCL → 銅箔基板). Deterministic, de-duplicated.
func (r *ResolveIndex) ResolveSectors(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, name := range r.sectorNames {
		if strings.Contains(text, name) {
			add(name)
		}
	}
	for theme, sector := range r.themeToSector {
		if strings.Contains(text, theme) {
			add(sector)
		}
	}
	sort.Strings(out)
	return out
}

// SectorsForCode returns the sectors a code belongs to (for divergence detection).
func (r *ResolveIndex) SectorsForCode(code string) []string { return r.codeToSectors[code] }

func (r *ResolveIndex) canonicalName(code, fallback string) string {
	if n, ok := r.codeToName[code]; ok && n != "" {
		return n
	}
	return fallback
}

func runeLen(s string) int { return len([]rune(s)) }
