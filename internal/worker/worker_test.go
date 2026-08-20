package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
)

func testRegistry() *capability.Registry {
	return capability.NewRegistry(
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAlive: "5m", MaxInputChars: 200},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAlive: "5m", MaxInputChars: 8000},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAlive: "2m", MaxInputChars: 16000},
	)
}

func testOverridePolicy(orgs []string, maxChars int) *capability.OverridePolicyHolder {
	h := capability.NewOverridePolicyHolder()
	h.Store(capability.PolicyFromOrgs(orgs, maxChars))
	return h
}

func strPtr(s string) *string { return &s }

func TestHandleIntentSuccess(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"intent":"wall-painting","confidence":0.95}`}
	models := &fakeModels{available: true}

	w := New(nil, publisher, ollama, models, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	response := publisher.events[0]
	if response.Type != cloudevent.EventTypeRequestCompleted {
		t.Fatalf("Type = %q", response.Type)
	}
	if _, ok := response.Data["model"]; ok {
		t.Fatalf("success payload must not include model")
	}
	input, ok := response.Data["input"].(map[string]any)
	if !ok || input["message"] != "I need my living room painted" {
		t.Fatalf("expected request input echoed, got %v", response.Data["input"])
	}
	if response.Data["capability"] != "intent-classification" {
		t.Fatalf("capability = %v", response.Data["capability"])
	}
	if ollama.model != "qwen3:4b-q4_K_M" {
		t.Fatalf("model used = %q", ollama.model)
	}
	if ollama.keepAlive != "5m" {
		t.Fatalf("intent keepAlive = %q, want 5m", ollama.keepAlive)
	}
}

func TestHandleRoutingSuccess(t *testing.T) {
	request := mustEvent(t, "request_routing.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	models := &fakeModels{available: true}

	w := New(nil, publisher, ollama, models, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	result := publisher.events[0].Data["result"].(map[string]any)
	if result["capability"] != "intent-classification" {
		t.Fatalf("result = %v", result)
	}
}

func TestHandleRoutingWithSystemPromptOverride(t *testing.T) {
	customPrompt := "Custom org routing prompt"
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-override-1",
		OrganisationID: strPtr("7"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": customPrompt,
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"jobs":[{"job_id":1,"confidence":0.9}],"route":"wizard","summary":"wall painting"}`}
	models := &fakeModels{available: true}

	w := New(nil, publisher, ollama, models, testRegistry(), testOverridePolicy([]string{"7"}, 4000), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	if ollama.systemPrompt != customPrompt {
		t.Fatalf("systemPrompt = %q, want override", ollama.systemPrompt)
	}
	result := publisher.events[0].Data["result"].(map[string]any)
	if result["route"] != "wizard" {
		t.Fatalf("result = %v", result)
	}
	if _, ok := result["capability"]; ok {
		t.Fatalf("custom prompt result should not be forced into capability schema, got %v", result)
	}
}

func TestHandleRejectsSystemPromptWithoutAllowlist(t *testing.T) {
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-override-denied",
		OrganisationID: strPtr("7"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": "Custom org routing prompt",
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy(nil, 4000), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called when system_prompt is not allowed")
	}
	if got := publisher.events[0].Data["error"]; got != "data.input.system_prompt is not allowed for this organisation" {
		t.Fatalf("error = %v", got)
	}
}

func TestHandleRejectsSystemPromptWithoutOrganisation(t *testing.T) {
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/intent/routing",
		ID:     "routing-override-no-org",
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": "Custom org routing prompt",
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy([]string{"7"}, 4000), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called when organisation_id is missing")
	}
}

func TestHandleRejectsOversizedSystemPrompt(t *testing.T) {
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-override-bounds",
		OrganisationID: strPtr("7"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": strings.Repeat("x", 11),
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy([]string{"7"}, 10), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	response := publisher.events[0]
	if response.Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", response.Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called when system_prompt exceeds character limit")
	}
	errField, ok := response.Data["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %T, want map", response.Data["error"])
	}
	if errField["reason"] != "Prompt is outside bounds" {
		t.Fatalf("reason = %v", errField["reason"])
	}
	if errField["max_characters"] != "10" {
		t.Fatalf("max_characters = %v", errField["max_characters"])
	}
}

func TestHandleRejectsSystemPromptForNonAllowlistedOrg(t *testing.T) {
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-override-wrong-org",
		OrganisationID: strPtr("99"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": "Custom org routing prompt",
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy([]string{"7"}, 4000), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called for non-allowlisted organisation")
	}
}

