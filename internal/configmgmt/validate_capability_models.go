package configmgmt

import (
	"fmt"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
)

// Input limits are mandatory on every ranked capability binding.
const (
	MinInputChars = 200
	MaxInputChars = 500000
)

func validateMaxInputChars(name string, limit int) error {
	if limit == 0 {
		return fmt.Errorf("capability %s has not defined max_input_chars please see documentation", name)
	}
	if limit < MinInputChars || limit > MaxInputChars {
		return fmt.Errorf("capability %s max_input_chars must be an integer between %d and %d", name, MinInputChars, MaxInputChars)
	}
	return nil
}

// validateCapabilityModels checks manifest.capability_models.
func validateCapabilityModels(bindings map[string][]RankedModelRef, endpointsByID map[string]Endpoint) error {
	if len(bindings) == 0 {
		return fmt.Errorf("manifest.capability_models must not be empty")
	}
	for capName := range bindings {
		if !capability.IsKnown(capName) {
			return fmt.Errorf("manifest.capability_models has unknown capability %q", capName)
		}
	}
	for _, required := range capability.Known() {
		refs, ok := bindings[required]
		if !ok || len(refs) == 0 {
			return fmt.Errorf("manifest.capability_models missing entries for %q", required)
		}
		seenRanks := make(map[int]struct{}, len(refs))
		hasRank0 := false
		for j, ref := range refs {
			if _, dup := seenRanks[ref.Rank]; dup {
				return fmt.Errorf("manifest.capability_models[%q] duplicates rank %d", required, ref.Rank)
			}
			seenRanks[ref.Rank] = struct{}{}
			if ref.Rank == 0 {
				hasRank0 = true
			}
			modelID := strings.TrimSpace(ref.Model)
			if modelID == "" {
				return fmt.Errorf("manifest.capability_models[%q][%d].model must not be blank", required, j)
			}
			if _, ok := endpointsByID[modelID]; !ok {
				return fmt.Errorf("manifest.capability_models[%q] references unknown model id %q", required, modelID)
			}
			if err := validateMaxInputChars(required, ref.MaxInputChars); err != nil {
				return err
			}
			if strings.TrimSpace(ref.SystemPrompt) == "" {
				return fmt.Errorf("capability %s has not defined system_prompt please see documentation", required)
			}
		}
		if !hasRank0 {
			return fmt.Errorf("manifest.capability_models[%q] must include rank 0", required)
		}
	}

	return nil
}
