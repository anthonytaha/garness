package orchestration

import (
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, handler http.HandlerFunc) *Coordinator {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Follow task scope."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := New(store, "implement fixture", Options{Provider: model.Provider{BaseUrl: server.URL}, Approve: func(context.Context, string, string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func response(w http.ResponseWriter, content string, calls ...any) {
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content, "tool_calls": calls}}}, "usage": map[string]any{"total_tokens": 10}})
}

func writeCall(path, content string) any {
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return map[string]any{"id": "write", "function": map[string]any{"name": "write_file", "arguments": string(args)}}
}

func payload(t *testing.T, r *http.Request) ([]map[string]any, string) {
	t.Helper()
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	text := ""
	for _, m := range body.Messages {
		if m["role"] == "user" {
			content, _ := m["content"].(string)
			text += content
		}
	}
	return body.Messages, text
}

func fixtureTask(id, path string) Task {
	return Task{ID: id, Role: "worker", Instructions: "write " + path, AllowedTools: []string{"read_file", "write_file"}, WriteScope: []string{path}, AcceptanceCriteria: []string{"file contains good"}, Checks: []Check{{Kind: "file_contains", Path: path, Contains: "good"}}, MaxAttempts: 3}
}

func planFor(tasks ...Task) Plan {
	p := Plan{Version: 1, Tasks: tasks, Limits: DefaultLimits()}
	for _, task := range tasks {
		for _, check := range task.Checks {
			check.TaskID = task.ID
			p.FinalChecks = append(p.FinalChecks, check)
		}
	}
	return p
}

func approve(t *testing.T, c *Coordinator, plan Plan) {
	t.Helper()
	if err := c.SetPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := c.ApprovePlan(plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
}

func simpleWriter(t *testing.T, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		messages, _ := payload(t, r)
		if messages[len(messages)-1]["role"] == "tool" {
			response(w, fmt.Sprintf(`{"summary":"done","artifacts":[%q],"unresolved":[]}`, path))
		} else {
			response(w, "", writeCall(path, "good"))
		}
	}
}

func TestApprovalExecutionAndFinalCopyBack(t *testing.T) {
	c := fixture(t, simpleWriter(t, "file"))
	plan := planFor(fixtureTask("one", "file"))
	if err := c.SetPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(context.Background()); err == nil {
		t.Fatal("executed without approval")
	}
	if err := c.ApprovePlan("stale", true); err == nil {
		t.Fatal("accepted wrong digest")
	}
	if err := c.ApprovePlan(plan.Digest(), false); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(context.Background()); err == nil {
		t.Fatal("executed denied plan")
	}
	if err := c.ApprovePlan(plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Run.State != Completed || c.Run.Tasks[0].State != Succeeded {
		t.Fatal("run incomplete")
	}
	if _, err := os.Stat(filepath.Join(c.Store.Workspace, "file")); !os.IsNotExist(err) {
		t.Fatal("worker copied changes to original")
	}
	if _, ok := c.Run.Working.Files[".env"]; ok {
		t.Fatal("secret entered snapshot")
	}
	c.Options.Approve = func(context.Context, string, string) bool { return false }
	if _, err := c.Apply(context.Background()); err == nil || c.Run.Applied {
		t.Fatal("copy-back bypassed approval")
	}
	c.Options.Approve = func(context.Context, string, string) bool { return true }
	if _, err := c.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(c.Store.Workspace, "file"))
	if err != nil || string(body) != "good" {
		t.Fatal("missing applied changes", err)
	}
	if _, err := c.Apply(context.Background()); err == nil {
		t.Fatal("reapplied completed run")
	}
}

func TestPlanRevisionInvalidatesApproval(t *testing.T) {
	c := fixture(t, simpleWriter(t, "file"))
	plan := planFor(fixtureTask("one", "file"))
	approve(t, c, plan)
	old := plan.Digest()
	plan.Version++
	if err := c.SetPlan(plan); err != nil {
		t.Fatal(err)
	}
	if c.Run.Approval != nil {
		t.Fatal("retained approval")
	}
	if err := c.ApprovePlan(old, true); err == nil {
		t.Fatal("approved stale plan")
	}
}

