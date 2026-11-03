package capability_test

import (
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
)

func testRegistry() *capability.Registry {
	return capability.NewRegistry(
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAlive: "5m", MaxInputChars: 200},
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAlive: "5m", MaxInputChars: 8000},
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAlive: "2m", MaxInputChars: 16000},
	)
}

func TestKnownCapabilities(t *testing.T) {
	known := capability.Known()
	if len(known) != 6 {
		t.Fatalf("Known() len = %d", len(known))
	}
	if known[0] != capability.Routing ||
		known[1] != capability.IntentClassification ||
		known[2] != capability.ParameterExtraction ||
		known[3] != capability.PersonalDetails ||
		known[4] != capability.Translate ||
		known[5] != capability.ConfigureEnvironment {
		t.Fatalf("Known() = %v", known)
	}
	if !capability.IsKnown(capability.Routing) ||
		capability.IsKnown("  translate  ") ||
		capability.IsKnown("summarize") ||
		capability.IsKnown("") {
		t.Fatalf("IsKnown mismatch")
	}

}

func TestRegistryFromBindingsNil(t *testing.T) {
	_, err := capability.NewRegistryFromBindings(nil)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("error = %v, want nil bindings", err)
	}
}

func TestRegistryFromBindingsTrimsURL(t *testing.T) {
	reg, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{
		capability.Routing:              {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "a", KeepAlive: "1m", MaxInputChars: 1},
		capability.IntentClassification: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "b", KeepAlive: "1m", MaxInputChars: 2},
		capability.ParameterExtraction:  {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "c", KeepAlive: "1m", MaxInputChars: 3},
		capability.PersonalDetails:      {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "c", KeepAlive: "1m", MaxInputChars: 3},
		capability.Translate:            {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "c", KeepAlive: "1m", MaxInputChars: 3},
		capability.ConfigureEnvironment: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm/", Model: "c", KeepAlive: "1m", MaxInputChars: 3},
	})
	if err != nil {
		t.Fatalf("NewRegistryFromBindings() error = %v", err)
	}
	def, err := reg.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if def.BaseURL != "http://llm" {
		t.Fatalf("BaseURL = %q", def.BaseURL)
	}
}

func TestRegistryGetRequiresURLAndModel(t *testing.T) {
	reg, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{
		capability.Routing:              {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "", Model: "a", KeepAlive: "1m"},
		capability.IntentClassification: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "", KeepAlive: "1m"},
		capability.ParameterExtraction:  {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "c", KeepAlive: "1m"},
		capability.PersonalDetails:      {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "c", KeepAlive: "1m"},
		capability.Translate:            {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "c", KeepAlive: "1m"},
		capability.ConfigureEnvironment: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "c", KeepAlive: "1m"},
	})
	if err != nil {
		t.Fatalf("NewRegistryFromBindings() error = %v", err)
	}
	if _, err := reg.Get(capability.Routing); err == nil || !strings.Contains(err.Error(), "no LLM URL") {
		t.Fatalf("routing error = %v", err)
	}
	if _, err := reg.Get(capability.IntentClassification); err == nil || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("intent error = %v", err)
	}
}

func TestRegistryGetAllowsEmptyOpenAIModel(t *testing.T) {
	reg, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{
		capability.Routing:              {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 1},
		capability.IntentClassification: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 2},
		capability.ParameterExtraction:  {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 3},
		capability.PersonalDetails:      {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 3},
		capability.Translate:            {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 3},
		capability.ConfigureEnvironment: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", Vendor: "openai", BaseURL: "https://someurl", Model: "", KeepAlive: "1m", MaxInputChars: 3},
	})
	if err != nil {
		t.Fatalf("NewRegistryFromBindings() error = %v", err)
	}
	def, err := reg.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if def.Model != "" || def.Vendor != "openai" {
		t.Fatalf("def = %+v", def)
	}
}

func TestHolderAllWhenReady(t *testing.T) {
	h := capability.NewHolder()
	h.Store(testRegistry())
	all := h.All()
	if len(all) != 6 {
		t.Fatalf("All() len = %d", len(all))
	}
	if all[0].Name != capability.Routing {
		t.Fatalf("All()[0] = %q", all[0].Name)
	}
}

