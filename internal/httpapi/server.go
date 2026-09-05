// Package httpapi is the R14 Health Dashboard's HTTP surface.
//
// This is the FIRST HTTP server in the repository — nothing here migrates or replaces an
// existing one, and no framework is introduced: net/http's ServeMux gained method and
// wildcard patterns in Go 1.22, which is the version this module already targets.
//
// # What this layer is allowed to do
//
// Route, decode, encode, and set a status code. Every answer it gives comes from a service
// that could equally be called from a CLI; there is no analysis in a handler, and no handler
// reaches for the network to render a page.
//
// # Read-only with respect to the scanner
//
// Nothing here writes a scanner artefact, and nothing it returns is fed back into a scan.
// The dashboard is a sidecar: it consumes what the scanner and the archive commands have
// already produced.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

// Server holds the dependencies every handler shares.
//
// The catalog is an immutable pointer built at start-up, so concurrent requests read it
// without a lock. Swapping in a reloaded catalog means replacing the whole Server.
type Server struct {
	Catalog *stockcatalog.Catalog
	// Health answers on-demand health checks. Nil → the route is not registered at all,
	// which is a truthful 404 rather than a route that exists and always fails.
	Health HealthService
	// HealthTimeout bounds one check. Zero → defaultHealthTimeout.
	HealthTimeout time.Duration
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/stocks", s.handleStockSearch)
	if s.Health != nil {
		mux.HandleFunc("GET /api/v1/stocks/{symbol}/health", s.handleStockHealth)
	} else {
		// Without this the path falls through to the "/" catch-all and answers
		// STOCK_NOT_FOUND — blaming the symbol for a service that was never configured. What
		// is missing is the health service, and the status code has to say so.
		mux.HandleFunc("GET /api/v1/stocks/{symbol}/health",
			func(w http.ResponseWriter, r *http.Request) {
				writeError(w, http.StatusServiceUnavailable, ErrUnavailable,
					"health checks are not enabled on this server")
			})
	}
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Registered last and matched last: "/" is ServeMux's catch-all, so every pattern above
	// still wins for its own path.
	mux.HandleFunc("GET /", s.handleDashboard)
	return mux
}

// ── JSON helpers ──────────────────────────────────────────────────────────────────────

// apiError is the single error shape every endpoint returns, so a client has one thing to
// parse rather than a status code plus prose.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// Error codes. Named constants because the dashboard branches on them.
const (
	ErrBadRequest  = "BAD_REQUEST"
	ErrNotFound    = "STOCK_NOT_FOUND"
	ErrUnavailable = "SERVICE_UNAVAILABLE"
	ErrTimeout     = "TIMEOUT"
	ErrInternal    = "INTERNAL"
)

// handleHealthz reports whether this process can actually answer requests.
//
// A liveness probe that says "ok" while the catalog is empty would be reporting the process,
// not the service — and it would contradict the search endpoint, which correctly refuses with
// 503 in exactly that state. One nil catalog must not produce two different verdicts.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil || s.Catalog.Len() == 0 {
		writeError(w, http.StatusServiceUnavailable, ErrUnavailable, "stock catalog is not loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "stocks": s.Catalog.Len()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The dashboard is served from the same origin, so no CORS header is set. Adding one
	// would widen who can call an endpoint that spends OpenAI credit.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// HTML escaping stays ON (the encoder's default). It buys real safety — the echoed query
	// is whatever the user typed, so a page that ever interpolates it into markup gets <, >
	// and & already neutralised — and costs nothing: escaping does not touch CJK, which is
	// most of what this API returns.
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		// The status line is already written; there is nothing left to say to the client.
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: msg}})
}

// intParam reads a bounded integer query parameter.
//
// A malformed value is an ERROR rather than a silent fallback to the default: a client that
// sent limit=abc has a bug, and answering it with 20 results hides that bug forever.
func intParam(r *http.Request, name string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &paramError{name: name, reason: "must be an integer"}
	}
	if v < min || v > max {
		return 0, &paramError{name: name, reason: "must be between " +
			strconv.Itoa(min) + " and " + strconv.Itoa(max)}
	}
	return v, nil
}

// boolParam reads a boolean query parameter, with the same strictness.
func boolParam(r *http.Request, name string, def bool) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, &paramError{name: name, reason: "must be true or false"}
	}
	return v, nil
}

type paramError struct{ name, reason string }

func (e *paramError) Error() string { return e.name + " " + e.reason }
