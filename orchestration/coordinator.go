package orchestration

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Provider     model.Provider
	Model        string
	SystemPrompt string
	AgentsDir    string
	SkillsRoots  []string
	Limits       Limits
	Policy       sandbox.SandboxPolicy
	Approve      func(context.Context, string, string) bool
	OnEvent      func(Event)
	Runner       sandbox.CommandRunner
}

// Coordinator is owned by one control loop. Workers never mutate Run; they
// return outcomes through channels. Call control methods only while idle.
type Coordinator struct {
	Run     *Run
	Store   *Store
	Options Options
	box     *sandbox.Sandbox
	budget  *budget
	unlock  func()
}

func normalizeOptions(options Options) Options {
	if options.Limits == (Limits{}) {
		options.Limits = DefaultLimits()
	}
	if options.Policy.Profile == "" {
		options.Policy = sandbox.DefaultSandboxPolicy("isolated")
	}
	options.Policy.Profile = "isolated"
	return options
}

func New(store *Store, objective string, options Options) (*Coordinator, error) {
	options = normalizeOptions(options)
	if strings.TrimSpace(objective) == "" {
		return nil, fmt.Errorf("objective must not be empty")
	}
	if err := options.Limits.Validate(); err != nil {
		return nil, err
	}
	id := store.NewID()
	unlock, err := store.Lock(id)
	if err != nil {
		return nil, err
	}
	box, err := sandbox.NewSandbox(store.Workspace, options.Policy)
	if err != nil {
		unlock()
		return nil, err
	}
	state, err := box.ExportState()
	if err != nil {
		box.Close()
		unlock()
		return nil, err
	}
	run := &Run{Version: 1, ID: id, Objective: objective, Workspace: store.Workspace, State: Planning, Working: state, Created: time.Now().UTC()}
	c := &Coordinator{Run: run, Store: store, Options: options, box: box, unlock: unlock}
	c.budget = &budget{store: store, id: id, limits: options.Limits}
	if err = c.save(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func Resume(store *Store, id string, options Options) (*Coordinator, error) {
	unlock, err := store.Lock(id)
	if err != nil {
		return nil, err
	}
	run, err := store.Load(id)
	if err != nil {
		unlock()
		return nil, err
	}
	options = normalizeOptions(options)
	box, err := sandbox.RestoreSandbox(store.Workspace, options.Policy, run.Working)
	if err != nil {
		unlock()
		return nil, err
	}
	usage, err := store.loadUsage(id)
	if err != nil {
		box.Close()
		unlock()
		return nil, err
	}
	limits := options.Limits
	if run.Plan != nil {
		limits = run.Plan.Limits
	}
	c := &Coordinator{Run: run, Store: store, Options: options, box: box, unlock: unlock, budget: &budget{store: store, id: id, limits: limits, usage: usage}}
	for i := range run.Tasks {
		task := &run.Tasks[i]
		if task.State == Running || task.State == Verifying {
			failure := &Failure{Kind: "interrupted", Message: "execution status is unknown; inspect the saved attempt and reconcile effects before retrying", Ambiguous: true}
			if len(task.Attempts) > 0 {
				a := &task.Attempts[len(task.Attempts)-1]
				a.State = Blocked
				a.Failure = failure
			}
			if err = c.taskTransition(task, Blocked, failure.Message); err != nil {
				c.Close()
				return nil, err
			}
		}
	}
	if run.State == Executing || run.State == Verifying || run.State == Planning || run.State == Cancelled {
		if err = c.transition(Blocked, "run interrupted; accepted workspace restored without replaying actions"); err != nil {
			c.Close()
			return nil, err
		}
	}
	if run.Applying {
		run.Failure = &Failure{Kind: "interrupted", Message: "final copy-back was interrupted; inspect original files and reconcile before applying again", Ambiguous: true}
	}
	if err = c.save(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Coordinator) Close() error {
	err := c.box.Close()
	if c.unlock != nil {
		c.unlock()
		c.unlock = nil
	}
	return err
}

func (c *Coordinator) save() error {
	state, err := c.box.ExportState()
	if err != nil {
		return err
	}
	c.Run.Working = state
	c.Run.Usage, err = c.budget.checkpoint()
	if err != nil {
		return err
	}
	return c.Store.Save(c.Run)
}

func (c *Coordinator) event(taskID, attemptID string, from, to State, detail string) {
	event := Event{RunID: c.Run.ID, TaskID: taskID, AttemptID: attemptID, From: from, To: to, Detail: detail, Time: time.Now().UTC()}
	c.Run.Events = append(c.Run.Events, event)
	if c.Options.OnEvent != nil {
		c.Options.OnEvent(event)
	}
}
func (c *Coordinator) transition(to State, detail string) error {
	if !validTransition(runTransitions, c.Run.State, to) {
		return fmt.Errorf("invalid run transition %s → %s", c.Run.State, to)
	}
	from := c.Run.State
	c.Run.State = to
	c.event("", "", from, to, detail)
	return nil
}
func (c *Coordinator) taskTransition(task *TaskRun, to State, detail string) error {
	if !validTransition(taskTransitions, task.State, to) {
		return fmt.Errorf("invalid task transition %s → %s", task.State, to)
	}
	from := task.State
	task.State = to
	attempt := ""
	if len(task.Attempts) > 0 {
		attempt = task.Attempts[len(task.Attempts)-1].ID
	}
	c.event(task.Task.ID, attempt, from, to, detail)
	return nil
}

func decode(text string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected a single JSON object")
	}
	return nil
}

// SetPlan validates a proposed plan and invalidates any earlier approval.
// Replanning is allowed before task execution; started runs require a new run
// so previously accepted work cannot silently acquire a different scope.
func (c *Coordinator) SetPlan(plan Plan) error {
	for _, task := range c.Run.Tasks {
		if len(task.Attempts) > 0 {
			return fmt.Errorf("create a new run to change a started plan")
		}
	}
	if c.Run.Plan != nil && plan.Version <= c.Run.Plan.Version {
		return fmt.Errorf("plan version must increase")
	}
	if plan.Limits != c.Options.Limits {
		return fmt.Errorf("plan must use configured limits")
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if c.Run.State != Planning {
		if err := c.transition(Planning, "revised plan"); err != nil {
			return err
		}
	}
	c.Run.Plan, c.Run.Approval, c.Run.Failure = &plan, nil, nil
	c.Run.Tasks = nil
	for _, task := range plan.Tasks {
		c.Run.Tasks = append(c.Run.Tasks, TaskRun{Task: task, State: Pending})
	}
	if err := c.transition(AwaitingApproval, "plan "+plan.Digest()); err != nil {
		return err
	}
	return c.save()
}

func (c *Coordinator) Plan(ctx context.Context) error {
	for _, task := range c.Run.Tasks {
		if len(task.Attempts) > 0 {
			return fmt.Errorf("create a new run to change a started plan")
		}
	}
	if c.Run.State != Planning {
		if err := c.transition(Planning, "planning requested"); err != nil {
			return err
		}
	}
	if err := c.save(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Options.Limits.TimeoutSeconds)*time.Second)
	defer cancel()
	box, err := sandbox.NewSandbox(c.box.Workspace.Root, c.Options.Policy)
	if err != nil {
		return err
	}
	defer box.Close()
	a := c.agent(ctx, box, []string{"read_file", "calculator"}, nil)
	version := 1
	if c.Run.Plan != nil {
		version = c.Run.Plan.Version + 1
	}
	limits, _ := json.Marshal(c.Options.Limits)
	example := `{"version":1,"tasks":[{"id":"implement","role":"worker","instructions":"Implement the requested change","dependencies":[],"allowed_tools":["read_file","write_file","edit_file","shell"],"write_scope":["src"],"acceptance_criteria":["Requested behavior works"],"checks":[{"kind":"shell","command":"go test ./..."}],"max_attempts":3,"review":true}],"final_checks":[{"kind":"shell","command":"go test ./...","task_id":"implement"}],"limits":{}}`
	prompt := fmt.Sprintf("Plan this objective: %s\nInspect files with read_file as needed. Return only a JSON Plan, without markdown. Example shape: %s\nUse version %d and these exact limits: %s. Use real repository paths and relevant checks. Each task needs concrete checks, dependencies, explicit tool permissions and write scopes. Allowed tools: calculator, read_file, write_file, edit_file, append_file, shell, review_changes, load_skill, read_skill_resource. Reviewers are read-only. Checks are file_exists, file_contains (contains text), or shell (command and optional exact domains). Final checks must name a task_id to repair. At most 64 tasks; at most 3 attempts per task. No recursive delegation.", c.Run.Objective, example, version, limits)
	var paths []string
	for path := range c.Run.Working.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	manifest, _ := json.Marshal(paths)
	prompt += "\nWorkspace paths: " + string(manifest)
	attemptID := c.Store.NewID()
	c.Run.PlannerAttempts = append(c.Run.PlannerAttempts, attemptID)
	if err := c.save(); err != nil {
		return err
	}
	a.Checkpoint = c.checkpoint(a, box, attemptID)
	for correction := 0; correction < 3; correction++ {
		text, runErr := a.RunContext(ctx, prompt)
		if runErr != nil {
			return c.stop(runErr)
		}
		var plan Plan
		err = decode(text, &plan)
		if err == nil && plan.Version != version {
			err = fmt.Errorf("expected version %d", version)
		}
		if err == nil {
			err = c.SetPlan(plan)
		}
		if err == nil {
			return nil
		}
		prompt = "Correct your Plan JSON. Validation error: " + err.Error() + ". Return only the complete corrected JSON."
	}
	return c.stop(&Failure{Kind: "format", Message: "planner did not produce a valid plan after three responses"})
}

func (c *Coordinator) ApprovePlan(digest string, approved bool) error {
	if c.Run.State == Blocked && c.Run.Plan != nil && (c.Run.Approval == nil || !c.Run.Approval.Approved) {
		if digest != c.Run.Plan.Digest() {
			return fmt.Errorf("approval digest does not match")
		}
		if err := c.transition(AwaitingApproval, "approval reconsidered"); err != nil {
			return err
		}
	}
	if c.Run.State != AwaitingApproval || c.Run.Plan == nil || digest != c.Run.Plan.Digest() {
		return fmt.Errorf("approval must match the current pending plan digest")
	}
	c.Run.Approval = &Approval{Digest: digest, Approved: approved, Time: time.Now().UTC()}
	if !approved {
		if err := c.transition(Blocked, "plan approval denied"); err != nil {
			return err
		}
	}
	return c.save()
}

func (c *Coordinator) stop(err error) error {
	failure := classify(err)
	c.Run.Failure = failure
	state := Blocked
	if errors.Is(err, context.Canceled) || failure.Kind == "cancelled" {
		state = Cancelled
	}
	if failure.Kind == "format" || failure.Kind == "attempts" {
		state = Failed
	}
	if c.Run.State != state {
		if transitionErr := c.transition(state, failure.Message); transitionErr != nil {
			return transitionErr
		}
	}
	if saveErr := c.save(); saveErr != nil {
		return saveErr
	}
	return failure
}

func classify(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	if errors.Is(err, context.Canceled) {
		return &Failure{Kind: "cancelled", Message: err.Error(), Ambiguous: true}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Kind: "deadline", Message: err.Error(), Ambiguous: true}
	}
	if errors.Is(err, harness.ErrToolLimit) {
		return &Failure{Kind: "budget", Message: err.Error()}
	}
	var provider *model.ProviderError
	if errors.As(err, &provider) {
		return &Failure{Kind: "provider", Message: err.Error()}
	}
	return &Failure{Kind: "execution", Message: err.Error(), Ambiguous: true}
}

func (c *Coordinator) Retry(taskID, evidence string) error {
	if c.Run.State != Blocked {
		return fmt.Errorf("retry requires a blocked run")
	}
	if c.Run.Plan == nil || c.Run.Approval == nil || !c.Run.Approval.Approved || c.Run.Approval.Digest != c.Run.Plan.Digest() {
		return fmt.Errorf("retry requires an approved plan")
	}
	for i := range c.Run.Tasks {
		task := &c.Run.Tasks[i]
		if task.Task.ID != taskID {
			continue
		}
		if task.State != Blocked {
			return fmt.Errorf("task is not blocked")
		}
		if len(task.Attempts) >= task.Task.MaxAttempts {
			return fmt.Errorf("attempt limit exhausted; create a new run")
		}
		if strings.TrimSpace(evidence) == "" {
			return fmt.Errorf("provide reconciliation evidence before retrying")
		}
		task.Feedback = "User reconciled the previous attempt: " + evidence
		if err := c.taskTransition(task, Pending, task.Feedback); err != nil {
			return err
		}
		c.Run.Failure = nil
		return c.save()
	}
	return fmt.Errorf("unknown task %q", taskID)
}

func (c *Coordinator) Cancel() error {
	if c.Run.State == Completed || c.Run.State == Cancelled {
		return fmt.Errorf("run is already terminal")
	}
	return c.stop(context.Canceled)
}

func (c *Coordinator) Changes() (string, error) { return c.box.Changes() }

func (c *Coordinator) Apply(ctx context.Context) (string, error) {
	if c.Run.State != Completed || c.Run.Applied {
		return "", fmt.Errorf("only an unapplied completed run can be applied")
	}
	if c.Run.Applying {
		return "", fmt.Errorf("copy-back status is unknown; inspect original files and call ReconcileApply")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.Run.Applying = true
	if err := c.save(); err != nil {
		c.Run.Applying = false
		return "", err
	}
	c.box.Approve = func(name, args string) bool { return c.Options.Approve != nil && c.Options.Approve(ctx, name, args) }
	result, err := c.box.ApplyChanges()
	c.Run.Applying = false
	if err == nil {
		c.Run.Applied = true
	}
	if saveErr := c.save(); saveErr != nil {
		return "", saveErr
	}
	return result, err
}

func (c *Coordinator) ReconcileApply() error {
	if !c.Run.Applying {
		return fmt.Errorf("no interrupted copy-back to reconcile")
	}
	applied, err := c.box.ReconcileApply()
	if err != nil {
		return err
	}
	c.Run.Applying, c.Run.Applied, c.Run.Failure = false, applied, nil
	c.event("", "", Completed, Completed, "copy-back reconciled against reviewed file hashes")
	return c.save()
}
