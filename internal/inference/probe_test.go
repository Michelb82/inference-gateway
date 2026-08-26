package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func openaiProbeServer(t *testing.T, lists, chats *atomic.Int32, catalog string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			lists.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(catalog))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			chats.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func ollamaProbeServer(t *testing.T, lists, chats *atomic.Int32, tags string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
			lists.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tags))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			chats.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestRefreshProbesListsAndPingsOncePerURL(t *testing.T) {
	var lists, chats atomic.Int32
	server := openaiProbeServer(t, &lists, &chats, `{"data":[{"id":"gpt-4o-mini"}]}`)
	defer server.Close()

	router := NewRouter("DEV_KEY")
	targets := []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"},
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"},
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: ""},
	}
	router.RefreshProbes(context.Background(), targets, 0)
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("lists=%d chats=%d, want 1 each", lists.Load(), chats.Load())
	}

	router.RefreshProbes(context.Background(), targets, ProbeTTL)
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("second refresh within TTL lists=%d chats=%d", lists.Load(), chats.Load())
	}

	probe, ok := router.Probe(VendorOpenAI, server.URL)
	if !ok || !probe.ChatOK {
		t.Fatalf("probe = %#v ok=%v", probe, ok)
	}
	if !probe.CatalogContains("gpt-4o-mini") {
		t.Fatalf("catalog = %#v", probe.Catalog)
	}
}

func TestRefreshProbesAfterTTL(t *testing.T) {
	var lists, chats atomic.Int32
	server := openaiProbeServer(t, &lists, &chats, `{"data":[{"id":"gpt-4o-mini"}]}`)
	defer server.Close()

	now := time.Unix(1_700_000_000, 0)
	router := NewRouter("DEV_KEY")
	router.SetNow(func() time.Time { return now })

	targets := []ModelTarget{{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"}}
	router.RefreshProbes(context.Background(), targets, 0)
	now = now.Add(ProbeTTL + time.Second)
	router.RefreshProbes(context.Background(), targets, ProbeTTL)
	if lists.Load() != 2 || chats.Load() != 2 {
		t.Fatalf("lists=%d chats=%d, want 2 each after TTL", lists.Load(), chats.Load())
	}
}

func TestRefreshProbesOllamaTagsAndChat(t *testing.T) {
	var lists, chats atomic.Int32
	server := ollamaProbeServer(t, &lists, &chats, `{"models":[{"name":"qwen3:1.7b"}]}`)
	defer server.Close()

	router := NewRouter("")
	router.RefreshProbes(context.Background(), []ModelTarget{
		{Vendor: VendorOllama, BaseURL: server.URL, Name: "qwen3:1.7b"},
		{Vendor: VendorOllama, BaseURL: server.URL, Name: "qwen3:4b"},
	}, 0)
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("lists=%d chats=%d, want 1 each", lists.Load(), chats.Load())
	}
}

func TestCompleteDoesNotListModels(t *testing.T) {
	var lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			lists.Add(1)
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	router := NewRouter("DEV_KEY")
	_, err := router.Complete(context.Background(), CompleteRequest{
		Vendor:       VendorOpenAI,
		BaseURL:      server.URL,
		SystemPrompt: "s",
		Prompt:       "p",
		Model:        "m",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if lists.Load() != 0 {
		t.Fatalf("Complete listed models %d times", lists.Load())
	}
}

func TestRefreshProbesChatFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
			return
		}
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	router := NewRouter("DEV_KEY")
	targets := []ModelTarget{{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"}}
	router.RefreshProbes(context.Background(), targets, 0)
	if router.AnyChatOK(targets) {
		t.Fatal("AnyChatOK() = true, want false")
	}
	probe, ok := router.Probe(VendorOpenAI, server.URL)
	if !ok || probe.ChatOK || probe.ChatErr == nil {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestPingUsesJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["model"]; ok {
			t.Fatalf("empty ping model should be omitted, got %v", body["model"])
		}
		msgs := body["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("messages = %#v", msgs)
		}
		sys := msgs[0].(map[string]any)
		usr := msgs[1].(map[string]any)
		if sys["role"] != "system" || sys["content"] != "ping" {
			t.Fatalf("system = %#v", sys)
		}
		if usr["role"] != "user" || usr["content"] != "pong" {
			t.Fatalf("user = %#v", usr)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	router := NewRouter("DEV_KEY")
	router.RefreshProbes(context.Background(), []ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: server.URL, Name: ""},
	}, 0)
	if !router.AnyChatOK([]ModelTarget{{Vendor: VendorOpenAI, BaseURL: server.URL}}) {
		t.Fatal("expected chat ok")
	}
}

func TestModelAvailableReadsCacheOnly(t *testing.T) {
	router := NewRouter("DEV_KEY")
	ok, err := router.ModelAvailable(context.Background(), ModelTarget{
		Vendor:  VendorOpenAI,
		BaseURL: "https://example",
		Name:    "m",
	})
	if err != nil || ok {
		t.Fatalf("empty cache ModelAvailable = %v %v", ok, err)
	}

	var lists, chats atomic.Int32
	server := openaiProbeServer(t, &lists, &chats, `{"data":[{"id":"gpt-4o-mini"}]}`)
	defer server.Close()
	target := ModelTarget{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"}
	router.RefreshProbes(context.Background(), []ModelTarget{target}, 0)
	if lists.Load() != 1 {
		t.Fatalf("lists = %d", lists.Load())
	}
	ok, err = router.ModelAvailable(context.Background(), target)
	if err != nil || !ok {
		t.Fatalf("cached ModelAvailable = %v %v", ok, err)
	}
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("ModelAvailable triggered HTTP lists=%d chats=%d", lists.Load(), chats.Load())
	}
}

func TestRefreshProbesSingleflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var lists, chats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			lists.Add(1)
			close(started)
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			chats.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	router := NewRouter("DEV_KEY")
	targets := []ModelTarget{{Vendor: VendorOpenAI, BaseURL: server.URL, Name: "gpt-4o-mini"}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.RefreshProbes(context.Background(), targets, 0)
	}()
	<-started
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		router.RefreshProbes(context.Background(), targets, 0)
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-done
	<-secondDone
	if lists.Load() != 1 || chats.Load() != 1 {
		t.Fatalf("lists=%d chats=%d, want 1 each (singleflight)", lists.Load(), chats.Load())
	}
}
