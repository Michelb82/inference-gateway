package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ManifestURL             string
	ManifestPollingInterval time.Duration
	Debug                   bool
	OpenAIBearerToken       string
}

func Load() (Config, error) {
	cfg := Config{
		ManifestURL:             envOrDefault("MANIFEST_URL", "http://ai-manager:80/manifest.json"),
		ManifestPollingInterval: 5 * time.Minute,
		Debug:                   envBool("DEBUG"),
		OpenAIBearerToken:       strings.TrimSpace(os.Getenv("OPENAI_BEARER_TOKEN")),
	}

	intervalRaw := strings.TrimSpace(os.Getenv("MANIFEST_POLLING_INTERVAL"))
	if intervalRaw != "" {
		interval, err := time.ParseDuration(intervalRaw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid MANIFEST_POLLING_INTERVAL %q: %w", intervalRaw, err)
		}
		if interval < time.Second {
			return Config{}, fmt.Errorf("invalid MANIFEST_POLLING_INTERVAL %q: must be >= 1s", intervalRaw)
		}
		cfg.ManifestPollingInterval = interval
	}

	if strings.TrimSpace(cfg.ManifestURL) == "" {
		return Config{}, fmt.Errorf("MANIFEST_URL must not be blank")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
