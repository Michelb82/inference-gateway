package capability_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
)

func binding() capability.ModelBinding {
	return capability.ModelBinding{Vendor: "openai", BaseURL: "http://llm/", Model: "test", InputField: "message", SystemPrompt: "Return JSON.", MaxInputChars: 200}
}
func registry(t *testing.T, bindings map[string]capability.ModelBinding) *capability.Registry {
	t.Helper()
	reg, err := capability.NewRegistryFromBindings(bindings)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
func TestDynamicRegistry(t *testing.T) {
	reg := registry(t, map[string]capability.ModelBinding{"z-new": binding(), "appointment-summary": binding()})
	all := reg.All()
	if len(all) != 2 || all[0].Name != "appointment-summary" || all[1].Name != "z-new" {
		t.Fatalf("definitions = %+v", all)
	}
	if all[0].BaseURL != "http://llm" {
		t.Fatal("URL not normalized")
	}
	if _, err := reg.Get("routing"); err == nil {
		t.Fatal("unconfigured built-in name accepted")
	}
	if _, err := reg.Get(" appointment-summary"); err == nil {
		t.Fatal("lookup silently trimmed ID")
	}
}
func TestRegistrationRejectsInvalidCandidates(t *testing.T) {
	if _, err := capability.NewRegistryFromBindings(nil); err == nil {
		t.Fatal("accepted empty registry")
	}
	for _, tc := range []struct {
		name   string
		change func(*capability.ModelBinding)
	}{
		{"bad id", func(b *capability.ModelBinding) {}},
		{"input", func(b *capability.ModelBinding) { b.InputField = "" }},
		{"prompt", func(b *capability.ModelBinding) { b.SystemPrompt = " " }},
		{"limit", func(b *capability.ModelBinding) { b.MaxInputChars = 199 }},
		{"url", func(b *capability.ModelBinding) { b.BaseURL = "" }},
		{"model", func(b *capability.ModelBinding) { b.Vendor = "ollama"; b.Model = "" }},
		{"variable", func(b *capability.ModelBinding) {
			b.PromptVariables = map[string]capability.PromptVariable{"secret": {Default: "x", Allowed: []string{"x"}}}
		}},
		{"placeholder", func(b *capability.ModelBinding) { b.SystemPrompt = "Hello {unknown}" }},
		{"default", func(b *capability.ModelBinding) {
			b.PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: " ", Allowed: []string{"nl", "en", "de"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := binding()
			tc.change(&b)
			if _, err := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{tc.name: b}); err == nil {
				t.Fatal("accepted invalid candidate")
			}
		})
	}
}
func TestRegistryOwnsAllMutableDefinitionData(t *testing.T) {
	b := binding()
	min := 1
	low, high := 0.0, 1.0
	b.PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}}
	b.OutputSchema = &capability.OutputSchema{Type: "object", Required: []string{"answer"}, Properties: map[string]capability.PropertySchema{"answer": {Type: "string", MinLength: &min}, "score": {Type: "number", Minimum: &low, Maximum: &high}}}
	reg := registry(t, map[string]capability.ModelBinding{"custom": b})
	b.PromptVariables["source_locale"] = capability.PromptVariable{Default: "changed", Allowed: []string{"changed"}}
	b.OutputSchema.Required[0] = "changed"
	min = 99
	low = 99
	high = 99
	def, _ := reg.Get("custom")
	if def.PromptVariables["source_locale"].Default != "nl" || def.OutputSchema.Required[0] != "answer" {
		t.Fatal("registry shares caller data")
	}
	if _, err := capability.ParseResult(def, `{"answer":"yes","score":0.5}`); err != nil {
		t.Fatal(err)
	}
	def.PromptVariables["source_locale"] = capability.PromptVariable{Default: "changed", Allowed: []string{"changed"}}
	def.OutputSchema.Required[0] = "changed"
	*def.OutputSchema.Properties["answer"].MinLength = 99
	all := reg.All()
	all[0].PromptVariables["source_locale"] = capability.PromptVariable{Default: "changed", Allowed: []string{"changed"}}
	all[0].OutputSchema.Required[0] = "changed"
	again, _ := reg.Get("custom")
	if again.PromptVariables["source_locale"].Default != "nl" || again.OutputSchema.Required[0] != "answer" {
		t.Fatal("lookup exposes registry data")
	}
	if _, err := capability.ParseResult(again, `{"answer":"yes"}`); err != nil {
		t.Fatal(err)
	}
}
func TestGenericPromptsBoundsAndOverride(t *testing.T) {
	for _, field := range []string{"message", "text"} {
		t.Run(field, func(t *testing.T) {
			b := binding()
			b.InputField = field
			b.SystemPrompt = "Translate {source_locale} to {target_locale} with 100% accuracy."
			b.PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}, "target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}}
			def, _ := registry(t, map[string]capability.ModelBinding{"custom": b}).Get("custom")
			prompt, user, err := capability.BuildPrompts(def, map[string]any{field: "Hallo"})
			if err != nil || user != "Hallo" || prompt != "Translate Dutch to English with 100% accuracy." {
				t.Fatalf("%q %q %v", prompt, user, err)
			}
			prompt, _, err = capability.BuildPrompts(def, map[string]any{field: "Hallo", "source_locale": "de", "target_locale": "fr"})
			if err != nil || !strings.Contains(prompt, "de to fr") {
				t.Fatalf("%q %v", prompt, err)
			}
			prompt, _, err = capability.BuildPrompts(def, map[string]any{field: "Hallo", "system_prompt": "Custom {source_locale} 100%"})
			if err != nil || prompt != "Custom {source_locale} 100%" {
				t.Fatal("override changed")
			}
			if _, _, err := capability.BuildPrompts(def, map[string]any{}); err == nil {
				t.Fatal("accepted missing input")
			}
			if err := capability.ValidateInputBounds(def, map[string]any{field: strings.Repeat("界", 200)}); err != nil {
				t.Fatal(err)
			}
			err = capability.ValidateInputBounds(def, map[string]any{field: strings.Repeat("界", 201)})
			if _, ok := err.(capability.PromptBoundsError); !ok {
				t.Fatalf("bounds error = %v", err)
			}
		})
	}
}
func TestSchemaAndGenericResults(t *testing.T) {
	b := binding()
	var schema capability.OutputSchema
	if err := json.Unmarshal([]byte(`{"type":"object","required":["answer","confidence"],"properties":{"answer":{"type":"string","minLength":1},"confidence":{"type":"number","minimum":0,"maximum":1},"count":{"type":"integer"},"ok":{"type":"boolean"},"empty":{"type":"null"}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	b.OutputSchema = &schema
	def, _ := registry(t, map[string]capability.ModelBinding{"custom": b}).Get("custom")
	good := `{"answer":"yes","confidence":0.5,"count":2,"ok":true,"empty":null,"extra":"kept"}`
	for _, raw := range []string{good, "```json\n" + good + "\n```", "```JSON\r\n" + good + "\r\n```", "```\n" + good + "\n```"} {
		got, err := capability.ParseResult(def, raw)
		if err != nil || got["extra"] != "kept" {
			t.Fatalf("result = %v %v", got, err)
		}
	}
	for _, raw := range []string{"", `{}`, `null`, `[]`, `not-json`, `{"answer":"x"}`, `{"answer":"","confidence":0.5}`, `{"answer":3,"confidence":0.5}`, `{"answer":"x","confidence":1.1}`, `{"answer":"x","confidence":-1}`, `{"answer":"x","confidence":"0.5"}`, `{"answer":"x","confidence":0.5,"count":1.5}`, `{"answer":"x","confidence":0.5,"ok":1}`, `{"answer":"x","confidence":0.5,"empty":false}`} {
		if _, err := capability.ParseResult(def, raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	def.OutputSchema = nil
	if _, err := capability.ParseResult(def, `{"anything":true}`); err != nil {
		t.Fatal(err)
	}
}
func TestSchemaDefinitionsRejectUnsupportedAndMalformedRules(t *testing.T) {
	for _, raw := range []string{`{"type":"object","$ref":"http://example.com"}`, `{"type":"object","properties":{"x":{"type":"string","pattern":".*"}}}`, `{"type":"array"}`, `{"type":"object","required":["missing"]}`, `{"type":"object","properties":{"x":{"type":"string","minimum":0}}}`, `{"type":"object","properties":{"x":{"type":"string","minLength":-1}}}`, `{"type":"object","properties":{"x":{"type":"number","minimum":2,"maximum":1}}}`, `{"type":"object","properties":{"x":null}}`, `{"type":"object","required":null}`, `{"type":"object","properties":{"x":{"type":"string","minLength":null}}}`} {
		t.Run(raw, func(t *testing.T) {
			var schema capability.OutputSchema
			err := json.Unmarshal([]byte(raw), &schema)
			if err == nil {
				b := binding()
				b.OutputSchema = &schema
				err = capability.ValidateDefinition("custom", b)
			}
			if err == nil {
				t.Fatal("accepted malformed schema")
			}
		})
	}
}
func TestHolderConcurrentReload(t *testing.T) {
	h := capability.NewHolder()
	if h.Ready() || len(h.All()) != 0 {
		t.Fatal("holder not dormant")
	}
	if _, err := h.Get("custom"); err == nil {
		t.Fatal("dormant lookup succeeded")
	}
	a := registry(t, map[string]capability.ModelBinding{"custom": binding()})
	b := binding()
	b.SystemPrompt = "second"
	other := registry(t, map[string]capability.ModelBinding{"other": b})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if j%2 == 0 {
					h.Store(a)
				} else {
					h.Store(other)
				}
				for _, def := range h.All() {
					if def.Name == "" {
						t.Error("partial definition")
					}
				}
				_, _ = h.Get("custom")
			}
		}()
	}
	wg.Wait()
	old := a.All()[0]
	h.Store(other)
	if old.Name != "custom" || old.SystemPrompt != "Return JSON." {
		t.Fatal("captured definition changed")
	}
}
func TestInputLimitError(t *testing.T) {
	err := capability.ValidateMaxInputChars("custom", 0)
	want := "capability custom has not defined max_input_chars please see documentation"
	if fmt.Sprint(err) != want {
		t.Fatal(err)
	}
}
