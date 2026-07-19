package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPort            = "8080"
	defaultLoadgenInterval = time.Second
	minResponseDelay       = 10 * time.Millisecond
	maxResponseDelay       = 100 * time.Millisecond
	chainLoadgenWeight     = 10
)

var propagatedTraceHeaders = []string{
	"traceparent",
	"tracestate",
	"baggage",
	"b3",
	"x-b3-traceid",
	"x-b3-spanid",
	"x-b3-parentspanid",
	"x-b3-sampled",
	"x-b3-flags",
}

type server struct {
	client    *http.Client
	chainURLs []string
	rand      *mathrand.Rand
	randMu    sync.Mutex
}

type response map[string]any

func main() {
	s := newServer()
	switch strings.ToLower(envOrDefault("APP_MODE", "api")) {
	case "api":
		runAPI(s)
	case "loadgen":
		runLoadGenerator(s, envOrDefault("LOADGEN_BASE_URL", "http://localhost:"+defaultPort), defaultLoadgenInterval)
	default:
		slog.Error("unknown APP_MODE", "mode", os.Getenv("APP_MODE"))
		os.Exit(1)
	}
}

func newServer() *server {
	return &server{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		chainURLs: parseCSVEnv("CHAIN_URLS"),
		rand:      mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
	}
}

func runAPI(s *server) {
	port := envOrDefault("PORT", defaultPort)

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := ":" + port
	slog.Info("starting hello world app", "addr", addr, "chain_url_count", len(s.chainURLs))
	if err := http.ListenAndServe(addr, requestLogger(s.responseDelay(mux))); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func runLoadGenerator(s *server, baseURL string, interval time.Duration) {
	targets, err := loadGeneratorTargets(baseURL, s.randomIntn)
	if err != nil {
		slog.Error("invalid LOADGEN_BASE_URL", "base_url", baseURL, "error", err)
		os.Exit(1)
	}

	go runLoadGeneratorHealthServer(s)

	slog.Info("starting load generator", "base_url", baseURL, "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		target := targets[s.randomIntn(len(targets))]()
		result := s.callURL(context.Background(), target, nil)
		if result.Error != "" {
			slog.Warn("load generator request failed", "url", result.URL, "error", result.Error)
			<-ticker.C
			continue
		}
		slog.Info("load generator request complete", "url", result.URL, "status", result.StatusCode)
		<-ticker.C
	}
}

func runLoadGeneratorHealthServer(s *server) {
	port := envOrDefault("PORT", defaultPort)
	mux := http.NewServeMux()
	s.registerLoadGeneratorHealthRoutes(mux)

	addr := ":" + port
	slog.Info("starting load generator health server", "addr", addr)
	if err := http.ListenAndServe(addr, requestLogger(s.responseDelay(mux))); err != nil {
		slog.Error("load generator health server exited", "error", err)
		os.Exit(1)
	}
}

type targetFactory func() string

func loadGeneratorTargets(baseURL string, randomIntn func(int) int) ([]targetFactory, error) {
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	chainTarget := func() string { return normalized + "/chain" }
	targets := []targetFactory{
		func() string { return normalized + "/hello" },
		func() string { return normalized + "/hello/" + strconv.Itoa(randomIntn(1000)) + "/world" },
		func() string { return normalized + "/hello/with/uuid/" + randomUUID() + "/world" },
		func() string {
			return normalized + "/hello/" + strconv.Itoa(randomIntn(1000)) + "/multi/" + strconv.Itoa(randomIntn(1000)) + "/path"
		},
		func() string { return normalized + "/error" },
		func() string { return normalized + "/external/call" },
	}
	for range chainLoadgenWeight {
		targets = append(targets, chainTarget)
	}
	return targets, nil
}

func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("base URL must include a host")
	}
	return trimmed, nil
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			time.Now().UnixNano(),
			time.Now().Nanosecond()&0xffff,
			time.Now().Second()&0xffff,
			time.Now().Minute()&0xffff,
			time.Now().UnixNano()&0xffffffffffff,
		)
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

func (s *server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hello", s.handleHello)
	mux.HandleFunc("GET /hello/{id}/world", s.handleHelloWorld)
	mux.HandleFunc("GET /hello/with/uuid/{uuid}/world", s.handleHelloUUIDWorld)
	mux.HandleFunc("GET /hello/{first}/multi/{second}/path", s.handleHelloMultiPath)
	mux.HandleFunc("GET /error", s.handleError)
	mux.HandleFunc("GET /external/call", s.handleExternalCall)
	mux.HandleFunc("GET /chain", s.handleChain)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *server) registerLoadGeneratorHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hello", s.handleHello)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *server) handleHello(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, response{
		"message": "hello",
	})
}

