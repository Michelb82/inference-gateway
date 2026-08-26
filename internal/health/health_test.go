package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/health"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

type fakeProbes struct {
	probes map[string]inference.EndpointProbe
}

func (f *fakeProbes) RefreshProbes(ctx context.Context, targets []inference.ModelTarget, maxAge time.Duration) {
}

func (f *fakeProbes) Probe(vendor, baseURL string) (inference.EndpointProbe, bool) {
	p, ok := f.probes[inference.NormalizeVendor(vendor)+"|"+strings.TrimRight(strings.TrimSpace(baseURL), "/")]
	return p, ok
}

func probeKey(url string) string {
	return "ollama|" + url
}

func testRegistry() *capability.Registry {
	return capability.NewRegistry(
		capability.ModelBinding{Vendor: "ollama", BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAlive: "5m"},
		capability.ModelBinding{Vendor: "ollama", BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAlive: "5m"},
		capability.ModelBinding{Vendor: "ollama", BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAlive: "2m"},
	)
}

func chatOK(url string, catalog []string) *fakeProbes {
	return &fakeProbes{probes: map[string]inference.EndpointProbe{
		probeKey(url): {
			Vendor:  "ollama",
			BaseURL: url,
			Catalog: catalog,
			ChatOK:  true,
		},
	}}
}

func TestHealthJSONDormant(t *testing.T) {
	handler := health.NewHandler(capability.NewHolder(), &fakeProbes{})
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var report health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Status != health.StatusDormant {
		t.Fatalf("status = %q", report.Status)
	}
	if len(report.Capabilities) != 0 {
		t.Fatalf("capabilities = %d", len(report.Capabilities))
	}
}

func TestHealthJSONReady(t *testing.T) {
	reg := testRegistry()
	handler := health.NewHandler(reg, chatOK("http://llm-model:11434", []string{
		"qwen3:1.7b-q4_K_M", "qwen3:4b-q4_K_M", "qwen3:14b-q4_K_M",
	}))
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var report health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Status != health.StatusReady {
		t.Fatalf("status = %q", report.Status)
	}
	if len(report.Capabilities) != 3 {
		t.Fatalf("capabilities = %d", len(report.Capabilities))
	}
	for _, item := range report.Capabilities {
		if item.Warning != "" {
			t.Fatalf("unexpected warning: %q", item.Warning)
		}
	}
}

func TestHealthJSONModelWarningStillReady(t *testing.T) {
	reg := testRegistry()
	handler := health.NewHandler(reg, chatOK("http://llm-model:11434", []string{"other:latest"}))
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var report health.Report
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Status != health.StatusReady {
		t.Fatalf("status = %q", report.Status)
	}
	if report.Capabilities[0].Warning == "" {
		t.Fatal("expected model-not-listed warning")
	}
}

func TestHealthJSONCatalogUnavailableWarning(t *testing.T) {
	reg := testRegistry()
	handler := health.NewHandler(reg, &fakeProbes{probes: map[string]inference.EndpointProbe{
		probeKey("http://llm-model:11434"): {
			Vendor:     "ollama",
			BaseURL:    "http://llm-model:11434",
			CatalogErr: errors.New("tags down"),
			ChatOK:     true,
		},
	}})
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var report health.Report
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Status != health.StatusReady {
		t.Fatalf("status = %q", report.Status)
	}
	if report.Capabilities[0].Warning != inference.WarningCatalogUnavailable() {
		t.Fatalf("warning = %q", report.Capabilities[0].Warning)
	}
}

func TestHealthJSONNotReady(t *testing.T) {
	reg := testRegistry()
	handler := health.NewHandler(reg, &fakeProbes{probes: map[string]inference.EndpointProbe{
		probeKey("http://llm-model:11434"): {
			Vendor:  "ollama",
			BaseURL: "http://llm-model:11434",
			ChatOK:  false,
			ChatErr: errors.New("ping failed"),
		},
	}})
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}

	var report health.Report
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Status != health.StatusNotReady {
		t.Fatalf("status = %q", report.Status)
	}
}

