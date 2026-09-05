package stockcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// write drops a file into a temp dir and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// overlayOnly builds Sources that use ONLY the given overlay, so a test states its whole
// universe inline instead of depending on the 1,980-line repo file.
func overlayOnly(path string) Sources {
	return Sources{NamesFile: "-", SectorsFile: "-", AliasesFile: "-", OverlayFile: path}
}

const sampleOverlay = `
stocks:
  - symbol: "2330.TW"
    name: "台積電"
    industry: "半導體"
    aliases: [TSMC, 台積]
    tags: [ai, foundry]
  - symbol: "2317"
    name: "鴻海"
    market: TWSE
    aliases: [Foxconn]
  - symbol: "6488.TWO"
    name: "環球晶"
  - symbol: "1234"
    name: "停用股"
    enabled: false
  - symbol: "9999"
    name: "無市場股"
`

func loadSample(t *testing.T) *Catalog {
	t.Helper()
	dir := t.TempDir()
	c, err := Load(overlayOnly(write(t, dir, "overlay.yaml", sampleOverlay)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestValidOverlayLoads(t *testing.T) {
	c := loadSample(t)
	if got := c.Len(); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}
	s, ok := c.Lookup("2330")
	if !ok {
		t.Fatal("2330 not found")
	}
	if s.Name != "台積電" || s.Market != fetcher.MarketTWSE || s.Industry != "半導體" {
		t.Fatalf("2330 = %+v", s)
	}
	if s.MarketSource != MarketFromCatalog {
		t.Fatalf("MarketSource = %s, want CATALOG", s.MarketSource)
	}
	if sym, ok := s.CanonicalSymbol(); !ok || sym != "2330.TW" {
		t.Fatalf("CanonicalSymbol = %q,%v", sym, ok)
	}
	if s.MarketLabel() != LabelTWSE {
		t.Fatalf("MarketLabel = %s", s.MarketLabel())
	}
}

// An unknown market must stay unknown. Defaulting it to TWSE would send every archive lookup
// for an OTC company to the wrong filename and report "no data" for a stock that has plenty.
func TestUnknownMarketIsNotGuessed(t *testing.T) {
	c := loadSample(t)
	s, _ := c.Lookup("9999")
	if s.Market != "" {
		t.Fatalf("Market = %q, want empty", s.Market)
	}
	if s.MarketSource != MarketUnknown {
		t.Fatalf("MarketSource = %s, want UNKNOWN", s.MarketSource)
	}
	if _, ok := s.CanonicalSymbol(); ok {
		t.Fatal("CanonicalSymbol must not be available without a market")
	}
	if s.MarketLabel() != LabelUnknown {
		t.Fatalf("MarketLabel = %s", s.MarketLabel())
	}
}

func TestMarketSpellings(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"TWSE", fetcher.MarketTWSE}, {"tw", fetcher.MarketTWSE}, {"上市", fetcher.MarketTWSE},
		{"TPEX", fetcher.MarketTPEX}, {"two", fetcher.MarketTPEX}, {"OTC", fetcher.MarketTPEX},
		{"上櫃", fetcher.MarketTPEX}, {"", ""},
	} {
		got, err := normaliseMarket(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("normaliseMarket(%q) = %q,%v want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := normaliseMarket("NYSE"); err == nil {
		t.Fatal("NYSE must be refused, not defaulted")
	}
}

func TestDuplicateSymbolIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", `
stocks:
  - symbol: "2330"
    name: "台積電"
  - symbol: "2330.TW"
    name: "台積電"
`)
	_, err := Load(overlayOnly(p))
	if err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("err = %v, want a duplicate-symbol error", err)
	}
}

// The identity failure this package exists to prevent: an overlay may not rename a stock the
// official name map already identifies.
func TestOverlayMayNotContradictTheOfficialName(t *testing.T) {
	dir := t.TempDir()
	names := write(t, dir, "names.yaml", "\"2330\": \"台積電\"\n")
	overlay := write(t, dir, "o.yaml", "stocks:\n  - symbol: \"2330\"\n    name: \"鴻海\"\n")
	_, err := Load(Sources{NamesFile: names, SectorsFile: "-", AliasesFile: "-", OverlayFile: overlay})
	if err == nil || !strings.Contains(err.Error(), "identity may not be overridden") {
		t.Fatalf("err = %v, want an identity-override error", err)
	}
}

func TestAliasClaimedByTwoStocksIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", `
stocks:
  - symbol: "2330"
    name: "台積電"
    aliases: [晶圓龍頭]
  - symbol: "2303"
    name: "聯電"
    aliases: [晶圓龍頭]
`)
	_, err := Load(overlayOnly(p))
	if err == nil || !strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("err = %v, want an ambiguous-alias error", err)
	}
}

func TestAliasMayNotShadowAnotherStocksCode(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", `
stocks:
  - symbol: "2330"
    name: "台積電"
    aliases: ["2317"]
  - symbol: "2317"
    name: "鴻海"
`)
	_, err := Load(overlayOnly(p))
	if err == nil || !strings.Contains(err.Error(), "is the code of") {
		t.Fatalf("err = %v, want an alias/code collision error", err)
	}
}

func TestInvalidYAMLIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", "stocks:\n  - symbol: \"2330\"\n   name: broken indent\n")
	if _, err := Load(overlayOnly(p)); err == nil {
		t.Fatal("malformed YAML must be an error")
	}
}

// A misspelled key must fail loudly. Without KnownFields it would load as an entry with no
// aliases and the human would never learn why their search stopped working.
func TestUnknownFieldIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", "stocks:\n  - symbol: \"2330\"\n    name: \"台積電\"\n    alias: [TSMC]\n")
	_, err := Load(overlayOnly(p))
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("err = %v, want a typo error naming the bad field", err)
	}
}

func TestEntryWithoutNameOrNameMapIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", "stocks:\n  - symbol: \"2330\"\n")
	if _, err := Load(overlayOnly(p)); err == nil {
		t.Fatal("an unknown stock with no name must be an error")
	}
}

func TestEmptyCatalogIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", "stocks: []\n")
	if _, err := Load(overlayOnly(p)); err == nil {
		t.Fatal("an empty catalog must be an error, not a silently useless dashboard")
	}
}

// ── search ────────────────────────────────────────────────────────────────────────────

func kinds(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Stock.Code + ":" + string(m.Kind)
	}
	return out
}

func TestSymbolSearch(t *testing.T) {
	c := loadSample(t)
	got := c.Search("2330", SearchOptions{})
	if len(got) != 1 || got[0].Stock.Code != "2330" || got[0].Kind != MatchSymbolExact {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestPartialSymbolSearch(t *testing.T) {
	c := loadSample(t)
	got := c.Search("23", SearchOptions{})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2317 and 2330", kinds(got))
	}
	// Ordering is (kind, code): both are SYMBOL_PREFIX, so the codes decide.
	if got[0].Stock.Code != "2317" || got[1].Stock.Code != "2330" {
		t.Fatalf("order = %v, want 2317 then 2330", kinds(got))
	}
}

func TestNameSearch(t *testing.T) {
	c := loadSample(t)
	got := c.Search("台積電", SearchOptions{})
	if len(got) != 1 || got[0].Stock.Code != "2330" || got[0].Kind != MatchNameExact {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestPartialNameSearch(t *testing.T) {
	c := loadSample(t)
	got := c.Search("台積", SearchOptions{})
	if len(got) != 1 || got[0].Stock.Code != "2330" {
		t.Fatalf("got %v", kinds(got))
	}
	// 台積 is BOTH a name prefix and an exact alias; the better of the two wins and the stock
	// appears once.
	if got[0].Kind != MatchAliasExact {
		t.Fatalf("kind = %s, want ALIAS_EXACT (the stronger of the two matches)", got[0].Kind)
	}
}

func TestAliasSearchIsCaseInsensitiveForASCII(t *testing.T) {
	c := loadSample(t)
	for _, q := range []string{"TSMC", "tsmc", "TsMc", " tsmc "} {
		got := c.Search(q, SearchOptions{})
		if len(got) != 1 || got[0].Stock.Code != "2330" || got[0].Kind != MatchAliasExact {
			t.Fatalf("Search(%q) = %v", q, kinds(got))
		}
	}
}

func TestDisabledStockIsHiddenFromSearchButStillResolvable(t *testing.T) {
	c := loadSample(t)
	if got := c.Search("停用股", SearchOptions{}); len(got) != 0 {
		t.Fatalf("disabled stock leaked into search: %v", kinds(got))
	}
	if got := c.Search("停用股", SearchOptions{IncludeDisabled: true}); len(got) != 1 {
		t.Fatalf("IncludeDisabled did not surface it: %v", kinds(got))
	}
	// Asked directly, the honest answer is the stock — "hidden" and "does not exist" differ.
	if _, ok := c.Lookup("1234"); !ok {
		t.Fatal("a disabled stock must still be findable by exact code")
	}
}

func TestEmptyQueryReturnsNothing(t *testing.T) {
	c := loadSample(t)
	for _, q := range []string{"", "   ", "\t"} {
		if got := c.Search(q, SearchOptions{}); len(got) != 0 {
			t.Fatalf("Search(%q) returned %d results; a blank box is not a request", q, len(got))
		}
	}
}

func TestSearchOrderingIsDeterministic(t *testing.T) {
	c := loadSample(t)
	first := kinds(c.Search("2", SearchOptions{}))
	for i := 0; i < 20; i++ {
		if got := kinds(c.Search("2", SearchOptions{})); !equalStrings(got, first) {
			t.Fatalf("run %d = %v, first = %v", i, got, first)
		}
	}
}

func TestSearchLimit(t *testing.T) {
	c := loadSample(t)
	if got := c.Search("2", SearchOptions{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit ignored: %d results", len(got))
	}
}

// ── identity resolution ───────────────────────────────────────────────────────────────

func TestResolveAcceptsCodeSymbolAndAlias(t *testing.T) {
	c := loadSample(t)
	for _, q := range []string{"2330", "2330.TW", "2330.tw", "TSMC", "台積"} {
		s, ok := c.Resolve(q)
		if !ok || s.Code != "2330" {
			t.Fatalf("Resolve(%q) = %+v,%v", q, s, ok)
		}
	}
}

// A name is not an identity: 65 names in the repo's own map belong to two listings each.
func TestResolveRefusesANonUniqueName(t *testing.T) {
	c := loadSample(t)
	if _, ok := c.Resolve("台積電"); ok {
		t.Fatal("Resolve must not accept a name; names are not unique")
	}
}

func TestResolveRefusesAMismatchedMarket(t *testing.T) {
	c := loadSample(t)
	if _, ok := c.Resolve("2330.TWO"); ok {
		t.Fatal("2330 is TWSE; a symbol naming TPEx must not resolve to it")
	}
}

func TestResolveUnknown(t *testing.T) {
	c := loadSample(t)
	for _, q := range []string{"", "0000", "not-a-stock", "9998"} {
		if _, ok := c.Resolve(q); ok {
			t.Fatalf("Resolve(%q) should have failed", q)
		}
	}
}

// ── composition ───────────────────────────────────────────────────────────────────────

func TestSectorsSupplyIndustryAndOverlaySuppliesTheRest(t *testing.T) {
	dir := t.TempDir()
	names := write(t, dir, "names.yaml", "\"2330\": \"台積電\"\n\"6488\": \"環球晶\"\n")
	sectors := write(t, dir, "sectors.yaml", `
sectors:
  - name: "半導體"
    stocks:
      - { code: "2330", name: "台積電" }
  - name: "矽晶圓"
    stocks:
      - { code: "6488", name: "環球晶", market: "TWO" }
`)
	aliases := write(t, dir, "aliases.yaml", "stock_aliases:\n  台積: \"2330\"\n")
	overlay := write(t, dir, "o.yaml", "stocks:\n  - symbol: \"2330.TW\"\n    aliases: [TSMC]\n    tags: [ai]\n")

	c, err := Load(Sources{NamesFile: names, SectorsFile: sectors, AliasesFile: aliases, OverlayFile: overlay})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tsmc, _ := c.Lookup("2330")
	if tsmc.Industry != "半導體" {
		t.Fatalf("Industry = %q", tsmc.Industry)
	}
	if len(tsmc.Aliases) != 2 {
		t.Fatalf("Aliases = %v, want the news dictionary's 台積 plus the overlay's TSMC", tsmc.Aliases)
	}
	if len(tsmc.Tags) != 1 || tsmc.Tags[0] != "ai" {
		t.Fatalf("Tags = %v", tsmc.Tags)
	}
	if tsmc.Market != fetcher.MarketTWSE {
		t.Fatalf("Market = %q", tsmc.Market)
	}
	// The market typed in sectors.yaml is a stated fact and must survive with no overlay entry.
	gw, _ := c.Lookup("6488")
	if gw.Market != fetcher.MarketTPEX {
		t.Fatalf("6488 market = %q, want TWO from sectors.yaml", gw.Market)
	}
}

// Two loads of the same files must produce identical catalogs. Alias order comes out of a
// YAML map, whose iteration order Go randomises — so this fails without an explicit sort.
func TestLoadIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	names := write(t, dir, "names.yaml", "\"2330\": \"台積電\"\n")
	aliases := write(t, dir, "aliases.yaml", "stock_aliases:\n  台積: \"2330\"\n  積電: \"2330\"\n  護國神山: \"2330\"\n")
	src := Sources{NamesFile: names, SectorsFile: "-", AliasesFile: aliases, OverlayFile: "-"}

	first, err := Load(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want, _ := first.Lookup("2330")
	for i := 0; i < 15; i++ {
		c, err := Load(src)
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		got, _ := c.Lookup("2330")
		if !equalStrings(got.Aliases, want.Aliases) {
			t.Fatalf("run %d aliases = %v, first = %v", i, got.Aliases, want.Aliases)
		}
	}
}

func TestMissingOptionalSourcesDegradeRatherThanFail(t *testing.T) {
	dir := t.TempDir()
	names := write(t, dir, "names.yaml", "\"2330\": \"台積電\"\n")
	c, err := Load(Sources{
		NamesFile:   names,
		SectorsFile: filepath.Join(dir, "nope-sectors.yaml"),
		AliasesFile: filepath.Join(dir, "nope-aliases.yaml"),
		OverlayFile: filepath.Join(dir, "nope-overlay.yaml"),
	})
	if err != nil {
		t.Fatalf("missing optional sources must not fail: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d", c.Len())
	}
}

// The real repo files must load and agree with themselves — this is what catches a bad
// commit to configs/stock_catalog.yaml before the dashboard does.
func TestRepoCatalogLoads(t *testing.T) {
	root := "../.."
	src := Sources{
		NamesFile:   filepath.Join(root, DefaultNamesFile),
		SectorsFile: filepath.Join(root, DefaultSectorsFile),
		AliasesFile: filepath.Join(root, DefaultAliasesFile),
		OverlayFile: filepath.Join(root, DefaultOverlayFile),
	}
	c, err := Load(src)
	if err != nil {
		t.Fatalf("the repo's own catalog does not load: %v", err)
	}
	if c.Len() < 1000 {
		t.Fatalf("Len = %d, expected the full listed universe", c.Len())
	}
	s, ok := c.Lookup("2330")
	if !ok || s.Name != "台積電" || s.Market != fetcher.MarketTWSE {
		t.Fatalf("2330 = %+v, ok=%v", s, ok)
	}
	// The acceptance criterion from the spec: typing 2330 finds 台積電.
	got := c.Search("2330", SearchOptions{})
	if len(got) == 0 || got[0].Stock.Name != "台積電" {
		t.Fatalf("Search(2330) = %v", kinds(got))
	}
	if got := c.Search("TSMC", SearchOptions{}); len(got) == 0 || got[0].Stock.Code != "2330" {
		t.Fatalf("Search(TSMC) = %v", kinds(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── review follow-ups (R14-M1) ────────────────────────────────────────────────────────

// The identity failure the first draft of configs/stock_catalog.yaml actually contained:
// 聯發 is 1459's official name, and attaching it to 2454 as an alias made Resolve("聯發")
// return 2454 while a search for 聯發 ranked 1459 BELOW it (ALIAS_EXACT beats NAME_EXACT).
func TestAliasMayNotShadowAnotherStocksOfficialName(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", `
stocks:
  - symbol: "2454"
    name: "聯發科"
    aliases: [聯發]
  - symbol: "1459"
    name: "聯發"
`)
	_, err := Load(overlayOnly(p))
	if err == nil || !strings.Contains(err.Error(), "is the official name of") {
		t.Fatalf("err = %v, want an alias-shadows-name error", err)
	}
}

// The check must fire regardless of load order — the shadowed stock may sort after the one
// carrying the alias, which is why byName is built before the alias pass.
func TestAliasShadowIsCaughtInEitherOrder(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "o.yaml", `
stocks:
  - symbol: "1459"
    name: "聯發"
  - symbol: "2454"
    name: "聯發科"
    aliases: [聯發]
`)
	if _, err := Load(overlayOnly(p)); err == nil {
		t.Fatal("alias shadowing must be caught whichever entry comes first")
	}
}

// A returned Stock must not share backing arrays with the catalog: the catalog is read by
// concurrent HTTP handlers, and a caller writing into a result would be a data race.
func TestReturnedStocksAreDeepCopies(t *testing.T) {
	c := loadSample(t)

	m := c.Search("tsmc", SearchOptions{})
	if len(m) != 1 || len(m[0].Stock.Aliases) == 0 {
		t.Fatalf("setup: %v", kinds(m))
	}
	m[0].Stock.Aliases[0] = "MUTATED"

	got, _ := c.Lookup("2330")
	for _, a := range got.Aliases {
		if a == "MUTATED" {
			t.Fatal("Search result shares its Aliases array with the catalog")
		}
	}

	got.Aliases[0] = "MUTATED-AGAIN"
	again, _ := c.Lookup("2330")
	for _, a := range again.Aliases {
		if a == "MUTATED-AGAIN" {
			t.Fatal("Lookup result shares its Aliases array with the catalog")
		}
	}

	all := c.All()
	for i := range all {
		if len(all[i].Tags) > 0 {
			all[i].Tags[0] = "MUTATED-TAG"
		}
	}
	fresh, _ := c.Lookup("2330")
	for _, tg := range fresh.Tags {
		if tg == "MUTATED-TAG" {
			t.Fatal("All() shares its Tags arrays with the catalog")
		}
	}
}

// A user who pastes the canonical symbol the API itself hands out must not get "no results".
func TestSearchAcceptsACanonicalSymbol(t *testing.T) {
	c := loadSample(t)
	for _, q := range []string{"2330.TW", "2330.tw", " 6488.TWO "} {
		got := c.Search(q, SearchOptions{})
		if len(got) == 0 {
			t.Fatalf("Search(%q) found nothing", q)
		}
		if got[0].Kind != MatchSymbolExact {
			t.Fatalf("Search(%q) kind = %s", q, got[0].Kind)
		}
	}
	if got := c.Search("2330.TW", SearchOptions{}); got[0].Stock.Code != "2330" {
		t.Fatalf("Search(2330.TW) = %v", kinds(got))
	}
}

// The repo's own overlay must not contain an alias that shadows another company's name.
// TestRepoCatalogLoads would already fail if it did; this states the intent by name.
func TestRepoOverlayHasNoShadowingAlias(t *testing.T) {
	root := "../.."
	_, err := Load(Sources{
		NamesFile:   filepath.Join(root, DefaultNamesFile),
		SectorsFile: filepath.Join(root, DefaultSectorsFile),
		AliasesFile: filepath.Join(root, DefaultAliasesFile),
		OverlayFile: filepath.Join(root, DefaultOverlayFile),
	})
	if err != nil {
		t.Fatalf("the repo catalog has an identity problem: %v", err)
	}
}
