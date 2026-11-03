package configmgmt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

// Resolve turns a validated manifest into a runtime snapshot using rank-0 bindings.
func Resolve(m Manifest) (Snapshot, error) {
	endpointsByID := make(map[string]Endpoint, len(m.InferenceEndpoint))
	for _, endpoint := range m.InferenceEndpoint {
		endpointsByID[strings.TrimSpace(endpoint.ID)] = endpoint
	}

	if err := Validate(m); err != nil {
		return Snapshot{}, err
	}
	known := make([]string, 0, len(m.CapabilityModels))
	for name := range m.CapabilityModels {
		known = append(known, name)
	}
	sort.Strings(known)
	bindings := make(map[string]ModelBinding, len(known))
	for _, capName := range known {
		refs := m.CapabilityModels[capName]
		var primary *RankedModelRef
		for i := range refs {
			if refs[i].Rank == 0 {
				primary = &refs[i]
				break
			}
		}
		if primary == nil {
			return Snapshot{}, fmt.Errorf("capability %s has no rank 0 model", capName)
		}
		endpoint, ok := endpointsByID[strings.TrimSpace(primary.Model)]
		if !ok {
			return Snapshot{}, fmt.Errorf("capability %s references unknown model %q", capName, primary.Model)
		}
		vendor, err := inference.ParseVendor(endpoint.Vendor)
		if err != nil {
			return Snapshot{}, fmt.Errorf("capability %s: %w", capName, err)
		}
		keepAlive := ""
		if endpoint.KeepAliveSeconds > 0 {
			keepAlive = fmt.Sprintf("%ds", endpoint.KeepAliveSeconds)
		}
		bindings[capName] = ModelBinding{
			Vendor:          vendor,
			BaseURL:         strings.TrimRight(strings.TrimSpace(endpoint.URL), "/"),
			Model:           strings.TrimSpace(endpoint.Model),
			KeepAlive:       keepAlive,
			MaxInputChars:   primary.MaxInputChars,
			SystemPrompt:    strings.TrimSpace(primary.SystemPrompt),
			InputField:      primary.InputField,
			PromptVariables: primary.PromptVariables,
			OutputSchema:    primary.OutputSchema,
		}
	}
	// The registry copies schema trees and variable maps; snapshots own their data.
	registry, err := capability.NewRegistryFromBindings(bindings)
	if err != nil {
		return Snapshot{}, err
	}
	for _, def := range registry.All() {
		bindings[def.Name] = def.ModelBinding
	}
	tools := make(map[string]Tool, len(m.Tools))
	for _, configured := range m.Tools {
		configured.Name = strings.TrimSpace(configured.Name)
		configured.Endpoint = strings.TrimSpace(configured.Endpoint)
		configured.Instruction = strings.TrimSpace(configured.Instruction)
		tools[configured.Name] = configured
	}

	maxSystemPromptChars := m.Config.MaxSystemPromptChars
	if maxSystemPromptChars <= 0 {
		maxSystemPromptChars = capability.DefaultMaxSystemPromptChars
	}
	overrideOrgs := make([]string, 0, len(m.Config.SystemPromptOverrideOrgs))
	for _, orgID := range m.Config.SystemPromptOverrideOrgs {
		orgID = strings.TrimSpace(orgID)
		if orgID == "" {
			continue
		}
		overrideOrgs = append(overrideOrgs, orgID)
	}
	sort.Strings(overrideOrgs)

	snap := Snapshot{
		RedisAddr:                strings.TrimSpace(m.Ingress.Address),
		InputQueue:               strings.TrimSpace(m.Ingress.IngressChannel),
		OutputQueue:              strings.TrimSpace(m.Ingress.EgressChannel),
		BRPopTimeout:             m.Ingress.BRPopTimeoutSeconds,
		CloudEventTypePrefix:     strings.TrimSpace(m.Config.MessagePrefix),
		HTTPAddr:                 strings.TrimSpace(m.Config.HTTPAddress),
		PriorityHighCount:        m.Config.PriorityCountHigh,
		PriorityMediumCount:      m.Config.PriorityCountMedium,
		MaxSystemPromptChars:     maxSystemPromptChars,
		SystemPromptOverrideOrgs: overrideOrgs,
		Bindings:                 bindings,
		Tools:                    tools,
		LokiEndpoint:             strings.TrimRight(strings.TrimSpace(m.Config.LokiEndpoint), "/"),
	}
	snap.Fingerprint = fingerprint(snap)
	return snap, nil
}

func fingerprint(s Snapshot) string {
	parts := []string{
		s.RedisAddr,
		s.InputQueue,
		s.OutputQueue,
		fmt.Sprintf("%d", s.BRPopTimeout),
		s.CloudEventTypePrefix,
		s.HTTPAddr,
		fmt.Sprintf("%d", s.PriorityHighCount),
		fmt.Sprintf("%d", s.PriorityMediumCount),
		fmt.Sprintf("%d", s.MaxSystemPromptChars),
		strings.Join(s.SystemPromptOverrideOrgs, ","),
		s.LokiEndpoint,
	}
	keys := make([]string, 0, len(s.Bindings))
	for k := range s.Bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := s.Bindings[k]
		encoded, _ := json.Marshal(b)
		parts = append(parts, k, string(encoded))
	}
	toolNames := make([]string, 0, len(s.Tools))
	for name := range s.Tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	for _, name := range toolNames {
		t := s.Tools[name]
		parts = append(parts, t.Name, t.Endpoint, t.Instruction)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}