func TestHealthHTMLIncludesTranslate(t *testing.T) {
	reg := testRegistry()
	handler := health.NewHandler(reg, &fakeProbes{probes: map[string]inference.EndpointProbe{
		probeKey("http://llm-model:11434"): {
			ChatOK:  false,
			ChatErr: errors.New("down"),
		},
	}})
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="translate"`) {
		t.Fatalf("missing translate div")
	}
	if !strings.Contains(body, "unavailable") {
		t.Fatalf("missing unavailable status")
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	handler := health.NewHandler(capability.NewHolder(), &fakeProbes{})
	mux := http.NewServeMux()
	handler.Register(mux)

	for _, path := range []string{"/health", "/health.json"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
}

func openaiCapRegistry(url, model string) *capability.Registry {
	b := capability.ModelBinding{Vendor: "openai", BaseURL: url, Model: model, MaxInputChars: 8000}
	return capability.NewRegistry(b, b, b)
}

func ollamaCapRegistry(url, model string) *capability.Registry {
	b := capability.ModelBinding{Vendor: "ollama", BaseURL: url, Model: model, KeepAlive: "5m", MaxInputChars: 8000}
	return capability.NewRegistry(b, b, b)
}

func countingOpenAI(t *testing.T, lists, chats *atomic.Int32, catalog string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			lists.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(catalog))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			chats.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func getHealthJSON(t *testing.T, mux http.Handler) (int, health.Report) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health.json", nil))
	var report health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return rec.Code, report
}

func TestHealthJSONOpenAIProbesOncePerURL(t *testing.T) {
	var lists, chats atomic.Int32
	server := countingOpenAI(t, &lists, &chats, `{"data":[{"id":"gpt-4o-mini"}]}`)
	defer server.Close()

	router := inference.NewRouter("DEV_KEY")
	mux := http.NewServeMux()
	health.NewHandler(openaiCapRegistry(server.URL, "gpt-4o-mini"), router).Register(mux)

	code, report := getHealthJSON(t, mux)
	if code != http.StatusOK || report.Status != health.StatusReady {
		t.Fatalf("first health status=%d report=%q", code, report.Status)
	}
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("lists=%d chats=%d, want 1 each", lists.Load(), chats.Load())
	}
	if len(report.Capabilities) != 3 {
		t.Fatalf("capabilities = %d", len(report.Capabilities))
	}

	code, report = getHealthJSON(t, mux)
	if code != http.StatusOK || report.Status != health.StatusReady {
		t.Fatalf("second health status=%d report=%q", code, report.Status)
	}
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("second health listed/pinged lists=%d chats=%d", lists.Load(), chats.Load())
	}
}

func TestHealthJSONRefreshAfterHour(t *testing.T) {
	var lists, chats atomic.Int32
	server := countingOpenAI(t, &lists, &chats, `{"data":[{"id":"gpt-4o-mini"}]}`)
	defer server.Close()

	now := time.Unix(1_700_000_000, 0)
	router := inference.NewRouter("DEV_KEY")
	router.SetNow(func() time.Time { return now })
	mux := http.NewServeMux()
	health.NewHandler(openaiCapRegistry(server.URL, "gpt-4o-mini"), router).Register(mux)

	getHealthJSON(t, mux)
	now = now.Add(inference.ProbeTTL + time.Second)
	code, report := getHealthJSON(t, mux)
	if code != http.StatusOK || report.Status != health.StatusReady {
		t.Fatalf("status=%d report=%q", code, report.Status)
	}
	if lists.Load() != 2 || chats.Load() != 2 {
		t.Fatalf("lists=%d chats=%d, want 2 each after TTL", lists.Load(), chats.Load())
	}
}

func TestHealthJSONMissingCatalogModelStillReady(t *testing.T) {
	var lists, chats atomic.Int32
	server := countingOpenAI(t, &lists, &chats, `{"data":[{"id":"other-model"}]}`)
	defer server.Close()

	router := inference.NewRouter("DEV_KEY")
	mux := http.NewServeMux()
	health.NewHandler(openaiCapRegistry(server.URL, "gpt-4o-mini"), router).Register(mux)

	code, report := getHealthJSON(t, mux)
	if code != http.StatusOK || report.Status != health.StatusReady {
		t.Fatalf("status=%d report=%q", code, report.Status)
	}
	if report.Capabilities[0].Warning != inference.WarningModelNotListed("gpt-4o-mini") {
		t.Fatalf("warning = %q", report.Capabilities[0].Warning)
	}
	if report.Capabilities[0].Status != health.StatusAvailable {
		t.Fatalf("cap status = %q", report.Capabilities[0].Status)
	}
}

func TestHealthJSONChatPingFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
			return
		}
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	router := inference.NewRouter("DEV_KEY")
	mux := http.NewServeMux()
	health.NewHandler(openaiCapRegistry(server.URL, "gpt-4o-mini"), router).Register(mux)

	code, report := getHealthJSON(t, mux)
	if code != http.StatusServiceUnavailable || report.Status != health.StatusNotReady {
		t.Fatalf("status=%d report=%q", code, report.Status)
	}
	if report.Capabilities[0].Status != health.StatusUnavailable {
		t.Fatalf("cap status = %q", report.Capabilities[0].Status)
	}
}

func TestHealthJSONOllamaProbesOncePerURL(t *testing.T) {
	var lists, chats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
			lists.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:1.7b"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			chats.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	router := inference.NewRouter("")
	mux := http.NewServeMux()
	health.NewHandler(ollamaCapRegistry(server.URL, "qwen3:1.7b"), router).Register(mux)

	code, report := getHealthJSON(t, mux)
	if code != http.StatusOK || report.Status != health.StatusReady {
		t.Fatalf("first health status=%d report=%q", code, report.Status)
	}
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("lists=%d chats=%d, want 1 each", lists.Load(), chats.Load())
	}

	getHealthJSON(t, mux)
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("second health listed/pinged lists=%d chats=%d", lists.Load(), chats.Load())
	}
}