func (s *server) handleHelloWorld(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathInt(w, r, "id")
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, response{
		"message": "hello world",
		"id":      id,
	})
}

func (s *server) handleHelloUUIDWorld(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if !isUUID(uuid) {
		writeError(w, http.StatusBadRequest, "uuid must be a valid UUID")
		return
	}

	writeJSON(w, http.StatusOK, response{
		"message": "hello uuid world",
		"uuid":    uuid,
	})
}

func (s *server) handleHelloMultiPath(w http.ResponseWriter, r *http.Request) {
	first, ok := parsePathInt(w, r, "first")
	if !ok {
		return
	}
	second, ok := parsePathInt(w, r, "second")
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, response{
		"message": "hello multi path",
		"first":   first,
		"second":  second,
		"sum":     first + second,
	})
}

func (s *server) handleError(w http.ResponseWriter, r *http.Request) {
	if s.randomIntn(10) == 0 {
		slog.Error("failed to handle request", "error", "simulated internal server error")
		writeError(w, http.StatusInternalServerError, "simulated internal server error")
		return
	}

	writeJSON(w, http.StatusOK, response{
		"message": "no error this time",
	})
}

func (s *server) handleExternalCall(w http.ResponseWriter, r *http.Request) {
	urls := []string{
		"https://www.example.com",
		"https://checkip.amazonaws.com",
	}

	results := s.callURLs(r.Context(), urls, r.Header)
	writeJSON(w, statusFromResults(results), response{
		"message": "external calls complete",
		"results": results,
	})
}

func (s *server) handleChain(w http.ResponseWriter, r *http.Request) {
	if len(s.chainURLs) == 0 {
		writeJSON(w, http.StatusOK, response{
			"message": "no downstream chain configured",
			"results": []callResult{},
		})
		return
	}

	results := s.callURLs(r.Context(), s.chainURLs, r.Header)
	writeJSON(w, statusFromResults(results), response{
		"message": "chain calls complete",
		"results": results,
	})
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, response{
		"status": "ok",
	})
}

type callResult struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode,omitempty"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *server) callURLs(ctx context.Context, urls []string, inboundHeaders http.Header) []callResult {
	results := make([]callResult, 0, len(urls))
	for _, url := range urls {
		results = append(results, s.callURL(ctx, url, inboundHeaders))
	}
	return results
}

func (s *server) callURL(ctx context.Context, url string, inboundHeaders http.Header) callResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return callResult{URL: url, Error: err.Error()}
	}
	propagateTraceHeaders(req.Header, inboundHeaders)

	resp, err := s.client.Do(req)
	if err != nil {
		return callResult{URL: url, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return callResult{URL: url, StatusCode: resp.StatusCode, Error: err.Error()}
	}

	return callResult{
		URL:        url,
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(body)),
	}
}

func propagateTraceHeaders(outbound, inbound http.Header) {
	if inbound == nil {
		return
	}
	for _, name := range propagatedTraceHeaders {
		values := inbound.Values(name)
		if len(values) == 0 {
			continue
		}
		outbound.Del(name)
		for _, value := range values {
			outbound.Add(name, value)
		}
	}
}

func statusFromResults(results []callResult) int {
	for _, result := range results {
		if result.Error != "" || result.StatusCode >= 500 {
			return http.StatusBadGateway
		}
	}
	return http.StatusOK
}

func parsePathInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.PathValue(name)
	value, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be an integer", name))
		return 0, false
	}
	return value, true
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHex(c) {
				return false
			}
		}
	}
	return true
}

func isHex(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func parseCSVEnv(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, response{
		"error": message,
	})
}

func (s *server) responseDelay(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(s.randomResponseDelay())
		next.ServeHTTP(w, r)
	})
}

func (s *server) randomResponseDelay() time.Duration {
	span := int(maxResponseDelay - minResponseDelay + time.Millisecond)
	return minResponseDelay + time.Duration(s.randomIntn(span/int(time.Millisecond)))*time.Millisecond
}

func (s *server) randomIntn(n int) int {
	s.randMu.Lock()
	defer s.randMu.Unlock()
	return s.rand.Intn(n)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		slog.Info("request complete",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func init() {
	if err := validateRuntime(); err != nil {
		slog.Warn("runtime validation warning", "error", err)
	}
}

func validateRuntime() error {
	if os.Getenv("CHAIN_URLS") == "" {
		return nil
	}
	for _, value := range parseCSVEnv("CHAIN_URLS") {
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return errors.New("CHAIN_URLS entries should be absolute http(s) URLs")
		}
	}
	return nil
}
