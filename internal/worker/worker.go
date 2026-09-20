package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
	tooldef "github.com/mywebsite/construction-ai-gateway/internal/tool"
)

type RequestConsumer interface {
	Consume(ctx context.Context) (*cloudevent.Event, error)
}

type ResponsePublisher interface {
	Publish(ctx context.Context, event *cloudevent.Event) error
}

type ChatCompleter interface {
	Complete(ctx context.Context, req inference.CompleteRequest) (string, error)
}

type CapabilityResolver interface {
	Get(name string) (capability.Definition, error)
}

type OverridePolicySource interface {
	Get() capability.OverridePolicy
}

type ToolResolver interface {
	Get(name string) (tooldef.Definition, error)
}

type Worker struct {
	consumer       RequestConsumer
	publisher      ResponsePublisher
	ollama         ChatCompleter
	registry       CapabilityResolver
	overridePolicy OverridePolicySource
	logger         *slog.Logger
	tools          ToolResolver
	httpClient     *http.Client
}

func NewWithTools(consumer RequestConsumer, publisher ResponsePublisher, llm ChatCompleter, registry CapabilityResolver, overridePolicy OverridePolicySource, tools ToolResolver, logger *slog.Logger) *Worker {
	w := New(consumer, publisher, llm, registry, overridePolicy, logger)
	w.tools = tools
	w.httpClient = &http.Client{Timeout: 120 * time.Second}
	return w
}

func New(
	consumer RequestConsumer,
	publisher ResponsePublisher,
	ollama ChatCompleter,
	registry CapabilityResolver,
	overridePolicy OverridePolicySource,
	logger *slog.Logger,
) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if overridePolicy == nil {
		overridePolicy = capability.NewOverridePolicyHolder()
	}
	return &Worker{
		consumer:       consumer,
		publisher:      publisher,
		ollama:         ollama,
		registry:       registry,
		overridePolicy: overridePolicy,
		logger:         logger,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		event, err := w.consumer.Consume(ctx)
		if err != nil {
			return err
		}
		if event == nil {
			continue
		}

		w.logger.Info("incoming traffic",
			"request_id", event.ID,
			"sender", event.Source,
		)
		w.logger.Debug("incoming traffic payload",
			"request_id", event.ID,
			"sender", event.Source,
			"payload", event.ToMap(),
		)

		if err := w.handle(ctx, event); err != nil {
			w.logger.Error("failed to handle event", "event_id", event.ID, "error", err)
		}
	}
}

func (w *Worker) handle(ctx context.Context, event *cloudevent.Event) error {
	capabilityName, input, err := parseRequest(event)
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}

	if priority := stringValue(event.Data["priority"]); priority != "" {
		w.logger.Info("request priority",
			"request_id", event.ID,
			"capability", capabilityName,
			"priority", priority,
		)
	}

	def, err := w.registry.Get(capabilityName)
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}

	if err := capability.ValidateInputBounds(def, input); err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}

	overrideAuthorized := false
	if override := stringValue(input["system_prompt"]); override != "" {
		policy := w.overridePolicy.Get()
		orgID := ""
		if event.OrganisationID != nil {
			orgID = strings.TrimSpace(*event.OrganisationID)
		}
		if !policy.Allows(orgID) {
			return w.publishFailure(ctx, event, capabilityName, fmt.Errorf("data.input.system_prompt is not allowed for this organisation"))
		}
		if utf8.RuneCountInString(override) > policy.MaxSystemPromptChars {
			return w.publishFailure(ctx, event, capabilityName, capability.PromptBoundsError{MaxCharacters: policy.MaxSystemPromptChars})
		}
		overrideAuthorized = true
	}

	systemPrompt, userPrompt, err := capability.BuildPrompts(def, input)
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}
	toolName := stringValue(event.Data["tool"])
	if toolName != "" {
		return w.handleTool(ctx, event, capabilityName, def, systemPrompt, userPrompt, toolName)
	}

	raw, err := w.ollama.Complete(ctx, inference.CompleteRequest{
		Vendor:       def.Vendor,
		BaseURL:      def.BaseURL,
		SystemPrompt: systemPrompt,
		Prompt:       userPrompt,
		Model:        def.Model,
		KeepAlive:    def.KeepAlive,
	})
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}

	var result map[string]any
	if overrideAuthorized {
		// Authorized caller-owned prompt ⇒ caller-owned response schema.
		result, err = capability.ParseRawJSON(raw)
	} else {
		result, err = capability.ParseResult(capabilityName, raw)
	}
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}

	response := cloudevent.NewResponse(event, cloudevent.EventTypeRequestCompleted, map[string]any{
		"capability": capabilityName,
		"result":     result,
	})
	return w.publish(ctx, event, response)
}

const toolProtocol = `Format the HTTP tool request as JSON only: {"body": <JSON value>}. Do not include markdown.`
const toolDecisionProtocol = `The HTTP tool returned an error. Respond as JSON only. To retry use {"action":"retry","body":<JSON value>}; to stop use {"action":"stop"}. Do not include markdown.`

type toolDecision struct {
	Action string `json:"action"`
	Body   any    `json:"body"`
}

