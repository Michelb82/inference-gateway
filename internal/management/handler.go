package management

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

const (
	ContractVersion  = "2"
	ManifestFormat   = "2"
	MaxManifestBytes = 1 << 20
)

type promptVariableSupport struct {
	RequiredFields       []string `json:"required_fields"`
	AllowedNonempty      bool     `json:"allowed_nonempty"`
	AllowedUnique        bool     `json:"allowed_unique"`
	DefaultMustBeAllowed bool     `json:"default_must_be_allowed"`
	Matching             string   `json:"matching"`
}

type capabilitySupport struct {
	Dynamic                  bool                  `json:"dynamic"`
	IDPattern                string                `json:"id_pattern"`
	InputFields              []string              `json:"input_fields"`
	InputFieldRequired       bool                  `json:"input_field_required"`
	PromptVariables          []string              `json:"prompt_variables"`
	PromptVariableDefinition promptVariableSupport `json:"prompt_variable_definition"`
	OutputSchemaKeywords     []string              `json:"output_schema_keywords"`
	OutputPropertyTypes      []string              `json:"output_property_types"`
	MinInputChars            int                   `json:"min_input_chars"`
	MaxInputChars            int                   `json:"max_input_chars"`
	MaxInputCharsRequired    bool                  `json:"max_input_chars_required"`
	SystemPromptRequired     bool                  `json:"system_prompt_required"`
}

type supportResponse struct {
	ContractVersion  string                    `json:"contract_version"`
	ManifestFormat   string                    `json:"manifest_format"`
	Capabilities     capabilitySupport         `json:"capabilities"`
	InferenceVendors []inference.VendorSupport `json:"inference_vendors"`
	IngressAdapters  []string                  `json:"ingress_adapters"`
	MaxManifestBytes int                       `json:"max_manifest_bytes"`
}

type validationResponse struct {
	Valid           bool   `json:"valid"`
	Error           string `json:"error,omitempty"`
	ContractVersion string `json:"contract_version"`
}

// Register adds private, dormant-safe management endpoints to mux.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("/management/v1/manifest-support", handleSupport)
	mux.HandleFunc("/management/v1/manifests/validate", handleValidation)
}

func handleSupport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	capabilities := capabilitySupport{Dynamic: true, InputFieldRequired: true, IDPattern: "[a-z][a-z0-9-]*",
		InputFields: []string{"message", "text"}, PromptVariables: []string{"source_locale", "target_locale"},
		PromptVariableDefinition: promptVariableSupport{RequiredFields: []string{"default", "allowed"}, AllowedNonempty: true, AllowedUnique: true, DefaultMustBeAllowed: true, Matching: "case-sensitive"},
		OutputSchemaKeywords:     []string{"type", "required", "properties", "minLength", "minimum", "maximum"},
		OutputPropertyTypes:      []string{"string", "number", "integer", "boolean", "null"},
		MinInputChars:            capability.MinInputChars, MaxInputChars: capability.MaxInputChars,
		MaxInputCharsRequired: true, SystemPromptRequired: true}
	writeJSON(w, http.StatusOK, supportResponse{
		ContractVersion: ContractVersion, ManifestFormat: ManifestFormat,
		Capabilities: capabilities, InferenceVendors: inference.SupportedVendors(),
		IngressAdapters: []string{"redis"}, MaxManifestBytes: MaxManifestBytes,
	})
}

func handleValidation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxManifestBytes+1))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, validationResponse{Valid: false, Error: "read manifest: " + err.Error(), ContractVersion: ContractVersion})
		return
	}
	if len(body) > MaxManifestBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, validationResponse{Valid: false, Error: "manifest exceeds 1 MiB limit", ContractVersion: ContractVersion})
		return
	}

	manifest, err := configmgmt.Parse(body)
	if err == nil {
		_, err = configmgmt.Resolve(manifest)
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, validationResponse{Valid: false, Error: err.Error(), ContractVersion: ContractVersion})
		return
	}
	writeJSON(w, http.StatusOK, validationResponse{Valid: true, ContractVersion: ContractVersion})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
