package inference

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
)

// openaiAdapter talks to an OpenAI-compatible API (OpenAPI: POST /chat/completions,
// GET /models, Authorization: Bearer). It is only constructed when an API key
// was supplied via OPENAI_BEARER_TOKEN and a binding uses vendor openai.
type openaiAdapter struct {
	apiKey     string
	httpClient *http.Client
}

func newOpenAIAdapter(apiKey string) *openaiAdapter {
	return &openaiAdapter{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type openaiChatRequest struct {
	Model       string              `json:"model,omitempty"`
	Messages    []openaiChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
}

type openaiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *openaiAPIError `json:"error,omitempty"`
}

type openaiModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *openaiAPIError `json:"error,omitempty"`
}

type openaiAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func (e openaiAPIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "openai api error"
	}
	if e.Type != "" {
		return fmt.Sprintf("%s (%s)", msg, e.Type)
	}
	return msg
}

func (a *openaiAdapter) Complete(ctx context.Context, req CompleteRequest) (string, error) {
	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	prompt := strings.TrimSpace(req.Prompt)
	model := strings.TrimSpace(req.Model)
	if prompt == "" {
		return "", fmt.Errorf("prompt must not be empty")
	}
	if systemPrompt == "" {
		return "", fmt.Errorf("system prompt must not be empty")
	}

	body := openaiChatRequest{
		Model: model,
		Messages: []openaiChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode chat request: %w", err)
	}

	url := openaiAPIURL(req.BaseURL, "/chat/completions")
	slog.Debug("openai outgoing",
		"url", url,
		"body", string(encoded),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("create chat request: %w", err)
	}
	a.setAuth(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read chat response: %w", err)
	}

	slog.Debug("openai incoming",
		"url", url,
		"status", resp.StatusCode,
		"body", string(respBody),
	)

	var decoded openaiChatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("chat request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return "", fmt.Errorf("chat request failed: %w", decoded.Error)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("chat response contained no choices")
	}

	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("chat response contained empty content")
	}
	return content, nil
}

func (a *openaiAdapter) ListModels(ctx context.Context, baseURL string) ([]string, error) {
	return a.listModels(ctx, baseURL)
}

func (a *openaiAdapter) EnsureModels(_ context.Context, targets []ModelTarget) (available []ModelTarget, unavailable []ModelTarget, err error) {
	unique := uniqueTargets(targets)
	if len(unique) == 0 {
		return nil, nil, fmt.Errorf("no models configured")
	}
	return unique, nil, nil
}

func (a *openaiAdapter) listModels(ctx context.Context, baseURL string) ([]string, error) {
	url := openaiAPIURL(baseURL, "/models")
	slog.Debug("openai outgoing",
		"url", url,
		"method", http.MethodGet,
		"body", "",
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	a.setAuth(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai api unreachable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}

	slog.Debug("openai incoming",
		"url", url,
		"status", resp.StatusCode,
		"body", string(respBody),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var decoded openaiModelsResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, decoded.Error
	}

	names := make([]string, 0, len(decoded.Data))
	for _, model := range decoded.Data {
		name := strings.TrimSpace(model.ID)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (a *openaiAdapter) setAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
}

// openaiAPIURL joins a configured base URL with an OpenAPI path. Spec servers
// are `https://api.openai.com/v1`; callers may pass that or the origin without `/v1`.
func openaiAPIURL(baseURL, path string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = "/" + strings.TrimLeft(path, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}
