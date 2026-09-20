package configmgmt_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func validManifest() configmgmt.Manifest {
	return configmgmt.Manifest{
		InferenceEndpoint: []configmgmt.Endpoint{
			{ID: "qwen3:1.7b", Vendor: "ollama", URL: "http://llm-model:11434", Model: "qwen3:1.7b-q4_K_M", KeepAliveSeconds: 300},
			{ID: "qwen3:4b", Vendor: "ollama", URL: "http://llm-model:11434", Model: "qwen3:4b-q4_K_M", KeepAliveSeconds: 300},
			{ID: "qwen3:14b", Vendor: "ollama", URL: "http://llm-model:11434", Model: "qwen3:14b-q4_K_M", KeepAliveSeconds: 120},
		},
		CapabilityModels: map[string][]configmgmt.RankedModelRef{
			"routing": {
				{Rank: 0, Model: "qwen3:1.7b"},
				{Rank: 1, Model: "qwen3:4b"},
			},
			"intent-classification": {
				{Rank: 0, Model: "qwen3:4b"},
				{Rank: 1, Model: "qwen3:14b"},
			},
			"parameter-extraction": {
				{Rank: 0, Model: "qwen3:14b"},
			},
			"personal-details-extraction": {
				{Rank: 0, Model: "qwen3:14b"},
			},
			"translate": {
				{Rank: 0, Model: "qwen3:14b"},
				{Rank: 1, Model: "qwen3:4b"},
			},
			"configure-environment": {
				{Rank: 0, Model: "qwen3:14b"},
			},
		},
		Ingress: configmgmt.Ingress{
			Adapter:             "redis",
			Address:             "redis:6379",
			IngressChannel:      "ai.requests",
			EgressChannel:       "ai.responses",
			BRPopTimeoutSeconds: 5,
		},
		Config: configmgmt.RuntimeConfig{
			MessagePrefix:       "com.mywebsite.ai",
			HTTPAddress:         ":80",
			PriorityCountHigh:   3,
			PriorityCountMedium: 3,
		},
	}
}

func TestLoadDistManifest(t *testing.T) {
	path := filepath.Join("..", "..", "manifest.json.dist")
	m, err := configmgmt.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(manifest.json.dist) error = %v", err)
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.Bindings["routing"].Model != "qwen3:1.7b-q4_K_M" {
		t.Fatalf("routing model = %q", snap.Bindings["routing"].Model)
	}
	if snap.Bindings["routing"].Vendor != "ollama" {
		t.Fatalf("routing vendor = %q", snap.Bindings["routing"].Vendor)
	}
	if snap.Bindings["routing"].MaxInputChars != 200 {
		t.Fatalf("routing max chars = %d", snap.Bindings["routing"].MaxInputChars)
	}
	if snap.Bindings["intent-classification"].Model != "qwen3:4b-q4_K_M" {
		t.Fatalf("intent model = %q", snap.Bindings["intent-classification"].Model)
	}
	if snap.Bindings["intent-classification"].MaxInputChars != 8000 {
		t.Fatalf("intent max chars = %d", snap.Bindings["intent-classification"].MaxInputChars)
	}
	if snap.Bindings["translate"].Model != "qwen3:14b-q4_K_M" {
		t.Fatalf("translate model = %q", snap.Bindings["translate"].Model)
	}
	if snap.Bindings["translate"].MaxInputChars != 16000 {
		t.Fatalf("translate max chars = %d", snap.Bindings["translate"].MaxInputChars)
	}
	for _, name := range []string{"parameter-extraction", "personal-details-extraction", "configure-environment"} {
		if snap.Bindings[name].Model != "qwen3:14b-q4_K_M" {
			t.Fatalf("%s model = %q", name, snap.Bindings[name].Model)
		}
		if snap.Bindings[name].MaxInputChars != 16000 {
			t.Fatalf("%s max chars = %d", name, snap.Bindings[name].MaxInputChars)
		}
	}
	if snap.Bindings["routing"].KeepAlive != "300s" {
		t.Fatalf("routing keep_alive = %q", snap.Bindings["routing"].KeepAlive)
	}
	if snap.Bindings["translate"].KeepAlive != "120s" {
		t.Fatalf("translate keep_alive = %q", snap.Bindings["translate"].KeepAlive)
	}

	var openai *configmgmt.Endpoint
	for i := range m.InferenceEndpoint {
		if m.InferenceEndpoint[i].ID == "openai" {
			openai = &m.InferenceEndpoint[i]
			break
		}
	}
	if openai == nil {
		t.Fatal("manifest.json.dist missing inference_endpoint id openai")
	}
	if openai.Vendor != "openai" {
		t.Fatalf("openai vendor = %q", openai.Vendor)
	}
	if openai.URL != "https://someurl" {
		t.Fatalf("openai url = %q", openai.URL)
	}
	if openai.Model != "" {
		t.Fatalf("openai model = %q, want empty (null)", openai.Model)
	}
	if last := lastRankedModel(m.CapabilityModels["routing"]); last != "openai" {
		t.Fatalf("routing last rank = %q, want openai", last)
	}
}

