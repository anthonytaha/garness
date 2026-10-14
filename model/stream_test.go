package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func streamResponse(text string) *http.Response {
	return &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(text))}
}

func TestCompletionStreamAssembly(t *testing.T) {
	stream := ": keepalive\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Working \",\"reasoning_content\":\"reason\",\"tool_calls\":[{\"index\":1,\"id\":\"second\",\"type\":\"function\",\"function\":{\"name\":\"calculator\",\"arguments\":\"{\\\"expression\\\":\"}},{\"index\":0,\"id\":\"first\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"now\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"file\\\"}\"}},{\"index\":1,\"function\":{\"arguments\":\"\\\"1+1\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":42}}\n\n" +
		"data: [DONE]\n\n"
	var deltas strings.Builder
	usageCalls := 0
	resp, err := readCompletionStream(streamResponse(stream), CompleteOptions{OnDelta: func(text string) { deltas.WriteString(text) }, OnUsage: func(usage map[string]any) {
		usageCalls++
		if usage["prompt_tokens"] != float64(42) {
			t.Error(usage)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Working now" || deltas.String() != resp.Content || usageCalls != 1 || len(resp.ToolCalls) != 2 || resp.Reasoning == nil || *resp.Reasoning != "reason" {
		t.Fatalf("response=%+v deltas=%s usage=%d", resp, deltas.String(), usageCalls)
	}
	for i, id := range []string{"first", "second"} {
		if resp.ToolCalls[i].(map[string]any)["id"] != id {
			t.Fatal("tool order changed")
		}
	}
}

func TestCompletionStreamRejectsIncompleteResponses(t *testing.T) {
	for _, stream := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
		"data: [DONE]\n\n",
		"data: invalid\n\n",
		"data: {\"error\":{\"message\":\"failed\"}}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"shell\",\"arguments\":\"{\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
	} {
		resp, err := readCompletionStream(streamResponse(stream), CompleteOptions{})
		if err == nil || resp != nil {
			t.Fatalf("partial stream returned executable response: %+v %v", resp, err)
		}
	}
}

func TestStreamingRequestAndJSONFallback(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			requests, reservations, usageCalls := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["stream"] != true || body["stream_options"].(map[string]any)["include_usage"] != true {
					t.Error("missing streaming flags", body)
				}
				if requests == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(503)
					return
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7}}\n\ndata: [DONE]\n\n")
				} else {
					io.WriteString(w, `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7}}`)
				}
			}))
			defer server.Close()
			var deltas strings.Builder
			resp, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{OnDelta: func(delta string) { deltas.WriteString(delta) }, BeforeRequest: func(int) error { reservations++; return nil }, OnUsage: func(map[string]any) { usageCalls++ }})
			if err != nil || resp.Content != "hello" || deltas.String() != "hello" || requests != 2 || reservations != 2 || usageCalls != 1 {
				t.Fatalf("response=%+v err=%v requests=%d reservations=%d usage=%d", resp, err, requests, reservations, usageCalls)
			}
		})
	}
}

func TestStreamingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	start := time.Now()
	resp, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{Context: ctx, OnDelta: func(string) { cancel() }})
	if err == nil || resp != nil || time.Since(start) > time.Second {
		t.Fatalf("stream did not cancel promptly: %+v %v", resp, err)
	}
}
