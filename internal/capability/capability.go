// Package capability implements manifest-defined inference capabilities.
package capability

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ModelBinding contains a resolved endpoint and the behavior supplied by a manifest.
type ModelBinding struct {
	Vendor          string
	BaseURL         string
	Model           string
	KeepAlive       string
	SystemPrompt    string
	MaxInputChars   int
	InputField      string
	PromptVariables map[string]PromptVariable
	OutputSchema    *OutputSchema
}

// Definition is an independent view of a registered capability.
type Definition struct {
	Name string
	ModelBinding
	plan *executionPlan
}

type Registry struct{ defs map[string]Definition }

func (r *Registry) Get(name string) (Definition, error) {
	def, ok := r.defs[name]
	if !ok {
		return Definition{}, fmt.Errorf("capability %q is not configured", name)
	}
	return cloneDefinition(def), nil
}

// All returns independent definitions, sorted by ID.
func (r *Registry) All() []Definition {
	names := make([]string, 0, len(r.defs))
	for name := range r.defs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Definition, 0, len(names))
	for _, name := range names {
		out = append(out, cloneDefinition(r.defs[name]))
	}
	return out
}

type PromptBoundsError struct{ MaxCharacters int }

func (e PromptBoundsError) Error() string {
	return fmt.Sprintf("prompt is outside bounds (max %d characters)", e.MaxCharacters)
}
func (e PromptBoundsError) ToMap() map[string]any {
	return map[string]any{"reason": "Prompt is outside bounds", "max_characters": fmt.Sprint(e.MaxCharacters)}
}

func ValidateInputBounds(def Definition, input map[string]any) error {
	if utf8.RuneCountInString(stringValue(input[def.InputField])) > def.MaxInputChars {
		return PromptBoundsError{MaxCharacters: def.MaxInputChars}
	}
	return nil
}

func BuildPrompts(def Definition, input map[string]any) (string, string, error) {
	prompt := stringValue(input[def.InputField])
	if prompt == "" {
		return "", "", fmt.Errorf("data.input.%s is required", def.InputField)
	}
	replacements, err := promptReplacements(def, input)
	if err != nil {
		return "", "", err
	}
	if override := stringValue(input["system_prompt"]); override != "" {
		return override, prompt, nil
	}

	return strings.NewReplacer(replacements...).Replace(def.SystemPrompt), prompt, nil
}

func stringValue(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }
func localeLabel(locale string) string {
	switch strings.ToLower(locale) {
	case "nl":
		return "Dutch"
	case "en":
		return "English"
	default:
		return locale
	}
}