func lastRankedModel(refs []configmgmt.RankedModelRef) string {
	if len(refs) == 0 {
		return ""
	}
	best := refs[0]
	for _, ref := range refs[1:] {
		if ref.Rank > best.Rank {
			best = ref
		}
	}
	return best.Model
}

func TestValidateAndResolve(t *testing.T) {
	m := validManifest()
	if err := configmgmt.Validate(m); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	routing := snap.Bindings["routing"]
	if routing.BaseURL != "http://llm-model:11434" {
		t.Fatalf("routing url = %q", routing.BaseURL)
	}
	if routing.Model != "qwen3:1.7b-q4_K_M" || routing.KeepAlive != "300s" {
		t.Fatalf("routing binding = %+v", routing)
	}
	if routing.MaxInputChars != 200 {
		t.Fatalf("routing max chars = %d", routing.MaxInputChars)
	}
	if routing.Vendor != "ollama" {
		t.Fatalf("routing vendor = %q, want ollama", routing.Vendor)
	}
	if snap.Bindings["intent-classification"].MaxInputChars != 8000 {
		t.Fatalf("intent max chars = %d", snap.Bindings["intent-classification"].MaxInputChars)
	}
	if snap.Bindings["translate"].MaxInputChars != 16000 {
		t.Fatalf("translate max chars = %d", snap.Bindings["translate"].MaxInputChars)
	}
	if snap.Bindings["intent-classification"].Model != "qwen3:4b-q4_K_M" {
		t.Fatalf("intent binding = %+v", snap.Bindings["intent-classification"])
	}
	if snap.Bindings["translate"].Model != "qwen3:14b-q4_K_M" {
		t.Fatalf("translate binding = %+v", snap.Bindings["translate"])
	}
	if snap.RedisAddr != "redis:6379" ||
		snap.InputQueue != "ai.requests" ||
		snap.OutputQueue != "ai.responses" ||
		snap.BRPopTimeout != 5 ||
		snap.CloudEventTypePrefix != "com.mywebsite.ai" ||
		snap.HTTPAddr != ":80" ||
		snap.PriorityHighCount != 3 ||
		snap.PriorityMediumCount != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Fingerprint == "" {
		t.Fatalf("expected fingerprint")
	}
}

func TestResolveUsesRankZeroOnly(t *testing.T) {
	m := validManifest()
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{
		{Rank: 1, Model: "qwen3:4b"},
		{Rank: 0, Model: "qwen3:1.7b"},
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.Bindings["routing"].Model != "qwen3:1.7b-q4_K_M" {
		t.Fatalf("expected rank 0 model, got %q", snap.Bindings["routing"].Model)
	}
}

func TestResolveOpenAIVendor(t *testing.T) {
	m := validManifest()
	m.InferenceEndpoint = append(m.InferenceEndpoint, configmgmt.Endpoint{
		ID:               "gpt-4o-mini",
		URL:              "https://api.openai.com",
		Model:            "gpt-4o-mini",
		Vendor:           "openai",
		KeepAliveSeconds: 0,
	})
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{
		{Rank: 0, Model: "gpt-4o-mini"},
	}
	if err := configmgmt.Validate(m); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	routing := snap.Bindings["routing"]
	if routing.Vendor != "openai" {
		t.Fatalf("vendor = %q", routing.Vendor)
	}
	if routing.BaseURL != "https://api.openai.com" {
		t.Fatalf("url = %q", routing.BaseURL)
	}
	if routing.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q", routing.Model)
	}
	if routing.KeepAlive != "" {
		t.Fatalf("keep_alive = %q, want empty", routing.KeepAlive)
	}
}

