package openai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
)

func customRegistry(t *testing.T, name, field string) *capability.Registry {
	t.Helper()
	reg, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{name: {BaseURL: "http://llm", Model: "test", InputField: field, MaxInputChars: 200, SystemPrompt: "Return JSON.", PromptVariables: map[string]capability.PromptVariable{"target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
func TestCustomCapabilityHTTPMapping(t *testing.T) {
	reg := customRegistry(t, "custom-translation", "text")
	event, err := eventFromRequest(ChatCompletionRequest{Model: "custom-translation", Messages: []ChatMessage{{Role: "user", Content: "Hallo"}}, SourceLocale: "nl", TargetLocale: "fr"}, "", reg)
	if err != nil {
		t.Fatal(err)
	}
	input := event.Data["input"].(map[string]any)
	if input["text"] != "Hallo" || input["message"] != nil || input["target_locale"] != "fr" || input["source_locale"] != nil {
		t.Fatalf("mapping = %v", input)
	}
}

type atomicSource struct {
	q        Queue
	registry atomic.Pointer[capability.Registry]
}

func (s *atomicSource) InferenceRuntime() (Queue, *capability.Registry) {
	return s.q, s.registry.Load()
}
func TestConcurrentHTTPRequestsAndRegistryReload(t *testing.T) {
	first := customRegistry(t, "custom-one", "message")
	second := customRegistry(t, "custom-two", "text")
	q := &fakeQueue{response: &cloudevent.Event{Data: map[string]any{"capability": "custom-one", "result": map[string]any{"ok": true}}}}
	source := &atomicSource{q: q}
	source.registry.Store(first)
	mux := testMux(source)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			source.registry.Store(first)
			source.registry.Store(second)
		}
	}()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
				var list ModelList
				if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || len(list.Data) != 1 {
					t.Errorf("listing = %d %s", rec.Code, rec.Body.String())
					return
				}
				rec = httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"custom-one","messages":[{"role":"user","content":"hello"}]}`)))
				if rec.Code != 200 && rec.Code != 400 {
					t.Errorf("admission during reload = %d %s", rec.Code, rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()
	source.registry.Store(second)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"custom-one","messages":[{"role":"user","content":"hello"}]}`)))
	if rec.Code != 400 {
		t.Fatalf("removed ID accepted: %d", rec.Code)
	}
}
func TestRemovedQueuedCapabilityIsAnOpenAIError(t *testing.T) {
	failed := &cloudevent.Event{Type: cloudevent.EventTypeRequestFailed, Data: map[string]any{"error": `capability "custom-one" is not configured`}}
	rec := httptest.NewRecorder()
	testMux(staticSource{q: &fakeQueue{response: failed}, reg: customRegistry(t, "custom-one", "message")}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"custom-one","messages":[{"role":"user","content":"hello"}]}`)))
	if rec.Code != 400 {
		t.Fatalf("failed queue response = %d %s", rec.Code, rec.Body.String())
	}
	assertOpenAIError(t, rec, "unknown_model")
}

func TestPromptVariableSelectionRejectedBeforeEnqueue(t *testing.T) {
	for _, tc := range []struct {
		source, target string
		status         int
		param          string
	}{
		{"", "", 200, ""}, {"nl", "pl", 200, ""}, {"en", "nl", 200, ""}, {"pl", "en", 400, "source_locale"}, {"nl", "de", 400, "target_locale"}, {"NL", "en", 400, "source_locale"},
	} {
		t.Run(tc.source+"-"+tc.target, func(t *testing.T) {
			b := capability.ModelBinding{BaseURL: "http://llm", Model: "test", InputField: "text", MaxInputChars: 200, SystemPrompt: "Translate {source_locale} to {target_locale}.", PromptVariables: map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en"}}, "target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl"}}}}
			reg, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{"translate-any": b})
			if err != nil {
				t.Fatal(err)
			}
			q := &fakeQueue{response: &cloudevent.Event{Data: map[string]any{"capability": "translate-any", "result": map[string]any{"text": "hello"}}}}
			data, _ := json.Marshal(ChatCompletionRequest{Model: "translate-any", Messages: []ChatMessage{{Role: "user", Content: "hello"}}, SourceLocale: tc.source, TargetLocale: tc.target})
			rec := httptest.NewRecorder()
			testMux(staticSource{q: q, reg: reg}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(data)))
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.status == 400 {
				var body ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != "invalid_prompt_variable" || body.Error.Param != tc.param {
					t.Fatalf("error=%+v", body.Error)
				}
				if len(q.events) != 0 {
					t.Fatal("invalid locale was enqueued")
				}
			} else if len(q.events) != 1 {
				t.Fatal("valid request was not enqueued")
			}
		})
	}
}
func TestQueuedVariablePolicyFailureIsAnOpenAI400(t *testing.T) {
	failed := &cloudevent.Event{Type: cloudevent.EventTypeRequestFailed, Data: map[string]any{"error": (&capability.PromptVariableError{Variable: "target_locale", Value: "pl", Allowed: []string{"en", "nl"}}).ToMap()}}
	rec := httptest.NewRecorder()
	testMux(staticSource{q: &fakeQueue{response: failed}, reg: customRegistry(t, "custom-one", "message")}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"custom-one","messages":[{"role":"user","content":"hello"}]}`)))
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 400 || body.Error.Code != "invalid_prompt_variable" || body.Error.Param != "target_locale" {
		t.Fatalf("queued failure=%d %+v", rec.Code, body.Error)
	}
}
