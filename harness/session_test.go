package harness

import (
	"anthonytaha/garness/session"
	"testing"
)

func TestRestoreInterruptedSession(t *testing.T) {
	a := &Agent{SystemPrompt: "current instructions", Skills: &SkillRegistry{skills: map[string]Skill{}}, activeSkills: map[string]Skill{}}
	v := &session.Session{Interrupted: true, Messages: []map[string]any{{"role": "system", "content": "old instructions"}, {"role": "assistant", "tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{"name": "shell", "arguments": "{}"}}}}}, Skills: []string{"missing"}}
	warnings := a.Restore(v)
	if len(warnings) != 1 {
		t.Fatal("missing skill warning")
	}
	if a.Messages[0]["content"] != "current instructions" {
		t.Fatal("stale instructions restored")
	}
	if a.Messages[2]["role"] != "tool" || a.Messages[2]["tool_call_id"] != "call1" {
		t.Fatal("missing interruption result")
	}
	if a.Interrupted {
		t.Fatal("interrupted flag not cleared")
	}
	var out session.Session
	a.Snapshot(&out)
	if len(out.Transcript) != 2 {
		t.Fatal("recovery transcript missing")
	}
}
