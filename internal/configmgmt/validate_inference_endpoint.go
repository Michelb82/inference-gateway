package configmgmt

import (
	"fmt"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

// validateInferenceEndpoints validates manifest.inference_endpoint and indexes its IDs
// for capability binding validation.
func validateInferenceEndpoints(endpoints []Endpoint) (map[string]Endpoint, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("manifest.inference_endpoint must not be empty")
	}
	endpointsByID := make(map[string]Endpoint, len(endpoints))
	for i, endpoint := range endpoints {
		id := strings.TrimSpace(endpoint.ID)
		if id == "" {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].id must not be blank", i)
		}
		if strings.TrimSpace(endpoint.URL) == "" {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].url must not be blank", i)
		}
		vendor, err := inference.ParseVendor(endpoint.Vendor)
		if err != nil {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].vendor: %w", i, err)
		}
		if vendor == inference.VendorOllama && strings.TrimSpace(endpoint.Model) == "" {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].model must not be blank", i)
		}
		if vendor == inference.VendorOllama && endpoint.KeepAliveSeconds < 1 {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].keep_alive_seconds must be >= 1", i)
		}
		if endpoint.KeepAliveSeconds < 0 {
			return nil, fmt.Errorf("manifest.inference_endpoint[%d].keep_alive_seconds must be >= 0", i)
		}
		if _, dup := endpointsByID[id]; dup {
			return nil, fmt.Errorf("manifest.inference_endpoint duplicates id %q", id)
		}
		endpointsByID[id] = endpoint
	}

	return endpointsByID, nil
}
