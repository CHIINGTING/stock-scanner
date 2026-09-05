package stockcatalog

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/news"
	"gopkg.in/yaml.v3"
)

// Default source paths. They are the files that already exist in this repo; only the overlay
// is new, and it is optional.
const (
	DefaultNamesFile   = "data/stock_names_zh.yaml"
	DefaultSectorsFile = "configs/sectors.yaml"
	DefaultAliasesFile = "configs/news_aliases.yaml"
	DefaultOverlayFile = "configs/stock_catalog.yaml"
)

// Sources names the files a catalog is composed from. An empty field means "use the default";
// the string "-" means "skip this source entirely", which is what tests use to isolate one
// layer without needing the whole repo on disk.
type Sources struct {
	NamesFile   string
	SectorsFile string
	AliasesFile string
	OverlayFile string
}

// DefaultSources is the repo layout.
func DefaultSources() Sources {
	return Sources{
		NamesFile:   DefaultNamesFile,
		SectorsFile: DefaultSectorsFile,
		AliasesFile: DefaultAliasesFile,
		OverlayFile: DefaultOverlayFile,
	}
}

func (s Sources) defaulted() Sources {
	if s.NamesFile == "" {
		s.NamesFile = DefaultNamesFile
	}
	if s.SectorsFile == "" {
		s.SectorsFile = DefaultSectorsFile
	}
	if s.AliasesFile == "" {
		s.AliasesFile = DefaultAliasesFile
	}
	if s.OverlayFile == "" {
		s.OverlayFile = DefaultOverlayFile
	}
	return s
}

// skipped reports whether a path was explicitly disabled.
func skipped(path string) bool { return path == "-" }

