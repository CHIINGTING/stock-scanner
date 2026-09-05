package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func dashboardBody(t *testing.T) string {
	t.Helper()
	b, err := dashboardFS.ReadFile("assets/dashboard.html")
	if err != nil {
		t.Fatalf("the page is not embedded in the binary: %v", err)
	}
	return string(b)
}

// ── the page reaches no one but this server (§24) ─────────────────────────────────────

// No key may appear in the HTML or the JavaScript, and there is no way for one to: the page
// never calls a model, it calls this server.
func TestPageContainsNoCredentialAndNoModelCall(t *testing.T) {
	page := dashboardBody(t)
	lower := strings.ToLower(page)

	for _, bad := range []string{
		"openai.com", "api.openai", "sk-", "bearer ", "authorization",
		"openai_api_key", "apikey", "api_key", "openapi-gateway",
	} {
		if strings.Contains(lower, bad) {
			t.Errorf("the page contains %q — a browser-side model call or a leaked credential", bad)
		}
	}
}

// Every request the page makes is same-origin. A page that fetched a third party would be
// handing that party whatever it was rendering.
func TestPageFetchesOnlyThisServer(t *testing.T) {
	page := dashboardBody(t)
	for _, m := range regexp.MustCompile(`fetch\(\s*"([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(m[1], "/api/v1/") {
			t.Errorf("fetch to %q — every call must be same-origin /api/v1/", m[1])
		}
	}
	// No external resource of any kind: no CDN script, no web font, no tracking pixel.
	for _, m := range regexp.MustCompile(`(?i)(src|href)\s*=\s*"([^"]*)"`).FindAllStringSubmatch(page, -1) {
		v := m[2]
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") ||
			strings.HasPrefix(v, "//") {
			t.Errorf("external resource %q=%q", m[1], v)
		}
	}
}

// The CSP header states the same rule to the browser, so a future edit that pastes in a CDN
// script fails visibly rather than shipping quietly.
func TestDashboardSendsALockedDownCSP(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy on the dashboard")
	}
	for _, want := range []string{"default-src 'none'", "connect-src 'self'",
		"frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q: %s", want, csp)
		}
	}
	// script-src must be a hash of the page's own script, never 'unsafe-inline': the page is
	// a fixed asset, so there is no reason to accept any other script.
	if strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("script-src is 'unsafe-inline', so the CSP offers no injection protection: %s", csp)
	}
	if !strings.Contains(csp, "'sha256-") {
		t.Errorf("script-src carries no hash: %s", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("the page can be framed by any site")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("no nosniff header")
	}
}

// An unknown path is a 404, not the dashboard. "/" is ServeMux's catch-all, so without an
// explicit check a typo would render a working-looking page.
func TestUnknownPathIsNotTheDashboard(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/nope", "/api/v1/nope", "/admin"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("GET %s served the dashboard", path)
		}
	}
}

// The API routes still win over the catch-all.
func TestAPIRoutesArentShadowedByTheDashboard(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stocks?q=2330", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("search returned %q — the dashboard shadowed the API route", ct)
	}
}

// ── the missing-value treatment (§21) ─────────────────────────────────────────────────

// A value with no number behind it must not be rendered as one. The page reads Value.display
// — which the server never fills with a bare 0 or "-" — and marks it visually as a label.
func TestPageRendersMissingValuesAsLabelsNotFigures(t *testing.T) {
	page := dashboardBody(t)

	// The branch exists: anything not AVAILABLE gets the .missing treatment.
	if !strings.Contains(page, `v.status !== AVAILABLE`) {
		t.Error("the renderer does not branch on status")
	}
	if !strings.Contains(page, `n.classList.add("missing")`) {
		t.Error("a non-available value is not marked as missing")
	}
	// And the treatment is visually a label, not a number: monospace, muted, dashed border.
	css := page[strings.Index(page, ".v.missing"):]
	css = css[:strings.Index(css, "}")]
	for _, want := range []string{"monospace", "dashed"} {
		if !strings.Contains(css, want) {
			t.Errorf(".v.missing lacks %q, so it can still read as a figure: %s", want, css)
		}
	}
	// The page must never invent a placeholder of its own for a missing number.
	if regexp.MustCompile(`\|\|\s*"0"`).MatchString(page) {
		t.Error(`the page falls back to "0" somewhere`)
	}
	// U+2014 as well as ASCII: the page uses the em dash, so a hyphen-only check here
	// would guard a spelling the page never had.
	if regexp.MustCompile(`(display|v\.display)\s*\|\|\s*"(-|—)"`).MatchString(page) {
		t.Error(`the page falls back to a dash for a display value`)
	}
}

// Server data is inserted as TEXT, never built into an HTML string. A stock name or an AI
// sentence is data; concatenating it into markup is how a bracket becomes a tag.
func TestPageNeverBuildsMarkupFromServerData(t *testing.T) {
	page := dashboardBody(t)
	script := page[strings.Index(page, "<script>"):]
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML",
		"document.write", "eval("} {
		if strings.Contains(script, bad) {
			t.Errorf("the page uses %s — server data could become markup", bad)
		}
	}
	if !strings.Contains(script, "textContent") {
		t.Error("the page does not use textContent")
	}
}

// The AI section must state, on the page, that the numbers are not its work.
func TestPageStatesTheAIBoundary(t *testing.T) {
	page := dashboardBody(t)
	if !strings.Contains(page, "a.disclaimer") {
		t.Error("the page never renders the AI disclaimer")
	}
	if !strings.Contains(page, "不受 AI 是否可用影響") {
		t.Error("a degraded AI section does not tell the reader the figures are unaffected")
	}
	if !strings.Contains(page, "AI 只負責文字判讀，不產生任何數字") {
		t.Error("the page header does not state the AI boundary")
	}
}

// Target prices must carry their provenance on the page, not only in the JSON.
func TestPageShowsTargetPriceProvenance(t *testing.T) {
	page := dashboardBody(t)
	for _, want := range []string{"t.rule", "t.rule_detail", "t.eps_basis", "t.eps_source",
		"t.eps_base_date", "t.current_date"} {
		if !strings.Contains(page, want) {
			t.Errorf("the target card does not render %s", want)
		}
	}
}

// The CSP hash must match the script that actually ships, or the page silently fails to run
// in a browser while every Go test still passes.
func TestCSPHashMatchesTheEmbeddedScript(t *testing.T) {
	page := dashboardBody(t)
	hashes := mustScriptHashes()
	if len(hashes) == 0 {
		t.Fatal("no script hash was computed")
	}
	n := strings.Count(page, "<script>")
	if len(hashes) != n {
		t.Fatalf("%d hashes for %d script blocks", len(hashes), n)
	}
	// Recompute independently of mustScriptHashes' own regexp.
	start := strings.Index(page, "<script>") + len("<script>")
	end := strings.LastIndex(page, "</script>")
	sum := sha256.Sum256([]byte(page[start:end]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if hashes[0] != want {
		t.Fatalf("hash %s does not match the embedded script %s", hashes[0], want)
	}
	if !strings.Contains(dashboardCSP(), want) {
		t.Fatalf("the CSP does not carry the script's hash")
	}
}
