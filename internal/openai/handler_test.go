package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/health"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
)

type fakeQueue struct {
	mu       sync.Mutex
	events   []*cloudevent.Event
	response *cloudevent.Event
	waitErr  error
	delay    time.Duration
}

func (f *fakeQueue) Enqueue(ctx context.Context, event *cloudevent.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	return nil
}

func (f *fakeQueue) Wait(ctx context.Context, requestID string, timeout time.Duration) (*cloudevent.Event, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.waitErr != nil {
		return nil, f.waitErr
	}
	if f.response != nil {
		return f.response, nil
	}
	return nil, queue.ErrWaitTimeout
}

type staticSource struct {
	q Queue
}

func (s staticSource) InferenceQueue() Queue { return s.q }

func testMux(source QueueSource) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(source, nil).Register(mux)
	return mux
}

func TestHandleModelsDormant(t *testing.T) {
	mux := testMux(staticSource{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	assertOpenAIError(t, rec, "dormant")
}

func TestHandleModelsListsCapabilities(t *testing.T) {
	mux := testMux(staticSource{q: &fakeQueue{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var list ModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Data) != 3 {
		t.Fatalf("len = %d", len(list.Data))
	}
}

func TestHandleModelsMethodNotAllowed(t *testing.T) {
	mux := testMux(staticSource{q: &fakeQueue{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/models", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsMethodNotAllowed(t *testing.T) {
	mux := testMux(staticSource{q: &fakeQueue{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsDormant(t *testing.T) {
	mux := testMux(staticSource{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsInvalidJSON(t *testing.T) {
	mux := testMux(staticSource{q: &fakeQueue{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsUnknownModel(t *testing.T) {
	fq := &fakeQueue{}
	mux := testMux(staticSource{q: fq})
	body := `{"model":"qwen3:1.7b-q4_K_M","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	assertOpenAIError(t, rec, "unknown_model")
	if len(fq.events) != 0 {
		t.Fatal("must not enqueue unknown models")
	}
}

func TestHandleCompletionsStreamRejected(t *testing.T) {
	mux := testMux(staticSource{q: &fakeQueue{}})
	body := `{"model":"routing","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsSuccess(t *testing.T) {
	subject := "chatcmpl-test"
	fq := &fakeQueue{
		response: &cloudevent.Event{
			Type:    cloudevent.EventTypeRequestCompleted,
			Subject: &subject,
			Data: map[string]any{
				"capability": capability.IntentClassification,
				"result":     map[string]any{"intent": "wall-painting", "confidence": 0.95},
			},
		},
	}
	mux := testMux(staticSource{q: fq})
	body := `{"model":"intent-classification","messages":[{"role":"user","content":"I need my living room painted"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set(HeaderOrganisationID, "7")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
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
	if got.Object != ObjectChatCompletion {
		t.Fatalf("object = %q", got.Object)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got.Choices[0].Message.Content), &parsed); err != nil {
		t.Fatalf("content = %q", got.Choices[0].Message.Content)
	}
	if parsed["intent"] != "wall-painting" {
		t.Fatalf("parsed = %v", parsed)
	}
	if len(fq.events) != 1 {
		t.Fatalf("enqueued = %d", len(fq.events))
	}
	if fq.events[0].OrganisationID == nil || *fq.events[0].OrganisationID != "7" {
		t.Fatalf("org = %v", fq.events[0].OrganisationID)
	}
}

func TestHandleCompletionsFailedEvent(t *testing.T) {
	fq := &fakeQueue{
		response: &cloudevent.Event{
			Type: cloudevent.EventTypeRequestFailed,
			Data: map[string]any{
				"error": map[string]any{"reason": "Prompt is outside bounds", "max_characters": "200"},
			},
		},
	}
	mux := testMux(staticSource{q: fq})
	body := `{"model":"routing","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	assertOpenAIError(t, rec, "prompt_bounds")
}

func TestHandleCompletionsWaitTimeout(t *testing.T) {
	fq := &fakeQueue{waitErr: queue.ErrWaitTimeout}
	mux := testMux(staticSource{q: fq})
	body := `{"model":"routing","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleCompletionsWaitOtherError(t *testing.T) {
	fq := &fakeQueue{waitErr: errors.New("redis down")}
	mux := testMux(staticSource{q: fq})
	body := `{"model":"routing","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSharedMuxHealthAndCompletions(t *testing.T) {
	mux := http.NewServeMux()
	health.NewHandler(capability.NewHolder(), &healthModels{}).Register(mux)
	NewHandler(staticSource{}, nil).Register(mux)

	healthRec := httptest.NewRecorder()
	mux.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/health.json", nil))
	if healthRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d", healthRec.Code)
	}

	compRec := httptest.NewRecorder()
	mux.ServeHTTP(compRec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"routing","messages":[{"role":"user","content":"hi"}]}`)))
	if compRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("completions status = %d", compRec.Code)
	}
}

type healthModels struct{}

func (healthModels) ModelAvailable(ctx context.Context, baseURL, name string) (bool, error) {
	return false, nil
}

func assertOpenAIError(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	if body.Error.Message == "" {
		t.Fatal("missing error.message")
	}
	if code != "" && body.Error.Code != code {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, code)
	}
}
