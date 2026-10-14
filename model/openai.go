package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultMaxOutputTokens = 8192

type CompleteOptions struct {
	OnDelta              func(string)
	Context              context.Context
	BeforeRequest        func(int) error
	OnUsage              func(map[string]any)
	Model                string
	Tools                []any
	Temperature          float64
	MaxTokens            int
	Timeout              time.Duration
	OnRetry              func(status int, delay time.Duration)
	OnRateLimitRemaining func(int64)
}

func completeOpenai(
	provider Provider,
	messages []map[string]any,
	options *CompleteOptions,
) (*LLMResponse, error) {
	opts := CompleteOptions{
		Temperature: 0,
		MaxTokens:   DefaultMaxOutputTokens,
		Timeout:     180 * time.Second,
	}

	if options != nil {
		opts = *options
	}
	if opts.Timeout == 0 {
		opts.Timeout = 180 * time.Second
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = DefaultMaxOutputTokens
	}

	model := opts.Model
	if model == "" {
		model = provider.Model
	}

	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"max_tokens":  opts.MaxTokens,
	}
	if opts.OnDelta != nil {
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(opts.Tools) > 0 {
		payload["tools"] = options.Tools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("json marshal error at completeOpenai: %w", err)
	}

	url := strings.TrimRight(provider.BaseUrl, "/") + "/chat/completions"

	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("create completeOpenai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+provider.ApiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: opts.Timeout}
	before := func() error {
		if opts.BeforeRequest != nil {
			// Reserve a conservative byte-based prompt estimate plus maximum output,
			// including every HTTP retry, before issuing the request.
			return opts.BeforeRequest(len(body) + opts.MaxTokens)
		}
		return nil
	}
	resp, err := doCompletionRequest(client, req, opts.OnRetry, opts.OnRateLimitRemaining, before)

	if err != nil {
		return nil, fmt.Errorf("send completeOpenAi request: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readCompletionStream(resp, opts)
	}

	var data struct {
		Choices []struct {
			Message struct {
				Content          *string `json:"content"`
				ReasoningContent *string `json:"reasoning_content"`
				ToolCalls        []any   `json:"tool_calls"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read completeOpenai response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var raw any
		if err := json.Unmarshal(body, &raw); err != nil {
			raw = string(body)
		}
		return nil, &ProviderError{Status: resp.StatusCode, Detail: fmt.Sprint(raw)}
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode completeOpenai response: %w", err)
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse completion response: %w", err)
	}

	if len(data.Choices) == 0 {
		return nil, fmt.Errorf("completion response contains no choices")
	}

	choice := data.Choices[0]
	if opts.OnUsage != nil {
		opts.OnUsage(data.Usage)
	}
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
	}
	if opts.OnDelta != nil && content != "" {
		opts.OnDelta(content)
	}

	toolCalls := choice.Message.ToolCalls
	if toolCalls == nil {
		toolCalls = []any{}
	}
	if data.Usage == nil {
		data.Usage = map[string]any{}
	}

	return &LLMResponse{
		Content:            content,
		RateLimitRemaining: rateLimitRemaining(resp.Header),
		Reasoning:          choice.Message.ReasoningContent,
		ToolCalls:          toolCalls,
		Usage:              data.Usage,
		FinishReason:       choice.FinishReason,
		Raw:                raw,
	}, nil
}
