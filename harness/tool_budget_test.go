package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anthonytaha/garness/model"
	"anthonytaha/garness/session"
	"anthonytaha/garness/tools"
)

func TestToolRoundBudget(t *testing.T) {
	for _, test := range []struct {
		name                          string
		limit, toolRounds, executions int
		ignoreLimit                   bool
		wantError                     bool
	}{
		{name: "default allows more than six", toolRounds: 8, executions: 8},
		{name: "summary after limit", limit: 2, toolRounds: 9, executions: 2},
		{name: "provider cannot bypass limit", limit: 2, toolRounds: 9, executions: 2, ignoreLimit: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests, executed := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var payload map[string]any
				if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
					t.Error(e)
				}
				_, hasTools := payload["tools"]
				if test.limit > 0 && requests == test.limit+1 && hasTools {
					t.Error("tools still offered at limit")
				}
				message := map[string]any{"content": "Completed fixture work."}
				if requests <= test.toolRounds && (hasTools || test.ignoreLimit) {
					message["tool_calls"] = []any{map[string]any{"id": fmt.Sprintf("call%d", requests), "function": map[string]any{"name": "fixture", "arguments": "{}"}}}
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer server.Close()
			a := NewAgent("", model.Provider{BaseUrl: server.URL}, "test", "")
			a.MaxToolSteps = test.limit
			a.Tools.Register(tools.Tool{Name: "fixture", Func: func(args map[string]any) (string, error) { executed++; return "done", nil }})
			var saved session.Session
			a.Checkpoint = func() error { a.Snapshot(&saved); return nil }
			reply, e := a.Run("do work")
			if (e != nil) != test.wantError || executed != test.executions {
				t.Fatalf("executed=%d error=%v", executed, e)
			}
			if test.limit > 0 && !test.wantError && !strings.Contains(reply, "Tool limit (2 rounds) reached") {
				t.Fatalf("missing progress message: %s", reply)
			}
			if test.limit == 0 && requests != 9 {
				t.Fatalf("requests=%d", requests)
			}
			if len(saved.Transcript) == 0 {
				t.Fatal("progress not saved")
			}
		})
	}
}

func TestTruncatedOutputDoesNotExecuteTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["max_tokens"] != float64(4096) {
			t.Errorf("agent output limit=%v", payload["max_tokens"])
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"length","message":{"tool_calls":[{"id":"cut","function":{"name":"fixture","arguments":"{}"}}]}}]}`)
	}))
	defer server.Close()
	a := NewAgent("", model.Provider{BaseUrl: server.URL}, "test", "")
	a.MaxOutputTokens = 4096
	executed := false
	a.Tools.Register(tools.Tool{Name: "fixture", Func: func(args map[string]any) (string, error) { executed = true; return "done", nil }})
	_, err := a.Run("write code")
	if err == nil || !strings.Contains(err.Error(), "token limit") || executed {
		t.Fatalf("executed=%t error=%v", executed, err)
	}
}
