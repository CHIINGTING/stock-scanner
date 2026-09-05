package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/healthcheck"
)

// fakeHealth stands in for the service so this file tests ROUTING and ERROR MAPPING, which is
// all this layer does. The service's own behaviour is tested in internal/healthcheck against
// real evidence.
type fakeHealth struct {
	got  healthcheck.Request
	resp *healthcheck.Health
	err  error
	//

	block time.Duration
}

func (f *fakeHealth) Check(ctx context.Context, req healthcheck.Request) (*healthcheck.Health, error) {
	f.got = req
	if f.block > 0 {
		select {
		case <-time.After(f.block):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.resp, f.err
}

func healthServer(t *testing.T, f *fakeHealth) *Server {
	t.Helper()
	s := testServer(t)
	s.Health = f
	return s
}

func TestHealthRouteIsServedWhenWired(t *testing.T) {
	f := &fakeHealth{resp: &healthcheck.Health{
		Identity: healthcheck.Identity{Symbol: "2330", Name: "台積電"},
		AsOf:     "2026-09-04",
	}}
	rec := get(t, healthServer(t, f), "/api/v1/stocks/2330/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out healthcheck.Health
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Identity.Symbol != "2330" || out.Identity.Name != "台積電" {
		t.Fatalf("identity = %+v", out.Identity)
	}
}

func TestHealthPassesSymbolAndAsOfThrough(t *testing.T) {
	f := &fakeHealth{resp: &healthcheck.Health{}}
	s := healthServer(t, f)
	get(t, s, "/api/v1/stocks/2330/health?as_of=2026-07-01&skip_ai=true")
	if f.got.Symbol != "2330" || f.got.AsOf != "2026-07-01" || !f.got.SkipAI {
		t.Fatalf("request = %+v", f.got)
	}
}

// An unknown stock is a well-formed request for something that does not exist: 404, not 400.
func TestUnknownStockIs404(t *testing.T) {
	f := &fakeHealth{err: healthcheck.ErrUnknownStock}
	rec := get(t, healthServer(t, f), "/api/v1/stocks/0000/health")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var env errorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != ErrNotFound {
		t.Fatalf("code = %q", env.Error.Code)
	}
}

func TestBadAsOfIs400(t *testing.T) {
	f := &fakeHealth{err: healthcheck.ErrBadDate}
	rec := get(t, healthServer(t, f), "/api/v1/stocks/2330/health?as_of=tomorrow")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBadSkipAIParamIs400(t *testing.T) {
	f := &fakeHealth{resp: &healthcheck.Health{}}
	rec := get(t, healthServer(t, f), "/api/v1/stocks/2330/health?skip_ai=perhaps")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthServiceErrorIs500(t *testing.T) {
	f := &fakeHealth{err: errors.New("archive on fire")}
	rec := get(t, healthServer(t, f), "/api/v1/stocks/2330/health")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	var env errorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != ErrInternal {
		t.Fatalf("code = %q", env.Error.Code)
	}
}

// A slow check must not hold a connection open forever; the deadline is the server's.
func TestHealthTimeoutIs504(t *testing.T) {
	f := &fakeHealth{block: 2 * time.Second, resp: &healthcheck.Health{}}
	s := healthServer(t, f)
	s.HealthTimeout = 20 * time.Millisecond
	rec := get(t, s, "/api/v1/stocks/2330/health")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

// The search route must keep working with the health route registered beside it.
func TestSearchStillWorksAlongsideHealth(t *testing.T) {
	f := &fakeHealth{resp: &healthcheck.Health{}}
	out := decodeSearch(t, get(t, healthServer(t, f), "/api/v1/stocks?q=2330"))
	if out.Count != 1 || out.Results[0].Symbol != "2330" {
		t.Fatalf("out = %+v", out)
	}
}
