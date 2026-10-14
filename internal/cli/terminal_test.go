package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/orchestration"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func terminalFixture(t *testing.T, accents ...string) *terminalModel {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Test instructions"), 0600)
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	agent := harness.NewAgent("fixture", model.Provider{}, "", root)
	box, err := sandbox.NewSandbox(root, sandbox.DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	agent.ConfigureSandbox(box)
	manager := &sessionManager{store: store, agent: agent, box: box}
	agent.Checkpoint = manager.save
	ctx, cancel := context.WithCancel(context.Background())
	accent := defaultAccentColor
	if len(accents) > 0 {
		accent = accents[0]
	}
	m, err := newTerminalModel(ctx, manager, orchestration.DefaultLimits(), accent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
		cancel()
		m.jobs.Wait()
		m.runs.close()
		manager.close()
		manager.box.Close()
	})
	return m
}

func TestTerminalApprovalDoesNotConsumeDraftOrQueue(t *testing.T) {
	m := terminalFixture(t)
	m.input.SetValue("unfinished draft")
	m.queued = []string{"y", "ordinary message"}
	answer := make(chan bool, 1)
	m.Update(approvalMessage{ctx: context.Background(), name: "shell_network", arguments: `{"domains":["example.com"]}`, answer: answer})
	m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y\n"), Paste: true})
	if m.pending == nil {
		t.Fatal("paste answered approval")
	}
	m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if <-answer {
		t.Fatal("default decision approved a tool")
	}
	if m.input.Value() != "unfinished draft" || len(m.queued) != 2 {
		t.Fatal("approval stole ordinary input")
	}

	m.Update(approvalMessage{ctx: context.Background(), name: "shell_network", answer: answer})
	m.updateKey(tea.KeyMsg{Type: tea.KeyRight})
	m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !<-answer {
		t.Fatal("explicit approval was not accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.Update(approvalMessage{ctx: ctx, name: "shell_network", answer: answer})
	m.updateKey(tea.KeyMsg{Type: tea.KeyRight})
	cancel()
	m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if <-answer {
		t.Fatal("stale request was approved")
	}
}

func TestTerminalComposerPasteCompletionAndHistory(t *testing.T) {
	m := terminalFixture(t)
	m.input.SetValue(":exec")
	m.updateKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != ":execute" {
		t.Fatal("colon completion failed", m.input.Value())
	}
	if commandMatches("/exec") != nil {
		t.Fatal("slash treated as a command prefix")
	}
	m.input.Reset()
	m.focus, m.action = "plan", 1
	m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
	if m.input.Value() != "first\nsecond" || m.cancel != nil || m.focus != "composer" {
		t.Fatal("paste submitted work or stayed on a plan action")
	}
	m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.input.Value() != "first\nsecond\n" {
		t.Fatal("newline shortcut failed", m.input.Value())
	}
	m.input.SetValue(":help")
	m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue("draft")
	m.updateKey(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	if m.input.Value() != ":help" {
		t.Fatal("history navigation failed")
	}
	m.updateKey(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	if m.input.Value() != "draft" {
		t.Fatal("history lost the draft")
	}
}

func TestTerminalQueuedCommandsDrainWithoutStartingWork(t *testing.T) {
	m := terminalFixture(t)
	m.queued = []string{":help", ":session", ":help"}
	for len(m.queued) > 0 {
		if cmd := m.dequeue(); cmd == nil {
			t.Fatal("idle queue stalled")
		}
		m.Update(queuedMessage{})
	}
	if m.cancel != nil {
		t.Fatal("presentation commands started model work")
	}
	m.cancel = func() {}
	m.queued = []string{"next"}
	if m.dequeue() != nil {
		t.Fatal("queue ran concurrently with active work")
	}
}

func TestTerminalLayoutAndControlSequences(t *testing.T) {
	m := terminalFixture(t)
	for _, size := range [][2]int{{80, 24}, {32, 10}, {12, 4}} {
		m.setSize(size[0], size[1])
		m.input.SetValue("draft")
		m.stream = strings.Repeat("long streamed output\n", 30)
		m.Update(approvalMessage{ctx: context.Background(), name: "shell", arguments: strings.Repeat("command detail\n", 50), answer: make(chan bool, 1)})
		m.approveChoice = true
		view := m.View()
		if strings.Contains(view, "\x1b") {
			t.Fatal("NO_COLOR emitted escape sequences")
		}
		if !strings.Contains(strings.ToLower(view), "approve") || strings.Count(view, "\n")+1 > size[1] {
			t.Fatalf("decision clipped at %v:\n%s", size, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("line exceeded terminal width: %q", line)
			}
		}
		m.answerApproval(false)
	}
	text := "hello\x1b[2J\x1b]52;c;evil\a\rworld\n\tfile"
	if got := terminalText(text); got != "helloworld\n\tfile" {
		t.Fatalf("unsafe terminal text: %q", got)
	}
}

func testPlan(version int) orchestration.Plan {
	check := orchestration.Check{Kind: "file_contains", Path: "file", Contains: "good"}
	final := check
	final.TaskID = "one"
	return orchestration.Plan{Version: version, Limits: orchestration.DefaultLimits(), Tasks: []orchestration.Task{{ID: "one", Role: "worker", Instructions: "write file", AllowedTools: []string{"write_file"}, WriteScope: []string{"file"}, AcceptanceCriteria: []string{"file contains good"}, Checks: []orchestration.Check{check}, MaxAttempts: 3}}, FinalChecks: []orchestration.Check{final}}
}

func TestTerminalPlanActionBindsDisplayedVersion(t *testing.T) {
	m := terminalFixture(t)
	c, err := orchestration.New(m.runs.store, "fixture", m.runs.options)
	if err != nil {
		t.Fatal(err)
	}
	m.runs.current = c
	if err := c.SetPlan(testPlan(1)); err != nil {
		t.Fatal(err)
	}
	m.loadSnapshot()
	if err := c.SetPlan(testPlan(2)); err != nil {
		t.Fatal(err)
	}
	m.focus, m.action = "plan", 1
	cmd := m.planKey("enter")
	if cmd == nil || !strings.Contains(fmt.Sprint(cmd()), "pending plan digest") {
		t.Fatalf("stale plan action was not rejected: %v", cmd())
	}
	if c.Run.Approval != nil {
		t.Fatal("approved a plan the user had not reviewed")
	}
	m.loadSnapshot()
	m.action = 1
	m.planKey("enter")
	if c.Run.Approval == nil || !c.Run.Approval.Approved {
		t.Fatal("current plan approval failed")
	}
	if m.cancel != nil {
		t.Fatal("approval also executed the plan")
	}
}

func TestTerminalToolActivityKeepsWorkerIdentity(t *testing.T) {
	m := terminalFixture(t)
	for _, task := range []string{"one", "two"} {
		m.updateActivity(activityMessage{task: task, event: harness.Event{Kind: "tool-start", ID: "same", Name: "read_file", Arguments: `{"path":"file"}`}})
	}
	m.updateActivity(activityMessage{task: "one", event: harness.Event{Kind: "tool-finish", ID: "same", Name: "read_file", Text: "one result"}})
	if !m.tools[0].finished || m.tools[1].finished || !strings.Contains(m.toolDetails(0), "one result") {
		t.Fatal("parallel activity was mixed")
	}
}

func TestSessionPickerSearchAndSelection(t *testing.T) {
	picker := newSessionPicker([]*session.Session{{ID: "first", Name: "alpha"}, {ID: "second", Name: "recovery"}})
	picker.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("rec")})
	picker.key(tea.KeyMsg{Type: tea.KeyDown})
	id, done, _ := picker.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !done || id != "second" {
		t.Fatal("filtered session selection failed", id)
	}
	picker = newSessionPicker(nil)
	_, done, action := picker.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !done || action != "new" {
		t.Fatal("empty picker could not start a session")
	}
}

// Exercise the real input/event/render loop through planning, execution, and
// approved copy-back. The provider uses local fixtures rather than credentials.
type terminalOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (out *terminalOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.buffer.Write(p)
}
func (out *terminalOutput) text() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return ansi.Strip(out.buffer.String())
}

func TestTerminalOrchestrationFlow(t *testing.T) {
	m := terminalFixture(t)
	plan := testPlan(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		planning := false
		for _, message := range body.Messages {
			text, _ := message["content"].(string)
			if strings.Contains(text, "Plan this objective:") {
				planning = true
			}
		}
		message := map[string]any{}
		if planning {
			data, _ := json.Marshal(plan)
			message["content"] = string(data)
		} else if body.Messages[len(body.Messages)-1]["role"] == "tool" {
			message["content"] = `{"summary":"done","artifacts":["file"],"unresolved":[]}`
		} else {
			message["tool_calls"] = []any{map[string]any{"id": "write", "function": map[string]any{"name": "write_file", "arguments": `{"path":"file","content":"good"}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	defer server.Close()
	m.manager.agent.Provider.BaseUrl, m.runs.options.Provider.BaseUrl = server.URL, server.URL
	output := &terminalOutput{}
	program := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(output), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { program.Kill(); program.Wait() })
	wait := func(marker string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(output.text(), marker) {
			if time.Now().After(deadline) {
				t.Fatalf("missing %q:\n%s", marker, output.text())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	send := func(line string) {
		program.Send(tea.KeyMsg{Type: tea.KeyEsc})
		program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(line)})
		program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	}
	wait("GARNESS")
	send(":plan fixture")
	wait("Approve with :approve")
	send(":approve " + plan.Digest())
	send(":execute")
	wait("Run completed.")
	if _, err := os.Stat(filepath.Join(m.manager.store.Workspace, "file")); !os.IsNotExist(err) {
		t.Fatal("execution copied back without approval")
	}
	send(":apply")
	wait("Approval · apply_changes")
	program.Send(tea.KeyMsg{Type: tea.KeyRight})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("applied 1 changes")
	body, err := os.ReadFile(filepath.Join(m.manager.store.Workspace, "file"))
	if err != nil || string(body) != "good" {
		t.Fatal("approved copy-back failed", err)
	}
	send(":quit")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal did not exit")
	}
	if len(m.tools) != 1 || m.tools[0].task != "one" || !m.tools[0].finished {
		t.Fatal("worker progress was not presented")
	}
	if !strings.Contains(output.text(), "--- /dev/null") {
		t.Fatal("approval did not show the change diff")
	}
}

var _ io.Writer = (*terminalOutput)(nil)
