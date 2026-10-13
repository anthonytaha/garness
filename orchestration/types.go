package orchestration

import (
	"anthonytaha/garness/sandbox"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type State string

const (
	Planning         State = "planning"
	AwaitingApproval State = "awaiting_approval"
	Executing        State = "executing"
	Verifying        State = "verifying"
	Completed        State = "completed"
	Blocked          State = "blocked"
	Failed           State = "failed"
	Cancelled        State = "cancelled"
	Pending          State = "pending"
	Ready            State = "ready"
	Running          State = "running"
	Succeeded        State = "succeeded"
	RetryWait        State = "retry_wait"
)

type Limits struct {
	Workers           int `json:"workers"`
	MaxRequests       int `json:"max_requests"`
	MaxReservedTokens int `json:"max_reserved_tokens"`
	MaxToolCalls      int `json:"max_tool_calls"`
	MaxToolSteps      int `json:"max_tool_steps"`
	MaxOutputTokens   int `json:"max_output_tokens"`
	TimeoutSeconds    int `json:"timeout_seconds"`
}

func DefaultLimits() Limits {
	return Limits{Workers: 2, MaxRequests: 128, MaxReservedTokens: 2_000_000, MaxToolCalls: 256, MaxToolSteps: 32, MaxOutputTokens: 8192, TimeoutSeconds: 1800}
}

func (l Limits) Validate() error {
	if l.Workers < 1 || l.Workers > 16 || l.MaxRequests < 1 || l.MaxReservedTokens < 1 || l.MaxToolCalls < 1 || l.MaxToolSteps < 1 || l.MaxOutputTokens < 1 || l.TimeoutSeconds < 1 || l.TimeoutSeconds > 86400 {
		return fmt.Errorf("invalid orchestration limits")
	}
	return nil
}

type Check struct {
	Kind     string   `json:"kind"`
	Path     string   `json:"path,omitempty"`
	Contains string   `json:"contains,omitempty"`
	Command  string   `json:"command,omitempty"`
	Domains  []string `json:"domains,omitempty"`
	TaskID   string   `json:"task_id,omitempty"`
}

type Task struct {
	ID                 string   `json:"id"`
	Role               string   `json:"role"`
	Instructions       string   `json:"instructions"`
	Dependencies       []string `json:"dependencies"`
	AllowedTools       []string `json:"allowed_tools"`
	WriteScope         []string `json:"write_scope"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Checks             []Check  `json:"checks"`
	MaxAttempts        int      `json:"max_attempts"`
	Review             bool     `json:"review"`
}

type Plan struct {
	Version     int     `json:"version"`
	Tasks       []Task  `json:"tasks"`
	FinalChecks []Check `json:"final_checks"`
	Limits      Limits  `json:"limits"`
}

var taskIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var capabilities = map[string]bool{"calculator": true, "read_file": true, "write_file": true, "edit_file": true, "append_file": true, "shell": true, "review_changes": true, "load_skill": true, "read_skill_resource": true}

func cleanPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, "../")
}

func (p *Plan) Validate() error {
	if p.Version < 1 || len(p.Tasks) == 0 || len(p.Tasks) > 64 || len(p.FinalChecks) == 0 {
		return fmt.Errorf("plan requires a version, 1–64 tasks, and final checks")
	}
	if err := p.Limits.Validate(); err != nil {
		return err
	}
	tasks := make(map[string]Task)
	for _, task := range p.Tasks {
		if !taskIDPattern.MatchString(task.ID) || tasks[task.ID].ID != "" {
			return fmt.Errorf("invalid or duplicate task ID %q", task.ID)
		}
		if task.Role != "worker" && task.Role != "reviewer" {
			return fmt.Errorf("invalid role for %s", task.ID)
		}
		if strings.TrimSpace(task.Instructions) == "" || len(task.AcceptanceCriteria) == 0 || len(task.Checks) == 0 || task.MaxAttempts < 1 || task.MaxAttempts > 3 {
			return fmt.Errorf("task %s requires instructions, criteria, checks, and 1–3 attempts", task.ID)
		}
		seen := map[string]bool{}
		for _, tool := range task.AllowedTools {
			if !capabilities[tool] || seen[tool] {
				return fmt.Errorf("invalid capability %q for %s", tool, task.ID)
			}
			seen[tool] = true
			if task.Role == "reviewer" && tool != "read_file" && tool != "calculator" && tool != "review_changes" {
				return fmt.Errorf("reviewer %s must be read-only", task.ID)
			}
		}
		if task.Role == "reviewer" && len(task.WriteScope) != 0 {
			return fmt.Errorf("reviewer %s cannot write", task.ID)
		}
		for _, path := range task.WriteScope {
			if !cleanPath(path) {
				return fmt.Errorf("invalid write scope %q", path)
			}
			for _, part := range strings.Split(path, "/") {
				if part == ".git" || part == ".garness" || part == ".agents" || part == ".codex" || part == "AGENTS.md" || part == ".env" || strings.HasPrefix(part, ".env.") && part != ".env.example" {
					return fmt.Errorf("protected write scope %q", path)
				}
			}
		}
		if task.Role == "worker" && (seen["write_file"] || seen["edit_file"] || seen["append_file"] || seen["shell"]) && len(task.WriteScope) == 0 {
			return fmt.Errorf("mutating task %s needs a write scope", task.ID)
		}
		for _, check := range task.Checks {
			if err := validateCheck(check); err != nil {
				return fmt.Errorf("task %s: %w", task.ID, err)
			}
		}
		tasks[task.ID] = task
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		task, ok := tasks[id]
		if !ok {
			return fmt.Errorf("unknown dependency %s", id)
		}
		visiting[id] = true
		seen := map[string]bool{}
		for _, dep := range task.Dependencies {
			if seen[dep] {
				return fmt.Errorf("duplicate dependency %s", dep)
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id], visited[id] = false, true
		return nil
	}
	for id := range tasks {
		if err := visit(id); err != nil {
			return err
		}
	}
	for _, check := range p.FinalChecks {
		if err := validateCheck(check); err != nil {
			return err
		}
		if tasks[check.TaskID].ID == "" {
			return fmt.Errorf("final check must identify a repair task")
		}
	}
	return nil
}

func validateCheck(c Check) error {
	switch c.Kind {
	case "file_exists", "file_contains":
		if !cleanPath(c.Path) || c.Path == "." || len(c.Domains) != 0 {
			return fmt.Errorf("invalid file check")
		}
		if c.Kind == "file_contains" && c.Contains == "" {
			return fmt.Errorf("file_contains requires nonempty contains")
		}
	case "shell":
		if strings.TrimSpace(c.Command) == "" {
			return fmt.Errorf("shell check requires command")
		}
	default:
		return fmt.Errorf("unknown check kind %q", c.Kind)
	}
	return nil
}

func (p Plan) Digest() string {
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

type Failure struct {
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Repairable bool   `json:"repairable"`
	Ambiguous  bool   `json:"ambiguous"`
}

func (e *Failure) Error() string { return e.Kind + ": " + e.Message }

type Result struct {
	Summary    string        `json:"summary"`
	Artifacts  []string      `json:"artifacts"`
	Checks     []CheckResult `json:"checks,omitempty"`
	Unresolved []string      `json:"unresolved"`
}

type CheckResult struct {
	Check  Check  `json:"check"`
	Passed bool   `json:"passed"`
	Output string `json:"output"`
}

type Attempt struct {
	ID       string   `json:"id"`
	ReviewID string   `json:"review_id,omitempty"`
	Number   int      `json:"number"`
	State    State    `json:"state"`
	Failure  *Failure `json:"failure,omitempty"`
	Result   *Result  `json:"result,omitempty"`
}

type TaskRun struct {
	Task     Task      `json:"task"`
	State    State     `json:"state"`
	Attempts []Attempt `json:"attempts,omitempty"`
	Feedback string    `json:"feedback,omitempty"`
}

type Approval struct {
	Digest   string    `json:"digest"`
	Approved bool      `json:"approved"`
	Time     time.Time `json:"time"`
}

type Usage struct {
	Requests       int `json:"requests"`
	ReservedTokens int `json:"reserved_tokens"`
	ReportedTokens int `json:"reported_tokens"`
	ToolCalls      int `json:"tool_calls"`
}

type Event struct {
	RunID     string    `json:"run_id"`
	TaskID    string    `json:"task_id,omitempty"`
	AttemptID string    `json:"attempt_id,omitempty"`
	From      State     `json:"from"`
	To        State     `json:"to"`
	Detail    string    `json:"detail,omitempty"`
	Time      time.Time `json:"time"`
}

type Run struct {
	Version         int            `json:"version"`
	ID              string         `json:"id"`
	Objective       string         `json:"objective"`
	Workspace       string         `json:"workspace"`
	State           State          `json:"state"`
	Plan            *Plan          `json:"plan,omitempty"`
	PlannerAttempts []string       `json:"planner_attempts,omitempty"`
	Tasks           []TaskRun      `json:"tasks,omitempty"`
	Approval        *Approval      `json:"approval,omitempty"`
	Usage           Usage          `json:"usage"`
	Events          []Event        `json:"events,omitempty"`
	Working         *sandbox.State `json:"working"`
	Created         time.Time      `json:"created"`
	Deadline        time.Time      `json:"deadline,omitempty"`
	Failure         *Failure       `json:"failure,omitempty"`
	FinalChecks     []CheckResult  `json:"final_checks,omitempty"`
	Applied         bool           `json:"applied"`
	Applying        bool           `json:"applying"`
}

var runTransitions = map[State][]State{
	Planning:         {AwaitingApproval, Failed, Cancelled, Blocked},
	AwaitingApproval: {Planning, Executing, Blocked, Cancelled},
	Executing:        {Verifying, Blocked, Failed, Cancelled},
	Verifying:        {Executing, Completed, Blocked, Failed, Cancelled},
	Blocked:          {Planning, AwaitingApproval, Executing, Cancelled},
	Failed:           {Planning, Cancelled},
	Cancelled:        {Planning, Blocked},
}

var taskTransitions = map[State][]State{
	Pending:   {Ready, Blocked, Cancelled},
	Ready:     {Running, Blocked, Cancelled},
	Running:   {Verifying, RetryWait, Blocked, Failed, Cancelled},
	Verifying: {Succeeded, RetryWait, Blocked, Failed, Cancelled},
	RetryWait: {Ready, Pending, Failed, Blocked, Cancelled},
	Blocked:   {Pending, Ready, Cancelled},
	Succeeded: {Pending, RetryWait},
}

func validTransition(table map[State][]State, from, to State) bool {
	for _, next := range table[from] {
		if next == to {
			return true
		}
	}
	return false
}
