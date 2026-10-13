package orchestration

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func inScope(path string, scope []string) bool {
	if !cleanPath(path) {
		return false
	}
	for _, allowed := range scope {
		if allowed == "." || path == allowed || strings.HasPrefix(path, allowed+"/") {
			return true
		}
	}
	return false
}

func scopeChanges(state *sandbox.State, scope []string, policy sandbox.SandboxPolicy) error {
	changes, err := sandbox.StateChanges(state, policy)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if inScope(change.Path, scope) {
			continue
		}
		// A new parent directory is necessary when the scope names a nested file.
		file := state.Files[change.Path]
		parent := false
		if change.Kind == "added" && file.Mode.IsDir() {
			for _, allowed := range scope {
				if strings.HasPrefix(allowed, change.Path+"/") {
					parent = true
				}
			}
		}
		if !parent {
			return &Failure{Kind: "permission", Message: "changed file outside approved write scope: " + change.Path}
		}
	}
	return nil
}

func (c *Coordinator) agent(ctx context.Context, box *sandbox.Sandbox, allowed []string, scope []string) *harness.Agent {
	root := c.Options.AgentsDir
	if root == "" {
		root = c.Store.Workspace
	}
	a := harness.NewAgent(c.Options.Model, c.Options.Provider, c.Options.SystemPrompt, root)
	if len(c.Options.SkillsRoots) > 0 {
		a.ConfigureSkills(c.Options.SkillsRoots...)
	}
	a.ConfigureSandbox(box)
	a.Tools.Restrict(allowed)
	a.MaxToolSteps = c.budget.limits.MaxToolSteps
	a.MaxOutputTokens = c.budget.limits.MaxOutputTokens
	a.RequireCompletion = true
	a.OnUsage = c.budget.record
	var fatal error
	a.BeforeRequest = func(tokens int) error {
		if fatal != nil {
			return fatal
		}
		return c.budget.beforeRequest(tokens)
	}
	a.Tools.BeforeCall = func(ctx context.Context, name string, args map[string]any) error {
		if fatal != nil {
			return fatal
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.budget.reserve(0, true); err != nil {
			fatal = err
			return err
		}
		if name == "write_file" || name == "edit_file" || name == "append_file" {
			path, _ := args["path"].(string)
			if !inScope(path, scope) {
				fatal = &Failure{Kind: "permission", Message: "write outside approved scope: " + path}
				return fatal
			}
		}
		return nil
	}
	a.Approve = func(name, args string) bool { return c.Options.Approve != nil && c.Options.Approve(ctx, name, args) }
	a.OnToolResult = func(name string, result tools.CallResult) {
		var denied *tools.DeniedError
		var sandboxDenied *sandbox.ApprovalDeniedError
		if errors.As(result.Err, &denied) || errors.As(result.Err, &sandboxDenied) {
			fatal = &Failure{Kind: "approval", Message: result.Err.Error()}
		}
		if name == "shell" {
			state, err := box.ExportState()
			if err == nil {
				err = scopeChanges(state, scope, box.Policy)
			}
			if err != nil {
				fatal = err
			}
		}
	}
	if c.Options.Runner != nil {
		box.Runner = c.Options.Runner
	}
	return a
}

func (c *Coordinator) checkpoint(a *harness.Agent, box *sandbox.Sandbox, attemptID string) func() error {
	return func() error {
		state, err := box.ExportState()
		if err != nil {
			return err
		}
		artifact := &Artifact{Sandbox: state}
		a.Snapshot(&artifact.Session)
		artifact.Session.ID = attemptID
		return c.Store.SaveArtifact(c.Run.ID, attemptID, artifact)
	}
}

type work struct {
	task    Task
	attempt Attempt
	box     *sandbox.Sandbox
	prompt  string
}

type outcome struct {
	taskID    string
	state     *sandbox.State
	result    *Result
	err       error
	verifying bool
	ack       chan error
}

func (c *Coordinator) prepare(task *TaskRun) (*work, error) {
	var box *sandbox.Sandbox
	var err error
	if len(task.Attempts) > 0 {
		last := task.Attempts[len(task.Attempts)-1]
		// Preserve failed edits for a bounded repair. Conflicts and interrupted
		// actions start from the latest accepted workspace after reconciliation.
		if last.Failure != nil && last.Failure.Repairable && last.Failure.Kind != "conflict" && last.Result != nil {
			artifact, loadErr := c.Store.LoadArtifact(c.Run.ID, last.ID)
			if loadErr != nil {
				return nil, loadErr
			}
			box, err = sandbox.RestoreSandbox(c.box.Workspace.Root, c.Options.Policy, artifact.Sandbox)
		}
	}
	if box == nil && err == nil {
		box, err = sandbox.NewSandbox(c.box.Workspace.Root, c.Options.Policy)
	}
	if err != nil {
		return nil, err
	}
	attempt := Attempt{ID: c.Store.NewID(), Number: len(task.Attempts) + 1, State: Running}
	if task.Task.Review {
		attempt.ReviewID = c.Store.NewID()
	}
	task.Attempts = append(task.Attempts, attempt)
	if err = c.taskTransition(task, Running, "dispatch"); err != nil {
		box.Close()
		return nil, err
	}
	dependencies := make(map[string]*Result)
	for _, dep := range task.Task.Dependencies {
		for _, other := range c.Run.Tasks {
			if other.Task.ID == dep && len(other.Attempts) > 0 {
				dependencies[dep] = other.Attempts[len(other.Attempts)-1].Result
			}
		}
	}
	input, _ := json.Marshal(map[string]any{"objective": c.Run.Objective, "task": task.Task, "dependencies": dependencies, "feedback": task.Feedback})
	prompt := "Perform only this approved task. You cannot delegate, expand permissions, apply changes, or mark the run complete. Return only JSON with summary (nonempty string), artifacts (workspace-relative file paths), and unresolved (array of strings). Report remaining work honestly. Input:\n" + string(input)
	return &work{task: task.Task, attempt: attempt, box: box, prompt: prompt}, nil
}

func (c *Coordinator) perform(ctx context.Context, job *work, events chan<- outcome) outcome {
	defer job.box.Close()
	out := outcome{taskID: job.task.ID}
	a := c.agent(ctx, job.box, job.task.AllowedTools, job.task.WriteScope)
	a.Checkpoint = c.checkpoint(a, job.box, job.attempt.ID)
	text, err := a.RunContext(ctx, job.prompt)
	if err != nil {
		out.err = err
		return out
	}
	result := &Result{}
	for correction := 0; correction < 3; correction++ {
		result = &Result{}
		err = decode(text, result)
		if err == nil && strings.TrimSpace(result.Summary) == "" {
			err = fmt.Errorf("result needs a nonempty summary")
		}
		if err == nil {
			for _, path := range result.Artifacts {
				if !cleanPath(path) || path == "." {
					err = fmt.Errorf("invalid artifact path %q", path)
					break
				}
			}
		}
		if err == nil {
			break
		}
		if correction == 2 {
			out.err = &Failure{Kind: "format", Message: err.Error()}
			return out
		}
		// Format corrections cannot execute further tools.
		a.Tools.Restrict(nil)
		text, err = a.RunContext(ctx, "Correct the final Result JSON only: "+err.Error())
		if err != nil {
			out.err = err
			return out
		}
	}
	out.result = result
	result.Checks = nil
	out.state, err = job.box.ExportState()
	if err != nil {
		out.err = err
		return out
	}
	if err = scopeChanges(out.state, job.task.WriteScope, job.box.Policy); err != nil {
		out.err = err
		return out
	}
	for _, path := range result.Artifacts {
		if file, ok := out.state.Files[path]; !ok || !file.Mode.IsRegular() {
			out.err = &Failure{Kind: "verification", Message: "missing file artifact: " + path, Repairable: true}
			return out
		}
	}
	if len(result.Unresolved) > 0 {
		out.err = &Failure{Kind: "verification", Message: strings.Join(result.Unresolved, "; "), Repairable: true}
		return out
	}
	ack := make(chan error, 1)
	select {
	case events <- outcome{taskID: job.task.ID, verifying: true, ack: ack}:
	case <-ctx.Done():
		out.err = ctx.Err()
		return out
	}
	select {
	case err := <-ack:
		if err != nil {
			out.err = err
			return out
		}
	case <-ctx.Done():
		out.err = ctx.Err()
		return out
	}
	// Verification uses a fresh clone so test-generated files cannot enter the
	// accepted worker patch or alter subsequent acceptance checks.
	checks, err := c.verify(ctx, job.box, job.task.Checks)
	result.Checks = checks
	if err != nil {
		out.err = err
		return out
	}
	if job.task.Review {
		if err = c.review(ctx, job.box, job.task, result, job.attempt.ReviewID); err != nil {
			out.err = err
			return out
		}
	}
	return out
}

func (c *Coordinator) verify(ctx context.Context, source *sandbox.Sandbox, checks []Check) ([]CheckResult, error) {
	var results []CheckResult
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		box, err := sandbox.NewSandbox(source.Workspace.Root, c.Options.Policy)
		if err != nil {
			return results, err
		}
		if c.Options.Runner != nil {
			box.Runner = c.Options.Runner
		}
		box.Approve = func(name, args string) bool { return c.Options.Approve != nil && c.Options.Approve(ctx, name, args) }
		item := CheckResult{Check: check}
		if check.Kind == "shell" {
			if err = c.budget.reserve(0, true); err == nil {
				var result sandbox.CommandResult
				result, err = box.RunResultContext(ctx, sandbox.CommandRequest{Command: check.Command, Domains: check.Domains})
				item.Output = fmt.Sprintf("exit code: %d\n%s", result.ExitCode, result.Output)
				item.Passed = err == nil && result.ExitCode == 0
			}
		} else if check.Kind == "file_exists" {
			var state *sandbox.State
			state, err = box.ExportState()
			if err == nil {
				file, exists := state.Files[check.Path]
				item.Passed = exists && file.Mode.IsRegular()
			}
			item.Output = "checked " + check.Path
		} else {
			var content string
			content, err = box.Workspace.ReadFile(check.Path)
			item.Passed = err == nil && strings.Contains(content, check.Contains)
			item.Output = "checked " + check.Path
		}
		box.Close()
		if err != nil {
			item.Output = err.Error()
		}
		results = append(results, item)
		if !item.Passed {
			var denied *sandbox.ApprovalDeniedError
			var failure *Failure
			if errors.As(err, &denied) {
				return results, &Failure{Kind: "approval", Message: err.Error()}
			}
			if errors.As(err, &failure) || ctx.Err() != nil {
				return results, err
			}
			if check.Kind == "shell" && err != nil {
				return results, &Failure{Kind: "configuration", Message: err.Error()}
			}
			return results, &Failure{Kind: "verification", Message: fmt.Sprintf("check failed: %v; %s", check, item.Output), Repairable: true}
		}
	}
	return results, nil
}

