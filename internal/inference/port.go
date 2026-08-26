package inference

import (
	"context"
	"fmt"
	"strings"
)

const (
	VendorOllama = "ollama"
	VendorOpenAI = "openai"
)

// ModelTarget is a model hosted at a specific runtime base URL.
type ModelTarget struct {
	Vendor  string
	BaseURL string
	Name    string
}

func (t ModelTarget) key() string {
	return NormalizeVendor(t.Vendor) + "|" + strings.TrimRight(strings.TrimSpace(t.BaseURL), "/") + "|" + strings.TrimSpace(t.Name)
}

// CompleteRequest is a single chat completion against a runtime vendor.
type CompleteRequest struct {
	Vendor       string
	BaseURL      string
	SystemPrompt string
	Prompt       string
	Model        string
	KeepAlive    string
}

// Runtime is the inference port. Vendors (Ollama, OpenAI API) implement it.
type Runtime interface {
	Complete(ctx context.Context, req CompleteRequest) (string, error)
	ListModels(ctx context.Context, baseURL string) ([]string, error)
	EnsureModels(ctx context.Context, targets []ModelTarget) (available []ModelTarget, unavailable []ModelTarget, err error)
}

// ParseVendor validates a manifest vendor name. Blank is rejected.
func ParseVendor(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	switch name {
	case VendorOllama:
		return VendorOllama, nil
	case VendorOpenAI:
		return VendorOpenAI, nil
	case "":
		return "", fmt.Errorf("inference vendor must not be blank")
	default:
		return "", fmt.Errorf("unsupported inference vendor %q", raw)
	}
}

// NormalizeVendor returns a lowercase vendor name. Invalid values are returned
// as trimmed lowercase without mapping to a default.
func NormalizeVendor(raw string) string {
	name, err := ParseVendor(raw)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return name
}

func uniqueTargets(targets []ModelTarget) []ModelTarget {
	seen := make(map[string]struct{}, len(targets))
	out := make([]ModelTarget, 0, len(targets))
	for _, target := range targets {
		target.Vendor = NormalizeVendor(target.Vendor)
		target.BaseURL = strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
		target.Name = strings.TrimSpace(target.Name)
		if target.BaseURL == "" {
			continue
		}
		vendor, err := ParseVendor(target.Vendor)
		if err != nil {
			continue
		}
		if target.Name == "" && vendor != VendorOpenAI {
			continue
		}
		key := target.key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	return out
}
