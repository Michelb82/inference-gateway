package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterOpenAIDisabledWithoutKey(t *testing.T) {
	router := NewRouter("")
	if router.OpenAIEnabled() {
		t.Fatal("OpenAIEnabled() = true, want false")
	}

	_, err := router.Complete(context.Background(), CompleteRequest{
		Vendor:       VendorOpenAI,
		BaseURL:      "https://api.openai.com",
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "gpt-4o-mini",
	})
	if !errors.Is(err, ErrOpenAIDisabled) {
		t.Fatalf("Complete() error = %v, want ErrOpenAIDisabled", err)
	}

	ok, err := router.ModelAvailable(context.Background(), ModelTarget{
		Vendor:  VendorOpenAI,
		BaseURL: "https://api.openai.com",
		Name:    "gpt-4o-mini",
	})
	if ok || err != nil {
		t.Fatalf("ModelAvailable() = %v, %v, want false, nil (cache miss)", ok, err)
	}

	_, unavailable, err := router.EnsureModels(context.Background(), []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: "https://api.openai.com", Name: "gpt-4o-mini"},
	})
	if err == nil {
		t.Fatal("EnsureModels() expected error when only openai targets and no key")
	}
	if len(unavailable) != 1 {
		t.Fatalf("unavailable = %#v", unavailable)
	}
}

func TestRouterDispatchesOpenAIWhenKeyPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
		case "/v1/chat/completions":
			if got := r.Header.Get("Authorization"); got != "Bearer DEV_KEY" {
				t.Fatalf("Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"from openai"}}]}`))
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	router := NewRouter("DEV_KEY")
	if !router.OpenAIEnabled() {
		t.Fatal("OpenAIEnabled() = false, want true")
	}

	got, err := router.Complete(context.Background(), CompleteRequest{
		Vendor:       VendorOpenAI,
		BaseURL:      server.URL,
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got != "from openai" {
		t.Fatalf("result = %q", got)
	}

	available, unavailable, err := router.EnsureModels(context.Background(), []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatalf("EnsureModels() error = %v", err)
	}
	if len(available) != 1 || len(unavailable) != 0 {
		t.Fatalf("available=%#v unavailable=%#v", available, unavailable)
	}
}

func TestRouterKeepsOllamaWhenOpenAIDisabled(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:1.7b"}]}`))
	}))
	defer ollama.Close()

	router := NewRouter("")
	available, unavailable, err := router.EnsureModels(context.Background(), []ModelTarget{
		{Vendor: VendorOllama, BaseURL: ollama.URL, Name: "qwen3:1.7b"},
		{Vendor: VendorOpenAI, BaseURL: "https://api.openai.com", Name: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatalf("EnsureModels() error = %v", err)
	}
	if len(available) != 1 || available[0].Name != "qwen3:1.7b" {
		t.Fatalf("available = %#v", available)
	}
	if len(unavailable) != 1 || unavailable[0].Vendor != VendorOpenAI {
		t.Fatalf("unavailable = %#v", unavailable)
	}
}

func TestRouterOpenAIOnlyDoesNotContactOllama(t *testing.T) {
	var ollamaHits int
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ollamaHits++
		http.NotFound(w, r)
	}))
	defer ollama.Close()

	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
	}))
	defer openaiSrv.Close()

	router := NewRouter("DEV_KEY")
	available, unavailable, err := router.EnsureModels(context.Background(), []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: openaiSrv.URL, Name: ""},
	})
	if err != nil {
		t.Fatalf("EnsureModels() error = %v", err)
	}
	if len(available) != 1 || len(unavailable) != 0 {
		t.Fatalf("available=%#v unavailable=%#v", available, unavailable)
	}
	if ollamaHits != 0 {
		t.Fatalf("ollama hits = %d, want 0", ollamaHits)
	}
}

func TestRouterUnknownVendor(t *testing.T) {
	router := NewRouter("DEV_KEY")
	_, err := router.Complete(context.Background(), CompleteRequest{
		Vendor:       "vllm",
		BaseURL:      "http://example",
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "m",
	})
	if err == nil {
		t.Fatal("Complete() expected unknown vendor error")
	}
}

func TestOpenAICompleteOmitsEmptyModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["model"]; ok {
			t.Fatalf("model field present: %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	got, err := adapter.Complete(context.Background(), CompleteRequest{
		Vendor:       VendorOpenAI,
		BaseURL:      server.URL,
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("result = %q", got)
	}
}
