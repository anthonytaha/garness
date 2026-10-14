package harness

import (
	"anthonytaha/garness/model"
	"anthonytaha/garness/tools"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamingToolsRequireCompleteResponse(t *testing.T) {
	for _, finish := range []string{"interrupted", "length", "tool_calls"} {
		t.Run(finish, func(t *testing.T) {
			requests, calls, oldObserver := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call", "type": "function", "function": map[string]any{"name": "marker", "arguments": `{}`}}}}}}}
				if requests > 1 {
					chunk = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "done"}, "finish_reason": "stop"}}}
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if finish == "interrupted" {
					return
				}
				if requests == 1 {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			var events []Event
			a := NewAgent("", model.Provider{BaseUrl: server.URL}, "", "", func(event Event) { events = append(events, event) })
			a.OnDelta = func(string) {}
			a.OnToolResult = func(string, tools.CallResult) { oldObserver++ }
			a.Tools.Register(tools.Tool{Name: "marker", Func: func(map[string]any) (string, error) { calls++; return "saved result", nil }})
			reply, err := a.Run("fixture")
			if finish != "tool_calls" {
				if err == nil || calls != 0 || oldObserver != 0 {
					t.Fatal("incomplete response executed a tool", err, calls)
				}
				return
			}
			if err != nil || reply != "done" || calls != 1 || oldObserver != 1 {
				t.Fatal("completed stream failed", reply, err, calls, oldObserver)
			}
			if len(a.Transcript) != 4 || a.Transcript[2]["content"] != "saved result" {
				t.Fatal("stream changed transcript persistence", a.Transcript)
			}
			var kinds []string
			for _, event := range events {
				kinds = append(kinds, event.Kind)
			}
			if !strings.Contains(strings.Join(kinds, ","), "tool-start,tool-finish") {
				t.Fatal("tool activity missing", kinds)
			}
		})
	}
}
