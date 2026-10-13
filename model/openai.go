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
	if len(opts.Tools) > 0 {
		payload["tools"] = options.Tools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("json marshal error at completeOpenai: %w", err)
	}

	url := strings.TrimRight(provider.BaseUrl, "/") + "/chat/completions"

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("create completeOpenai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+provider.ApiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := doCompletionRequest(client, req, opts.OnRetry, opts.OnRateLimitRemaining)

	if err != nil {
		return nil, fmt.Errorf("send completeOpenAi request: %w", err)
	}

	defer resp.Body.Close()

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
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("model provider rate limit exceeded (HTTP 429): %v; automatic retries stopped. Wait before continuing; if this persists, check your provider request/token limits and workspace usage limits", raw)
		}
		if retryableStatus(resp.StatusCode) {
			return nil, fmt.Errorf("model provider returned a temporary error (HTTP %s): %v; try again shortly", resp.Status, raw)
		}
		return nil, fmt.Errorf(
			"completion request failed with status %s: %v",
			resp.Status,
			raw,
		)
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
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
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
