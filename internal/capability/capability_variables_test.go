package capability_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
)

func multilingualBinding() capability.ModelBinding {
	b := binding()
	b.InputField = "text"
	b.SystemPrompt = "Translate {source_locale} to {target_locale}."
	b.PromptVariables = map[string]capability.PromptVariable{
		"source_locale": {Default: "nl", Allowed: []string{"nl", "en"}},
		"target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl"}},
	}
	return b
}
func TestPromptVariableDefinitionsRejectInvalidDefaultsAndAllowedLists(t *testing.T) {
	for name, variable := range map[string]capability.PromptVariable{
		"missing default":      {Allowed: []string{"nl"}},
		"blank default":        {Default: " ", Allowed: []string{"nl"}},
		"missing allowed":      {Default: "nl"},
		"empty allowed":        {Default: "nl", Allowed: []string{}},
		"blank allowed":        {Default: "nl", Allowed: []string{"nl", " "}},
		"duplicate allowed":    {Default: "nl", Allowed: []string{"nl", "nl"}},
		"normalized duplicate": {Default: "nl", Allowed: []string{"nl", " nl "}},
		"default not allowed":  {Default: "nl", Allowed: []string{"en"}},
	} {
		t.Run(name, func(t *testing.T) {
			b := multilingualBinding()
			b.PromptVariables["source_locale"] = variable
			if err := capability.ValidateDefinition("translate-any", b); err == nil {
				t.Fatal("accepted invalid variable declaration")
			}
		})
	}
}
func TestPromptVariableJSONRejectsLegacyAndMalformedDeclarations(t *testing.T) {
	for _, raw := range []string{`"nl"`, `["nl"]`, `null`, `{}`, `{"default":null,"allowed":["nl"]}`, `{"default":42,"allowed":["nl"]}`, `{"default":"nl","allowed":"nl"}`, `{"default":"nl","allowed":[42]}`, `{"default":"nl","allowed":[null]}`, `{"default":"nl","allowed":["nl"],"allowd":["en"]}`} {
		t.Run(raw, func(t *testing.T) {
			var variable capability.PromptVariable
			err := json.Unmarshal([]byte(raw), &variable)
			if err == nil {
				b := multilingualBinding()
				b.PromptVariables["source_locale"] = variable
				err = capability.ValidateDefinition("custom", b)
			}
			if err == nil {
				t.Fatal("accepted malformed declaration")
			}
		})
	}
}
func TestSingleCapabilitySupportsAllConfiguredLanguagePairs(t *testing.T) {
	def, err := registry(t, map[string]capability.ModelBinding{"translate-any": multilingualBinding()}).Get("translate-any")
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"nl": "Dutch", "en": "English", "pl": "pl"}
	for _, source := range []string{"nl", "en"} {
		for _, target := range []string{"en", "nl", "pl"} {
			t.Run(source+"-"+target, func(t *testing.T) {
				prompt, _, err := capability.BuildPrompts(def, map[string]any{"text": "hello", "source_locale": source, "target_locale": target})
				want := fmt.Sprintf("Translate %s to %s.", labels[source], labels[target])
				if err != nil || prompt != want {
					t.Fatalf("prompt=%q err=%v want=%q", prompt, err, want)
				}
			})
		}
	}
	for _, input := range []map[string]any{{"text": "hello"}, {"text": "hello", "source_locale": " ", "target_locale": ""}} {
		prompt, _, err := capability.BuildPrompts(def, input)
		if err != nil || prompt != "Translate Dutch to English." {
			t.Fatalf("defaults: %q %v", prompt, err)
		}
	}
	for _, variable := range []string{"source_locale", "target_locale"} {
		for _, value := range []string{"de", "NL"} {
			input := map[string]any{"text": "hello", variable: value}
			_, _, err := capability.BuildPrompts(def, input)
			var variableErr *capability.PromptVariableError
			if !errors.As(err, &variableErr) || variableErr.Variable != variable {
				t.Fatalf("invalid selection error=%v", err)
			}
			input["system_prompt"] = "Override {target_locale}"
			if _, _, err := capability.BuildPrompts(def, input); err == nil {
				t.Fatal("override bypassed allowed values")
			}
		}
	}
	prompt, _, err := capability.BuildPrompts(def, map[string]any{"text": "hello", "source_locale": "en", "target_locale": "pl", "system_prompt": "Override {target_locale}"})
	if err != nil || prompt != "Override {target_locale}" {
		t.Fatal("valid override was changed")
	}
}
func TestPromptVariableAllowedSlicesAreOwnedByRegistry(t *testing.T) {
	b := multilingualBinding()
	reg := registry(t, map[string]capability.ModelBinding{"custom": b})
	b.PromptVariables["source_locale"].Allowed[0] = "mutated"
	def, _ := reg.Get("custom")
	def.PromptVariables["source_locale"].Allowed[0] = "mutated"
	all := reg.All()
	all[0].PromptVariables["target_locale"].Allowed[0] = "mutated"
	again, _ := reg.Get("custom")
	if strings.Contains(strings.Join(again.PromptVariables["source_locale"].Allowed, ","), "mutated") || strings.Contains(strings.Join(again.PromptVariables["target_locale"].Allowed, ","), "mutated") {
		t.Fatal("registry shares mutable allowed slices")
	}
	if _, _, err := capability.BuildPrompts(again, map[string]any{"text": "hello", "source_locale": "nl", "target_locale": "pl"}); err != nil {
		t.Fatal(err)
	}
}
