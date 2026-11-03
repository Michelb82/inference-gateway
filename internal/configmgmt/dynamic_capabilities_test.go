package configmgmt_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func dynamicManifest() configmgmt.Manifest {
	m := validManifest()
	m.CapabilityModels = map[string][]configmgmt.RankedModelRef{"appointment-summary": {{Rank: 0, Model: "qwen3:1.7b", MaxInputChars: 200, SystemPrompt: "Return a summary.", InputField: "message"}}}
	return m
}
func TestManifestSupportsArbitrarySubsetOfCapabilities(t *testing.T) {
	m := dynamicManifest()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configmgmt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := configmgmt.Resolve(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Bindings) != 1 || snap.Bindings["appointment-summary"].SystemPrompt != "Return a summary." {
		t.Fatalf("bindings = %+v", snap.Bindings)
	}
}
func TestEveryDefinitionChangeAffectsFingerprint(t *testing.T) {
	for name, mutate := range map[string]func(*configmgmt.Manifest){
		"prompt": func(m *configmgmt.Manifest) {
			m.CapabilityModels["appointment-summary"][0].SystemPrompt = "Another prompt."
		},
		"mapping": func(m *configmgmt.Manifest) { m.CapabilityModels["appointment-summary"][0].InputField = "text" },
		"limit":   func(m *configmgmt.Manifest) { m.CapabilityModels["appointment-summary"][0].MaxInputChars = 500000 },
		"variables": func(m *configmgmt.Manifest) {
			m.CapabilityModels["appointment-summary"][0].PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}}
		},
		"schema": func(m *configmgmt.Manifest) {
			m.CapabilityModels["appointment-summary"][0].OutputSchema = &capability.OutputSchema{Type: "object", Properties: map[string]capability.PropertySchema{"summary": {Type: "string"}}}
		},
		"addition": func(m *configmgmt.Manifest) {
			m.CapabilityModels["new-one"] = m.CapabilityModels["appointment-summary"]
		},
		"rename-removal": func(m *configmgmt.Manifest) {
			m.CapabilityModels["new-one"] = m.CapabilityModels["appointment-summary"]
			delete(m.CapabilityModels, "appointment-summary")
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := dynamicManifest()
			before, err := configmgmt.Resolve(m)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&m)
			after, err := configmgmt.Resolve(m)
			if err != nil {
				t.Fatal(err)
			}
			if before.Fingerprint == after.Fingerprint {
				t.Fatal("change not fingerprinted")
			}
		})
	}
}
func TestFingerprintIsIndependentOfSchemaMapOrderAndSnapshotOwnsData(t *testing.T) {
	m := dynamicManifest()
	min := 1
	schema := &capability.OutputSchema{Type: "object", Properties: map[string]capability.PropertySchema{"a": {Type: "string", MinLength: &min}, "b": {Type: "boolean"}}}
	m.CapabilityModels["appointment-summary"][0].OutputSchema = schema
	m.CapabilityModels["appointment-summary"][0].PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}, "target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}}
	before, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	schema.Properties = map[string]capability.PropertySchema{"b": {Type: "boolean"}, "a": {Type: "string", MinLength: &min}}
	m.CapabilityModels["appointment-summary"][0].PromptVariables = map[string]capability.PromptVariable{"target_locale": {Default: "en", Allowed: []string{"en", "nl", "pl", "fr"}}, "source_locale": {Default: "nl", Allowed: []string{"nl", "en", "de"}}}
	after, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint != after.Fingerprint {
		t.Fatal("map order changed fingerprint")
	}
	min = 99
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"] = capability.PromptVariable{Default: "de", Allowed: []string{"de"}}
	b := before.Bindings["appointment-summary"]
	if *b.OutputSchema.Properties["a"].MinLength != 1 || b.PromptVariables["source_locale"].Default != "nl" {
		t.Fatal("snapshot shares caller data")
	}
}
func TestManifestAndRegistryShareDefinitionRules(t *testing.T) {
	for name, change := range map[string]func(*configmgmt.RankedModelRef){
		"input":  func(r *configmgmt.RankedModelRef) { r.InputField = "" },
		"limit":  func(r *configmgmt.RankedModelRef) { r.MaxInputChars = 199 },
		"prompt": func(r *configmgmt.RankedModelRef) { r.SystemPrompt = "" },
		"variables": func(r *configmgmt.RankedModelRef) {
			r.PromptVariables = map[string]capability.PromptVariable{"unknown": {Default: "x", Allowed: []string{"x"}}}
		},
		"placeholder": func(r *configmgmt.RankedModelRef) { r.SystemPrompt = "{unknown}" },
		"schema":      func(r *configmgmt.RankedModelRef) { r.OutputSchema = &capability.OutputSchema{Type: "array"} },
	} {
		t.Run(name, func(t *testing.T) {
			m := dynamicManifest()
			change(&m.CapabilityModels["appointment-summary"][0])
			r := m.CapabilityModels["appointment-summary"][0]
			manifestErr := configmgmt.Validate(m)
			_, registryErr := capability.NewRegistryFromBindings(map[string]capability.ModelBinding{"appointment-summary": {BaseURL: "http://llm", Model: "test", InputField: r.InputField, SystemPrompt: r.SystemPrompt, MaxInputChars: r.MaxInputChars, PromptVariables: r.PromptVariables, OutputSchema: r.OutputSchema}})
			if manifestErr == nil || registryErr == nil || manifestErr.Error() != registryErr.Error() {
				t.Fatalf("manifest = %v; registry = %v", manifestErr, registryErr)
			}
		})
	}
}
func TestManifestRejectsUnsupportedSchemaKeywords(t *testing.T) {
	data, _ := json.Marshal(dynamicManifest())
	data = []byte(strings.Replace(string(data), `"input_field":"message"`, `"input_field":"message","output_schema":{"type":"object","$ref":"http://remote"}`, 1))
	if _, err := configmgmt.Parse(data); err == nil {
		t.Fatal("accepted remote schema reference")
	}
}