func TestResolveOpenAINullModel(t *testing.T) {
	m := validManifest()
	m.InferenceEndpoint = append(m.InferenceEndpoint, configmgmt.Endpoint{
		ID:               "openai",
		Vendor:           "openai",
		URL:              "https://someurl",
		Model:            "",
		KeepAliveSeconds: 300,
	})
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{
		{Rank: 0, Model: "openai"},
	}
	if err := configmgmt.Validate(m); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	routing := snap.Bindings["routing"]
	if routing.Vendor != "openai" {
		t.Fatalf("vendor = %q", routing.Vendor)
	}
	if routing.Model != "" {
		t.Fatalf("model = %q, want empty", routing.Model)
	}
	if routing.BaseURL != "https://someurl" {
		t.Fatalf("url = %q", routing.BaseURL)
	}
	if routing.KeepAlive != "300s" {
		t.Fatalf("keep_alive = %q", routing.KeepAlive)
	}
}

func TestResolveUsesCapabilityDefaultMaxInputChars(t *testing.T) {
	m := validManifest()
	// Omitting max_input_chars (and zero) must use capability.DefaultMaxInputChars.
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "qwen3:1.7b"}}
	m.CapabilityModels["intent-classification"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "qwen3:4b", MaxInputChars: 0}}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.Bindings["routing"].MaxInputChars != 200 {
		t.Fatalf("routing max = %d, want capability default 200", snap.Bindings["routing"].MaxInputChars)
	}
	if snap.Bindings["intent-classification"].MaxInputChars != 8000 {
		t.Fatalf("intent max = %d, want capability default 8000", snap.Bindings["intent-classification"].MaxInputChars)
	}
}

func TestResolveHonorsExplicitMaxInputChars(t *testing.T) {
	m := validManifest()
	m.CapabilityModels["translate"] = []configmgmt.RankedModelRef{
		{Rank: 0, Model: "qwen3:14b", MaxInputChars: 42},
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.Bindings["translate"].MaxInputChars != 42 {
		t.Fatalf("translate max = %d, want 42", snap.Bindings["translate"].MaxInputChars)
	}
}

func TestResolveErrorsWithoutRankZero(t *testing.T) {
	m := validManifest()
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 1, Model: "qwen3:1.7b"}}
	_, err := configmgmt.Resolve(m)
	if err == nil || !strings.Contains(err.Error(), "rank 0") {
		t.Fatalf("error = %v, want rank 0", err)
	}
}

func TestResolveErrorsUnknownModel(t *testing.T) {
	m := validManifest()
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "missing"}}
	_, err := configmgmt.Resolve(m)
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("error = %v, want unknown model", err)
	}
}

func TestResolveBindsOnlyKnownCapabilities(t *testing.T) {
	m := validManifest()
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(snap.Bindings) != 6 {
		t.Fatalf("bindings = %d, want 6 known capabilities", len(snap.Bindings))
	}
	for _, name := range []string{"routing", "intent-classification", "parameter-extraction", "personal-details-extraction", "translate", "configure-environment"} {
		if _, ok := snap.Bindings[name]; !ok {
			t.Fatalf("missing binding for %q", name)
		}
	}
}

func TestResolveTrimsTrailingSlashOnURL(t *testing.T) {
	m := validManifest()
	m.InferenceEndpoint[0].URL = "http://llm-model:11434/"
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.Bindings["routing"].BaseURL != "http://llm-model:11434" {
		t.Fatalf("BaseURL = %q", snap.Bindings["routing"].BaseURL)
	}
}

