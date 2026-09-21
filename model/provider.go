package model

type LLMResponse struct {
	CONTENT       string         `json:"content"`
	REASONING     *string        `json:"reasoning,omitempty"`
	TOOL_CALLS    []any          `json:"tool_calls"`
	USAGE         map[string]any `json:"usage"`
	FINISH_REASON *string        `json:"reasoning,omitempty"`
	RAW           map[string]any `json:"raw"`
}

func NewLLMResponse(content string) *LLMResponse {
	return &LLMResponse{
		CONTENT:    content,
		TOOL_CALLS: []any{},
		USAGE:      map[string]any{},
		RAW:        map[string]any{},
	}
}

type Provider struct {
	BASE_URL string `json:"base_url"`
	MODEL    string `json:"model"`
	API_KEY  string
}

func NewProvider(baseUrl string, model string, apiKey string) *Provider {
	return &Provider{
		BASE_URL: baseUrl,
		MODEL:    model,
		API_KEY:  apiKey,
	}
}

func NewOpenRouterProvider(model string, apiKey string) *Provider {
	return NewProvider("https://openrouter.ai/api/v1", model, apiKey)
}