func (c *Coordinator) review(ctx context.Context, source *sandbox.Sandbox, task Task, result *Result, attemptID string) error {
	box, err := sandbox.NewSandbox(source.Workspace.Root, c.Options.Policy)
	if err != nil {
		return err
	}
	defer box.Close()
	a := c.agent(ctx, box, []string{"read_file", "review_changes", "calculator"}, nil)
	a.Checkpoint = c.checkpoint(a, box, attemptID)
	input, _ := json.Marshal(map[string]any{"task": task, "result": result, "attempt_id": attemptID})
	prompt := "Review correctness against the acceptance criteria. Inspect actual files; use check evidence. Return only JSON {\"passed\":true,\"issues\":[]}. Input:\n" + string(input)
	for correction := 0; correction < 3; correction++ {
		text, err := a.RunContext(ctx, prompt)
		if err != nil {
			return err
		}
		var decision struct {
			Passed bool     `json:"passed"`
			Issues []string `json:"issues"`
		}
		if err = decode(text, &decision); err != nil {
			prompt = "Correct the review JSON only: " + err.Error()
			continue
		}
		if !decision.Passed || len(decision.Issues) > 0 {
			return &Failure{Kind: "verification", Message: "review rejected: " + strings.Join(decision.Issues, "; "), Repairable: true}
		}
		return nil
	}
	return &Failure{Kind: "format", Message: "reviewer did not produce valid JSON"}
}
