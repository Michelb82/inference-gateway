package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/health"
)

type fakeModels struct {
	available map[string]bool
	err       error
}

func (f *fakeModels) ModelAvailable(ctx context.Context, baseURL, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.available[name], nil
}

func testRegistry() *capability.Registry {
	return capability.NewRegistry(
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAlive: "5m"},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAlive: "5m"},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAlive: "2m"},
	)
}

func TestHealthJSONDormant(t *testing.T) {
	handler := health.NewHandler(capability.NewHolder(), &fakeModels{})
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
	models := &fakeModels{available: map[string]bool{
		"qwen3:1.7b-q4_K_M": true,
		"qwen3:4b-q4_K_M":   true,
		"qwen3:14b-q4_K_M":  true,
	}}
	handler := health.NewHandler(reg, models)
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
}

func TestHealthJSONNotReady(t *testing.T) {
	reg := testRegistry()
	models := &fakeModels{available: map[string]bool{
		"qwen3:1.7b-q4_K_M": true,
		"qwen3:4b-q4_K_M":   false,
		"qwen3:14b-q4_K_M":  true,
	}}
	handler := health.NewHandler(reg, models)
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
	found := false
	for _, item := range report.Capabilities {
		if item.Capability == capability.IntentClassification {
			found = true
			if item.Status != health.StatusUnavailable {
				t.Fatalf("intent status = %q", item.Status)
			}
		}
	}
	if !found {
		t.Fatalf("intent capability missing")
	}
}

func TestHealthHTMLIncludesTranslate(t *testing.T) {
	reg := testRegistry()
	models := &fakeModels{available: map[string]bool{
		"qwen3:1.7b-q4_K_M": true,
		"qwen3:4b-q4_K_M":   true,
		"qwen3:14b-q4_K_M":  false,
	}}
	handler := health.NewHandler(reg, models)
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
	handler := health.NewHandler(capability.NewHolder(), &fakeModels{})
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
