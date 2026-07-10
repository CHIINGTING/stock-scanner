package news

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// epNumRe matches an episode number in a title ("股癌筆記EP677", "EP 677").
var epNumRe = regexp.MustCompile(`(?i)EP\s*0*([0-9]{1,5})`)

// epSlugRe matches the episode number in a SocialWorkerDaily slug/URL
// ("notes-of-gooaye-ep-677").
var epSlugRe = regexp.MustCompile(`(?i)notes-of-gooaye-ep-0*([0-9]{1,5})`)

// NormalizeEpisode derives a stable, source-independent episode id from any of the
// available strings (title, slug, url). It returns "" when no episode number is found,
// in which case dedup falls back to a content fingerprint.
//
// The returned id is the cross-source dedup key: SocialWorkerDaily "notes-of-gooaye-ep-677"
// and a TWETQ "EP677" both normalize to "gooaye-ep-677" and merge into one event.
func NormalizeEpisode(candidates ...string) string {
	for _, s := range candidates {
		if m := epSlugRe.FindStringSubmatch(s); m != nil {
			return "gooaye-ep-" + strings.TrimLeft(m[1], "0")
		}
	}
	for _, s := range candidates {
		if m := epNumRe.FindStringSubmatch(s); m != nil {
			return "gooaye-ep-" + strings.TrimLeft(m[1], "0")
		}
	}
	return ""
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe    = regexp.MustCompile(`[ \t]+`)
	nlRe    = regexp.MustCompile(`\n{3,}`)
	blockRe = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|tr|br)>|<br\s*/?>`)
)

// PlainText converts an HTML fragment (e.g. WordPress content.rendered) to readable
// plain text: block-closing tags become newlines, remaining tags are dropped, HTML
// entities are unescaped, and whitespace is collapsed. Stdlib only — no x/net.
func PlainText(htmlFrag string) string {
	s := blockRe.ReplaceAllString(htmlFrag, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = wsRe.ReplaceAllString(s, " ")
	// trim spaces around each line, then collapse blank runs.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSpace(ln)
	}
	s = strings.Join(lines, "\n")
	s = nlRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// EpisodeNumber returns the numeric episode from a normalized id ("gooaye-ep-677" → 677),
// or 0 when the id is empty/non-episodic. Used for stable ordering/labels.
func EpisodeNumber(episodeID string) int {
	if episodeID == "" {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(episodeID, "gooaye-ep-%d", &n); err != nil {
		return 0
	}
	return n
}
