package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompleteUsesSpecAndBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer DEV_KEY" {
			t.Fatalf("Authorization = %q", got)
		}
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["keep_alive"]; ok {
			t.Fatal("openai chat request must not include keep_alive")
		}
		if _, ok := body["think"]; ok {
			t.Fatal("openai chat request must not include think")
		}
		if body["model"] != "gpt-4o-mini" {
			t.Fatalf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello from api"}}]}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	got, err := adapter.Complete(context.Background(), CompleteRequest{
		Vendor:       VendorOpenAI,
		BaseURL:      server.URL,
		SystemPrompt: "system",
		Prompt:       "user",
		Model:        "gpt-4o-mini",
		KeepAlive:    "5m",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got != "hello from api" {
		t.Fatalf("result = %q", got)
	}
}

func TestOpenAICompleteBaseURLAlreadyHasV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	_, err := adapter.Complete(context.Background(), CompleteRequest{
		BaseURL:      server.URL + "/v1",
		SystemPrompt: "system",
		Prompt:       "user",
		Model:        "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestOpenAICompleteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	_, err := adapter.Complete(context.Background(), CompleteRequest{
		BaseURL:      server.URL,
		SystemPrompt: "system",
		Prompt:       "user",
		Model:        "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("Complete() expected error")
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAICompleteValidation(t *testing.T) {
	adapter := newOpenAIAdapter("DEV_KEY")
	if _, err := adapter.Complete(context.Background(), CompleteRequest{SystemPrompt: "s", Prompt: "", Model: "m"}); err == nil {
		t.Fatal("expected empty prompt error")
	}
	if _, err := adapter.Complete(context.Background(), CompleteRequest{SystemPrompt: "", Prompt: "p", Model: "m"}); err == nil {
		t.Fatal("expected empty system prompt error")
	}
}

func TestOpenAIListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer DEV_KEY" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o-mini"},{"id":"gpt-4o"}]}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	names, err := adapter.ListModels(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(names) != 2 || names[0] != "gpt-4o-mini" {
		t.Fatalf("names = %#v", names)
	}
}

func TestOpenAIEnsureModelsDoesNotPull(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		t.Fatal("EnsureModels must not call the OpenAI API")
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	available, unavailable, err := adapter.EnsureModels(context.Background(), []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"},
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "missing"},
	})
	if err != nil {
		t.Fatalf("EnsureModels() error = %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("available = %#v", available)
	}
	if len(unavailable) != 0 {
		t.Fatalf("unavailable = %#v", unavailable)
	}
	if len(methods) != 0 {
		t.Fatalf("unexpected calls %v", methods)
	}
}

func TestOpenAIListModelsUnreachable(t *testing.T) {
	adapter := newOpenAIAdapter("DEV_KEY")
	_, err := adapter.listModels(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("listModels() expected error")
	}
}

func TestOpenAICompleteEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer server.Close()

	adapter := newOpenAIAdapter("DEV_KEY")
	_, err := adapter.Complete(context.Background(), CompleteRequest{
		BaseURL:      server.URL,
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "m",
	})
	if err == nil {
		t.Fatal("Complete() expected error")
	}
}

func TestOpenAIAPIURL(t *testing.T) {
	if got := openaiAPIURL("https://api.openai.com", "/chat/completions"); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("got %q", got)
	}
	if got := openaiAPIURL("https://api.openai.com/v1", "/models"); got != "https://api.openai.com/v1/models" {
		t.Fatalf("got %q", got)
	}
}
