package configmgmt

import "github.com/mywebsite/construction-ai-gateway/internal/capability"

// Manifest is the ops/bindings document supplied locally or by AI Manager.
// The manifest declares capability IDs, input mapping, prompts, limits, and output
// contracts. The capability package supplies their shared validation and execution.
type Manifest struct {
	InferenceEndpoint []Endpoint                  `json:"inference_endpoint"`
	CapabilityModels  map[string][]RankedModelRef `json:"capability_models"`
	Tools             []Tool                      `json:"tools,omitempty"`
	Ingress           Ingress                     `json:"ingress"`
	Config            RuntimeConfig               `json:"config"`
}

type Tool struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Instruction string `json:"instruction"`
}

type Endpoint struct {
	ID               string `json:"id"`
	Vendor           string `json:"vendor"`
	URL              string `json:"url"`
	Model            string `json:"model"`
	KeepAliveSeconds int    `json:"keep_alive_seconds"`
}

type RankedModelRef struct {
	Rank            int                                  `json:"rank"`
	Model           string                               `json:"model"`
	MaxInputChars   int                                  `json:"max_input_chars"`
	SystemPrompt    string                               `json:"system_prompt"`
	InputField      string                               `json:"input_field"`
	PromptVariables map[string]capability.PromptVariable `json:"prompt_variables,omitempty"`
	OutputSchema    *capability.OutputSchema             `json:"output_schema,omitempty"`
}

// Ingress is the internal Redis work queue (not the producer contract).
// Producers call OpenAI HTTP; the gateway enqueues CloudEvents here.
type Ingress struct {
	Adapter             string `json:"adapter"`
	Address             string `json:"address"`
	IngressChannel      string `json:"ingress_channel"`
	EgressChannel       string `json:"egress_channel"`
	BRPopTimeoutSeconds int    `json:"brpop_timeout_seconds"`
}

type RuntimeConfig struct {
	MessagePrefix            string   `json:"message_prefix"`
	HTTPAddress              string   `json:"http_address"`
	PriorityCountHigh        int      `json:"priority_count_high"`
	PriorityCountMedium      int      `json:"priority_count_medium"`
	MaxSystemPromptChars     int      `json:"max_system_prompt_chars,omitempty"`
	SystemPromptOverrideOrgs []string `json:"system_prompt_override_orgs,omitempty"`
	LokiEndpoint             string   `json:"loki_endpoint,omitempty"`
}

// ModelBinding is the resolved rank-0 binding for a capability.
type ModelBinding = capability.ModelBinding

// Snapshot is the resolved runtime configuration applied to the data plane.
type Snapshot struct {
	Fingerprint              string
	RedisAddr                string
	InputQueue               string
	OutputQueue              string
	BRPopTimeout             int
	CloudEventTypePrefix     string
	HTTPAddr                 string
	PriorityHighCount        int
	PriorityMediumCount      int
	MaxSystemPromptChars     int
	SystemPromptOverrideOrgs []string
	Bindings                 map[string]ModelBinding
	Tools                    map[string]Tool
	LokiEndpoint             string
}
