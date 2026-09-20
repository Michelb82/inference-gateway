package capability

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	Routing              = "routing"
	IntentClassification = "intent-classification"
	ParameterExtraction  = "parameter-extraction"
	PersonalDetails      = "personal-details-extraction"
	Translate            = "translate"
	ConfigureEnvironment = "configure-environment"
)

// Known returns the gateway's built-in capability ids in stable order.
// This is the single source of truth for which capabilities exist; the
// manifest only supplies model/ingress bindings for these names.
func Known() []string {
	return []string{Routing, IntentClassification, ParameterExtraction, PersonalDetails, Translate, ConfigureEnvironment}
}

// DefaultMaxInputChars returns the built-in input character limit for a
// known capability when the manifest omits max_input_chars on rank 0.
// Returns 0 for unknown names.
func DefaultMaxInputChars(name string) int {
	switch name {
	case Routing:
		return 200
	case IntentClassification:
		return 8000
	case ParameterExtraction, PersonalDetails, ConfigureEnvironment:
		return 16000
	case Translate:
		return 16000
	default:
		return 0
	}
}

// IsKnown reports whether name is exactly a built-in capability id.
// Comparison is exact (no trim) so manifest map keys cannot sneak past
// validation via surrounding whitespace.
func IsKnown(name string) bool {
	for _, known := range Known() {
		if name == known {
			return true
		}
	}
	return false
}

type Definition struct {
	Name          string
	Vendor        string
	BaseURL       string
	Model         string
	KeepAlive     string
	SystemPrompt  string
	MaxInputChars int
}

// ModelBinding maps a capability to its LLM endpoint, model, keep-alive TTL, and input bounds.
type ModelBinding struct {
	Vendor        string
	BaseURL       string
	Model         string
	KeepAlive     string
	MaxInputChars int
}

type Registry struct {
	defs map[string]Definition
}

var knownSystemPrompts = map[string]string{
	Routing:              routingSystemPrompt,
	IntentClassification: intentSystemPrompt,
	ParameterExtraction:  genericJSONSystemPrompt,
	PersonalDetails:      genericJSONSystemPrompt,
	Translate:            translateSystemPrompt,
	ConfigureEnvironment: genericJSONSystemPrompt,
}

// NewRegistryFromBindings builds a registry from capability→model bindings.
// All known capabilities must be present.
func NewRegistryFromBindings(bindings map[string]ModelBinding) (*Registry, error) {
	if bindings == nil {
		return nil, fmt.Errorf("bindings must not be nil")
	}
	known := Known()
	defs := make(map[string]Definition, len(known))
	for _, name := range known {
		binding, ok := bindings[name]
		if !ok {
			return nil, fmt.Errorf("missing binding for capability %s", name)
		}
		prompt, ok := knownSystemPrompts[name]
		if !ok || strings.TrimSpace(prompt) == "" {
			return nil, fmt.Errorf("missing system prompt for capability %s", name)
		}
		defs[name] = Definition{
			Name:          name,
			Vendor:        strings.ToLower(strings.TrimSpace(binding.Vendor)),
			BaseURL:       strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/"),
			Model:         strings.TrimSpace(binding.Model),
			KeepAlive:     strings.TrimSpace(binding.KeepAlive),
			SystemPrompt:  prompt,
			MaxInputChars: binding.MaxInputChars,
		}
	}
	return &Registry{defs: defs}, nil
}

// NewRegistry builds a registry from the known capability bindings.
func NewRegistry(routing, intent, translate ModelBinding) *Registry {
	reg, err := NewRegistryFromBindings(map[string]ModelBinding{
		Routing:              routing,
		IntentClassification: intent,
		ParameterExtraction:  translate,
		PersonalDetails:      translate,
		Translate:            translate,
		ConfigureEnvironment: translate,
	})
	if err != nil {
		panic(err)
	}
	return reg
}

// Holder is a thread-safe registry pointer used while manifests are reloaded.
type Holder struct {
	mu  sync.RWMutex
	reg *Registry
}

func NewHolder() *Holder {
	return &Holder{}
}

func (h *Holder) Store(reg *Registry) {
	h.mu.Lock()
	h.reg = reg
	h.mu.Unlock()
}

func (h *Holder) Get(name string) (Definition, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.reg == nil {
		return Definition{}, fmt.Errorf("gateway is dormant; no manifest applied")
	}
	return h.reg.Get(name)
}

func (h *Holder) All() []Definition {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.reg == nil {
		return nil
	}
	return h.reg.All()
}

func (h *Holder) Ready() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.reg != nil
}

func (r *Registry) Get(name string) (Definition, error) {
	def, ok := r.defs[strings.TrimSpace(name)]
	if !ok {
		return Definition{}, fmt.Errorf("unknown capability: %s", name)
	}
	if def.BaseURL == "" {
		return Definition{}, fmt.Errorf("capability %s has no LLM URL configured", name)
	}
	if def.Model == "" && def.Vendor != "openai" {
		return Definition{}, fmt.Errorf("capability %s has no model configured", name)
	}
	return def, nil
}

func (r *Registry) All() []Definition {
	order := Known()
	out := make([]Definition, 0, len(order))
	for _, name := range order {
		if def, ok := r.defs[name]; ok {
			out = append(out, def)
		}
	}
	return out
}

// PromptBoundsError is returned when user input exceeds the capability character limit.
type PromptBoundsError struct {
	MaxCharacters int
}

