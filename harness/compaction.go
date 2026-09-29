package harness

import (
	"anthonytaha/garness/model"
	"fmt"
	"strings"
)

const DEFAULT_CONTEXT_BUDGET int = 100_000
const COMPACT_KEEP_HEAD int = 2
const COMPACT_KEEP_TAIL int = 12
const SUMMARY_MAX_TOKENS int = 1024
const TOOL_RESULT_PREVIEW int = 500
const CHARS_PER_TOKEN int = 4

const SUMMARY_PREFIX string = "[Summary of earlier conversation]\n\n"

const SUMMARY_SYS_PROMPT string = `You are compacting a long conversation to free up context.
Summarize the exchange below as a dense briefing for an assistant that will continue the work.
Preserve: decisions made, facts established, file paths, symbol names, tool results, and any
unfinished threads or TODOs. Be faithful and concise; do not invent anything not present.
If the transcript opens with a PREVIOUS SUMMARY, fold its content into your summary rather
than summarizing it as dialogue, so nothing it recorded is lost.`

func compact(provider model.Provider, modelName string, messages []map[string]any, keepHead int, keepTail int) ([]map[string]any, error) {
	n := len(messages)
	if keepHead+keepTail >= n {
		return messages, nil
	}

	headEnd := snapPastToolResults(messages, keepHead)
	tailStart := snapPastToolResults(messages, n-keepTail)
	if headEnd >= tailStart {
		return messages, nil
	}

	summary, err := summarize(provider, modelName, messages[headEnd:tailStart])
	if err != nil {
		return messages, err
	}

	out := make([]map[string]any, 0, headEnd+1+n-tailStart)
	out = append(out, messages[:headEnd]...)
	out = append(out, map[string]any{
		"role":    "user",
		"content": "[Summary of earlier conversation]\n\n" + summary,
	})
	out = append(out, messages[tailStart:]...)
	return out, nil
}

func summarize(provider model.Provider, modelName string, messages []map[string]any) (string, error) {
	req := []map[string]any{
		{"role": "system", "content": SUMMARY_SYS_PROMPT},
		{"role": "user", "content": renderTranscript(messages)},
	}
	resp, err := model.Chat(provider, req, &model.CompleteOptions{
		Model:     modelName,
		MaxTokens: SUMMARY_MAX_TOKENS,
	})
	if err != nil {
		return "", fmt.Errorf("summarize conversation: %w", err)
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("summarize conversation: empty summary")
	}
	return summary, nil
}

func renderTranscript(messages []map[string]any) string {
	var b strings.Builder
	for _, m := range messages {
		role := msgRole(m)
		content, _ := m["content"].(string)
		if isSummary(m) {
			fmt.Fprintf(&b, "PREVIOUS SUMMARY:\n%s\n", strings.TrimPrefix(content, SUMMARY_PREFIX))
			continue
		}
		switch role {
		case "assistant":
			if content != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", content)
			}
			for _, call := range msgToolCalls(m) {
				name, args := callNameArgs(call)
				fmt.Fprintf(&b, "ASSISTANT called %s(%s)\n", name, args)
			}
		case "tool":
			fmt.Fprintf(&b, "TOOL RESULT: %s\n", truncate(content, TOOL_RESULT_PREVIEW))
		default:
			fmt.Fprintf(&b, "%s: %s\n", strings.ToUpper(role), content)
		}
	}
	return b.String()
}

func estimateTokens(messages []map[string]any) int {
	chars := 0
	for _, m := range messages {
		content, _ := m["content"].(string)
		chars += len(content)
		for _, call := range msgToolCalls(m) {
			name, args := callNameArgs(call)
			chars += len(name) + len(args)
		}
	}
	return chars / CHARS_PER_TOKEN
}

func isSummary(m map[string]any) bool {
	content, _ := m["content"].(string)
	return msgRole(m) == "user" && strings.HasPrefix(content, SUMMARY_PREFIX)
}

func msgRole(m map[string]any) string {
	r, _ := m["role"].(string)
	return r
}

func snapPastToolResults(messages []map[string]any, i int) int {
	for i < len(messages) && msgRole(messages[i]) == "tool" {
		i++
	}
	return i
}

func msgToolCalls(m map[string]any) []any {
	switch tc := m["tool_calls"].(type) {
	case []any:
		return tc
	case []map[string]any:
		out := make([]any, len(tc))
		for i, c := range tc {
			out[i] = c
		}
		return out
	}
	return nil
}

func callNameArgs(call any) (string, string) {
	tc, ok := call.(map[string]any)
	if !ok {
		return "?", ""
	}
	fn, _ := tc["function"].(map[string]any)
	name, _ := fn["name"].(string)
	args, _ := fn["arguments"].(string)
	return name, args
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
