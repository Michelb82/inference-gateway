package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
	"github.com/mywebsite/construction-ai-gateway/internal/worker"
	"github.com/redis/go-redis/v9"
)

func TestE2EIntentSuccess(t *testing.T) {
	mux, ollama, stop := newE2E(t, nil)
	defer stop()

	body := `{"model":"intent-classification","messages":[{"role":"user","content":"I need my living room painted"}]}`
	rec := postCompletions(t, mux, body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var got ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Model != capability.IntentClassification {
		t.Fatalf("model = %q", got.Model)
	}
	if ollama.model != "qwen3:4b-q4_K_M" {
		t.Fatalf("ollama model = %q", ollama.model)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got.Choices[0].Message.Content), &parsed); err != nil {
		t.Fatalf("content = %q", got.Choices[0].Message.Content)
	}
	if parsed["intent"] != "wall-painting" {
		t.Fatalf("parsed = %v", parsed)
	}
}

func TestE2ETranslateAndRouting(t *testing.T) {
	mux, ollama, stop := newE2E(t, nil)
	defer stop()

	rec := postCompletions(t, mux, `{"model":"translate","messages":[{"role":"user","content":"Elektrische installaties voor woningen"}],"source_locale":"nl","target_locale":"en"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("translate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if ollama.prompt != "Elektrische installaties voor woningen" {
		t.Fatalf("prompt = %q", ollama.prompt)
	}

	rec = postCompletions(t, mux, `{"model":"routing","messages":[{"role":"user","content":"I want my bathroom renovated"}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("routing status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestE2ESystemPromptDenied(t *testing.T) {
	mux, ollama, stop := newE2E(t, nil)
	defer stop()

	body := `{"model":"routing","messages":[{"role":"system","content":"custom"},{"role":"user","content":"hi"}]}`
	rec := postCompletions(t, mux, body, "7")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if ollama.called {
		t.Fatal("Complete should not be called")
	}
}

func TestE2EConcurrentWaitersDoNotSteal(t *testing.T) {
	mux, _, stop := newE2E(t, map[string]string{
		"one": `{"intent":"a","confidence":0.1}`,
		"two": `{"intent":"b","confidence":0.2}`,
	})
	defer stop()

	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	bodies := []string{
		`{"model":"intent-classification","messages":[{"role":"user","content":"one"}]}`,
		`{"model":"intent-classification","messages":[{"role":"user","content":"two"}]}`,
	}
	for i, body := range bodies {
		wg.Add(1)
		go func(i int, body string) {
			defer wg.Done()
			results[i] = postCompletions(t, mux, body, "")
		}(i, body)
	}
	wg.Wait()

	intents := map[string]bool{}
	for i, rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("status[%d] = %d body = %s", i, rec.Code, rec.Body.String())
		}
		var got ChatCompletionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(got.Choices[0].Message.Content), &parsed); err != nil {
			t.Fatalf("content[%d] = %q", i, got.Choices[0].Message.Content)
		}
		intent, _ := parsed["intent"].(string)
		intents[intent] = true
	}
	if !intents["a"] || !intents["b"] {
		t.Fatalf("intents = %v, waiters stole replies", intents)
	}
}

func postCompletions(t *testing.T, mux *http.ServeMux, body, org string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set(HeaderOrganisationID, org)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func newE2E(t *testing.T, byPrompt map[string]string) (*http.ServeMux, *e2eOllama, func()) {
	t.Helper()
	t.Cleanup(func() { cloudevent.ConfigureTypes(cloudevent.DefaultTypePrefix) })

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	q := queue.NewRedisQueue(client, "ai.requests", "ai.responses", 1, 3, 3)

	ollama := &e2eOllama{
		byPrompt: byPrompt,
		fallback: `{"intent":"wall-painting","confidence":0.95}`,
	}
	if byPrompt == nil {
		ollama.fallback = `{"intent":"wall-painting","confidence":0.95}`
		ollama.routing = `{"capability":"intent-classification"}`
		ollama.translate = `{"text":"Electrical installations for homes"}`
	}

	reg := capability.NewRegistry(
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAlive: "5m", MaxInputChars: 200},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAlive: "5m", MaxInputChars: 8000},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAlive: "2m", MaxInputChars: 16000},
	)

	ctx, cancel := context.WithCancel(context.Background())
	w := worker.New(q, q, ollama, &e2eModels{available: true}, reg, nil, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()

	mux := http.NewServeMux()
	h := NewHandler(staticSource{q: q}, nil)
	h.waitTimeout = 5 * time.Second
	h.Register(mux)

	stop := func() {
		cancel()
		<-done
		_ = client.Close()
	}
	return mux, ollama, stop
}

type e2eOllama struct {
	mu        sync.Mutex
	byPrompt  map[string]string
	fallback  string
	routing   string
	translate string
	prompt    string
	model     string
	called    bool
}

func (f *e2eOllama) Complete(ctx context.Context, baseURL, systemPrompt, prompt, model, keepAlive string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = true
	f.prompt = prompt
	f.model = model
	if f.byPrompt != nil {
		if v, ok := f.byPrompt[prompt]; ok {
			return v, nil
		}
	}
	if f.routing != "" && model == "qwen3:1.7b-q4_K_M" {
		return f.routing, nil
	}
	if f.translate != "" && model == "qwen3:14b-q4_K_M" {
		return f.translate, nil
	}
	return f.fallback, nil
}

type e2eModels struct {
	available bool
}

func (f *e2eModels) ModelAvailable(ctx context.Context, baseURL, name string) (bool, error) {
	return f.available, nil
}
