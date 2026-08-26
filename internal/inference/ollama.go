package inference

import (
	"context"

	"github.com/mywebsite/construction-ai-gateway/internal/ollama"
)

// ollamaAdapter wraps the existing Ollama client/pool as an inference Runtime.
type ollamaAdapter struct {
	pool *ollama.Pool
}

func newOllamaAdapter() *ollamaAdapter {
	return &ollamaAdapter{pool: ollama.NewPool()}
}

func (a *ollamaAdapter) Complete(ctx context.Context, req CompleteRequest) (string, error) {
	return a.pool.Complete(ctx, req.BaseURL, req.SystemPrompt, req.Prompt, req.Model, req.KeepAlive)
}

func (a *ollamaAdapter) ListModels(ctx context.Context, baseURL string) ([]string, error) {
	return a.pool.ListModels(ctx, baseURL)
}

func (a *ollamaAdapter) EnsureModels(ctx context.Context, targets []ModelTarget) (available []ModelTarget, unavailable []ModelTarget, err error) {
	ollamaTargets := make([]ollama.ModelTarget, 0, len(targets))
	for _, target := range targets {
		ollamaTargets = append(ollamaTargets, ollama.ModelTarget{BaseURL: target.BaseURL, Name: target.Name})
	}
	ready, missing, err := a.pool.EnsureModels(ctx, ollamaTargets)
	available = fromOllamaTargets(ready)
	unavailable = fromOllamaTargets(missing)
	return available, unavailable, err
}

func fromOllamaTargets(targets []ollama.ModelTarget) []ModelTarget {
	out := make([]ModelTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, ModelTarget{
			Vendor:  VendorOllama,
			BaseURL: target.BaseURL,
			Name:    target.Name,
		})
	}
	return out
}
