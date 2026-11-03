package capability

import (
	"strings"
	"sync"
)

// DefaultMaxSystemPromptChars is used when a manifest omits or sets a non-positive cap.
const DefaultMaxSystemPromptChars = 4000

// OverridePolicy controls which organisations may supply data.input.system_prompt.
type OverridePolicy struct {
	AllowedOrgs          map[string]struct{}
	MaxSystemPromptChars int
}

// Allows reports whether orgID may override the manifest-configured system prompt.
func (p OverridePolicy) Allows(orgID string) bool {
	if p.AllowedOrgs == nil {
		return false
	}
	_, ok := p.AllowedOrgs[strings.TrimSpace(orgID)]
	return ok
}

// OverridePolicyHolder is a thread-safe policy pointer used while manifests are reloaded.
type OverridePolicyHolder struct {
	mu     sync.RWMutex
	policy OverridePolicy
}

func NewOverridePolicyHolder() *OverridePolicyHolder {
	return &OverridePolicyHolder{
		policy: OverridePolicy{
			AllowedOrgs:          map[string]struct{}{},
			MaxSystemPromptChars: DefaultMaxSystemPromptChars,
		},
	}
}

func (h *OverridePolicyHolder) Store(policy OverridePolicy) {
	h.mu.Lock()
	h.policy = cloneOverridePolicy(policy)
	h.mu.Unlock()
}

func (h *OverridePolicyHolder) Get() OverridePolicy {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneOverridePolicy(h.policy)
}

// PolicyFromOrgs builds an OverridePolicy from a resolved org allowlist and character cap.
func PolicyFromOrgs(orgs []string, maxChars int) OverridePolicy {
	allowed := make(map[string]struct{}, len(orgs))
	for _, orgID := range orgs {
		orgID = strings.TrimSpace(orgID)
		if orgID == "" {
			continue
		}
		allowed[orgID] = struct{}{}
	}
	if maxChars <= 0 {
		maxChars = DefaultMaxSystemPromptChars
	}
	return OverridePolicy{
		AllowedOrgs:          allowed,
		MaxSystemPromptChars: maxChars,
	}
}

func cloneOverridePolicy(policy OverridePolicy) OverridePolicy {
	copied := make(map[string]struct{}, len(policy.AllowedOrgs))
	for orgID := range policy.AllowedOrgs {
		orgID = strings.TrimSpace(orgID)
		if orgID == "" {
			continue
		}
		copied[orgID] = struct{}{}
	}
	maxChars := policy.MaxSystemPromptChars
	if maxChars <= 0 {
		maxChars = DefaultMaxSystemPromptChars
	}
	return OverridePolicy{
		AllowedOrgs:          copied,
		MaxSystemPromptChars: maxChars,
	}
}
