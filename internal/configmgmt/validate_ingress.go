package configmgmt

import (
	"fmt"
	"strings"
)

// validateIngress checks manifest.ingress.
func validateIngress(ingress Ingress) error {
	if strings.TrimSpace(ingress.Adapter) == "" {
		return fmt.Errorf("manifest.ingress.adapter must not be blank")
	}
	if strings.ToLower(strings.TrimSpace(ingress.Adapter)) != "redis" {
		return fmt.Errorf("manifest.ingress.adapter %q is not supported", ingress.Adapter)
	}
	if strings.TrimSpace(ingress.Address) == "" {
		return fmt.Errorf("manifest.ingress.address must not be blank")
	}
	if strings.TrimSpace(ingress.IngressChannel) == "" {
		return fmt.Errorf("manifest.ingress.ingress_channel must not be blank")
	}
	if strings.TrimSpace(ingress.EgressChannel) == "" {
		return fmt.Errorf("manifest.ingress.egress_channel must not be blank")
	}
	if ingress.BRPopTimeoutSeconds < 1 {
		return fmt.Errorf("manifest.ingress.brpop_timeout_seconds must be >= 1")
	}

	return nil
}