func TestFingerprintChangesWhenBindingChanges(t *testing.T) {
	m := validManifest()
	a, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "qwen3:4b"}}
	b, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatalf("expected fingerprints to differ")
	}
}

func TestResolveSystemPromptPolicyDefaults(t *testing.T) {
	m := validManifest()
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.MaxSystemPromptChars != 4000 {
		t.Fatalf("MaxSystemPromptChars = %d, want 4000", snap.MaxSystemPromptChars)
	}
	if len(snap.SystemPromptOverrideOrgs) != 0 {
		t.Fatalf("SystemPromptOverrideOrgs = %v, want empty", snap.SystemPromptOverrideOrgs)
	}
}

func TestResolveSystemPromptPolicy(t *testing.T) {
	m := validManifest()
	m.Config.MaxSystemPromptChars = 1200
	m.Config.SystemPromptOverrideOrgs = []string{" 42 ", "7"}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.MaxSystemPromptChars != 1200 {
		t.Fatalf("MaxSystemPromptChars = %d", snap.MaxSystemPromptChars)
	}
	if len(snap.SystemPromptOverrideOrgs) != 2 ||
		snap.SystemPromptOverrideOrgs[0] != "42" ||
		snap.SystemPromptOverrideOrgs[1] != "7" {
		t.Fatalf("SystemPromptOverrideOrgs = %v", snap.SystemPromptOverrideOrgs)
	}
}

func TestFingerprintChangesWhenSystemPromptPolicyChanges(t *testing.T) {
	m := validManifest()
	a, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	m.Config.SystemPromptOverrideOrgs = []string{"7"}
	b, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatalf("expected fingerprints to differ when allowlist changes")
	}
	m.Config.MaxSystemPromptChars = 100
	c, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if b.Fingerprint == c.Fingerprint {
		t.Fatalf("expected fingerprints to differ when max chars change")
	}
}

