// Package testfixture supplies explicit example definitions to gateway tests.
// Runtime capability discovery never depends on these example IDs.
package testfixture

import "github.com/mywebsite/construction-ai-gateway/internal/capability"

func Names() []string {
	return []string{"routing", "intent-classification", "parameter-extraction", "personal-details-extraction", "translate", "configure-environment"}
}
func Behavior(name string) capability.ModelBinding {
	b := capability.ModelBinding{InputField: "message", MaxInputChars: 16000, SystemPrompt: "Test manifest prompt."}
	min := 1
	zero, one := 0.0, 1.0
	switch name {
	case "routing":
		b.MaxInputChars = 200
		b.OutputSchema = &capability.OutputSchema{Type: "object", Required: []string{"capability"}, Properties: map[string]capability.PropertySchema{"capability": {Type: "string", MinLength: &min}}}
	case "intent-classification":
		b.MaxInputChars = 8000
		b.OutputSchema = &capability.OutputSchema{Type: "object", Required: []string{"intent", "confidence"}, Properties: map[string]capability.PropertySchema{"intent": {Type: "string", MinLength: &min}, "confidence": {Type: "number", Minimum: &zero, Maximum: &one}}}
	case "translate":
		b.InputField = "text"
		b.SystemPrompt = "Translate {source_locale} to {target_locale}."
		b.PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}, "target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}}
		b.OutputSchema = &capability.OutputSchema{Type: "object", Required: []string{"text"}, Properties: map[string]capability.PropertySchema{"text": {Type: "string", MinLength: &min}}}
	}
	return b
}
func NewRegistry(routing, intent, translation capability.ModelBinding) *capability.Registry {
	bindings := make(map[string]capability.ModelBinding)
	for _, name := range Names() {
		b := translation
		switch name {
		case "routing":
			b = routing
		case "intent-classification":
			b = intent
		}
		behavior := Behavior(name)
		b.InputField = behavior.InputField
		b.OutputSchema = behavior.OutputSchema
		if b.MaxInputChars == 0 {
			b.MaxInputChars = behavior.MaxInputChars
		}
		if b.SystemPrompt == "" {
			b.SystemPrompt = behavior.SystemPrompt
		}
		b.PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}, "target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}}
		bindings[name] = b
	}
	reg, err := capability.NewRegistryFromBindings(bindings)
	if err != nil {
		panic(err)
	}
	return reg
}
func Registry() *capability.Registry {
	return NewRegistry(
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", MaxInputChars: 200},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", MaxInputChars: 8000},
		capability.ModelBinding{BaseURL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", MaxInputChars: 16000},
	)
}
