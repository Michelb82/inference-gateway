package configmgmt

import (
	"fmt"
	"net/url"
	"strings"
)

// validateTools checks manifest.tools.
func validateTools(tools []Tool) error {
	seenTools := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return fmt.Errorf("manifest.tools[%d].name must not be blank", i)
		}
		if _, exists := seenTools[name]; exists {
			return fmt.Errorf("manifest.tools duplicates name %q", name)
		}
		seenTools[name] = struct{}{}
		endpoint, err := url.ParseRequestURI(strings.TrimSpace(tool.Endpoint))
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return fmt.Errorf("manifest.tools[%d].endpoint must be an absolute HTTP(S) URL", i)
		}
		if strings.TrimSpace(tool.Instruction) == "" {
			return fmt.Errorf("manifest.tools[%d].instruction must not be blank", i)
		}
	}

	return nil
}
