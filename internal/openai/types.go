package openai

const (
	HeaderOrganisationID = "X-Organisation-Id"
	ObjectChatCompletion = "chat.completion"
	ObjectList           = "list"
	ObjectModel          = "model"
	OwnedBy              = "inference-gateway"
	FinishStop           = "stop"
	Source               = "/openai"
)

type ChatCompletionRequest struct {
	Model        string        `json:"model"`
	Messages     []ChatMessage `json:"messages"`
	Stream       *bool         `json:"stream,omitempty"`
	Priority     string        `json:"priority,omitempty"`
	SourceLocale string        `json:"source_locale,omitempty"`
	TargetLocale string        `json:"target_locale,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ModelList struct {
	Object string      `json:"object"`
	Data   []ModelCard `json:"data"`
}

type ModelCard struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}
