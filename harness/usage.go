package harness

import (
	"encoding/json"
	"math"
)

// Prefer the provider's measured prompt size; estimate only content changed
// since that request. Before the first measurement, estimate the whole context.
func (a *Agent) estimatedTokens() int {
	if a.usageTokens > 0 && a.usageMsgCount > 0 && a.usageMsgCount <= len(a.Messages) {
		tokens := a.usageTokens + estimateTokens(a.Messages[a.usageMsgCount:])
		current, _ := a.Messages[0]["content"].(string)
		if current != a.usageSystem {
			tokens += estimateTokens([]map[string]any{{"content": current}}) - estimateTokens([]map[string]any{{"content": a.usageSystem}})
		}
		return max(0, tokens)
	}
	return estimateTokens(a.Messages)
}

func reportedTokenCount(value any) (int, bool) {
	var count float64
	switch n := value.(type) {
	case float64:
		count = n
	case int:
		return n, n > 0
	case int64:
		converted := int(n)
		return converted, n > 0 && int64(converted) == n
	case json.Number:
		var err error
		count, err = n.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(count) || math.IsInf(count, 0) || count <= 0 || math.Trunc(count) != count || count >= float64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(count), true
}

func (a *Agent) recordUsage(usage map[string]any, sent int) {
	prompt, ok := reportedTokenCount(usage["prompt_tokens"])
	if !ok {
		prompt, ok = reportedTokenCount(usage["input_tokens"])
	}
	if !ok || sent <= 0 || sent > len(a.Messages) {
		a.usageTokens, a.usageMsgCount = 0, 0
		a.usageSystem = ""
		return
	}
	a.usageTokens, a.usageMsgCount = prompt, sent
	a.usageSystem, _ = a.Messages[0]["content"].(string)
}

// EstimatedContextTokens returns the current context estimate. Read it only
// while the agent is idle or from its execution goroutine.
func (a *Agent) EstimatedContextTokens() int { return a.estimatedTokens() }