func (w *Worker) handleTool(ctx context.Context, event *cloudevent.Event, capabilityName string, def capability.Definition, systemPrompt, userPrompt, toolName string) error {
	if w.tools == nil {
		return w.publishFailure(ctx, event, capabilityName, fmt.Errorf("tools are not configured"))
	}
	selected, err := w.tools.Get(toolName)
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}
	raw, err := w.ollama.Complete(ctx, inference.CompleteRequest{Vendor: def.Vendor, BaseURL: def.BaseURL, SystemPrompt: systemPrompt + "\n\n" + selected.Instruction + "\n\n" + toolProtocol, Prompt: userPrompt, Model: def.Model, KeepAlive: def.KeepAlive})
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, err)
	}
	decision, err := parseToolDecision(raw)
	if err != nil {
		return w.publishFailure(ctx, event, capabilityName, fmt.Errorf("invalid tool request from inference: %w", err))
	}
	for attempt := 0; attempt <= 3; attempt++ {
		status, responseBody, callErr := w.callTool(ctx, selected.Endpoint, decision.Body)
		if callErr != nil {
			return w.publishFailure(ctx, event, capabilityName, callErr)
		}
		if status >= 200 && status < 300 {
			return w.publish(ctx, event, cloudevent.NewResponse(event, cloudevent.EventTypeRequestCompleted, map[string]any{"capability": capabilityName, "result": decodeToolBody(responseBody)}))
		}
		if attempt == 3 {
			return w.publishToolFailure(ctx, event, capabilityName, status, responseBody)
		}
		feedback := fmt.Sprintf("Tool request body: %s\nTool HTTP status: %d\nTool response: %s", mustJSON(decision.Body), status, responseBody)
		raw, err = w.ollama.Complete(ctx, inference.CompleteRequest{Vendor: def.Vendor, BaseURL: def.BaseURL, SystemPrompt: systemPrompt + "\n\n" + selected.Instruction + "\n\n" + toolDecisionProtocol, Prompt: feedback, Model: def.Model, KeepAlive: def.KeepAlive})
		if err != nil {
			return w.publishFailure(ctx, event, capabilityName, err)
		}
		decision, err = parseToolDecision(raw)
		if err != nil {
			return w.publishFailure(ctx, event, capabilityName, fmt.Errorf("invalid tool decision from inference: %w", err))
		}
		if strings.EqualFold(decision.Action, "stop") {
			return w.publishToolFailure(ctx, event, capabilityName, status, responseBody)
		}
		if !strings.EqualFold(decision.Action, "retry") {
			return w.publishFailure(ctx, event, capabilityName, fmt.Errorf("inference returned unknown tool action %q", decision.Action))
		}
	}
	return nil
}

func (w *Worker) callTool(ctx context.Context, endpoint string, body any) (int, string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, "", fmt.Errorf("encode tool request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, "", fmt.Errorf("create tool request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := w.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("call tool: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, "", fmt.Errorf("read tool response: %w", err)
	}
	return response.StatusCode, string(raw), nil
}

func parseToolDecision(raw string) (toolDecision, error) {
	var result toolDecision
	err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &result)
	return result, err
}
func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func decodeToolBody(raw string) any {
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		return value
	}
	return raw
}
func (w *Worker) publishToolFailure(ctx context.Context, event *cloudevent.Event, capabilityName string, status int, body string) error {
	response := cloudevent.NewResponse(event, cloudevent.EventTypeRequestFailed, map[string]any{"capability": capabilityName, "error": map[string]any{"status": status, "body": decodeToolBody(body)}})
	return w.publish(ctx, event, response)
}

func (w *Worker) publishFailure(ctx context.Context, event *cloudevent.Event, capabilityName string, cause error) error {
	var errorField any
	if boundsErr, ok := cause.(capability.PromptBoundsError); ok {
		errorField = boundsErr.ToMap()
	} else {
		errorField = cause.Error()
	}
	data := map[string]any{
		"error": errorField,
	}
	if strings.TrimSpace(capabilityName) != "" {
		data["capability"] = capabilityName
	}
	response := cloudevent.NewResponse(event, cloudevent.EventTypeRequestFailed, data)
	return w.publish(ctx, event, response)
}

// responseData builds response payload fields expected by construction PHP consumers,
// including echoed callback context used for handler dispatch.
func responseData(request *cloudevent.Event, data map[string]any) map[string]any {
	if callback, ok := request.Data["callback"]; ok {
		data["callback"] = callback
	}
	return data
}

func (w *Worker) publish(ctx context.Context, request *cloudevent.Event, response *cloudevent.Event) error {
	if err := w.publisher.Publish(ctx, response); err != nil {
		return err
	}
	w.logger.Info("outgoing traffic",
		"request_id", request.ID,
		"sender", request.Source,
	)
	w.logger.Debug("outgoing traffic payload",
		"request_id", request.ID,
		"sender", request.Source,
		"payload", response.ToMap(),
	)
	return nil
}

func parseRequest(event *cloudevent.Event) (capabilityName string, input map[string]any, err error) {
	if event.Type != cloudevent.EventTypeRequest {
		return "", nil, fmt.Errorf("unsupported event type: %s", event.Type)
	}
	if _, hasModel := event.Data["model"]; hasModel {
		return stringValue(event.Data["capability"]), nil, fmt.Errorf("data.model is not allowed; models are selected by the AI gateway")
	}

	capabilityName = stringValue(event.Data["capability"])
	if capabilityName == "" {
		return "", nil, fmt.Errorf("data.capability is required")
	}

	input, _ = event.Data["input"].(map[string]any)
	if input == nil {
		return capabilityName, nil, fmt.Errorf("data.input is required")
	}
	return capabilityName, input, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
	default:
		return ""
	}
}