func TestLoadDistManifestSystemPromptPolicy(t *testing.T) {
	path := filepath.Join("..", "..", "manifest.json.dist")
	m, err := configmgmt.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(manifest.json.dist) error = %v", err)
	}
	snap, err := configmgmt.Resolve(m)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snap.MaxSystemPromptChars != 4000 {
		t.Fatalf("MaxSystemPromptChars = %d", snap.MaxSystemPromptChars)
	}
	if len(snap.SystemPromptOverrideOrgs) != 0 {
		t.Fatalf("SystemPromptOverrideOrgs = %v, want empty", snap.SystemPromptOverrideOrgs)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*configmgmt.Manifest)
		substr string
	}{
		{
			name: "empty capability_models",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels = nil
			},
			substr: "capability_models",
		},
		{
			name: "unknown capability_models key",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["summarize"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "qwen3:1.7b"}}
			},
			substr: "unknown capability",
		},
		{
			name: "padded capability_models key",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels[" routing "] = []configmgmt.RankedModelRef{{Rank: 0, Model: "qwen3:1.7b"}}
			},
			substr: "unknown capability",
		},
		{
			name: "empty capability_models map",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels = map[string][]configmgmt.RankedModelRef{}
			},
			substr: "capability_models",
		},
		{
			name: "empty capability_models entry list",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["translate"] = []configmgmt.RankedModelRef{}
			},
			substr: "translate",
		},
		{
			name: "blank capability model ref",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "  "}}
			},
			substr: "model must not be blank",
		},
		{
			name: "negative max_input_chars",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{
					{Rank: 0, Model: "qwen3:1.7b", MaxInputChars: -1},
				}
			},
			substr: "max_input_chars",
		},
		{
			name: "missing capability_models entry",
			mutate: func(m *configmgmt.Manifest) {
				delete(m.CapabilityModels, "translate")
			},
			substr: "translate",
		},
		{
			name: "empty inference_endpoint",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint = nil
			},
			substr: "inference_endpoint",
		},
		{
			name: "blank endpoint id",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].ID = "  "
			},
			substr: "id",
		},
		{
			name: "blank endpoint url",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].URL = ""
			},
			substr: "url",
		},
		{
			name: "blank ollama model name",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].Model = ""
			},
			substr: "model",
		},
		{
			name: "keep_alive too low",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].KeepAliveSeconds = 0
			},
			substr: "keep_alive_seconds",
		},
		{
			name: "unsupported inference vendor",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].Vendor = "vllm"
			},
			substr: "inference vendor",
		},
		{
			name: "blank vendor",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].Vendor = "  "
			},
			substr: "vendor",
		},
		{
			name: "negative keep_alive",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint[0].KeepAliveSeconds = -1
			},
			substr: "keep_alive_seconds",
		},
		{
			name: "duplicate endpoint id",
			mutate: func(m *configmgmt.Manifest) {
				m.InferenceEndpoint = append(m.InferenceEndpoint, m.InferenceEndpoint[0])
			},
			substr: "duplicates id",
		},
		{
			name: "missing rank 0",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 1, Model: "qwen3:1.7b"}}
			},
			substr: "rank 0",
		},
		{
			name: "duplicate rank",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{
					{Rank: 0, Model: "qwen3:1.7b"},
					{Rank: 0, Model: "qwen3:4b"},
				}
			},
			substr: "duplicates rank",
		},
		{
			name: "unknown model reference",
			mutate: func(m *configmgmt.Manifest) {
				m.CapabilityModels["routing"] = []configmgmt.RankedModelRef{{Rank: 0, Model: "missing"}}
			},
			substr: "unknown model",
		},
		{
			name: "blank adapter",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.Adapter = "  "
			},
			substr: "adapter",
		},
		{
			name: "unsupported adapter",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.Adapter = "kafka"
			},
			substr: "not supported",
		},
		{
			name: "blank ingress address",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.Address = ""
			},
			substr: "address",
		},
		{
			name: "blank ingress channel",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.IngressChannel = ""
			},
			substr: "ingress_channel",
		},
		{
			name: "blank egress channel",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.EgressChannel = ""
			},
			substr: "egress_channel",
		},
		{
			name: "brpop timeout too low",
			mutate: func(m *configmgmt.Manifest) {
				m.Ingress.BRPopTimeoutSeconds = 0
			},
			substr: "brpop_timeout_seconds",
		},
		{
			name: "blank message prefix",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.MessagePrefix = ""
			},
			substr: "message_prefix",
		},
		{
			name: "blank http address",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.HTTPAddress = ""
			},
			substr: "http_address",
		},
		{
			name: "priority high too low",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.PriorityCountHigh = 0
			},
			substr: "priority_count_high",
		},
		{
			name: "priority medium too low",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.PriorityCountMedium = 0
			},
			substr: "priority_count_medium",
		},
		{
			name: "negative max_system_prompt_chars",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.MaxSystemPromptChars = -1
			},
			substr: "max_system_prompt_chars",
		},
		{
			name: "blank system_prompt_override_orgs entry",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.SystemPromptOverrideOrgs = []string{"7", " "}
			},
			substr: "system_prompt_override_orgs",
		},
		{
			name: "duplicate system_prompt_override_orgs",
			mutate: func(m *configmgmt.Manifest) {
				m.Config.SystemPromptOverrideOrgs = []string{"7", "7"}
			},
			substr: "duplicates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.mutate(&m)
			err := configmgmt.Validate(m)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.substr)
			}
			if !strings.Contains(err.Error(), tt.substr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.substr)
			}
		})
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := configmgmt.Parse([]byte(`{"inference_endpoint":`))
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestParseRejectsInvalidManifest(t *testing.T) {
	_, err := configmgmt.Parse([]byte(`{"inference_endpoint":[]}`))
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseIgnoresLegacyCapabilitiesField(t *testing.T) {
	m := validManifest()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	asMap["capabilities"] = []string{"routing", "intent-classification", "translate", "extra"}
	withLegacy, err := json.Marshal(asMap)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	got, err := configmgmt.Parse(withLegacy)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.InferenceEndpoint) != 3 {
		t.Fatalf("inference_endpoint = %d", len(got.InferenceEndpoint))
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	data, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := configmgmt.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if len(m.InferenceEndpoint) != 3 {
		t.Fatalf("inference_endpoint = %d", len(m.InferenceEndpoint))
	}
}

func TestLoadFileMissing(t *testing.T) {
	_, err := configmgmt.LoadFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestLoadFileInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := configmgmt.LoadFile(path)
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestFetchURLSuccess(t *testing.T) {
	m := validManifest()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	got, err := configmgmt.FetchURL(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("FetchURL() error = %v", err)
	}
	if len(got.InferenceEndpoint) != 3 {
		t.Fatalf("inference_endpoint = %d", len(got.InferenceEndpoint))
	}
}

func TestFetchURLNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := configmgmt.FetchURL(context.Background(), nil, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("error = %v, want status error", err)
	}
}

func TestFetchURLInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"inference_endpoint":[]}`))
	}))
	defer srv.Close()

	_, err := configmgmt.FetchURL(context.Background(), nil, srv.URL)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestManagerBootstrapAndSoftFailPoll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	data, _ := json.Marshal(validManifest())
	_ = os.WriteFile(path, data, 0o644)

	var applies atomic.Int32
	mgr := configmgmt.NewManager("http://127.0.0.1:1/missing", time.Hour, nil, func(snap configmgmt.Snapshot, first bool) error {
		applies.Add(1)
		if !first {
			t.Errorf("bootstrap apply should report first=true")
		}
		return nil
	})
	if err := mgr.Bootstrap(path); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if !mgr.HasSnapshot() {
		t.Fatalf("expected snapshot after bootstrap")
	}
	if applies.Load() != 1 {
		t.Fatalf("applies = %d", applies.Load())
	}
	snap := mgr.Snapshot()
	if snap == nil || snap.RedisAddr != "redis:6379" {
		t.Fatalf("Snapshot() = %+v", snap)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if applies.Load() != 1 {
		t.Fatalf("soft-fail poll should not clear/reapply; applies = %d", applies.Load())
	}
	if !mgr.HasSnapshot() {
		t.Fatalf("expected snapshot retained after soft-fail")
	}
}

func TestManagerBootstrapInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte(`{"inference_endpoint":[]}`), 0o644)
	mgr := configmgmt.NewManager("http://example.invalid/manifest.json", time.Hour, nil, nil)
	if err := mgr.Bootstrap(path); err == nil {
		t.Fatalf("expected bootstrap error")
	}
	if mgr.HasSnapshot() {
		t.Fatalf("expected no snapshot after failed bootstrap")
	}
}

func TestManagerBootstrapApplyError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	data, _ := json.Marshal(validManifest())
	_ = os.WriteFile(path, data, 0o644)

	mgr := configmgmt.NewManager("http://example.invalid/manifest.json", time.Hour, nil, func(snap configmgmt.Snapshot, first bool) error {
		return context.DeadlineExceeded
	})
	if err := mgr.Bootstrap(path); err == nil {
		t.Fatalf("expected bootstrap apply error")
	}
	if mgr.HasSnapshot() {
		t.Fatalf("failed apply must not store snapshot")
	}
}

func TestManagerDormantUntilRemote(t *testing.T) {
	m := validManifest()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	var applies atomic.Int32
	var sawFirst atomic.Bool
	mgr := configmgmt.NewManager(srv.URL, 50*time.Millisecond, nil, func(snap configmgmt.Snapshot, first bool) error {
		applies.Add(1)
		if first {
			sawFirst.Store(true)
		}
		return nil
	})
	if err := mgr.Bootstrap(""); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if mgr.HasSnapshot() {
		t.Fatalf("expected dormant before poll")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if applies.Load() < 1 || !mgr.HasSnapshot() {
		t.Fatalf("expected remote apply; applies=%d has=%v", applies.Load(), mgr.HasSnapshot())
	}
	if !sawFirst.Load() {
		t.Fatalf("expected first=true on initial remote apply")
	}
}

func TestManagerRemainsDormantOnPollFailure(t *testing.T) {
	mgr := configmgmt.NewManager("http://127.0.0.1:1/missing", 20*time.Millisecond, nil, nil)
	_ = mgr.Bootstrap("")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)
	if mgr.HasSnapshot() {
		t.Fatalf("expected still dormant")
	}
}

func TestManagerPollReplacesChangedManifest(t *testing.T) {
	m1 := validManifest()
	m2 := validManifest()
	m2.Config.PriorityCountHigh = 9

	var serveSecond atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveSecond.Load() {
			_ = json.NewEncoder(w).Encode(m2)
			return
		}
		_ = json.NewEncoder(w).Encode(m1)
	}))
	defer srv.Close()

	var applies atomic.Int32
	mgr := configmgmt.NewManager(srv.URL, 30*time.Millisecond, nil, func(snap configmgmt.Snapshot, first bool) error {
		applies.Add(1)
		return nil
	})
	_ = mgr.Bootstrap("")

	ctx1, cancel1 := context.WithTimeout(context.Background(), 120*time.Millisecond)
	mgr.Run(ctx1)
	cancel1()
	if applies.Load() < 1 {
		t.Fatalf("expected first apply")
	}
	firstCount := applies.Load()
	if mgr.Snapshot().PriorityHighCount != 3 {
		t.Fatalf("priority = %d", mgr.Snapshot().PriorityHighCount)
	}

	serveSecond.Store(true)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()
	mgr.Run(ctx2)

	if applies.Load() <= firstCount {
		t.Fatalf("expected second apply after manifest change; applies=%d first=%d", applies.Load(), firstCount)
	}
	if mgr.Snapshot().PriorityHighCount != 9 {
		t.Fatalf("priority after update = %d", mgr.Snapshot().PriorityHighCount)
	}
}

func TestManagerUnchangedFingerprintDoesNotReapply(t *testing.T) {
	m := validManifest()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	var applies atomic.Int32
	mgr := configmgmt.NewManager(srv.URL, 20*time.Millisecond, nil, func(snap configmgmt.Snapshot, first bool) error {
		applies.Add(1)
		return nil
	})
	_ = mgr.Bootstrap("")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if applies.Load() != 1 {
		t.Fatalf("unchanged remote body should apply once; applies=%d", applies.Load())
	}
}

func TestManagerPollApplyErrorKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	data, _ := json.Marshal(validManifest())
	_ = os.WriteFile(path, data, 0o644)

	m2 := validManifest()
	m2.Config.PriorityCountHigh = 7
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(m2)
	}))
	defer srv.Close()

	var remoteAttempts atomic.Int32
	mgr := configmgmt.NewManager(srv.URL, 30*time.Millisecond, nil, func(snap configmgmt.Snapshot, first bool) error {
		if first {
			return nil
		}
		remoteAttempts.Add(1)
		return context.Canceled
	})
	if err := mgr.Bootstrap(path); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	before := mgr.Snapshot().Fingerprint

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if remoteAttempts.Load() < 1 {
		t.Fatalf("expected failed remote apply attempt")
	}
	if mgr.Snapshot().Fingerprint != before {
		t.Fatalf("failed remote apply must keep previous snapshot")
	}
	if mgr.Snapshot().PriorityHighCount != 3 {
		t.Fatalf("priority = %d, want previous value 3", mgr.Snapshot().PriorityHighCount)
	}
}

func TestManagerPollInvalidBodyKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	data, _ := json.Marshal(validManifest())
	_ = os.WriteFile(path, data, 0o644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"inference_endpoint":[]}`))
	}))
	defer srv.Close()

	mgr := configmgmt.NewManager(srv.URL, 30*time.Millisecond, nil, func(snap configmgmt.Snapshot, first bool) error {
		return nil
	})
	if err := mgr.Bootstrap(path); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if !mgr.HasSnapshot() || mgr.Snapshot().PriorityHighCount != 3 {
		t.Fatalf("invalid remote body must keep previous snapshot")
	}
}
