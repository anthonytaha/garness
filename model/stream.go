package model

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Streamed tool arguments are assembled before returning a response. Neither
// partial responses nor interrupted streams can reach the tool executor.
func readCompletionStream(resp *http.Response, opts CompleteOptions) (*LLMResponse, error) {
	result := NewLLMResponse("")
	result.RateLimitRemaining = rateLimitRemaining(resp.Header)
	type streamedCall struct{ id, kind, name, arguments string }
	calls := map[int]*streamedCall{}
	var content, reasoning strings.Builder
	var data []string
	done := false
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			done = true
			return nil
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode completion stream: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return fmt.Errorf("completion stream error: %s", chunk.Error)
		}
		if chunk.Usage != nil {
			result.Usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			content.WriteString(choice.Delta.Content)
			reasoning.WriteString(choice.Delta.Reasoning)
			if opts.OnDelta != nil && choice.Delta.Content != "" {
				opts.OnDelta(choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				result.FinishReason = choice.FinishReason
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index < 0 || delta.Index > 1024 {
					return fmt.Errorf("invalid streamed tool index")
				}
				call := calls[delta.Index]
				if call == nil {
					call = &streamedCall{}
					calls[delta.Index] = call
				}
				call.id += delta.ID
				call.kind += delta.Type
				call.name += delta.Function.Name
				call.arguments += delta.Function.Arguments
			}
		}
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := consume(); err != nil {
				return nil, err
			}
			if done {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read completion stream: %w", err)
	}
	if !done {
		if err := consume(); err != nil {
			return nil, err
		}
	}
	if !done || result.FinishReason == nil {
		return nil, fmt.Errorf("completion stream interrupted before completion; partial tools were not executed")
	}
	indices := make([]int, 0, len(calls))
	for index := range calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := calls[index]
		if call.id == "" || call.name == "" || call.kind != "function" || !json.Valid([]byte(call.arguments)) {
			return nil, fmt.Errorf("incomplete streamed tool call")
		}
		result.ToolCalls = append(result.ToolCalls, map[string]any{"id": call.id, "type": call.kind, "function": map[string]any{"name": call.name, "arguments": call.arguments}})
	}
	result.Content = content.String()
	if reasoning.Len() > 0 {
		text := reasoning.String()
		result.Reasoning = &text
	}
	if opts.OnUsage != nil {
		opts.OnUsage(result.Usage)
	}
	return result, nil
}
