package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CompleteOptions struct {
	Model       string
	Tools       []any
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

func completeOpenai(
	provider Provider,
	messages []map[string]any,
	options *CompleteOptions,
) (*LLMResponse, error) {
	opts := CompleteOptions{
		Temperature: 0,
		MaxTokens:   1024,
		Timeout:     180 * time.Second,
	}

	if options != nil {
		opts = *options
	}
	if opts.Timeout == 0 {
		opts.Timeout = 180 * time.Second
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 1024
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

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("create completeOpenai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+provider.ApiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)

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
		Content:      content,
		Reasoning:    choice.Message.ReasoningContent,
		ToolCalls:    toolCalls,
		Usage:        data.Usage,
		FinishReason: choice.FinishReason,
		Raw:          raw,
	}, nil
}
