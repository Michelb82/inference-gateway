package configmgmt

import (
	"fmt"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

// Validate checks structural and referential integrity of a manifest.
// Known capability ids come from the capability package; the manifest only
// binds endpoints (and ops settings) to those ids via capability_models.
func Validate(m Manifest) error {
	known := capability.Known()

	if len(m.InferenceEndpoint) == 0 {
		return fmt.Errorf("manifest.inference_endpoint must not be empty")
	}
	endpointsByID := make(map[string]Endpoint, len(m.InferenceEndpoint))
	for i, endpoint := range m.InferenceEndpoint {
		id := strings.TrimSpace(endpoint.ID)
		if id == "" {
			return fmt.Errorf("manifest.inference_endpoint[%d].id must not be blank", i)
		}
		if strings.TrimSpace(endpoint.URL) == "" {
			return fmt.Errorf("manifest.inference_endpoint[%d].url must not be blank", i)
		}
		vendor, err := inference.ParseVendor(endpoint.Vendor)
		if err != nil {
			return fmt.Errorf("manifest.inference_endpoint[%d].vendor: %w", i, err)
		}
		if vendor == inference.VendorOllama && strings.TrimSpace(endpoint.Model) == "" {
			return fmt.Errorf("manifest.inference_endpoint[%d].model must not be blank", i)
		}
		if vendor == inference.VendorOllama && endpoint.KeepAliveSeconds < 1 {
			return fmt.Errorf("manifest.inference_endpoint[%d].keep_alive_seconds must be >= 1", i)
		}
		if endpoint.KeepAliveSeconds < 0 {
			return fmt.Errorf("manifest.inference_endpoint[%d].keep_alive_seconds must be >= 0", i)
		}
		if _, dup := endpointsByID[id]; dup {
			return fmt.Errorf("manifest.inference_endpoint duplicates id %q", id)
		}
		endpointsByID[id] = endpoint
	}

	if len(m.CapabilityModels) == 0 {
		return fmt.Errorf("manifest.capability_models must not be empty")
	}
	for capName := range m.CapabilityModels {
		if !capability.IsKnown(capName) {
			return fmt.Errorf("manifest.capability_models has unknown capability %q", capName)
		}
	}
	for _, required := range known {
		refs, ok := m.CapabilityModels[required]
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
			if ref.MaxInputChars < 0 {
				return fmt.Errorf("manifest.capability_models[%q][%d].max_input_chars must be >= 0", required, j)
			}
		}
		if !hasRank0 {
			return fmt.Errorf("manifest.capability_models[%q] must include rank 0", required)
		}
	}

	if strings.TrimSpace(m.Ingress.Adapter) == "" {
		return fmt.Errorf("manifest.ingress.adapter must not be blank")
	}
	if strings.ToLower(strings.TrimSpace(m.Ingress.Adapter)) != "redis" {
		return fmt.Errorf("manifest.ingress.adapter %q is not supported", m.Ingress.Adapter)
	}
	if strings.TrimSpace(m.Ingress.Address) == "" {
		return fmt.Errorf("manifest.ingress.address must not be blank")
	}
	if strings.TrimSpace(m.Ingress.IngressChannel) == "" {
		return fmt.Errorf("manifest.ingress.ingress_channel must not be blank")
	}
	if strings.TrimSpace(m.Ingress.EgressChannel) == "" {
		return fmt.Errorf("manifest.ingress.egress_channel must not be blank")
	}
	if m.Ingress.BRPopTimeoutSeconds < 1 {
		return fmt.Errorf("manifest.ingress.brpop_timeout_seconds must be >= 1")
	}

	if strings.TrimSpace(m.Config.MessagePrefix) == "" {
		return fmt.Errorf("manifest.config.message_prefix must not be blank")
	}
	if strings.TrimSpace(m.Config.HTTPAddress) == "" {
		return fmt.Errorf("manifest.config.http_address must not be blank")
	}
	if m.Config.PriorityCountHigh < 1 {
		return fmt.Errorf("manifest.config.priority_count_high must be >= 1")
	}
	if m.Config.PriorityCountMedium < 1 {
		return fmt.Errorf("manifest.config.priority_count_medium must be >= 1")
	}
	if m.Config.MaxSystemPromptChars < 0 {
		return fmt.Errorf("manifest.config.max_system_prompt_chars must be >= 0")
	}
	seenOrgs := make(map[string]struct{}, len(m.Config.SystemPromptOverrideOrgs))
	for i, orgID := range m.Config.SystemPromptOverrideOrgs {
		orgID = strings.TrimSpace(orgID)
		if orgID == "" {
			return fmt.Errorf("manifest.config.system_prompt_override_orgs[%d] must not be blank", i)
		}
		if _, dup := seenOrgs[orgID]; dup {
			return fmt.Errorf("manifest.config.system_prompt_override_orgs duplicates %q", orgID)
		}
		seenOrgs[orgID] = struct{}{}
	}

	return nil
}
