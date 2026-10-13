package harness

import (
	"anthonytaha/garness/tools"
	"encoding/json"
	"math"
	"testing"
)

func TestReportedUsagePreferred(t *testing.T) {
	for _, value := range []any{float64(100), 100, int64(100), json.Number("100")} {
		a := &Agent{Messages: []map[string]any{{"role": "system", "content": "base"}}}
		a.recordUsage(map[string]any{"prompt_tokens": value}, 1)
		a.Messages = append(a.Messages, map[string]any{"role": "user", "content": "12345678"})
		if got := a.estimatedTokens(); got != 102 {
			t.Fatalf("%T: got %d", value, got)
		}
		a.Messages[0]["content"] = "base12345678"
		if got := a.estimatedTokens(); got != 104 {
			t.Fatalf("changed system: %d", got)
		}
	}
}

func TestUsageFallback(t *testing.T) {
	for _, value := range []any{nil, "100", float64(0), -1, 1.5, math.NaN(), math.Inf(1)} {
		a := &Agent{Messages: []map[string]any{{"content": "12345678"}}}
		a.recordUsage(map[string]any{"prompt_tokens": 100}, 1)
		a.recordUsage(map[string]any{"prompt_tokens": value}, 1)
		if got := a.estimatedTokens(); got != 2 {
			t.Fatalf("%v: got %d", value, got)
		}
	}
	a := &Agent{Messages: []map[string]any{{"content": "12345678"}}}
	a.recordUsage(map[string]any{"input_tokens": float64(90)}, 1)
	if a.estimatedTokens() != 90 {
		t.Fatal("input_tokens not used")
	}
	a.ContextBudget = 1
	a.maybeCompact() // Too few messages to compact: retain the measurement.
	if a.estimatedTokens() != 90 {
		t.Fatal("no-op compaction discarded usage")
	}
}

func TestSkillLoadingRetainsReportedUsage(t *testing.T) {
	root := t.TempDir()
	fixtureSkill(t, root, "review", "review")
	a := &Agent{SystemPrompt: "base", Messages: []map[string]any{{"role": "system", "content": "base"}}, Tools: tools.DefaultTools()}
	a.ConfigureSkills(root)
	a.recordUsage(map[string]any{"prompt_tokens": float64(500)}, 1)
	before := estimateTokens(a.Messages)
	a.activateSkill("review")
	want := 500 + estimateTokens(a.Messages) - before
	if got := a.estimatedTokens(); got != want {
		t.Fatalf("got %d want %d", got, want)
	}
}
