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
	ContractVersion  = "1"
	ManifestFormat   = "1"
	MaxManifestBytes = 1 << 20
)

type capabilitySupport struct {
	ID                    string `json:"id"`
	MinInputChars         int    `json:"min_input_chars"`
	MaxInputChars         int    `json:"max_input_chars"`
	MaxInputCharsRequired bool   `json:"max_input_chars_required"`
	SystemPromptRequired  bool   `json:"system_prompt_required"`
}

type supportResponse struct {
	ContractVersion  string                    `json:"contract_version"`
	ManifestFormat   string                    `json:"manifest_format"`
	Capabilities     []capabilitySupport       `json:"capabilities"`
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

	capabilities := make([]capabilitySupport, 0, len(capability.Known()))
	for _, id := range capability.Known() {
		capabilities = append(capabilities, capabilitySupport{ID: id, MinInputChars: configmgmt.MinInputChars, MaxInputChars: configmgmt.MaxInputChars, MaxInputCharsRequired: true, SystemPromptRequired: true})
	}
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
