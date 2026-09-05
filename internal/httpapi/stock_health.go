package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/healthcheck"
)

// HealthService is implemented by *healthcheck.Service.
//
// It is an interface so this package does not have to construct a fetcher and a scanner in
// order to be tested, and so a build without the health layer simply does not register the
// route — a 404 being a truthful answer to "this build has no health service".
type HealthService interface {
	Check(ctx context.Context, req healthcheck.Request) (*healthcheck.Health, error)
}

// defaultHealthTimeout bounds one on-demand check.
//
// A health check may fetch a price series and, later, call a model. Neither should be able to
// hold a browser connection open indefinitely, and the deadline is set HERE rather than left
// to the client so a hung request cannot pin a server goroutine either.
const defaultHealthTimeout = 60 * time.Second

// handleStockHealth serves GET /api/v1/stocks/{symbol}/health?as_of=YYYY-MM-DD
//
// The response is the deterministic model, verbatim. This handler adds no number, no label
// and no judgement of its own: everything a reader sees was decided by healthcheck.Service,
// which is what makes the API and a future CLI incapable of disagreeing.
func (s *Server) handleStockHealth(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.PathValue("symbol"))
	if symbol == "" {
		writeError(w, http.StatusBadRequest, ErrBadRequest, "symbol is required")
		return
	}
	asOf := strings.TrimSpace(r.URL.Query().Get("as_of"))
	skipAI, err := boolParam(r, "skip_ai", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
		return
	}
	if s.Health == nil {
		writeError(w, http.StatusServiceUnavailable, ErrUnavailable, "health service is not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.healthTimeout())
	defer cancel()

	h, err := s.Health.Check(ctx, healthcheck.Request{Symbol: symbol, AsOf: asOf, SkipAI: skipAI})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, h)
	case errors.Is(err, healthcheck.ErrUnknownStock):
		// 404, not 400: the request was well-formed, the stock simply is not in the catalog.
		writeError(w, http.StatusNotFound, ErrNotFound, err.Error())
	case errors.Is(err, healthcheck.ErrBadDate):
		writeError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, http.StatusGatewayTimeout, ErrTimeout, "the health check did not finish in time")
	default:
		// The message is the service's own and names a source, never a credential: nothing
		// in the health path puts a key or a header into an error.
		writeError(w, http.StatusInternalServerError, ErrInternal, err.Error())
	}
}

func (s *Server) healthTimeout() time.Duration {
	if s != nil && s.HealthTimeout > 0 {
		return s.HealthTimeout
	}
	return defaultHealthTimeout
}
