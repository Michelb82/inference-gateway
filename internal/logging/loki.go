package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type LokiHandler struct {
	local  slog.Handler
	state  *lokiState
	attrs  []slog.Attr
	groups []string
}

type lokiState struct {
	mu       sync.RWMutex
	endpoint string
	client   *http.Client
}

func NewLokiHandler(local slog.Handler) *LokiHandler {
	return &LokiHandler{local: local, state: &lokiState{client: &http.Client{Timeout: 2 * time.Second}}}
}

func (h *LokiHandler) Configure(ctx context.Context, endpoint string) error {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	h.state.mu.Lock()
	h.state.endpoint = ""
	h.state.mu.Unlock()
	if endpoint == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/ready", nil)
	if err != nil {
		return err
	}
	response, err := h.state.client.Do(req)
	if err != nil {
		return fmt.Errorf("loki readiness: %w", err)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("loki readiness returned HTTP %d", response.StatusCode)
	}
	h.state.mu.Lock()
	h.state.endpoint = endpoint
	h.state.mu.Unlock()
	return nil
}

func (h *LokiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.local.Enabled(ctx, level)
}

func (h *LokiHandler) Handle(ctx context.Context, record slog.Record) error {
	localErr := h.local.Handle(ctx, record)
	h.state.mu.RLock()
	endpoint := h.state.endpoint
	h.state.mu.RUnlock()
	if endpoint == "" {
		return localErr
	}
	fields := map[string]any{"time": record.Time.UTC().Format(time.RFC3339Nano), "level": record.Level.String(), "msg": record.Message}
	for _, attr := range h.attrs {
		addAttr(fields, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool { addAttr(fields, h.groups, attr); return true })
	line, err := json.Marshal(fields)
	if err != nil {
		return localErr
	}
	payload := map[string]any{"streams": []any{map[string]any{"stream": map[string]string{"service": "ai-gateway", "level": strings.ToLower(record.Level.String())}, "values": [][]string{{fmt.Sprintf("%d", record.Time.UnixNano()), string(line)}}}}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return localErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/loki/api/v1/push", bytes.NewReader(raw))
	if err != nil {
		return localErr
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := h.state.client.Do(req)
	if err == nil {
		response.Body.Close()
	}
	return localErr
}

func (h *LokiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.local = h.local.WithAttrs(attrs)
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}
func (h *LokiHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.local = h.local.WithGroup(name)
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}
func addAttr(fields map[string]any, groups []string, attr slog.Attr) {
	key := strings.Join(append(groups, attr.Key), ".")
	fields[key] = attr.Value.Any()
}
