package logging

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLokiHandlerMirrorsRecordsAfterReadiness(t *testing.T) {
	var pushes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			w.WriteHeader(http.StatusOK)
		case "/loki/api/v1/push":
			pushes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	handler := NewLokiHandler(slog.NewJSONHandler(io.Discard, nil))
	if err := handler.Configure(context.Background(), server.URL); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	slog.New(handler).Info("hello", "request_id", "123")
	if pushes.Load() != 1 {
		t.Fatalf("pushes = %d, want 1", pushes.Load())
	}
}

func TestLokiHandlerKeepsLocalLoggingWhenReadinessFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	handler := NewLokiHandler(slog.NewJSONHandler(io.Discard, nil))
	if err := handler.Configure(context.Background(), server.URL); err == nil {
		t.Fatal("Configure() error = nil")
	}
	if err := handler.Handle(context.Background(), slog.Record{Message: "still local"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}
