package capability

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

// executionPlan holds validated metadata prepared once during registration.
type executionPlan struct {
	variables    []string
	outputFields []string
}

// NewRegistryFromBindings validates the complete candidate before returning a registry.
// It never changes a live holder and never retains caller-owned mutable data.
func NewRegistryFromBindings(bindings map[string]ModelBinding) (*Registry, error) {
	if len(bindings) == 0 {
		return nil, fmt.Errorf("capability bindings must not be empty")
	}
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make(map[string]Definition, len(bindings))
	for _, name := range names {
		binding := bindings[name]
		binding.Vendor = strings.ToLower(strings.TrimSpace(binding.Vendor))
		if err := ValidateDefinition(name, binding); err != nil {
			return nil, err
		}
		if strings.TrimSpace(binding.BaseURL) == "" {
			return nil, fmt.Errorf("capability %s has no LLM URL configured", name)
		}
		if strings.TrimSpace(binding.Model) == "" && binding.Vendor != "openai" {
			return nil, fmt.Errorf("capability %s has no model configured", name)
		}
		binding.BaseURL = strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
		binding.Model = strings.TrimSpace(binding.Model)
		binding.KeepAlive = strings.TrimSpace(binding.KeepAlive)
		binding.SystemPrompt = strings.TrimSpace(binding.SystemPrompt)
		def := cloneDefinition(Definition{Name: name, ModelBinding: binding})
		def.plan = &executionPlan{}
		for key, value := range def.PromptVariables {
			value.Default = strings.TrimSpace(value.Default)
			for i := range value.Allowed {
				value.Allowed[i] = strings.TrimSpace(value.Allowed[i])
			}
			sort.Strings(value.Allowed)
			def.PromptVariables[key] = value
			def.plan.variables = append(def.plan.variables, key)
		}
		sort.Strings(def.plan.variables)
		if def.OutputSchema != nil {
			def.plan.outputFields = def.OutputSchema.keys()
		}
		defs[name] = def
	}
	return &Registry{defs: defs}, nil
}

func cloneDefinition(def Definition) Definition {
	def.PromptVariables = maps.Clone(def.PromptVariables)
	for name, variable := range def.PromptVariables {
		variable.Allowed = append([]string(nil), variable.Allowed...)
		def.PromptVariables[name] = variable
	}
	def.OutputSchema = def.OutputSchema.clone()
	return def
}
