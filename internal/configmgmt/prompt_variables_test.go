package configmgmt_test

import (
	"encoding/json"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func TestVariablePolicyChangesReloadAndAllowedOrderDoesNot(t *testing.T) {
	m := dynamicManifest()
	m.CapabilityModels["appointment-summary"][0].PromptVariables = map[string]capability.PromptVariable{"source_locale": {Default: "nl", Allowed: []string{"nl", "en"}}}
	before, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"].Allowed[0] = "en"
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"].Allowed[1] = "nl"
	reordered, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint != reordered.Fingerprint {
		t.Fatal("allowed order changed fingerprint")
	}
	variable := m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"]
	variable.Default = "en"
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"] = variable
	changedDefault, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == changedDefault.Fingerprint {
		t.Fatal("default edit did not change fingerprint")
	}
	variable.Allowed = append(variable.Allowed, "pl")
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"] = variable
	expanded, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Fingerprint == changedDefault.Fingerprint {
		t.Fatal("allowed edit did not change fingerprint")
	}
	m.CapabilityModels["appointment-summary"][0].PromptVariables["source_locale"].Allowed[0] = "mutated"
	if before.Bindings["appointment-summary"].PromptVariables["source_locale"].Allowed[0] != "en" {
		t.Fatal("snapshot shares allowed slice")
	}
}
func TestManifestValidatesVariablePolicyOnEveryRank(t *testing.T) {
	m := dynamicManifest()
	fallback := m.CapabilityModels["appointment-summary"][0]
	fallback.Rank = 1
	fallback.PromptVariables = map[string]capability.PromptVariable{"target_locale": {Default: "en", Allowed: []string{"nl"}}}
	m.CapabilityModels["appointment-summary"] = append(m.CapabilityModels["appointment-summary"], fallback)
	if err := configmgmt.Validate(m); err == nil {
		t.Fatal("accepted invalid fallback variable policy")
	}
}
func TestManifestRejectsLegacyVariableDefaults(t *testing.T) {
	data, err := json.Marshal(dynamicManifest())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	ref := raw["capability_models"].(map[string]any)["appointment-summary"].([]any)[0].(map[string]any)
	ref["prompt_variables"] = map[string]any{"source_locale": "nl"}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configmgmt.Parse(data); err == nil {
		t.Fatal("accepted legacy string default")
	}
}
