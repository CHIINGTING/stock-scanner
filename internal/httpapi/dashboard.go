package httpapi

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The dashboard page.
//
// It is EMBEDDED, not read from disk at request time, for the same reason the report is
// generated rather than fetched: a page that reads a file per request behaves differently
// depending on where the binary was started from, and a missing file becomes a 500 at the
// worst moment instead of a build failure.
//
// # It calls no one but this server
//
// The page talks to /api/v1/* and nothing else. There is no OpenAI call from the browser, no
// key in the JavaScript, no key in the HTML, and no third-party script or font — the whole
// page is one file with no outbound reference of any kind. A key that reached this file would
// be readable by anyone who opened the page.
//
// # What the CSP does and does not buy
//
// It stops the page loading or contacting anything but this origin, and it stops the page
// being framed. Because the page is a fixed asset, script-src is a SHA-256 hash of the one
// script it contains rather than 'unsafe-inline' — so the policy also stops an injected
// script, and an edit to the script that forgets to update the hash fails loudly in the
// browser instead of quietly weakening the policy.
//
// style-src stays 'unsafe-inline' for the one <style> block. That is a smaller hole: a style
// injection needs an injection point, and the page builds every node with textContent.
//
//go:embed assets/dashboard.html
var dashboardFS embed.FS

// dashboardStarted is the build-time-ish instant used for cache validation. It is fixed for
// the life of the process, so a browser re-fetches when the binary restarts and not otherwise.
var dashboardStarted = time.Now()

// scriptHashes are the base64 SHA-256 digests of the page's inline <script> blocks, computed
// once at start-up from the embedded asset. Computing rather than hard-coding means the hash
// cannot drift out of date, while the policy still refuses any script that is not this one.
var scriptHashes = mustScriptHashes()

var inlineScriptRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func mustScriptHashes() []string {
	b, err := dashboardFS.ReadFile("assets/dashboard.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range inlineScriptRe.FindAllSubmatch(b, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

// dashboardCSP builds the policy.
//
// With no hash available the script-src falls back to 'none' rather than to 'unsafe-inline':
// a page that fails to run is a visible bug, where a silently weakened policy is not.
func dashboardCSP() string {
	src := "'none'"
	if len(scriptHashes) > 0 {
		src = strings.Join(scriptHashes, " ")
	}
	return "default-src 'none'; style-src 'unsafe-inline'; script-src " + src + "; " +
		"connect-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; " +
		"frame-ancestors 'none'"
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// ServeMux's "/" matches everything unmatched, so an unknown path would otherwise serve
	// the dashboard and hide a typo behind a working-looking page.
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, errorEnvelope{apiError{
			Code: ErrNotFound, Message: "no such page: " + r.URL.Path}})
		return
	}
	b, err := dashboardFS.ReadFile("assets/dashboard.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{apiError{
			Code: ErrInternal, Message: "the dashboard page is missing from the binary"}})
		return
	}
	// default-src 'none' with an explicit allowance for what the page actually uses: its own
	// inline style and script, and same-origin XHR. No external anything, so a future edit
	// that pastes in a CDN script fails visibly in the browser console rather than silently
	// shipping a third party the page's data.
	w.Header().Set("Content-Security-Policy", dashboardCSP())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// frame-ancestors is not covered by default-src, and X-Frame-Options covers the browsers
	// that predate it. Without both, any site could frame this page.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "dashboard.html", dashboardStarted, newBytesReader(b))
}
