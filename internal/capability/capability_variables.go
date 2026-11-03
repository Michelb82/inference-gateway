package capability

import (
	"fmt"
	"slices"
	"strings"
)

// PromptVariable declares a default and the values clients may select per request.
type PromptVariable struct {
	Default string   `json:"default"`
	Allowed []string `json:"allowed"`
}

func (v *PromptVariable) UnmarshalJSON(data []byte) error {
	type plain PromptVariable
	var value plain
	if err := strictDecode(data, &value); err != nil {
		return err
	}
	*v = PromptVariable(value)
	return nil
}

// PromptVariableError identifies invalid request input, including queued requests
// whose selected value is no longer allowed when worker processing begins.
type PromptVariableError struct {
	Variable string
	Value    string
	Allowed  []string
}

func (e *PromptVariableError) Error() string {
	return fmt.Sprintf("prompt variable %s value %q is not allowed; allowed values: %s", e.Variable, e.Value, strings.Join(e.Allowed, ", "))
}
func (e *PromptVariableError) ToMap() map[string]any {
	return map[string]any{"code": "invalid_prompt_variable", "message": e.Error(), "param": e.Variable, "allowed": append([]string(nil), e.Allowed...)}
}

// ValidatePromptVariables enforces the same selection rules at HTTP admission
// and during worker prompt construction. Blank values use the configured default.
func ValidatePromptVariables(def Definition, input map[string]any) error {
	_, err := promptReplacements(def, input)
	return err
}

func promptReplacements(def Definition, input map[string]any) ([]string, error) {
	names := []string{"source_locale", "target_locale"}
	if def.plan != nil {
		names = def.plan.variables
	}
	replacements := make([]string, 0, len(def.PromptVariables)*2)
	for _, name := range names {
		variable, declared := def.PromptVariables[name]
		if !declared {
			continue
		}
		value := stringValue(input[name])
		if value == "" {
			value = variable.Default
		}
		if !slices.Contains(variable.Allowed, value) {
			return nil, &PromptVariableError{Variable: name, Value: value, Allowed: append([]string(nil), variable.Allowed...)}
		}
		replacements = append(replacements, "{"+name+"}", localeLabel(value))
	}
	return replacements, nil
}
