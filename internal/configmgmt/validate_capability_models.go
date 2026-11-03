package configmgmt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
)

// Input limits are shared with direct registry construction.
const (
	MinInputChars = capability.MinInputChars
	MaxInputChars = capability.MaxInputChars
)

// validateCapabilityModels checks manifest.capability_models.
func validateCapabilityModels(bindings map[string][]RankedModelRef, endpointsByID map[string]Endpoint) error {
	if len(bindings) == 0 {
		return fmt.Errorf("manifest.capability_models must not be empty")
	}
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, required := range names {
		if err := capability.ValidateID(required); err != nil {
			return err
		}
		refs := bindings[required]
		if len(refs) == 0 {
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
			if err := capability.ValidateDefinition(required, behaviorBinding(ref)); err != nil {
				return err
			}
		}
		if !hasRank0 {
			return fmt.Errorf("manifest.capability_models[%q] must include rank 0", required)
		}
	}

	return nil
}

// behaviorBinding maps the manifest behavior to the shared validation contract.
func behaviorBinding(ref RankedModelRef) capability.ModelBinding {
	return capability.ModelBinding{SystemPrompt: ref.SystemPrompt, MaxInputChars: ref.MaxInputChars,
		InputField: ref.InputField, PromptVariables: ref.PromptVariables, OutputSchema: ref.OutputSchema}
}
