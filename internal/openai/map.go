package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
)

func eventFromRequest(req ChatCompletionRequest, organisationID string, registry *capability.Registry) (*cloudevent.Event, *requestError) {
	if req.Stream != nil && *req.Stream {
		return nil, newRequestError(http.StatusBadRequest, "stream is not supported", "invalid_request_error", "stream", "stream_not_supported")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, newRequestError(http.StatusBadRequest, "model is required", "invalid_request_error", "model", "missing_model")
	}
	def, lookupErr := registry.Get(model)
	if lookupErr != nil {
		return nil, newRequestError(http.StatusBadRequest, fmt.Sprintf("unknown model: %s", model), "invalid_request_error", "model", "unknown_model")
	}

	userContent, systemPrompt, err := extractMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	input := map[string]any{}
	if systemPrompt != "" {
		input["system_prompt"] = systemPrompt
	}
	input[def.InputField] = userContent
	for name := range def.PromptVariables {
		value := ""
		switch name {
		case "source_locale":
			value = req.SourceLocale
		case "target_locale":
			value = req.TargetLocale
		}
		if value = strings.TrimSpace(value); value != "" {
			input[name] = value
		}
	}

	if err := capability.ValidatePromptVariables(def, input); err != nil {
		var variableErr *capability.PromptVariableError
		param := ""
		if errors.As(err, &variableErr) {
			param = variableErr.Variable
		}
		return nil, newRequestError(http.StatusBadRequest, err.Error(), "invalid_request_error", param, "invalid_prompt_variable")
	}

	data := map[string]any{
		"capability": model,
		"input":      input,
	}
	if prio := strings.TrimSpace(req.Priority); prio != "" {
		data["priority"] = prio
	}
	if tool := strings.TrimSpace(req.Tool); tool != "" {
		data["tool"] = tool
	}

	event := &cloudevent.Event{
		Type:            cloudevent.EventTypeRequest,
		Source:          Source,
		ID:              "chatcmpl-" + cloudevent.NewID(),
		Time:            time.Now().UTC(),
		DataContentType: cloudevent.DataContentTypeJSON,
		Data:            data,
	}
	if org := strings.TrimSpace(organisationID); org != "" {
		event.OrganisationID = &org
	}
	return event, nil
}

func extractMessages(messages []ChatMessage) (userContent, systemPrompt string, err *requestError) {
	if len(messages) == 0 {
		return "", "", newRequestError(http.StatusBadRequest, "messages is required", "invalid_request_error", "messages", "missing_messages")
	}

	var lastUser string
	var hasUser bool
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := strings.TrimSpace(msg.Content)
		switch role {
		case "user":
			lastUser = content
			hasUser = true
		case "system":
			if systemPrompt == "" && content != "" {
				systemPrompt = content
			}
		}
	}
	if !hasUser {
		return "", "", newRequestError(http.StatusBadRequest, "messages must include a user message", "invalid_request_error", "messages", "missing_user")
	}
	if lastUser == "" {
		return "", "", newRequestError(http.StatusBadRequest, "user message content must not be blank", "invalid_request_error", "messages", "blank_user")
	}
	return lastUser, systemPrompt, nil
}

func completionFromEvent(event *cloudevent.Event) ChatCompletionResponse {
	capabilityName := stringValue(event.Data["capability"])
	id := event.ID
	if event.Subject != nil && strings.TrimSpace(*event.Subject) != "" {
		id = strings.TrimSpace(*event.Subject)
	}

	content := "{}"
	if result, ok := event.Data["result"]; ok && result != nil {
		encoded, err := json.Marshal(result)
		if err == nil {
			content = string(encoded)
		}
	}

	created := event.Time.UTC().Unix()
	if created <= 0 {
		created = time.Now().UTC().Unix()
	}

	return ChatCompletionResponse{
		ID:      id,
		Object:  ObjectChatCompletion,
		Created: created,
		Model:   capabilityName,
		Choices: []Choice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: FinishStop,
		}},
	}
}

func modelList(registry *capability.Registry) ModelList {
	known := registry.All()
	data := make([]ModelCard, 0, len(known))
	for _, def := range known {
		data = append(data, ModelCard{
			ID:      def.Name,
			Object:  ObjectModel,
			Created: 0,
			OwnedBy: OwnedBy,
		})
	}
	return ModelList{Object: ObjectList, Data: data}
}

func stringValue(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
