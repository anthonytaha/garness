package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/orchestration"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApprovalBrokerCancellation(t *testing.T) {
	broker := &approvalBroker{requests: make(chan approvalRequest)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- broker.approve(ctx, "network", "fixture") }()
	request := <-broker.requests
	cancel()
	select {
	case approved := <-done:
		if approved {
			t.Fatal("cancelled approval accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not cancel")
	}
	request.answer <- true
	if broker.approve(ctx, "network", "fixture") {
		t.Fatal("cancelled context queued another approval")
	}
}

func TestOrchestrationCLIFlow(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("task instructions"), 0600)
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	limits := orchestration.DefaultLimits()
	check := orchestration.Check{Kind: "file_contains", Path: "file", Contains: "good"}
	final := check
	final.TaskID = "one"
	plan := orchestration.Plan{Version: 1, Limits: limits, Tasks: []orchestration.Task{{ID: "one", Role: "worker", Instructions: "write file", AllowedTools: []string{"write_file"}, WriteScope: []string{"file"}, AcceptanceCriteria: []string{"file contains good"}, Checks: []orchestration.Check{check}, MaxAttempts: 3}}, FinalChecks: []orchestration.Check{final}}
	var ordinary atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		planning, worker := false, false
		for _, m := range body.Messages {
			text, _ := m["content"].(string)
			if strings.Contains(text, "Plan this objective:") {
				planning = true
			}
			if strings.Contains(text, "Perform only this approved task") {
				worker = true
			}
		}
		message := map[string]any{}
		if planning {
			data, _ := json.Marshal(plan)
			message["content"] = string(data)
		} else if !worker {
			ordinary.Add(1)
			message["content"] = "ordinary reply"
		} else if body.Messages[len(body.Messages)-1]["role"] == "tool" {
			message["content"] = `{"summary":"done","artifacts":["file"],"unresolved":[]}`
		} else {
			message["tool_calls"] = []any{map[string]any{"id": "write", "function": map[string]any{"name": "write_file", "arguments": `{"path":"file","content":"good"}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	defer server.Close()
	a := harness.NewAgent("", model.Provider{BaseUrl: server.URL}, "", root)
	box, err := sandbox.NewSandbox(root, sandbox.DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	a.ConfigureSandbox(box)
	manager := &sessionManager{store: store, agent: a, box: box}
	defer manager.close()
	a.Checkpoint = manager.save
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	output, capture, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = capture
	defer func() { os.Stdout = old; capture.Close(); output.Close() }()
	chunks := make(chan string, 128)
	go func() {
		data := make([]byte, 4096)
		for {
			n, err := output.Read(data)
			if n > 0 {
				chunks <- string(data[:n])
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { runREPL(bufio.NewScanner(input), a, box, manager, limits); close(done) }()
	seen := ""
	wait := func(marker string) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for !strings.Contains(seen, marker) {
			select {
			case chunk := <-chunks:
				seen += chunk
			case <-timer.C:
				t.Fatalf("missing output %q: %s", marker, seen)
			}
		}
	}
	io.WriteString(writer, ":plan implement fixture\n")
	wait("Approve with :approve")
	io.WriteString(writer, ":execute\n")
	wait("execution requires approval")
	io.WriteString(writer, ":approve "+plan.Digest()+"\n:execute\n")
	wait("Run completed.")
	io.WriteString(writer, ":apply\ny\n")
	wait("applied 1 changes")
	io.WriteString(writer, ":run-close\nfirst turn\nsecond turn\n")
	writer.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("REPL did not exit")
	}
	if ordinary.Load() != 2 {
		t.Fatal("piped turns were dropped", ordinary.Load())
	}
	body, err := os.ReadFile(filepath.Join(root, "file"))
	if err != nil || string(body) != "good" {
		t.Fatal("CLI did not apply changes", err)
	}
}
