package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
)

const (
	defaultWaitTimeout = 120 * time.Second
	maxBodyBytes       = 1 << 20
)

type Queue interface {
	Enqueue(ctx context.Context, event *cloudevent.Event) error
	Wait(ctx context.Context, requestID string, timeout time.Duration) (*cloudevent.Event, error)
}

type QueueSource interface {
	InferenceQueue() Queue
}

type Handler struct {
	source      QueueSource
	logger      *slog.Logger
	waitTimeout time.Duration
}

func NewHandler(source QueueSource, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		source:      source,
		logger:      logger,
		waitTimeout: defaultWaitTimeout,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/chat/completions", h.handleCompletions)
	mux.HandleFunc("/v1/models", h.handleModels)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "", "method_not_allowed")
		return
	}
	if h.queue() == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway is dormant", "server_error", "", "dormant")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelList())
}

func (h *Handler) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "", "method_not_allowed")
		return
	}

	q := h.queue()
	if q == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway is dormant", "server_error", "", "dormant")
		return
	}

	var req ChatCompletionRequest
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		msg := "invalid JSON body"
		if errors.Is(err, io.EOF) {
			msg = "request body is required"
		}
		writeError(w, http.StatusBadRequest, msg, "invalid_request_error", "", "invalid_json")
		return
	}

	event, reqErr := eventFromRequest(req, r.Header.Get(HeaderOrganisationID))
	if reqErr != nil {
		writeRequestError(w, reqErr)
		return
	}

	h.logger.Info("incoming traffic",
		"request_id", event.ID,
		"capability", event.Data["capability"],
	)
	h.logger.Debug("incoming traffic payload",
		"request_id", event.ID,
		"payload", event.ToMap(),
	)

	ctx, cancel := context.WithTimeout(r.Context(), h.waitTimeout)
	defer cancel()

	if err := q.Enqueue(ctx, event); err != nil {
		h.logger.Error("enqueue failed", "request_id", event.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to enqueue request", "server_error", "", "enqueue_failed")
		return
	}

	response, err := q.Wait(ctx, event.ID, h.waitTimeout)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if errors.Is(err, queue.ErrWaitTimeout) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "timed out waiting for inference", "timeout_error", "", "wait_timeout")
			return
		}
		h.logger.Error("wait failed", "request_id", event.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to wait for inference", "server_error", "", "wait_failed")
		return
	}

	h.logger.Info("outgoing traffic",
		"request_id", event.ID,
		"sender", event.Source,
	)
	h.logger.Debug("outgoing traffic payload",
		"request_id", event.ID,
		"payload", response.ToMap(),
	)

	if response.Type == cloudevent.EventTypeRequestFailed {
		status, message, errType, param, code := errorFromFailedEvent(response)
		writeError(w, status, message, errType, param, code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(completionFromEvent(response))
}

func (h *Handler) queue() Queue {
	if h.source == nil {
		return nil
	}
	return h.source.InferenceQueue()
}
