package inference

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrOpenAIDisabled is returned when a request targets the OpenAI vendor but
// no OPENAI_BEARER_TOKEN was provided at startup.
var ErrOpenAIDisabled = fmt.Errorf("openai inference vendor is disabled; no OPENAI_BEARER_TOKEN")

// Router is the inference port implementation: it dispatches to vendor
// adapters only when a request or applied binding uses that vendor.
type Router struct {
	mu       sync.Mutex
	apiKey   string
	adapters map[string]Runtime
	probes   map[string]EndpointProbe
	inflight map[string]chan struct{}
	now      func() time.Time
}

// NewRouter builds the runtime router. apiKey may be empty; in that case the
// OpenAI vendor cannot be used. Ollama is not constructed until a binding
// targets it.
func NewRouter(apiKey string) *Router {
	return &Router{
		apiKey:   strings.TrimSpace(apiKey),
		adapters: map[string]Runtime{},
		probes:   map[string]EndpointProbe{},
		inflight: map[string]chan struct{}{},
	}
}

func (r *Router) OpenAIEnabled() bool {
	return r.apiKey != ""
}

func (r *Router) vendor(kind string) (Runtime, error) {
	name, err := ParseVendor(kind)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if rt, ok := r.adapters[name]; ok {
		return rt, nil
	}
	switch name {
	case VendorOllama:
		rt := newOllamaAdapter()
		r.adapters[name] = rt
		return rt, nil
	case VendorOpenAI:
		if r.apiKey == "" {
			return nil, ErrOpenAIDisabled
		}
		rt := newOpenAIAdapter(r.apiKey)
		r.adapters[name] = rt
		return rt, nil
	default:
		return nil, fmt.Errorf("inference vendor %q is not available", name)
	}
}

func (r *Router) Complete(ctx context.Context, req CompleteRequest) (string, error) {
	rt, err := r.vendor(req.Vendor)
	if err != nil {
		return "", err
	}
	return rt.Complete(ctx, req)
}

func (r *Router) EnsureModels(ctx context.Context, targets []ModelTarget) (available []ModelTarget, unavailable []ModelTarget, err error) {
	unique := uniqueTargets(targets)
	if len(unique) == 0 {
		return nil, nil, fmt.Errorf("no models configured")
	}

	grouped := map[string][]ModelTarget{}
	order := make([]string, 0)
	for _, target := range unique {
		if _, ok := grouped[target.Vendor]; !ok {
			order = append(order, target.Vendor)
		}
		grouped[target.Vendor] = append(grouped[target.Vendor], target)
	}

	for _, kind := range order {
		group := grouped[kind]
		rt, vendorErr := r.vendor(kind)
		if vendorErr != nil {
			unavailable = append(unavailable, group...)
			continue
		}
		ready, missing, ensureErr := rt.EnsureModels(ctx, group)
		available = append(available, ready...)
		unavailable = append(unavailable, missing...)
		_ = ensureErr
	}

	if len(available) == 0 {
		return nil, unavailable, fmt.Errorf("no configured models available")
	}
	return available, unavailable, nil
}