func TestPromptBoundsErrorMessage(t *testing.T) {
	err := capability.PromptBoundsError{MaxCharacters: 10}
	if !strings.Contains(err.Error(), "10") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestValidateInputBoundsDisabled(t *testing.T) {
	def, err := testRegistry().Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	def.MaxInputChars = 0
	if err := capability.ValidateInputBounds(def, map[string]any{"message": strings.Repeat("x", 1000)}); err != nil {
		t.Fatalf("disabled bounds: error = %v", err)
	}
}

func TestValidateInputBoundsUnknownCapability(t *testing.T) {
	err := capability.ValidateInputBounds(capability.Definition{Name: "summarize", MaxInputChars: 1}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unknown capability") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildPromptsRequiresInput(t *testing.T) {
	reg := testRegistry()
	routing, err := reg.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, _, err := capability.BuildPrompts(routing, map[string]any{}); err == nil {
		t.Fatalf("expected missing message error")
	}
	translate, err := reg.Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, _, err := capability.BuildPrompts(translate, map[string]any{}); err == nil {
		t.Fatalf("expected missing text error")
	}
	if _, _, err := capability.BuildPrompts(capability.Definition{Name: "summarize"}, map[string]any{}); err == nil {
		t.Fatalf("expected unknown capability error")
	}
}

func TestBuildPromptsTranslateDefaultLocales(t *testing.T) {
	def, err := testRegistry().Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	systemPrompt, _, err := capability.BuildPrompts(def, map[string]any{"text": "Hallo"})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if !strings.Contains(systemPrompt, "Dutch") || !strings.Contains(systemPrompt, "English") {
		t.Fatalf("systemPrompt = %q", systemPrompt)
	}
}

func TestParseRawJSONErrors(t *testing.T) {
	if _, err := capability.ParseRawJSON(""); err == nil {
		t.Fatalf("expected empty error")
	}
	if _, err := capability.ParseRawJSON("{}"); err == nil {
		t.Fatalf("expected empty JSON object error")
	}
}

func TestParseResultUnknownCapability(t *testing.T) {
	if _, err := capability.ParseResult("summarize", `{"x":1}`); err == nil {
		t.Fatalf("expected unknown capability error")
	}
}

func TestRegistryGet(t *testing.T) {
	reg := testRegistry()

	routing, err := reg.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get(routing) error = %v", err)
	}
	if routing.Model != "qwen3:1.7b-q4_K_M" {
		t.Fatalf("routing model = %q", routing.Model)
	}
	if routing.BaseURL != "http://llm-model:11434" {
		t.Fatalf("routing url = %q", routing.BaseURL)
	}
	if routing.KeepAlive != "5m" {
		t.Fatalf("routing keep_alive = %q", routing.KeepAlive)
	}

	intent, err := reg.Get(capability.IntentClassification)
	if err != nil {
		t.Fatalf("Get(intent) error = %v", err)
	}
	if intent.KeepAlive != "5m" {
		t.Fatalf("intent keep_alive = %q", intent.KeepAlive)
	}

	translate, err := reg.Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get(translate) error = %v", err)
	}
	if translate.Model != "qwen3:14b-q4_K_M" {
		t.Fatalf("translate model = %q", translate.Model)
	}
	if translate.KeepAlive != "2m" {
		t.Fatalf("translate keep_alive = %q", translate.KeepAlive)
	}
}

func TestRegistryFromBindingsRequiresKnownCapabilities(t *testing.T) {
	_, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{
		capability.Routing: {SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm", Model: "a", KeepAlive: "1m"},
	})
	if err == nil {
		t.Fatalf("expected error for incomplete bindings")
	}
}

func TestRegistryUnknownCapability(t *testing.T) {
	reg := capability.NewRegistry(
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "a", KeepAlive: "1m"},
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "b", KeepAlive: "1m"},
		capability.ModelBinding{SystemPrompt: "Test prompt from {source_locale} to {target_locale}.", BaseURL: "http://llm-model:11434", Model: "c", KeepAlive: "1m"},
	)
	_, err := reg.Get("unknown")
	if err == nil {
		t.Fatalf("expected error for unknown capability")
	}
}

