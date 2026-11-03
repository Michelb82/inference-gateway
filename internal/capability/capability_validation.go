package capability

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	MinInputChars = 200
	MaxInputChars = 500000
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var placeholder = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

func ValidateID(name string) error {
	if !identifier.MatchString(name) {
		return fmt.Errorf("invalid capability ID %q: must match [a-z][a-z0-9-]*", name)
	}
	return nil
}
func ValidateMaxInputChars(name string, limit int) error {
	if limit == 0 {
		return fmt.Errorf("capability %s has not defined max_input_chars please see documentation", name)
	}
	if limit < MinInputChars || limit > MaxInputChars {
		return fmt.Errorf("capability %s max_input_chars must be an integer between %d and %d", name, MinInputChars, MaxInputChars)
	}
	return nil
}

// ValidateDefinition is shared by manifest validation and registry construction.
// Endpoint references and ranked lists are validated by configmgmt.
func ValidateDefinition(name string, binding ModelBinding) error {
	if err := ValidateID(name); err != nil {
		return err
	}
	if err := ValidateMaxInputChars(name, binding.MaxInputChars); err != nil {
		return err
	}
	if strings.TrimSpace(binding.SystemPrompt) == "" {
		return fmt.Errorf("capability %s has not defined system_prompt please see documentation", name)
	}
	if binding.InputField != "message" && binding.InputField != "text" {
		return fmt.Errorf("capability %s input_field must be message or text", name)
	}
	variables := make([]string, 0, len(binding.PromptVariables))
	for key := range binding.PromptVariables {
		variables = append(variables, key)
	}
	sort.Strings(variables)
	for _, key := range variables {
		if key != "source_locale" && key != "target_locale" {
			return fmt.Errorf("capability %s has unsupported prompt variable %q", name, key)
		}
		variable := binding.PromptVariables[key]
		defaultValue := strings.TrimSpace(variable.Default)
		if defaultValue == "" {
			return fmt.Errorf("capability %s prompt variable %s needs a nonblank default", name, key)
		}
		if len(variable.Allowed) == 0 {
			return fmt.Errorf("capability %s prompt variable %s needs a nonempty allowed list", name, key)
		}
		seen := make(map[string]bool, len(variable.Allowed))
		for _, value := range variable.Allowed {
			value = strings.TrimSpace(value)
			if value == "" {
				return fmt.Errorf("capability %s prompt variable %s allowed values must not be blank", name, key)
			}
			if seen[value] {
				return fmt.Errorf("capability %s prompt variable %s duplicates allowed value %q", name, key, value)
			}
			seen[value] = true
		}
		if !seen[defaultValue] {
			return fmt.Errorf("capability %s prompt variable %s default %q must be in allowed", name, key, defaultValue)
		}
	}
	for _, match := range placeholder.FindAllStringSubmatch(binding.SystemPrompt, -1) {
		if _, ok := binding.PromptVariables[match[1]]; !ok {
			return fmt.Errorf("capability %s has undeclared prompt placeholder %q", name, match[1])
		}
	}
	if err := binding.OutputSchema.validate(); err != nil {
		return fmt.Errorf("capability %s output_schema: %w", name, err)
	}
	return nil
}
