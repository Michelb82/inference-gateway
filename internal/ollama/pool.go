package ollama

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ModelTarget is a model hosted at a specific LLM base URL.
type ModelTarget struct {
	BaseURL string
	Name    string
}

func (t ModelTarget) key() string {
	return strings.TrimRight(strings.TrimSpace(t.BaseURL), "/") + "|" + strings.TrimSpace(t.Name)
}

// Pool lazily creates one Client per distinct LLM base URL.
type Pool struct {
	mu      sync.Mutex
	clients map[string]*Client
}

func NewPool() *Pool {
	return &Pool{clients: make(map[string]*Client)}
}

func (p *Pool) Client(baseURL string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("LLM base URL must not be blank")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if client, ok := p.clients[baseURL]; ok {
		return client, nil
	}
	client := NewClient(baseURL)
	p.clients[baseURL] = client
	return client, nil
}

func (p *Pool) Complete(ctx context.Context, baseURL, systemPrompt, prompt, model, keepAlive string) (string, error) {
	client, err := p.Client(baseURL)
	if err != nil {
		return "", err
	}
	return client.Complete(ctx, systemPrompt, prompt, model, keepAlive)
}

func (p *Pool) ListModels(ctx context.Context, baseURL string) ([]string, error) {
	client, err := p.Client(baseURL)
	if err != nil {
		return nil, err
	}
	return client.ListModels(ctx)
}

// EnsureModels checks each target's LLM endpoint and pulls missing models.
// A down endpoint marks its models unavailable without failing the whole set.
// Returns an error only when none of the configured models are available.
func (p *Pool) EnsureModels(ctx context.Context, targets []ModelTarget) (available []ModelTarget, unavailable []ModelTarget, err error) {
	unique := uniqueTargets(targets)
	if len(unique) == 0 {
		return nil, nil, fmt.Errorf("no models configured")
	}

	grouped := map[string][]string{}
	order := make([]string, 0)
	for _, target := range unique {
		if _, ok := grouped[target.BaseURL]; !ok {
			order = append(order, target.BaseURL)
		}
		grouped[target.BaseURL] = append(grouped[target.BaseURL], target.Name)
	}

	for _, baseURL := range order {
		names := grouped[baseURL]
		client, clientErr := p.Client(baseURL)
		if clientErr != nil {
			for _, name := range names {
				unavailable = append(unavailable, ModelTarget{BaseURL: baseURL, Name: name})
			}
			continue
		}

		ready, missing, ensureErr := client.EnsureModels(ctx, names)
		if ensureErr != nil && len(ready) == 0 {
			for _, name := range names {
				unavailable = append(unavailable, ModelTarget{BaseURL: baseURL, Name: name})
			}
			continue
		}
		for _, name := range ready {
			available = append(available, ModelTarget{BaseURL: baseURL, Name: name})
		}
		for _, name := range missing {
			unavailable = append(unavailable, ModelTarget{BaseURL: baseURL, Name: name})
		}
	}

	if len(available) == 0 {
		return nil, unavailable, fmt.Errorf("no configured models available")
	}
	return available, unavailable, nil
}

func uniqueTargets(targets []ModelTarget) []ModelTarget {
	seen := make(map[string]struct{}, len(targets))
	out := make([]ModelTarget, 0, len(targets))
	for _, target := range targets {
		target.BaseURL = strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
		target.Name = strings.TrimSpace(target.Name)
		if target.BaseURL == "" || target.Name == "" {
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