func TestPlanValidation(t *testing.T) {
	for _, mutation := range []func(*Plan){
		func(p *Plan) { p.Tasks = append(p.Tasks, p.Tasks[0]) },
		func(p *Plan) { p.Tasks[0].Dependencies = []string{"missing"} },
		func(p *Plan) { p.Tasks[0].Dependencies = []string{"one"} },
		func(p *Plan) { p.Tasks[0].AllowedTools = []string{"apply_changes"} },
		func(p *Plan) { p.Tasks[0].WriteScope = []string{"../escape"} },
		func(p *Plan) { p.Tasks[0].WriteScope = []string{".env"} },
		func(p *Plan) { p.Tasks[0].Role = "reviewer" },
		func(p *Plan) { p.FinalChecks[0].TaskID = "missing" },
		func(p *Plan) { p.Tasks[0].Checks = nil },
		func(p *Plan) { p.Tasks[0].MaxAttempts = 4 },
	} {
		p := planFor(fixtureTask("one", "file"))
		mutation(&p)
		if p.Validate() == nil {
			t.Fatal("invalid plan accepted", p)
		}
	}
}

func TestVerificationRepairsPreserveEditsAndStopAtLimit(t *testing.T) {
	for _, repair := range []bool{true, false} {
		t.Run(fmt.Sprint(repair), func(t *testing.T) {
			var starts atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				messages, text := payload(t, r)
				if messages[len(messages)-1]["role"] == "tool" {
					response(w, `{"summary":"done","artifacts":["file"],"unresolved":[]}`)
					return
				}
				n := starts.Add(1)
				if n > 1 && !strings.Contains(text, "check failed") {
					t.Error("repair lacks feedback")
				}
				value := "bad"
				if repair && n > 1 {
					value = "good"
				}
				response(w, "", writeCall("file", value))
			})
			approve(t, c, planFor(fixtureTask("one", "file")))
			err := c.Execute(context.Background())
			if repair {
				if err != nil || starts.Load() != 2 || c.Run.State != Completed {
					t.Fatalf("repair: %v (%d)", err, starts.Load())
				}
			} else if err == nil || starts.Load() != 3 || c.Run.State != Failed {
				t.Fatalf("retry limit: %v (%d)", err, starts.Load())
			}
			artifact, err := c.Store.LoadArtifact(c.Run.ID, c.Run.Tasks[0].Attempts[0].ID)
			if err != nil || string(artifact.Sandbox.Files["file"].Body) != "bad" {
				t.Fatal("failed attempt edits not persisted", err)
			}
		})
	}
}

func TestParallelSchedulingDependenciesAndConflicts(t *testing.T) {
	var firstRequests atomic.Int32
	barrier := make(chan struct{})
	var mutex sync.Mutex
	starts := map[string]int{}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		messages, text := payload(t, r)
		id := "one"
		if strings.Contains(text, `"id":"two"`) {
			id = "two"
		}
		if strings.Contains(text, `"id":"dependent"`) {
			id = "dependent"
		}
		if messages[len(messages)-1]["role"] == "tool" {
			response(w, `{"summary":"done","artifacts":["file"],"unresolved":[]}`)
			return
		}
		mutex.Lock()
		starts[id]++
		n := starts[id]
		mutex.Unlock()
		if id != "dependent" && n == 1 {
			if firstRequests.Add(1) == 2 {
				close(barrier)
			}
			select {
			case <-barrier:
			case <-r.Context().Done():
				return
			}
		}
		if id == "dependent" && !strings.Contains(text, `"dependencies":{"one":`) {
			t.Error("missing dependency results")
		}
		response(w, "", writeCall("file", "good "+id))
	})
	one, two, dep := fixtureTask("one", "file"), fixtureTask("two", "file"), fixtureTask("dependent", "file")
	dep.Dependencies = []string{"one", "two"}
	approve(t, c, planFor(one, two, dep))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if starts["one"]+starts["two"] != 3 || starts["dependent"] != 1 {
		t.Fatal("conflict was not repaired exactly once", starts)
	}
	if c.Run.State != Completed {
		t.Fatal("not complete")
	}
}

