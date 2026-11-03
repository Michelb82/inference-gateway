package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
	"github.com/mywebsite/construction-ai-gateway/internal/health"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
	"github.com/mywebsite/construction-ai-gateway/internal/openai"
)

func TestDynamicCapabilityManifestHTTPRedisAndReload(t *testing.T) {
	t.Cleanup(func() { cloudevent.ConfigureTypes(cloudevent.DefaultTypePrefix) })
	mr := miniredis.RunT(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"test"}]}`))
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"summary":"appointment next Tuesday","extra":true}`}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	holder := capability.NewHolder()
	router := inference.NewRouter("")
	plane := &dataPlane{parent: ctx, registry: holder, overridePolicy: capability.NewOverridePolicyHolder(), llmPool: router, logger: slog.Default()}
	t.Cleanup(plane.Stop)
	manifest := configmgmt.Manifest{
		InferenceEndpoint: []configmgmt.Endpoint{{ID: "endpoint", Vendor: "ollama", URL: provider.URL, Model: "test", KeepAliveSeconds: 60}},
		CapabilityModels:  map[string][]configmgmt.RankedModelRef{"appointment-summary": {{Rank: 0, Model: "endpoint", MaxInputChars: 200, SystemPrompt: "Summarize as JSON.", InputField: "message", OutputSchema: &capability.OutputSchema{Type: "object", Required: []string{"summary"}, Properties: map[string]capability.PropertySchema{"summary": {Type: "string"}}}}}},
		Ingress:           configmgmt.Ingress{Adapter: "redis", Address: mr.Addr(), IngressChannel: "ai.requests", EgressChannel: "ai.responses", BRPopTimeoutSeconds: 1},
		Config:            configmgmt.RuntimeConfig{MessagePrefix: "com.mywebsite.ai", HTTPAddress: defaultHTTPAddr, PriorityCountHigh: 3, PriorityCountMedium: 3},
	}
	apply := func() {
		t.Helper()
		snap, err := configmgmt.Resolve(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := plane.Apply(snap, false); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	mux := http.NewServeMux()
	openai.NewHandler(plane, nil).Register(mux)
	health.NewHandler(holder, router).Register(mux)
	request := func(name string) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"model": name, "messages": []map[string]string{{"role": "user", "content": "Move my appointment"}}, "priority": "LOW"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(data)))
		return rec
	}
	checkListing := func(count int) {
		t.Helper()
		for _, path := range []string{"/v1/models", "/health.json"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
			}
			var body struct {
				Data         []json.RawMessage `json:"data"`
				Capabilities []json.RawMessage `json:"capabilities"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Data)+len(body.Capabilities) != count {
				t.Fatalf("%s unexpected listing: %s", path, rec.Body.String())
			}
		}
	}
	checkListing(1)
	rec := request("appointment-summary")
	if rec.Code != 200 {
		t.Fatalf("completion: %d %s", rec.Code, rec.Body.String())
	}
	var completion openai.ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &completion); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &result); err != nil {
		t.Fatal(err)
	}
	if result["summary"] != "appointment next Tuesday" || result["extra"] != true {
		t.Fatalf("result = %+v", result)
	}
	manifest.CapabilityModels["another-custom"] = append([]configmgmt.RankedModelRef(nil), manifest.CapabilityModels["appointment-summary"]...)
	oldQueue := plane.InferenceQueue()
	apply()
	checkListing(2)
	if plane.InferenceQueue() != oldQueue {
		t.Fatal("capability-only reload replaced queue")
	}
	if rec := request("another-custom"); rec.Code != 200 {
		t.Fatalf("added capability: %d %s", rec.Code, rec.Body.String())
	}
	snap, err := configmgmt.Resolve(manifest)
	if err != nil {
		t.Fatal(err)
	}
	b := snap.Bindings["another-custom"]
	b.MaxInputChars = 199
	snap.Bindings["another-custom"] = b
	if err := plane.Apply(snap, false); err == nil {
		t.Fatal("accepted invalid registry")
	}
	checkListing(2)
	delete(manifest.CapabilityModels, "appointment-summary")
	apply()
	checkListing(1)
	if rec := request("appointment-summary"); rec.Code != 400 {
		t.Fatalf("removed capability: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request("another-custom"); rec.Code != 200 {
		t.Fatalf("remaining capability: %d %s", rec.Code, rec.Body.String())
	}
}
