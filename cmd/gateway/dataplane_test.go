package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
	"github.com/mywebsite/construction-ai-gateway/internal/ollama"
)

func TestApplyEnsureModelsFailureLeavesLivePlaneUntouched(t *testing.T) {
	t.Cleanup(func() { cloudevent.ConfigureTypes(cloudevent.DefaultTypePrefix) })

	mr := miniredis.RunT(t)
	goodLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"good:latest"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(goodLLM.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	holder := capability.NewHolder()
	plane := &dataPlane{
		parent:         ctx,
		registry:       holder,
		overridePolicy: capability.NewOverridePolicyHolder(),
		llmPool:        ollama.NewPool(),
		logger:         slog.Default(),
	}
	t.Cleanup(plane.Stop)

	goodSnap := testSnapshot(mr.Addr(), goodLLM.URL, "good:latest", "fp-good", "com.mywebsite.ai")
	if err := plane.Apply(goodSnap, true); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if !holder.Ready() {
		t.Fatal("registry not ready after first Apply")
	}
	before, err := holder.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get(routing) after first Apply: %v", err)
	}
	if before.Model != "good:latest" {
		t.Fatalf("routing model = %q, want good:latest", before.Model)
	}
	if plane.redis == nil {
		t.Fatal("redis client nil after first Apply")
	}
	if plane.InferenceQueue() == nil {
		t.Fatal("inference queue nil after first Apply")
	}
	oldRedis := plane.redis
	oldQueue := plane.queue
	oldPrefix := cloudevent.EventTypeRequest

	badSnap := testSnapshot(mr.Addr(), "http://127.0.0.1:1", "bad:latest", "fp-bad", "com.example.bad")
	if err := plane.Apply(badSnap, false); err == nil {
		t.Fatal("second Apply() expected EnsureModels failure")
	}

	after, err := holder.Get(capability.Routing)
	if err != nil {
		t.Fatalf("Get(routing) after failed Apply: %v", err)
	}
	if after.Model != "good:latest" {
		t.Fatalf("routing model after failed Apply = %q, want good:latest", after.Model)
	}
	if plane.redis != oldRedis {
		t.Fatal("redis client changed after failed Apply")
	}
	if plane.queue != oldQueue {
		t.Fatal("inference queue changed after failed Apply")
	}
	if plane.snap == nil || plane.snap.Fingerprint != "fp-good" {
		t.Fatalf("snap fingerprint = %#v, want fp-good", plane.snap)
	}
	if cloudevent.EventTypeRequest != oldPrefix {
		t.Fatalf("EventTypeRequest = %q, want %q (ConfigureTypes must not run on failed Apply)", cloudevent.EventTypeRequest, oldPrefix)
	}
}

func TestApplyAbortsWhenParentCancelled(t *testing.T) {
	t.Cleanup(func() { cloudevent.ConfigureTypes(cloudevent.DefaultTypePrefix) })

	mr := miniredis.RunT(t)
	goodLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"good:latest"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(goodLLM.Close)

	ctx, cancel := context.WithCancel(context.Background())
	holder := capability.NewHolder()
	plane := &dataPlane{
		parent:         ctx,
		registry:       holder,
		overridePolicy: capability.NewOverridePolicyHolder(),
		llmPool:        ollama.NewPool(),
		logger:         slog.Default(),
	}
	t.Cleanup(plane.Stop)

	cancel()
	snap := testSnapshot(mr.Addr(), goodLLM.URL, "good:latest", "fp-cancel", "com.mywebsite.ai")
	if err := plane.Apply(snap, true); err == nil {
		t.Fatal("Apply() expected abort when parent cancelled")
	}
	if holder.Ready() {
		t.Fatal("registry should stay empty when Apply aborts before swap")
	}
	if plane.InferenceQueue() != nil {
		t.Fatal("inference queue should stay nil when Apply aborts")
	}
}

func testSnapshot(redisAddr, llmURL, model, fingerprint, prefix string) configmgmt.Snapshot {
	binding := configmgmt.ModelBinding{
		BaseURL:       llmURL,
		Model:         model,
		KeepAlive:     "5m",
		MaxInputChars: 1000,
	}
	return configmgmt.Snapshot{
		Fingerprint:          fingerprint,
		RedisAddr:            redisAddr,
		InputQueue:           "ai.requests",
		OutputQueue:          "ai.responses",
		BRPopTimeout:         1,
		CloudEventTypePrefix: prefix,
		HTTPAddr:             defaultHTTPAddr,
		PriorityHighCount:    3,
		PriorityMediumCount:  3,
		Bindings: map[string]configmgmt.ModelBinding{
			capability.Routing:              binding,
			capability.IntentClassification: binding,
			capability.Translate:            binding,
		},
	}
}