// OverlayStock is one entry of configs/stock_catalog.yaml.
//
// Every field except Symbol is optional, and every one of them is something no other source
// in this repo records. The overlay is deliberately not a place to restate names that
// data/stock_names_zh.yaml already carries — doing so would create the two-lists-that-
// disagree problem this package exists to avoid — but it MAY carry a name, for a listing the
// generated file predates.
type OverlayStock struct {
	Symbol   string   `yaml:"symbol"`
	Name     string   `yaml:"name,omitempty"`
	Market   string   `yaml:"market,omitempty"`
	Industry string   `yaml:"industry,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Aliases  []string `yaml:"aliases,omitempty"`
	Tags     []string `yaml:"tags,omitempty"`
}

// Overlay is the parsed overlay file.
type Overlay struct {
	Stocks []OverlayStock `yaml:"stocks"`
}

// Load composes and validates the catalog.
//
// Layering is base-first, overlay-last, and every layer only ADDS what the previous one could
// not know:
//
//  1. names     code → official name, and nothing else
//  2. sectors   industry membership; a market where a human typed one
//  3. aliases   the news module's existing alias dictionary
//  4. overlay   market / aliases / tags / enabled / industry, and new listings
//
// A missing sectors, aliases or overlay file is NOT an error — the catalog degrades to fewer
// attributes, which is a smaller failure than refusing to start. A missing names file is only
// an error when nothing else produced any stock, because a caller may legitimately run on an
// overlay alone (the tests do).
func Load(src Sources) (*Catalog, error) {
	src = src.defaulted()

	drafts := map[string]*Stock{}
	origin := map[string]string{}

	if !skipped(src.NamesFile) {
		if err := loadNames(src.NamesFile, drafts, origin); err != nil {
			return nil, err
		}
	}
	if !skipped(src.SectorsFile) {
		if err := loadSectors(src.SectorsFile, drafts, origin); err != nil {
			return nil, err
		}
	}
	if !skipped(src.AliasesFile) {
		if err := loadAliases(src.AliasesFile, drafts); err != nil {
			return nil, err
		}
	}
	if !skipped(src.OverlayFile) {
		if err := loadOverlay(src.OverlayFile, drafts, origin); err != nil {
			return nil, err
		}
	}
	return build(drafts, origin)
}

// loadNames reads the generated code → official-name map.
//
// The file is a flat YAML mapping, which is why it is decoded into map[string]string rather
// than a struct: it has no schema beyond "code: name", and inventing one here would mean a
// regenerated file could stop parsing.
func loadNames(path string, drafts map[string]*Stock, origin map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stockcatalog: read %s: %w", path, err)
	}
	var m map[string]string
	if err := yaml.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("stockcatalog: parse %s: %w", path, err)
	}
	for rawCode, name := range m {
		code := normaliseCode(rawCode)
		name = strings.TrimSpace(name)
		if code == "" || name == "" {
			continue
		}
		if !validCode(code) {
			return fmt.Errorf("stockcatalog: %s: %q is not a Taiwan stock code", path, rawCode)
		}
		if _, dup := drafts[code]; dup {
			// Two entries for one code in a generated file means the generator is broken,
			// and every later lookup would silently get whichever won the map iteration.
			return fmt.Errorf("stockcatalog: %s: %s appears twice", path, code)
		}
		drafts[code] = &Stock{Code: code, Name: name, Enabled: true, NameSource: path}
		origin[code] = path
	}
	return nil
}

// loadSectors folds in industry membership and any market a human typed.
//
// It reuses fetcher.LoadSectorList rather than parsing sectors.yaml a second time: that file
// has one owner, and a second parser would be free to disagree with it about what a sector is.
func loadSectors(path string, drafts map[string]*Stock, origin map[string]string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stockcatalog: stat %s: %w", path, err)
	}
	list, err := fetcher.LoadSectorList(path)
	if err != nil {
		return fmt.Errorf("stockcatalog: %w", err)
	}
	for _, sec := range list.Sectors {
		sector := strings.TrimSpace(sec.Name)
		for _, st := range sec.Stocks {
			code := normaliseCode(st.Code)
			if code == "" || !validCode(code) {
				continue
			}
			d, ok := drafts[code]
			if !ok {
				name := strings.TrimSpace(st.Name)
				if name == "" {
					// A sector member with no name and no entry in the name map cannot be
					// given an identity, and inventing one is the failure this package is
					// about. Skipping it is the honest outcome.
					continue
				}
				d = &Stock{Code: code, Name: name, Enabled: true, NameSource: path}
				drafts[code] = d
				origin[code] = path
			}
			if sector != "" {
				d.Sectors = append(d.Sectors, sector)
				if d.Industry == "" {
					d.Industry = sector
				}
			}
			// A market typed in sectors.yaml is a stated fact; the file leaves it blank when
			// the human wanted auto-detection, and blank must stay unknown.
			if m, err := normaliseMarket(st.Market); err == nil && m != "" && d.Market == "" {
				d.Market = m
			}
		}
	}
	return nil
}

// loadAliases folds in the news module's alias dictionary.
//
// news.LoadAliases is reused deliberately: configs/news_aliases.yaml already maps 別名 → 代號,
// a human maintains it, and a second alias file would be a second thing to keep in sync.
func loadAliases(path string, drafts map[string]*Stock) error {
	d, err := news.LoadAliases(path)
	if err != nil {
		return fmt.Errorf("stockcatalog: load %s: %w", path, err)
	}
	if d == nil {
		return nil
	}
	// Map iteration is random, so the aliases are applied in sorted order — otherwise the
	// Aliases slice of a stock with two aliases would differ between two loads of the same
	// file, and so would every listing built from it.
	keys := make([]string, 0, len(d.StockAliases))
	for alias := range d.StockAliases {
		keys = append(keys, alias)
	}
	sort.Strings(keys)
	for _, alias := range keys {
		code := normaliseCode(d.StockAliases[alias])
		st, ok := drafts[code]
		if !ok {
			// An alias for a stock the catalog does not hold is not an error: the news
			// dictionary covers companies mentioned on a podcast, not only listed ones.
			continue
		}
		st.Aliases = append(st.Aliases, strings.TrimSpace(alias))
	}
	return nil
}

// loadOverlay applies the human-maintained R14 overlay.
func loadOverlay(path string, drafts map[string]*Stock, origin map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stockcatalog: read %s: %w", path, err)
	}
	// KnownFields turns a typo into an error instead of a silent no-op: `alias:` would
	// otherwise load as an entry with no aliases, and the human would never be told why
	// their search stopped working.
	var ov Overlay
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&ov); err != nil {
		return fmt.Errorf("stockcatalog: parse %s: %w", path, err)
	}

	seen := map[string]int{}
	for i, o := range ov.Stocks {
		sym := strings.TrimSpace(o.Symbol)
		if sym == "" {
			return fmt.Errorf("stockcatalog: %s: entry %d has no symbol", path, i+1)
		}
		code := normaliseCode(sym)
		market := ""
		// Both "2330" and "2330.TW" are accepted; the second states the market at the same
		// time, which is exactly what most overlay entries exist to do.
		if c, m, err := fetcher.ParseYahooSymbol(sym); err == nil {
			code, market = normaliseCode(c), m
		} else if !validCode(code) {
			return fmt.Errorf("stockcatalog: %s: entry %d: %q is not a Taiwan stock code",
				path, i+1, o.Symbol)
		}
		if first, dup := seen[code]; dup {
			return fmt.Errorf("stockcatalog: %s: %s appears twice (entry %d and entry %d)",
				path, code, first+1, i+1)
		}
		seen[code] = i

		if o.Market != "" {
			m, err := normaliseMarket(o.Market)
			if err != nil {
				return fmt.Errorf("stockcatalog: %s: entry %d (%s): %w", path, i+1, code, err)
			}
			if market != "" && m != market {
				return fmt.Errorf("stockcatalog: %s: entry %d (%s): symbol says market %s but "+
					"market: says %s", path, i+1, code, market, m)
			}
			market = m
		}

		d, ok := drafts[code]
		if !ok {
			if strings.TrimSpace(o.Name) == "" {
				return fmt.Errorf("stockcatalog: %s: entry %d (%s) is not in %s, so it needs a "+
					"name", path, i+1, code, DefaultNamesFile)
			}
			d = &Stock{Code: code, Enabled: true, NameSource: path}
			drafts[code] = d
			origin[code] = path
		}
		if n := strings.TrimSpace(o.Name); n != "" {
			// An overlay name that CONTRADICTS the official one is refused rather than
			// applied. Silently overriding it is precisely how "2330 / 鴻海" would be born.
			if d.Name != "" && d.Name != n {
				return fmt.Errorf("stockcatalog: %s: entry %d: %s is %q in %s but %q here — "+
					"identity may not be overridden", path, i+1, code, d.Name, d.NameSource, n)
			}
			d.Name = n
		}
		if market != "" {
			d.Market = market
		}
		if o.Industry != "" {
			d.Industry = strings.TrimSpace(o.Industry)
		}
		if o.Enabled != nil {
			d.Enabled = *o.Enabled
		}
		d.Aliases = append(d.Aliases, o.Aliases...)
		d.Tags = append(d.Tags, o.Tags...)
	}
	return nil
}

// normaliseMarket accepts the several spellings a human might write and returns the repo's
// internal code. An unrecognised market is an ERROR, never a default: "2330.US" silently
// becoming TWSE is the class of bug fetcher.ParseYahooSymbol already refuses to allow.
func normaliseMarket(m string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "":
		return "", nil
	case "TW", "TWSE", "上市":
		return fetcher.MarketTWSE, nil
	case "TWO", "TPEX", "OTC", "上櫃":
		return fetcher.MarketTPEX, nil
	default:
		return "", fmt.Errorf("market %q is not supported (want TWSE/TW or TPEX/TWO)", m)
	}
}

// validCode reuses the fetcher's own idea of a plausible Taiwan listing, so the catalog and
// the price layer cannot disagree about what a code is.
func validCode(code string) bool {
	_, _, err := fetcher.ParseYahooSymbol(code + "." + fetcher.MarketTWSE)
	return err == nil
}
