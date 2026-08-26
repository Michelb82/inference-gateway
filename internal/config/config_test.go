package config_test

import (
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ManifestURL != "http://ai-manager:80/manifest.json" {
		t.Fatalf("ManifestURL = %q", cfg.ManifestURL)
	}
	if cfg.ManifestPollingInterval != 5*time.Minute {
		t.Fatalf("ManifestPollingInterval = %v", cfg.ManifestPollingInterval)
	}
	if cfg.Debug {
		t.Fatalf("Debug = true, want false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("MANIFEST_URL", "http://manager:8080/manifest.json")
	t.Setenv("MANIFEST_POLLING_INTERVAL", "30s")
	t.Setenv("DEBUG", "true")
	t.Setenv("OPENAI_BEARER_TOKEN", "sk-test")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ManifestURL != "http://manager:8080/manifest.json" {
		t.Fatalf("ManifestURL = %q", cfg.ManifestURL)
	}
	if cfg.ManifestPollingInterval != 30*time.Second {
		t.Fatalf("ManifestPollingInterval = %v", cfg.ManifestPollingInterval)
	}
	if !cfg.Debug {
		t.Fatalf("Debug = false, want true")
	}
	if cfg.OpenAIBearerToken != "sk-test" {
		t.Fatalf("OpenAIBearerToken = %q", cfg.OpenAIBearerToken)
	}
}

func TestLoadInvalidPollingInterval(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "not a duration", value: "abc"},
		{name: "too short", value: "500ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MANIFEST_POLLING_INTERVAL", tt.value)
			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load() expected error for MANIFEST_POLLING_INTERVAL=%q", tt.value)
			}
		})
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"MANIFEST_URL", "MANIFEST_POLLING_INTERVAL", "DEBUG", "OPENAI_BEARER_TOKEN"} {
		t.Setenv(key, "")
	}
}
