package twetq

import (
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/news"
)

// urlset is the sitemap.xml shape (only <loc> is needed).
type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

var stockCodeRe = regexp.MustCompile(`/stock/([0-9]{4,6}[A-Za-z]?)$`)

// StockCodesFromSitemap extracts the unique, sorted set of stock codes from a twetq
// sitemap (URLs like https://twetq.com/stock/2327). Malformed XML yields an empty set,
// never a panic.
func StockCodesFromSitemap(body []byte) []string {
	var doc urlset
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, u := range doc.URLs {
		if m := stockCodeRe.FindStringSubmatch(strings.TrimSpace(u.Loc)); m != nil {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// titleStockRe pulls "國巨股份有限公司（2327）" style name+code from a /stock page title.
var titleStockRe = regexp.MustCompile(`<title>\s*([^（(]+)[（(]\s*([0-9]{4,6}[A-Za-z]?)\s*[）)]`)

// ParseStockPage extracts (code, name) from a server-rendered twetq /stock/<code> page.
// Returns ("","") when the title does not match. This is quant-coverage metadata, not a
// news signal.
func ParseStockPage(html string) (code, name string) {
	m := titleStockRe.FindStringSubmatch(html)
	if m == nil {
		return "", ""
	}
	return strings.TrimSpace(m[2]), strings.TrimSpace(m[1])
}

// epRe finds episode references if the podcast page is ever server-rendered.
var epRe = regexp.MustCompile(`(?i)EP\s*0*([0-9]{2,4})`)

// parsePodcast attempts to extract podcast-episode items from the /podcast HTML. The
// live page is a JS shell with no server-side episode data, so this returns an empty
// slice today — a graceful degrade. If twetq ever server-renders episodes, this picks
// them up without further changes.
func parsePodcast(body []byte) []news.RawNewsItem {
	html := string(body)
	matches := epRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil // JS-rendered shell: nothing to parse (expected)
	}
	seen := map[string]bool{}
	var out []news.RawNewsItem
	for _, m := range matches {
		ep := news.NormalizeEpisode("EP" + m[1])
		if ep == "" || seen[ep] {
			continue
		}
		seen[ep] = true
		out = append(out, news.RawNewsItem{
			Source:    name,
			Title:     "股癌 " + strings.ToUpper("EP"+m[1]),
			URL:       defaultBaseURL + "/podcast",
			EpisodeID: ep,
		})
	}
	return out
}

func readAll(resp *http.Response) ([]byte, error) {
	const maxBytes = 8 << 20
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}
