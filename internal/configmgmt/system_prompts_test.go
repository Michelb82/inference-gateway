package configmgmt_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func TestManifestRequiresSystemPromptForEveryCapabilityAndRank(t *testing.T) {
	for name, refs := range validManifest().CapabilityModels {
		for index := range refs {
			for _, value := range []string{"", " \n\t"} {
				t.Run(fmt.Sprintf("%s/rank-%d/%q", name, index, value), func(t *testing.T) {
					m := validManifest()
					m.CapabilityModels[name][index].SystemPrompt = value
					err := configmgmt.Validate(m)
					want := fmt.Sprintf("capability %s has not defined system_prompt please see documentation", name)
					if err == nil || err.Error() != want {
						t.Fatalf("Validate() error = %v, want %q", err, want)
					}
				})
			}
		}
	}
}

func TestManifestRejectsMissingNullAndNonStringPrompts(t *testing.T) {
	for _, value := range []any{nil, 42, true} {
		m := validManifest()
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		ref := raw["capability_models"].(map[string]any)["routing"].([]any)[0].(map[string]any)
		ref["system_prompt"] = value
		data, err = json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := configmgmt.Parse(data); err == nil {
			t.Fatalf("accepted system_prompt %v", value)
		}
		delete(ref, "system_prompt")
		data, err = json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := configmgmt.Parse(data); err == nil {
			t.Fatal("accepted missing system_prompt")
		}
	}
}

func TestResolveUsesRankZeroPromptAndFingerprintsPromptEdits(t *testing.T) {
	m := validManifest()
	m.CapabilityModels["routing"][0].SystemPrompt = "  First routing prompt.  "
	m.CapabilityModels["routing"][1].SystemPrompt = "Inactive fallback prompt."
	before, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if before.Bindings["routing"].SystemPrompt != "First routing prompt." {
		t.Fatal("did not resolve rank-0 prompt")
	}
	m.CapabilityModels["routing"][0].SystemPrompt = "Updated routing prompt."
	after, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatal("prompt update did not change the snapshot fingerprint")
	}
	m.CapabilityModels["routing"][0].SystemPrompt = ""
	if _, err := configmgmt.Resolve(m); err == nil {
		t.Fatal("Resolve accepted a missing prompt")
	}
}
