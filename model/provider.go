package model

type LLMResponse struct {
	Content      string         `json:"content"`
	Reasoning    *string        `json:"reasoning,omitempty"`
	ToolCalls    []any          `json:"tool_calls"`
	Usage        map[string]any `json:"usage"`
	FinishReason *string        `json:"reasoning,omitempty"`
	Raw          map[string]any `json:"raw"`
}

func NewLLMResponse(content string) *LLMResponse {
	return &LLMResponse{
		Content:   content,
		ToolCalls: []any{},
		Usage:     map[string]any{},
		Raw:       map[string]any{},
	}
}

type Provider struct {
	BaseUrl string `json:"base_url"`
	Model   string `json:"model"`
	ApiKey  string
}

func NewProvider(baseUrl string, model string, apiKey string) *Provider {
	return &Provider{
		BaseUrl: baseUrl,
		Model:   model,
		ApiKey:  apiKey,
	}
}

func NewGeminiProvider(model string, apiKey string) *Provider {
	return NewProvider("https://generativelanguage.googleapis.com/v1beta/openai", model, apiKey)
}