func TestHolderDormant(t *testing.T) {
	h := capability.NewHolder()
	if h.Ready() {
		t.Fatalf("expected not ready")
	}
	if len(h.All()) != 0 {
		t.Fatalf("expected empty All()")
	}
	_, err := h.Get(capability.Routing)
	if err == nil {
		t.Fatalf("expected dormant get error")
	}
	h.Store(testRegistry())
	if !h.Ready() {
		t.Fatalf("expected ready after store")
	}
	if _, err := h.Get(capability.Routing); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestRegistryAllOrder(t *testing.T) {
	reg := testRegistry()
	all := reg.All()
	if len(all) != 6 {
		t.Fatalf("All() len = %d", len(all))
	}
	if all[0].Name != capability.Routing ||
		all[1].Name != capability.IntentClassification ||
		all[2].Name != capability.ParameterExtraction ||
		all[3].Name != capability.PersonalDetails ||
		all[4].Name != capability.Translate ||
		all[5].Name != capability.ConfigureEnvironment {
		t.Fatalf("unexpected order: %+v", all)
	}
}

func TestBuildPromptsTranslate(t *testing.T) {
	def, err := testRegistry().Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	systemPrompt, userPrompt, err := capability.BuildPrompts(def, map[string]any{
		"text":          "Hallo wereld",
		"source_locale": "nl",
		"target_locale": "en",
	})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if userPrompt != "Hallo wereld" {
		t.Fatalf("userPrompt = %q", userPrompt)
	}
	if !strings.Contains(systemPrompt, "Dutch") || !strings.Contains(systemPrompt, "English") {
		t.Fatalf("systemPrompt = %q", systemPrompt)
	}
}

func TestBuildPromptsForConstructionCapabilities(t *testing.T) {
	for _, name := range []string{
		capability.ParameterExtraction,
		capability.PersonalDetails,
		capability.ConfigureEnvironment,
	} {
		def, err := testRegistry().Get(name)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", name, err)
		}
		systemPrompt, userPrompt, err := capability.BuildPrompts(def, map[string]any{"message": "customer input"})
		if err != nil {
			t.Fatalf("BuildPrompts(%s) error = %v", name, err)
		}
		if systemPrompt == "" || userPrompt != "customer input" {
			t.Fatalf("BuildPrompts(%s) = %q, %q", name, systemPrompt, userPrompt)
		}
		parsed, err := capability.ParseResult(name, `{"result":"ok"}`)
		if err != nil || parsed["result"] != "ok" {
			t.Fatalf("ParseResult(%s) = %#v, %v", name, parsed, err)
		}
	}
}

func TestBuildPromptsRoutingUsesBuiltInPrompt(t *testing.T) {
	def, err := testRegistry().Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	systemPrompt, userPrompt, err := capability.BuildPrompts(def, map[string]any{
		"message": "route me",
	})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if userPrompt != "route me" {
		t.Fatalf("userPrompt = %q", userPrompt)
	}
	if systemPrompt == "" || strings.Contains(systemPrompt, "route me") {
		t.Fatalf("expected configured system prompt, got %q", systemPrompt)
	}
}

func TestBuildPromptsTranslateUnknownLocale(t *testing.T) {
	def, err := testRegistry().Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	systemPrompt, _, err := capability.BuildPrompts(def, map[string]any{
		"text":          "Hallo",
		"source_locale": "de",
		"target_locale": "fr",
	})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if !strings.Contains(systemPrompt, "de") || !strings.Contains(systemPrompt, "fr") {
		t.Fatalf("systemPrompt = %q", systemPrompt)
	}
}

func TestBuildPromptsUsesSystemPromptOverride(t *testing.T) {
	def, err := testRegistry().Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	custom := "Custom routing system prompt for this organisation."
	systemPrompt, userPrompt, err := capability.BuildPrompts(def, map[string]any{
		"message":       `{"customer_request":"muren verven"}`,
		"system_prompt": custom,
	})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if systemPrompt != custom {
		t.Fatalf("systemPrompt = %q, want override", systemPrompt)
	}
	if userPrompt != `{"customer_request":"muren verven"}` {
		t.Fatalf("userPrompt = %q", userPrompt)
	}
}

func TestBuildPromptsTranslateUsesSystemPromptOverride(t *testing.T) {
	def, err := testRegistry().Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	custom := "Translate this service description carefully."
	systemPrompt, userPrompt, err := capability.BuildPrompts(def, map[string]any{
		"text":          "Hallo",
		"system_prompt": custom,
		"source_locale": "nl",
		"target_locale": "en",
	})
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if systemPrompt != custom {
		t.Fatalf("systemPrompt = %q, want override", systemPrompt)
	}
	if userPrompt != "Hallo" {
		t.Fatalf("userPrompt = %q", userPrompt)
	}
}

func TestParseRawJSON(t *testing.T) {
	result, err := capability.ParseRawJSON(`{"jobs":[{"job_id":1,"confidence":0.9}],"route":"wizard"}`)
	if err != nil {
		t.Fatalf("ParseRawJSON() error = %v", err)
	}
	if result["route"] != "wizard" {
		t.Fatalf("route = %v", result["route"])
	}
}

func TestParseRoutingResult(t *testing.T) {
	result, err := capability.ParseResult(capability.Routing, `{"capability":"intent-classification"}`)
	if err != nil {
		t.Fatalf("ParseResult() error = %v", err)
	}
	if result["capability"] != "intent-classification" {
		t.Fatalf("capability = %v", result["capability"])
	}
}

func TestParseIntentResult(t *testing.T) {
	result, err := capability.ParseResult(capability.IntentClassification, "```json\n{\"intent\":\"wall-painting\",\"confidence\":0.95}\n```")
	if err != nil {
		t.Fatalf("ParseResult() error = %v", err)
	}
	if result["intent"] != "wall-painting" {
		t.Fatalf("intent = %v", result["intent"])
	}
}