func TestHandleAllowsSystemPromptAtExactCharLimit(t *testing.T) {
	customPrompt := strings.Repeat("é", 10)
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-override-exact-bounds",
		OrganisationID: strPtr("7"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message":       `{"customer_request":"muren verven"}`,
				"system_prompt": customPrompt,
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"jobs":[{"job_id":1}],"route":"wizard"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy([]string{"7"}, 10), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestCompleted {
		t.Fatalf("Type = %q, want completed", publisher.events[0].Type)
	}
	if ollama.systemPrompt != customPrompt {
		t.Fatalf("systemPrompt = %q, want exact-limit override", ollama.systemPrompt)
	}
}

func TestHandleAllowlistedOrgWithoutSystemPromptUsesCapabilitySchema(t *testing.T) {
	event := &cloudevent.Event{
		Type:           cloudevent.EventTypeRequest,
		Source:         "/intent/routing",
		ID:             "routing-no-override",
		OrganisationID: strPtr("7"),
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message": `{"customer_request":"muren verven"}`,
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification","extra":"ignored"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), testOverridePolicy([]string{"7"}, 4000), nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestCompleted {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
	if !strings.Contains(ollama.systemPrompt, "routing model") {
		t.Fatalf("expected builtin routing system prompt, got %q", ollama.systemPrompt)
	}
	result := publisher.events[0].Data["result"].(map[string]any)
	if result["capability"] != "intent-classification" {
		t.Fatalf("result = %v", result)
	}
	if _, ok := result["extra"]; ok {
		t.Fatalf("capability schema should drop extra fields, got %v", result)
	}
}

func TestHandleTranslateSuccess(t *testing.T) {
	request := mustEvent(t, "request_translate.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"text":"Electrical installations for homes"}`}
	models := &fakeModels{available: true}

	w := New(nil, publisher, ollama, models, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	if ollama.prompt != "Elektrische installaties voor woningen" {
		t.Fatalf("prompt = %q", ollama.prompt)
	}
	if ollama.model != "qwen3:14b-q4_K_M" {
		t.Fatalf("model = %q", ollama.model)
	}
	if ollama.keepAlive != "2m" {
		t.Fatalf("keepAlive = %q", ollama.keepAlive)
	}
	result := publisher.events[0].Data["result"].(map[string]any)
	if result["text"] != "Electrical installations for homes" {
		t.Fatalf("result = %v", result)
	}
}

func TestHandleModelUnavailable(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"intent":"x","confidence":1}`}
	models := &fakeModels{available: false}

	w := New(nil, publisher, ollama, models, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	response := publisher.events[0]
	if response.Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", response.Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called when model unavailable")
	}
}

func TestHandleUnknownCapability(t *testing.T) {
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/test",
		ID:     "1",
		Data: map[string]any{
			"capability": "unknown",
			"input":      map[string]any{"message": "hi"},
		},
	}
	publisher := &fakePublisher{}
	w := New(nil, publisher, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil)
	_ = w.handle(context.Background(), event)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestHandleRejectsCallerModel(t *testing.T) {
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/test",
		ID:     "1",
		Data: map[string]any{
			"capability": "routing",
			"model":      "sneaky",
			"input":      map[string]any{"message": "hi"},
		},
	}
	publisher := &fakePublisher{}
	w := New(nil, publisher, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil)
	_ = w.handle(context.Background(), event)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestHandleOllamaError(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{err: errors.New("boom")}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), nil, nil)
	_ = w.handle(context.Background(), request)
	if publisher.events[0].Data["error"] != "boom" {
		t.Fatalf("error = %v", publisher.events[0].Data["error"])
	}
}

func TestHandleInputExceedsCharacterLimit(t *testing.T) {
	longMessage := strings.Repeat("a", 201)
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/test",
		ID:     "bounds-1",
		Data: map[string]any{
			"capability": "routing",
			"input": map[string]any{
				"message": longMessage,
			},
		},
	}
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"capability":"intent-classification"}`}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), event); err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	response := publisher.events[0]
	if response.Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", response.Type)
	}
	if ollama.called {
		t.Fatalf("Complete should not be called when input exceeds character limit")
	}
	input, ok := response.Data["input"].(map[string]any)
	if !ok || input["message"] != longMessage {
		t.Fatalf("expected original input echoed, got %v", response.Data["input"])
	}
	errField, ok := response.Data["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %T, want map", response.Data["error"])
	}
	if errField["reason"] != "Prompt is outside bounds" {
		t.Fatalf("reason = %v", errField["reason"])
	}
	if errField["max_characters"] != "200" {
		t.Fatalf("max_characters = %v", errField["max_characters"])
	}
}