func (e PromptBoundsError) Error() string {
	return fmt.Sprintf("prompt is outside bounds (max %d characters)", e.MaxCharacters)
}

func (e PromptBoundsError) ToMap() map[string]any {
	return map[string]any{
		"reason":         "Prompt is outside bounds",
		"max_characters": strconv.Itoa(e.MaxCharacters),
	}
}

func ValidateInputBounds(def Definition, input map[string]any) error {
	if def.MaxInputChars <= 0 {
		return nil
	}

	var content string
	switch def.Name {
	case Routing, IntentClassification, ParameterExtraction, PersonalDetails, ConfigureEnvironment:
		content = stringValue(input["message"])
	case Translate:
		content = stringValue(input["text"])
	default:
		return fmt.Errorf("unknown capability: %s", def.Name)
	}

	if utf8.RuneCountInString(content) > def.MaxInputChars {
		return PromptBoundsError{MaxCharacters: def.MaxInputChars}
	}
	return nil
}

func BuildPrompts(def Definition, input map[string]any) (systemPrompt, userPrompt string, err error) {
	override := stringValue(input["system_prompt"])

	switch def.Name {
	case Routing, IntentClassification, ParameterExtraction, PersonalDetails, ConfigureEnvironment:
		message := stringValue(input["message"])
		if message == "" {
			return "", "", fmt.Errorf("data.input.message is required")
		}
		if override != "" {
			return override, message, nil
		}
		return def.SystemPrompt, message, nil
	case Translate:
		text := stringValue(input["text"])
		if text == "" {
			return "", "", fmt.Errorf("data.input.text is required")
		}
		if override != "" {
			return override, text, nil
		}
		sourceLocale := stringValue(input["source_locale"])
		if sourceLocale == "" {
			sourceLocale = "nl"
		}
		targetLocale := stringValue(input["target_locale"])
		if targetLocale == "" {
			targetLocale = "en"
		}
		systemPrompt := fmt.Sprintf(def.SystemPrompt, localeLabel(sourceLocale), localeLabel(targetLocale))
		return systemPrompt, text, nil
	default:
		return "", "", fmt.Errorf("unknown capability: %s", def.Name)
	}
}

// ParseRawJSON decodes model output as a JSON object without capability-specific
// schema checks. Used when the caller supplied a custom system_prompt.
func ParseRawJSON(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty model output")
	}

	raw = stripCodeFence(raw)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON model output: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("empty JSON model output")
	}
	return parsed, nil
}

func ParseResult(capabilityName, raw string) (map[string]any, error) {
	parsed, err := ParseRawJSON(raw)
	if err != nil {
		return nil, err
	}

	switch capabilityName {
	case Routing:
		capability, _ := parsed["capability"].(string)
		if strings.TrimSpace(capability) == "" {
			return nil, fmt.Errorf("routing result missing capability")
		}
		return map[string]any{"capability": strings.TrimSpace(capability)}, nil
	case IntentClassification:
		intent, _ := parsed["intent"].(string)
		if strings.TrimSpace(intent) == "" {
			return nil, fmt.Errorf("intent-classification result missing intent")
		}
		confidence, ok := asFloat(parsed["confidence"])
		if !ok {
			return nil, fmt.Errorf("intent-classification result missing confidence")
		}
		return map[string]any{
			"intent":     strings.TrimSpace(intent),
			"confidence": confidence,
		}, nil
	case ParameterExtraction, PersonalDetails, ConfigureEnvironment:
		return parsed, nil
	case Translate:
		text, _ := parsed["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("translate result missing text")
		}
		return map[string]any{"text": strings.TrimSpace(text)}, nil
	default:
		return nil, fmt.Errorf("unknown capability: %s", capabilityName)
	}
}

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		f, err := typed.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		first := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
		if first == "json" {
			trimmed = trimmed[idx+1:]
		}
	}
	if end := strings.LastIndex(trimmed, "```"); end >= 0 {
		trimmed = trimmed[:end]
	}
	return strings.TrimSpace(trimmed)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
	default:
		return ""
	}
}

func localeLabel(locale string) string {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "nl":
		return "Dutch"
	case "en":
		return "English"
	default:
		return locale
	}
}

const routingSystemPrompt = `You are a routing model for a construction platform AI gateway.
Given a user message, choose the single best next AI capability.
Respond with ONLY valid JSON in this exact shape:
{"capability":"<capability-name>"}
Allowed capability values: intent-classification, translate.
Do not include markdown, explanation, or extra fields.`

const intentSystemPrompt = `You are an intent classification model for a construction services platform.
Given a user message, determine the user's intent and a confidence score between 0 and 1.
Respond with ONLY valid JSON in this exact shape:
{"intent":"<intent-slug>","confidence":0.0}
Use lowercase kebab-case for intent (for example wall-painting, bathroom-renovation).
Do not include markdown, explanation, or extra fields.`

const genericJSONSystemPrompt = `You are an AI assistant for a construction services platform.
Follow the user request and respond with ONLY one valid JSON object.
Do not include markdown or explanation.`

const translateSystemPrompt = `You are a professional translator for a construction services website. Translate the following service description from %s to %s.
Respond with ONLY valid JSON in this exact shape:
{"text":"<translated text>"}
Return the translated text with no quotes inside the value beyond normal punctuation, and no markdown or explanation.`
