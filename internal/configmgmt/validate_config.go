package configmgmt

import (
	"fmt"
	"net/url"
	"strings"
)

// validateRuntimeConfig checks manifest.config.
func validateRuntimeConfig(config RuntimeConfig) error {
	if strings.TrimSpace(config.MessagePrefix) == "" {
		return fmt.Errorf("manifest.config.message_prefix must not be blank")
	}
	if strings.TrimSpace(config.HTTPAddress) == "" {
		return fmt.Errorf("manifest.config.http_address must not be blank")
	}
	if config.PriorityCountHigh < 1 {
		return fmt.Errorf("manifest.config.priority_count_high must be >= 1")
	}
	if config.PriorityCountMedium < 1 {
		return fmt.Errorf("manifest.config.priority_count_medium must be >= 1")
	}
	if config.MaxSystemPromptChars < 0 {
		return fmt.Errorf("manifest.config.max_system_prompt_chars must be >= 0")
	}
	seenOrgs := make(map[string]struct{}, len(config.SystemPromptOverrideOrgs))
	for i, orgID := range config.SystemPromptOverrideOrgs {
		orgID = strings.TrimSpace(orgID)
		if orgID == "" {
			return fmt.Errorf("manifest.config.system_prompt_override_orgs[%d] must not be blank", i)
		}
		if _, dup := seenOrgs[orgID]; dup {
			return fmt.Errorf("manifest.config.system_prompt_override_orgs duplicates %q", orgID)
		}
		seenOrgs[orgID] = struct{}{}
	}
	if raw := strings.TrimSpace(config.LokiEndpoint); raw != "" {
		endpoint, err := url.ParseRequestURI(raw)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return fmt.Errorf("manifest.config.loki_endpoint must be an absolute HTTP(S) URL")
		}
	}

	return nil
}
