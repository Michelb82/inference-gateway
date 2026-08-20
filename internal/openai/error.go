package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
)

type requestError struct {
	Status  int
	Message string
	Type    string
	Param   string
	Code    string
}

func (e *requestError) Error() string {
	return e.Message
}

func newRequestError(status int, message, errType, param, code string) *requestError {
	if errType == "" {
		errType = "invalid_request_error"
	}
	return &requestError{
		Status:  status,
		Message: message,
		Type:    errType,
		Param:   param,
		Code:    code,
	}
}

func writeError(w http.ResponseWriter, status int, message, errType, param, code string) {
	if errType == "" {
		switch {
		case status == http.StatusGatewayTimeout:
			errType = "timeout_error"
		case status >= 500:
			errType = "server_error"
		default:
			errType = "invalid_request_error"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error: ErrorBody{
			Message: message,
			Type:    errType,
			Param:   param,
			Code:    code,
		},
	})
}

func writeRequestError(w http.ResponseWriter, err *requestError) {
	writeError(w, err.Status, err.Message, err.Type, err.Param, err.Code)
}

func errorFromFailedEvent(event *cloudevent.Event) (status int, message, errType, param, code string) {
	status = http.StatusInternalServerError
	errType = "server_error"
	raw := event.Data["error"]
	switch typed := raw.(type) {
	case map[string]any:
		reason, _ := typed["reason"].(string)
		maxChars := typed["max_characters"]
		if strings.Contains(strings.ToLower(reason), "outside bounds") {
			status = http.StatusBadRequest
			errType = "invalid_request_error"
			code = "prompt_bounds"
			message = fmt.Sprintf("%s (max_characters=%v)", reason, maxChars)
			return status, message, errType, "", code
		}
		encoded, _ := json.Marshal(typed)
		message = string(encoded)
		return status, message, errType, "", code
	default:
		message = strings.TrimSpace(fmt.Sprint(raw))
	}
	if message == "" {
		message = "request failed"
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "system_prompt is not allowed"):
		return http.StatusForbidden, message, "invalid_request_error", "messages", "system_prompt_denied"
	case strings.Contains(lower, "model unavailable"):
		return http.StatusServiceUnavailable, message, "server_error", "model", "model_unavailable"
	case strings.Contains(lower, "unknown capability"):
		return http.StatusBadRequest, message, "invalid_request_error", "model", "unknown_model"
	case strings.Contains(lower, "data.model is not allowed"):
		return http.StatusBadRequest, message, "invalid_request_error", "model", "model_not_allowed"
	case strings.Contains(lower, "required"):
		return http.StatusBadRequest, message, "invalid_request_error", "", "invalid_request"
	default:
		return status, message, errType, param, code
	}
}