func TestParseTranslateResult(t *testing.T) {
	result, err := capability.ParseResult(capability.Translate, `{"text":"Hello world"}`)
	if err != nil {
		t.Fatalf("ParseResult() error = %v", err)
	}
	if result["text"] != "Hello world" {
		t.Fatalf("text = %v", result["text"])
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := capability.ParseResult(capability.Routing, "not-json")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseMissingFields(t *testing.T) {
	_, err := capability.ParseResult(capability.Routing, `{"capability":""}`)
	if err == nil {
		t.Fatalf("expected error for empty capability")
	}
	_, err = capability.ParseResult(capability.IntentClassification, `{"intent":"x"}`)
	if err == nil {
		t.Fatalf("expected error for missing confidence")
	}
	_, err = capability.ParseResult(capability.IntentClassification, `{"intent":"","confidence":0.5}`)
	if err == nil {
		t.Fatalf("expected error for empty intent")
	}
	_, err = capability.ParseResult(capability.Translate, `{"text":""}`)
	if err == nil {
		t.Fatalf("expected error for empty text")
	}
}

func TestValidateInputBoundsRouting(t *testing.T) {
	def, err := testRegistry().Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	def.MaxInputChars = 5

	if err := capability.ValidateInputBounds(def, map[string]any{"message": "hello"}); err != nil {
		t.Fatalf("under limit: error = %v", err)
	}
	if err := capability.ValidateInputBounds(def, map[string]any{"message": "12345"}); err != nil {
		t.Fatalf("at limit: error = %v", err)
	}
	err = capability.ValidateInputBounds(def, map[string]any{"message": "123456"})
	if err == nil {
		t.Fatalf("expected error over limit")
	}
	boundsErr, ok := err.(capability.PromptBoundsError)
	if !ok {
		t.Fatalf("error type = %T, want PromptBoundsError", err)
	}
	if boundsErr.MaxCharacters != 5 {
		t.Fatalf("MaxCharacters = %d, want 5", boundsErr.MaxCharacters)
	}
}

func TestValidateInputBoundsTranslate(t *testing.T) {
	def, err := testRegistry().Get(capability.Translate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	def.MaxInputChars = 3

	if err := capability.ValidateInputBounds(def, map[string]any{"text": "abc"}); err != nil {
		t.Fatalf("at limit: error = %v", err)
	}
	if err := capability.ValidateInputBounds(def, map[string]any{"text": "abcd"}); err == nil {
		t.Fatalf("expected error over limit")
	}
}

func TestValidateInputBoundsCountsRunesNotBytes(t *testing.T) {
	def, err := testRegistry().Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	def.MaxInputChars = 2

	// é is one rune but two bytes in UTF-8.
	if err := capability.ValidateInputBounds(def, map[string]any{"message": "é"}); err != nil {
		t.Fatalf("single rune: error = %v", err)
	}
	if err := capability.ValidateInputBounds(def, map[string]any{"message": "éé"}); err != nil {
		t.Fatalf("two runes at limit: error = %v", err)
	}
	if err := capability.ValidateInputBounds(def, map[string]any{"message": "ééé"}); err == nil {
		t.Fatalf("expected error over limit")
	}
}

func TestPromptBoundsErrorToMap(t *testing.T) {
	m := capability.PromptBoundsError{MaxCharacters: 200}.ToMap()
	if m["reason"] != "Prompt is outside bounds" {
		t.Fatalf("reason = %v", m["reason"])
	}
	if m["max_characters"] != "200" {
		t.Fatalf("max_characters = %v", m["max_characters"])
	}
}

func TestRegistryUsesConfiguredPromptAndRejectsMissingPrompt(t *testing.T) {
	bindings := make(map[string]capability.ModelBinding)
	for _, name := range capability.Known() {
		bindings[name] = capability.ModelBinding{BaseURL: "http://llm", Model: "test", SystemPrompt: "Configured " + name}
	}
	reg, err := capability.NewRegistryFromBindings(bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range capability.Known() {
		def, err := reg.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		prompt, _, err := capability.BuildPrompts(def, map[string]any{"message": "hello", "text": "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if prompt != "Configured "+name {
			t.Fatalf("%s prompt = %q", name, prompt)
		}
	}
	bindings[capability.Routing] = capability.ModelBinding{}
	if _, err := capability.NewRegistryFromBindings(bindings); err == nil {
		t.Fatal("accepted missing prompt")
	}
}

func TestTranslatePromptPreservesLiteralPercentSigns(t *testing.T) {
	def := capability.Definition{Name: capability.Translate, SystemPrompt: "Translate {source_locale} to {target_locale}, 100% accurately."}
	prompt, _, err := capability.BuildPrompts(def, map[string]any{"text": "Hallo"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "Translate Dutch to English, 100% accurately." {
		t.Fatalf("prompt = %q", prompt)
	}
}