func TestHandleUnparseableOutput(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: "not-json"}
	w := New(nil, publisher, ollama, &fakeModels{available: true}, testRegistry(), nil, nil)
	_ = w.handle(context.Background(), request)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestHandleModelAvailabilityError(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{}
	ollama := &fakeOllama{result: `{"intent":"x","confidence":1}`}
	w := New(nil, publisher, ollama, &fakeModels{err: errors.New("tags down")}, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
	if ollama.called {
		t.Fatal("Complete should not be called when availability check errors")
	}
}

func TestHandlePublishError(t *testing.T) {
	request := mustEvent(t, "request_intent.json")
	publisher := &fakePublisher{err: errors.New("redis write")}
	w := New(nil, publisher, &fakeOllama{result: `{"intent":"x","confidence":1}`}, &fakeModels{available: true}, testRegistry(), nil, nil)
	if err := w.handle(context.Background(), request); err == nil {
		t.Fatal("handle() expected publish error")
	}
}

func TestHandleMissingInput(t *testing.T) {
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/test",
		ID:     "1",
		Data:   map[string]any{"capability": "routing"},
	}
	publisher := &fakePublisher{}
	_ = New(nil, publisher, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil).handle(context.Background(), event)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestHandleMissingCapability(t *testing.T) {
	event := &cloudevent.Event{
		Type:   cloudevent.EventTypeRequest,
		Source: "/test",
		ID:     "1",
		Data:   map[string]any{"input": map[string]any{"message": "hi"}},
	}
	publisher := &fakePublisher{}
	_ = New(nil, publisher, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil).handle(context.Background(), event)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestHandleUnsupportedEventType(t *testing.T) {
	event := &cloudevent.Event{
		Type:   "not-a-request",
		Source: "/test",
		ID:     "1",
		Data:   map[string]any{"capability": "routing", "input": map[string]any{"message": "hi"}},
	}
	publisher := &fakePublisher{}
	_ = New(nil, publisher, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil).handle(context.Background(), event)
	if publisher.events[0].Type != cloudevent.EventTypeRequestFailed {
		t.Fatalf("Type = %q", publisher.events[0].Type)
	}
}

func TestRunReturnsOnConsumeError(t *testing.T) {
	w := New(&fakeConsumer{err: errors.New("brpop failed")}, &fakePublisher{}, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil)
	if err := w.Run(context.Background()); err == nil {
		t.Fatal("Run() expected consume error")
	}
}

func TestRunSkipsNilEventsUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	consumer := &fakeConsumer{nilForever: true, cancel: cancel, nilsBeforeCancel: 2}
	w := New(consumer, &fakePublisher{}, &fakeOllama{}, &fakeModels{available: true}, testRegistry(), nil, nil)
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if consumer.calls < 2 {
		t.Fatalf("calls = %d, want at least 2 nil consumes", consumer.calls)
	}
}

func mustEvent(t *testing.T, fixture string) *cloudevent.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	event, err := cloudevent.FromJSON(string(raw))
	if err != nil {
		t.Fatalf("FromJSON() error = %v", err)
	}
	return event
}

type fakePublisher struct {
	events []*cloudevent.Event
	err    error
}

func (f *fakePublisher) Publish(ctx context.Context, event *cloudevent.Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeConsumer struct {
	events           []*cloudevent.Event
	err              error
	calls            int
	nilForever       bool
	nilsBeforeCancel int
	cancel           context.CancelFunc
}

func (f *fakeConsumer) Consume(ctx context.Context) (*cloudevent.Event, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.nilForever {
		if f.cancel != nil && f.calls >= f.nilsBeforeCancel {
			f.cancel()
		}
		return nil, nil
	}
	if len(f.events) == 0 {
		return nil, nil
	}
	event := f.events[0]
	f.events = f.events[1:]
	return event, nil
}

type fakeOllama struct {
	systemPrompt string
	prompt       string
	model        string
	keepAlive    string
	result       string
	err          error
	called       bool
}

func (f *fakeOllama) Complete(ctx context.Context, baseURL, systemPrompt, prompt, model, keepAlive string) (string, error) {
	f.called = true
	f.systemPrompt = systemPrompt
	f.prompt = prompt
	f.model = model
	f.keepAlive = keepAlive
	if f.err != nil {
		return "", f.err
	}
	return f.result, nil
}

type fakeModels struct {
	available bool
	err       error
}

func (f *fakeModels) ModelAvailable(ctx context.Context, baseURL, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.available, nil
}