func TestWriteScopeCannotBeExpandedByWorker(t *testing.T) {
	c := fixture(t, simpleWriter(t, "outside"))
	approve(t, c, planFor(fixtureTask("one", "file")))
	if err := c.Execute(context.Background()); err == nil {
		t.Fatal("out of scope write accepted")
	}
	if c.Run.Tasks[0].State != Blocked || c.Run.Tasks[0].Attempts[0].Failure.Kind != "permission" {
		t.Fatal("permission failure not blocked")
	}
	if _, ok := c.Run.Working.Files["outside"]; ok {
		t.Fatal("out of scope changes integrated")
	}
}

func TestResumePreservesAcceptedWorkAndRequiresReconciliation(t *testing.T) {
	c := fixture(t, simpleWriter(t, "file"))
	plan := planFor(fixtureTask("one", "file"))
	approve(t, c, plan)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := c.Run.ID
	options := c.Options
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Resume(c.Store, id, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Run.State != Completed || string(restored.Run.Working.Files["file"].Body) != "good" {
		t.Fatal("accepted edits lost")
	}
	if restored.Run.Usage.Requests < 2 {
		t.Fatal("budget reset")
	}
	// Simulate a durable dispatch whose result was never committed.
	restored.Run.State = Executing
	restored.Run.Tasks[0].State = Running
	restored.Run.Tasks[0].Attempts[0].State = Running
	if err := restored.save(); err != nil {
		t.Fatal(err)
	}
	restored.Close()
	recovered, err := Resume(c.Store, id, options)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Run.State != Blocked || recovered.Run.Tasks[0].State != Blocked {
		t.Fatal("unknown action replayed")
	}
	if err := recovered.Execute(context.Background()); err == nil {
		t.Fatal("executed without reconciliation")
	}
	if err := recovered.Retry("one", ""); err == nil {
		t.Fatal("retry without evidence")
	}
	if err := recovered.Retry("one", "Inspected files; no external effects; rerun is safe"); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationStopsModelRequestAndRetainsAttempt(t *testing.T) {
	started := make(chan struct{})
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { payload(t, r); close(started); <-r.Context().Done() })
	approve(t, c, planFor(fixtureTask("one", "file")))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Execute(ctx) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil || c.Run.State != Cancelled || c.Run.Tasks[0].State != Blocked {
			t.Fatal("cancel did not stop execution", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel hung")
	}
	if _, err := c.Store.LoadArtifact(c.Run.ID, c.Run.Tasks[0].Attempts[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestProviderRetriesShareBudgetAndDoNotReplayTools(t *testing.T) {
	var requests atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		messages, _ := payload(t, r)
		if messages[len(messages)-1]["role"] != "tool" {
			response(w, "", writeCall("file", "good"))
			return
		}
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		response(w, `{"summary":"done","artifacts":["file"],"unresolved":[]}`)
	})
	approve(t, c, planFor(fixtureTask("one", "file")))
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Run.Usage.Requests != 3 || c.Run.Usage.ToolCalls != 1 {
		t.Fatal("retry replayed a tool or escaped budget", c.Run.Usage)
	}
	// A new run with one request must stop before the second HTTP request.
	c2, err := New(c.Store, "limited", c.Options)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.Options.Limits.MaxRequests = 1
	c2.budget.limits = c2.Options.Limits
	plan := planFor(fixtureTask("one", "file"))
	plan.Limits = c2.Options.Limits
	approve(t, c2, plan)
	err = c2.Execute(context.Background())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "budget" || c2.Run.Usage.Requests != 1 {
		t.Fatal("budget not enforced", err)
	}
}

type fixtureRunner func(context.Context, *sandbox.Workspace, sandbox.CommandRequest) (sandbox.CommandResult, error)

func (f fixtureRunner) Run(ctx context.Context, ws *sandbox.Workspace, _ sandbox.SandboxPolicy, request sandbox.CommandRequest, _ *sandbox.NetworkGrant) (sandbox.CommandResult, error) {
	return f(ctx, ws, request)
}

func TestFinalVerificationRepairsAndInvalidatesDependents(t *testing.T) {
	var starts atomic.Int32
	var dependentStarts atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		messages, text := payload(t, r)
		dependent := strings.Contains(text, `"id":"dependent"`)
		if messages[len(messages)-1]["role"] == "tool" {
			path := "file"
			if dependent {
				path = "dependent"
			}
			response(w, fmt.Sprintf(`{"summary":"done","artifacts":[%q],"unresolved":[]}`, path))
			return
		}
		if dependent {
			dependentStarts.Add(1)
			response(w, "", writeCall("dependent", "good"))
			return
		}
		value := "good"
		if starts.Add(1) > 1 {
			value = "good repaired"
		}
		response(w, "", writeCall("file", value))
	})
	c.Options.Runner = fixtureRunner(func(ctx context.Context, ws *sandbox.Workspace, request sandbox.CommandRequest) (sandbox.CommandResult, error) {
		body, _ := ws.ReadFile("file")
		if body != "good repaired" {
			return sandbox.CommandResult{ExitCode: 1, Output: "combined check failed"}, nil
		}
		return sandbox.CommandResult{}, nil
	})
	dep := fixtureTask("dependent", "dependent")
	dep.Dependencies = []string{"one"}
	plan := planFor(fixtureTask("one", "file"), dep)
	plan.FinalChecks = []Check{{Kind: "shell", Command: "fixture", TaskID: "one"}}
	approve(t, c, plan)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 2 || dependentStarts.Load() != 2 || c.Run.State != Completed {
		t.Fatal("combined failure was not repaired")
	}
}

func TestInterruptedFinalCopyBackCanBeReconciled(t *testing.T) {
	c := fixture(t, simpleWriter(t, "file"))
	approve(t, c, planFor(fixtureTask("one", "file")))
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Run.Applying = true
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	c.box.Approve = func(string, string) bool { return true }
	if _, err := c.box.ApplyChanges(); err != nil {
		t.Fatal(err)
	}
	c.Close()
	restored, err := Resume(c.Store, c.Run.ID, c.Options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Apply(context.Background()); err == nil {
		t.Fatal("unknown copy-back replayed")
	}
	if err := restored.ReconcileApply(); err != nil {
		t.Fatal(err)
	}
	if !restored.Run.Applied || restored.Run.Applying {
		t.Fatal("copy-back not reconciled")
	}
	if changes, err := restored.Changes(); err != nil || changes != "no changes" {
		t.Fatal("reconciled changes retained", changes, err)
	}
}

func TestPlannerCorrectionsAndReviewer(t *testing.T) {
	var planning atomic.Int32
	var review atomic.Int32
	plan := planFor(fixtureTask("one", "file"))
	plan.Tasks[0].Review = true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		messages, text := payload(t, r)
		if !strings.Contains(text, "Plan this objective:") && strings.Contains(text, "Inspect workspace context for planning:") {
			response(w, "No relevant existing implementation; create the requested file.")
			return
		}
		if strings.Contains(text, "Plan this objective:") {
			if planning.Add(1) == 1 {
				response(w, `{"version":1}`)
			} else {
				data, _ := json.Marshal(plan)
				response(w, string(data))
			}
			return
		}
		if strings.Contains(text, "Review correctness") {
			review.Add(1)
			response(w, `{"passed":true,"issues":[]}`)
			return
		}
		if messages[len(messages)-1]["role"] == "tool" {
			response(w, `{"summary":"done","artifacts":["file"],"unresolved":[]}`)
		} else {
			response(w, "", writeCall("file", "good"))
		}
	})
	if err := c.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Run.State != AwaitingApproval || planning.Load() != 2 {
		t.Fatal("planner format correction failed")
	}
	if err := c.ApprovePlan(c.Run.Plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if review.Load() != 1 {
		t.Fatal("review subagent not invoked")
	}
}
