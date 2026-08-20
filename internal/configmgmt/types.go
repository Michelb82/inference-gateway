package configmgmt

// Manifest is the ops/bindings document supplied locally or by AI Manager.
// Capability semantics (prompts, I/O shapes, parsers) live in the gateway's
// capability package; this document only catalogs models and binds them to
// those known capability ids via capability_models.
type Manifest struct {
	Models           []Model                     `json:"models"`
	CapabilityModels map[string][]RankedModelRef `json:"capability_models"`
	Ingress          Ingress                     `json:"ingress"`
	Config           RuntimeConfig               `json:"config"`
}

type Model struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	Model            string `json:"model"`
	KeepAliveSeconds int    `json:"keep_alive_seconds"`
}

type RankedModelRef struct {
	Rank          int    `json:"rank"`
	Model         string `json:"model"`
	MaxInputChars int    `json:"max_input_chars,omitempty"`
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
}

// ModelBinding is the resolved rank-0 binding for a capability.
type ModelBinding struct {
	BaseURL       string
	Model         string
	KeepAlive     string
	MaxInputChars int
}

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
}
