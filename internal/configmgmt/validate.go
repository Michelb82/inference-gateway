package configmgmt

// Validate checks structural and referential integrity of a manifest.
// Each segment is checked in order; the first validation error is returned.
func Validate(m Manifest) error {
	endpointsByID, err := validateInferenceEndpoints(m.InferenceEndpoint)
	if err != nil {
		return err
	}
	if err := validateTools(m.Tools); err != nil {
		return err
	}
	if err := validateCapabilityModels(m.CapabilityModels, endpointsByID); err != nil {
		return err
	}
	if err := validateIngress(m.Ingress); err != nil {
		return err
	}
	return validateRuntimeConfig(m.Config)
}
