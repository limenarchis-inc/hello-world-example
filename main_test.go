package main

import (
	"encoding/json"
	mathrand "math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHelloEndpoints(t *testing.T) {
	app := testServer(nil)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantKey    string
		wantValue  any
	}{
		{
			name:       "plain hello",
			path:       "/hello",
			wantStatus: http.StatusOK,
			wantKey:    "message",
			wantValue:  "hello",
		},
		{
			name:       "integer path",
			path:       "/hello/123/world",
			wantStatus: http.StatusOK,
			wantKey:    "id",
			wantValue:  float64(123),
		},
		{
			name:       "uuid path",
			path:       "/hello/with/uuid/123e4567-e89b-12d3-a456-426614174000/world",
			wantStatus: http.StatusOK,
			wantKey:    "uuid",
			wantValue:  "123e4567-e89b-12d3-a456-426614174000",
		},
		{
			name:       "multi integer path",
			path:       "/hello/4/multi/8/path",
			wantStatus: http.StatusOK,
			wantKey:    "sum",
			wantValue:  float64(12),
		},
		{
			name:       "invalid integer path",
			path:       "/hello/not-an-int/world",
			wantStatus: http.StatusBadRequest,
			wantKey:    "error",
			wantValue:  "id must be an integer",
		},
		{
			name:       "invalid uuid path",
			path:       "/hello/with/uuid/not-a-uuid/world",
			wantStatus: http.StatusBadRequest,
			wantKey:    "error",
			wantValue:  "uuid must be a valid UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			app.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}

			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if got[tt.wantKey] != tt.wantValue {
				t.Fatalf("%s = %#v, want %#v", tt.wantKey, got[tt.wantKey], tt.wantValue)
			}
		})
	}
}

func TestChainWithoutURLs(t *testing.T) {
	app := testServer(nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chain", nil)

	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestLoadGeneratorHealthRoutes(t *testing.T) {
	s := &server{}
	mux := http.NewServeMux()
	s.registerLoadGeneratorHealthRoutes(mux)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/hello", wantStatus: http.StatusOK},
		{path: "/healthz", wantStatus: http.StatusOK},
		{path: "/chain", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			mux.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}

func TestErrorEndpointCanReturnSuccessAndFailure(t *testing.T) {
	s := &server{rand: mathrand.New(mathrand.NewSource(2))}

	sawFailure := false
	sawSuccess := false
	for range 100 {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/error", nil)

		s.handleError(rr, req)

		switch rr.Code {
		case http.StatusOK:
			sawSuccess = true
		case http.StatusInternalServerError:
			sawFailure = true
		default:
			t.Fatalf("unexpected status %d", rr.Code)
		}
	}

	if !sawSuccess || !sawFailure {
		t.Fatalf("expected both success and failure, saw success=%t failure=%t", sawSuccess, sawFailure)
	}
}

func TestLoadGeneratorTargets(t *testing.T) {
	random := seededRandomIntn(1)
	targets, err := loadGeneratorTargets("http://example.test/", random)
	if err != nil {
		t.Fatalf("loadGeneratorTargets returned error: %v", err)
	}
	if len(targets) != 6+chainLoadgenWeight {
		t.Fatalf("target count = %d, want %d", len(targets), 6+chainLoadgenWeight)
	}

	chainTargets := 0
	for _, target := range targets {
		got := target()
		if !strings.HasPrefix(got, "http://example.test/") {
			t.Fatalf("target %q does not use normalized base URL", got)
		}
		if got == "http://example.test/chain" {
			chainTargets++
		}
	}
	if chainTargets != chainLoadgenWeight {
		t.Fatalf("chain target count = %d, want %d", chainTargets, chainLoadgenWeight)
	}

	uuidTarget := targets[2]()
	uuid := strings.TrimSuffix(strings.TrimPrefix(uuidTarget, "http://example.test/hello/with/uuid/"), "/world")
	if !isUUID(uuid) {
		t.Fatalf("generated UUID %q is invalid", uuid)
	}
}

func TestRandomResponseDelay(t *testing.T) {
	s := &server{rand: mathrand.New(mathrand.NewSource(1))}

	for range 100 {
		got := s.randomResponseDelay()
		if got < 10*time.Millisecond || got > 100*time.Millisecond {
			t.Fatalf("delay = %s, want between 10ms and 100ms", got)
		}
		if got%time.Millisecond != 0 {
			t.Fatalf("delay = %s, want millisecond precision", got)
		}
	}
}

func TestPropagateTraceHeaders(t *testing.T) {
	inbound := http.Header{}
	inbound.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	inbound.Set("Tracestate", "rojo=00f067aa0ba902b7")
	inbound.Add("Baggage", "user.id=123")
	inbound.Add("Baggage", "feature=hello")
	inbound.Set("X-B3-TraceId", "4bf92f3577b34da6a3ce929d0e0e4736")
	inbound.Set("X-B3-SpanId", "00f067aa0ba902b7")
	inbound.Set("Unrelated", "do-not-forward")

	outbound := http.Header{}
	propagateTraceHeaders(outbound, inbound)

	if got := outbound.Get("Traceparent"); got != inbound.Get("Traceparent") {
		t.Fatalf("traceparent = %q, want %q", got, inbound.Get("Traceparent"))
	}
	if got := outbound.Get("Tracestate"); got != inbound.Get("Tracestate") {
		t.Fatalf("tracestate = %q, want %q", got, inbound.Get("Tracestate"))
	}
	if got := outbound.Values("Baggage"); len(got) != 2 {
		t.Fatalf("baggage values = %#v, want 2 values", got)
	}
	if got := outbound.Get("X-B3-TraceId"); got != inbound.Get("X-B3-TraceId") {
		t.Fatalf("x-b3-traceid = %q, want %q", got, inbound.Get("X-B3-TraceId"))
	}
	if got := outbound.Get("Unrelated"); got != "" {
		t.Fatalf("unrelated header was forwarded: %q", got)
	}
}

func TestChainPropagatesTraceHeaders(t *testing.T) {
	received := make(chan http.Header, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		writeJSON(w, http.StatusOK, response{"message": "downstream ok"})
	}))
	defer downstream.Close()

	s := &server{
		client:    downstream.Client(),
		chainURLs: []string{downstream.URL},
		rand:      mathrand.New(mathrand.NewSource(1)),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("Baggage", "tenant=example")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	select {
	case got := <-received:
		if got.Get("Traceparent") != req.Header.Get("Traceparent") {
			t.Fatalf("traceparent = %q, want %q", got.Get("Traceparent"), req.Header.Get("Traceparent"))
		}
		if got.Get("Baggage") != req.Header.Get("Baggage") {
			t.Fatalf("baggage = %q, want %q", got.Get("Baggage"), req.Header.Get("Baggage"))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for downstream request")
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	got, err := normalizeBaseURL(" http://example.test/api/ ")
	if err != nil {
		t.Fatalf("normalizeBaseURL returned error: %v", err)
	}
	if got != "http://example.test/api" {
		t.Fatalf("normalized base URL = %q, want %q", got, "http://example.test/api")
	}

	if _, err := normalizeBaseURL("ftp://example.test"); err == nil {
		t.Fatal("expected invalid scheme to fail")
	}
}

func seededRandomIntn(seed int64) func(int) int {
	r := mathrand.New(mathrand.NewSource(seed))
	return r.Intn
}

func testServer(chainURLs []string) http.Handler {
	s := &server{
		chainURLs: chainURLs,
		rand:      mathrand.New(mathrand.NewSource(1)),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}
