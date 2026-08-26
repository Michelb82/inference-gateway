package inference

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	ProbeTTL    = time.Hour
	PingTimeout = 15 * time.Second
	pingSystem  = "ping"
	pingUser    = "pong"
)

// EndpointProbe is a cached models-list plus chat ping for one vendor URL.
type EndpointProbe struct {
	Vendor     string
	BaseURL    string
	Catalog    []string
	CatalogErr error
	ChatOK     bool
	ChatErr    error
	CheckedAt  time.Time
}

func (p EndpointProbe) CatalogContains(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, model := range p.Catalog {
		if model == name {
			return true
		}
	}
	return false
}

func endpointKey(vendor, baseURL string) string {
	return NormalizeVendor(vendor) + "|" + strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func uniqueEndpoints(targets []ModelTarget) []ModelTarget {
	seen := make(map[string]struct{})
	out := make([]ModelTarget, 0, len(targets))
	for _, target := range targets {
		target.Vendor = NormalizeVendor(target.Vendor)
		target.BaseURL = strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
		target.Name = strings.TrimSpace(target.Name)
		if target.BaseURL == "" {
			continue
		}
		if _, err := ParseVendor(target.Vendor); err != nil {
			continue
		}
		key := endpointKey(target.Vendor, target.BaseURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	return out
}

func pingModel(targets []ModelTarget, vendor, baseURL string) string {
	vendor = NormalizeVendor(vendor)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, target := range targets {
		if NormalizeVendor(target.Vendor) != vendor {
			continue
		}
		if strings.TrimRight(strings.TrimSpace(target.BaseURL), "/") != baseURL {
			continue
		}
		if name := strings.TrimSpace(target.Name); name != "" {
			return name
		}
	}
	return ""
}

func (r *Router) nowTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// SetNow overrides the clock used for probe TTL. Tests use this to advance time.
func (r *Router) SetNow(now func() time.Time) {
	r.now = now
}

// Probe returns the cached probe for a vendor URL, if any.
func (r *Router) Probe(vendor, baseURL string) (EndpointProbe, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.probes[endpointKey(vendor, baseURL)]
	return p, ok
}

// AnyChatOK reports whether any unique endpoint in targets has a successful
// cached chat ping.
func (r *Router) AnyChatOK(targets []ModelTarget) bool {
	for _, target := range uniqueEndpoints(targets) {
		p, ok := r.Probe(target.Vendor, target.BaseURL)
		if ok && p.ChatOK {
			return true
		}
	}
	return false
}

// RefreshProbes lists models and runs one chat ping per unique vendor URL.
// Probes younger than maxAge are reused. maxAge <= 0 always refreshes.
func (r *Router) RefreshProbes(ctx context.Context, targets []ModelTarget, maxAge time.Duration) {
	groups := map[string][]ModelTarget{}
	order := make([]string, 0)
	for _, target := range uniqueTargets(append([]ModelTarget{}, targets...)) {
		key := endpointKey(target.Vendor, target.BaseURL)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], target)
	}
	// uniqueTargets drops empty-name ollama rows; still probe those URLs.
	for _, target := range uniqueEndpoints(targets) {
		key := endpointKey(target.Vendor, target.BaseURL)
		if _, ok := groups[key]; ok {
			continue
		}
		order = append(order, key)
		groups[key] = []ModelTarget{target}
	}

	for _, key := range order {
		group := groups[key]
		r.refreshOne(ctx, group[0].Vendor, group[0].BaseURL, pingModel(targets, group[0].Vendor, group[0].BaseURL), maxAge)
	}
}

func (r *Router) refreshOne(ctx context.Context, vendor, baseURL, model string, maxAge time.Duration) {
	key := endpointKey(vendor, baseURL)

	r.mu.Lock()
	if maxAge > 0 {
		if p, ok := r.probes[key]; ok && r.nowTime().Sub(p.CheckedAt) < maxAge {
			r.mu.Unlock()
			return
		}
	}
	if wait, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		}
		return
	}
	wait := make(chan struct{})
	r.inflight[key] = wait
	r.mu.Unlock()

	probe := r.runProbe(ctx, vendor, baseURL, model)

	r.mu.Lock()
	r.probes[key] = probe
	delete(r.inflight, key)
	close(wait)
	r.mu.Unlock()
}

func (r *Router) runProbe(ctx context.Context, vendor, baseURL, model string) EndpointProbe {
	probe := EndpointProbe{
		Vendor:    NormalizeVendor(vendor),
		BaseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		CheckedAt: r.nowTime(),
	}

	rt, err := r.vendor(vendor)
	if err != nil {
		probe.CatalogErr = err
		probe.ChatErr = err
		return probe
	}

	names, listErr := rt.ListModels(ctx, probe.BaseURL)
	if listErr != nil {
		probe.CatalogErr = listErr
	} else {
		probe.Catalog = names
	}

	pingCtx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	_, chatErr := rt.Complete(pingCtx, CompleteRequest{
		Vendor:       probe.Vendor,
		BaseURL:      probe.BaseURL,
		SystemPrompt: pingSystem,
		Prompt:       pingUser,
		Model:        model,
	})
	if chatErr != nil {
		probe.ChatErr = chatErr
		return probe
	}
	probe.ChatOK = true
	return probe
}

func (r *Router) ModelAvailable(_ context.Context, target ModelTarget) (bool, error) {
	p, ok := r.Probe(target.Vendor, target.BaseURL)
	if !ok {
		return false, nil
	}
	if !p.ChatOK {
		if p.ChatErr != nil {
			return false, p.ChatErr
		}
		return false, nil
	}
	return true, nil
}

func WarningCatalogUnavailable() string {
	return "catalog unavailable"
}

func WarningModelNotListed(model string) string {
	return fmt.Sprintf("model %q is not listed by endpoint", model)
}
