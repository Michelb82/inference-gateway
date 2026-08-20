package openai

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
)

func TestEventFromRequestIntent(t *testing.T) {
	event, err := eventFromRequest(ChatCompletionRequest{
		Model:    capability.IntentClassification,
		Messages: []ChatMessage{{Role: "user", Content: "I need my living room painted"}},
		Priority: "HIGH",
	}, "7")
	if err != nil {
		t.Fatalf("eventFromRequest() error = %v", err)
	}
	if event.Type != cloudevent.EventTypeRequest {
		t.Fatalf("Type = %q", event.Type)
	}
	if event.Data["capability"] != capability.IntentClassification {
		t.Fatalf("capability = %v", event.Data["capability"])
	}
	if _, ok := event.Data["model"]; ok {
		t.Fatalf("must not set data.model")
	}
	input, _ := event.Data["input"].(map[string]any)
	if input["message"] != "I need my living room painted" {
		t.Fatalf("input = %v", input)
	}
	if event.Data["priority"] != "HIGH" {
		t.Fatalf("priority = %v", event.Data["priority"])
	}
	if event.OrganisationID == nil || *event.OrganisationID != "7" {
		t.Fatalf("organisation = %v", event.OrganisationID)
	}
	if event.ID == "" || event.ID[:9] != "chatcmpl-" {
		t.Fatalf("ID = %q, want chatcmpl- prefix", event.ID)
	}
}

func TestEventFromRequestTranslateLocales(t *testing.T) {
	event, err := eventFromRequest(ChatCompletionRequest{
		Model:        capability.Translate,
		Messages:     []ChatMessage{{Role: "user", Content: "Hallo"}},
		SourceLocale: "nl",
		TargetLocale: "en",
	}, "")
	if err != nil {
		t.Fatalf("eventFromRequest() error = %v", err)
	}
	input, _ := event.Data["input"].(map[string]any)
	if input["text"] != "Hallo" || input["source_locale"] != "nl" || input["target_locale"] != "en" {
		t.Fatalf("input = %v", input)
	}
}

func TestEventFromRequestSystemAndLastUser(t *testing.T) {
	event, err := eventFromRequest(ChatCompletionRequest{
		Model: capability.Routing,
		Messages: []ChatMessage{
			{Role: "system", Content: "custom"},
			{Role: "user", Content: "first"},
			{Role: "user", Content: "last"},
		},
	}, "7")
	if err != nil {
		t.Fatalf("eventFromRequest() error = %v", err)
	}
	input, _ := event.Data["input"].(map[string]any)
	if input["message"] != "last" {
		t.Fatalf("last user = %v", input["message"])
	}
	if input["system_prompt"] != "custom" {
		t.Fatalf("system_prompt = %v", input["system_prompt"])
	}
}

func TestEventFromRequestRejectsUnknownAndStream(t *testing.T) {
	if _, err := eventFromRequest(ChatCompletionRequest{
		Model:    "qwen3:1.7b-q4_K_M",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, ""); err == nil || err.Status != http.StatusBadRequest || err.Code != "unknown_model" {
		t.Fatalf("unknown model err = %#v", err)
	}

	stream := true
	if _, err := eventFromRequest(ChatCompletionRequest{
		Model:    capability.Routing,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   &stream,
	}, ""); err == nil || err.Param != "stream" {
		t.Fatalf("stream err = %#v", err)
	}

	if _, err := eventFromRequest(ChatCompletionRequest{Model: capability.Routing}, ""); err == nil {
		t.Fatal("expected missing messages")
	}
	if _, err := eventFromRequest(ChatCompletionRequest{
		Model:    capability.Routing,
		Messages: []ChatMessage{{Role: "system", Content: "only"}},
	}, ""); err == nil {
		t.Fatal("expected missing user")
	}
	if _, err := eventFromRequest(ChatCompletionRequest{
		Model:    capability.Routing,
		Messages: []ChatMessage{{Role: "user", Content: "  "}},
	}, ""); err == nil {
		t.Fatal("expected blank user")
	}
}

func TestCompletionFromEventJSONString(t *testing.T) {
	subject := "chatcmpl-abc"
	event := &cloudevent.Event{
		Type:    cloudevent.EventTypeRequestCompleted,
		Subject: &subject,
		ID:      "response-id",
		Data: map[string]any{
			"capability": capability.IntentClassification,
			"result":     map[string]any{"intent": "wall-painting", "confidence": 0.95},
		},
	}
	got := completionFromEvent(event)
	if got.ID != subject {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Model != capability.IntentClassification {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.Object != ObjectChatCompletion {
		t.Fatalf("Object = %q", got.Object)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got.Choices[0].Message.Content), &parsed); err != nil {
		t.Fatalf("content not JSON string: %q", got.Choices[0].Message.Content)
	}
	if parsed["intent"] != "wall-painting" {
		t.Fatalf("parsed = %v", parsed)
	}
}

func TestModelListIDsAreCapabilities(t *testing.T) {
	list := modelList()
	if list.Object != ObjectList {
		t.Fatalf("object = %q", list.Object)
	}
	got := map[string]bool{}
	for _, m := range list.Data {
		got[m.ID] = true
		if m.ID == "qwen3:1.7b-q4_K_M" {
			t.Fatal("must not list Ollama model names")
		}
	}
	for _, name := range capability.Known() {
		if !got[name] {
			t.Fatalf("missing capability %q", name)
		}
	}
}
