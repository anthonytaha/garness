package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/orchestration"
	"anthonytaha/garness/sandbox"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type approvalRequest struct {
	ctx             context.Context
	name, arguments string
	answer          chan bool
}

// The REPL alone reads stdin. Workers queue approvals and wait on their own
// contexts, so cancellation and simultaneous prompts cannot steal input.
type approvalBroker struct{ requests chan approvalRequest }

func (b *approvalBroker) approve(ctx context.Context, name, args string) bool {
	request := approvalRequest{ctx: ctx, name: name, arguments: args, answer: make(chan bool, 1)}
	select {
	case b.requests <- request:
	case <-ctx.Done():
		return false
	}
	select {
	case answer := <-request.answer:
		return answer
	case <-ctx.Done():
		return false
	}
}

type runManager struct {
	out     io.Writer
	rich    bool
	store   *orchestration.Store
	current *orchestration.Coordinator
	options orchestration.Options
}

func newRunManager(manager *sessionManager, agent *harness.Agent, limits orchestration.Limits, broker *approvalBroker, events chan<- orchestration.Event) (*runManager, error) {
	store, err := orchestration.OpenRuns(manager.store)
	if err != nil {
		return nil, err
	}
	options := orchestration.Options{Provider: agent.Provider, Model: agent.Model, SystemPrompt: agent.SystemPrompt, AgentsDir: agent.AgentsDir, Limits: limits, Policy: manager.box.Policy, Approve: broker.approve}
	options.OnEvent = func(event orchestration.Event) {
		select {
		case events <- event:
		default:
		}
	}
	options.SkillsRoots = []string{agent.AgentsDir + "/.garness/skills"}
	if root := os.Getenv("GARNESS_SKILLS_DIR"); root != "" {
		options.SkillsRoots = append(options.SkillsRoots, root)
	}
	return &runManager{store: store, options: options}, nil
}

func (m *runManager) close() {
	if m.current != nil {
		m.current.Close()
	}
}

func (m *runManager) status() error {
	if m.current == nil {
		return fmt.Errorf("no orchestration run selected")
	}
	// Atomic persisted snapshots support status queries while execution runs.
	run, err := m.store.Load(m.current.Run.ID)
	if err != nil {
		return err
	}
	if m.rich {
		fmt.Fprintln(m.output(), formatRun(run))
		return nil
	}
	fmt.Fprintf(m.output(), "Run %s: %s\n", run.ID, run.State)
	for _, task := range run.Tasks {
		fmt.Fprintf(m.output(), "  %s: %s (%d/%d attempts)\n", task.Task.ID, task.State, len(task.Attempts), task.Task.MaxAttempts)
	}
	fmt.Fprintf(m.output(), "Requests: %d; reserved tokens: %d; reported tokens: %d; tool calls: %d\n", run.Usage.Requests, run.Usage.ReservedTokens, run.Usage.ReportedTokens, run.Usage.ToolCalls)
	if run.Failure != nil {
		fmt.Fprintln(m.output(), run.Failure)
	}
	return nil
}

func (m *runManager) printPlan() {
	if m.current == nil || m.current.Run.Plan == nil {
		return
	}
	if m.rich {
		fmt.Fprintln(m.output(), formatPlan(m.current.Run))
		return
	}
	data, _ := json.MarshalIndent(m.current.Run.Plan, "", "  ")
	fmt.Fprintf(m.output(), "Run: %s\n%s\nPlan digest: %s\nApprove with :approve <digest>; execute with :execute.\n", m.current.Run.ID, data, m.current.Run.Plan.Digest())
}

// command performs idle-only mutations or returns an operation to execute in
// the REPL job goroutine. Status and cancellation are handled separately.
func (m *runManager) command(line string) (bool, func(context.Context) (string, error), error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil, nil
	}
	switch parts[0] {
	case ":plan":
		if len(parts) == 1 {
			m.printPlan()
			return true, nil, nil
		}
		objective := strings.TrimSpace(strings.TrimPrefix(line, ":plan"))
		c, err := orchestration.New(m.store, objective, m.options)
		if err != nil {
			return true, nil, err
		}
		m.close()
		m.current = c
		return true, func(ctx context.Context) (string, error) { err := c.Plan(ctx); return "", err }, nil
	case ":approve", ":reject":
		if m.current == nil || m.current.Run.Plan == nil || len(parts) != 2 {
			return true, nil, fmt.Errorf("usage: :approve|:reject <plan-digest>")
		}
		return true, nil, m.current.ApprovePlan(parts[1], parts[0] == ":approve")
	case ":execute":
		if m.current == nil {
			return true, nil, fmt.Errorf("no orchestration run selected")
		}
		c := m.current
		return true, func(ctx context.Context) (string, error) {
			err := c.Execute(ctx)
			if err != nil {
				return "", err
			}
			return "Run completed. Review with :changes and apply with :apply.", nil
		}, nil
	case ":runs":
		runs, err := m.store.List()
		if err != nil {
			return true, nil, err
		}
		for _, run := range runs {
			fmt.Fprintln(m.output(), run.ID, run.State, run.Objective)
		}
		return true, nil, nil
	case ":run-resume":
		if len(parts) != 2 {
			return true, nil, fmt.Errorf("usage: :run-resume <id>")
		}
		if m.current != nil && m.current.Run.ID == parts[1] {
			return true, nil, fmt.Errorf("run already selected")
		}
		c, err := orchestration.Resume(m.store, parts[1], m.options)
		if err != nil {
			return true, nil, err
		}
		m.close()
		m.current = c
		return true, nil, m.status()
	case ":retry":
		if m.current == nil || len(parts) < 3 {
			return true, nil, fmt.Errorf("usage: :retry <task> <reconciliation evidence>")
		}
		return true, nil, m.current.Retry(parts[1], strings.Join(parts[2:], " "))
	case ":attempt":
		if m.current == nil || len(parts) != 2 {
			return true, nil, fmt.Errorf("usage: :attempt <task>")
		}
		for _, task := range m.current.Run.Tasks {
			if task.Task.ID == parts[1] && len(task.Attempts) > 0 {
				attempt := task.Attempts[len(task.Attempts)-1]
				artifact, err := m.store.LoadArtifact(m.current.Run.ID, attempt.ID)
				if err != nil {
					return true, nil, err
				}
				box, err := sandbox.RestoreSandbox(m.store.Workspace, m.options.Policy, artifact.Sandbox)
				if err != nil {
					return true, nil, err
				}
				defer box.Close()
				review, err := box.Changes()
				if err != nil {
					return true, nil, err
				}
				fmt.Fprintln(m.output(), "Attempt:", attempt.ID, attempt.State, attempt.Failure)
				fmt.Fprintln(m.output(), review)
				data, err := json.MarshalIndent(artifact.Session.Transcript, "", "  ")
				if err == nil {
					fmt.Fprintln(m.output(), string(data))
				}
				return true, nil, err
			}
		}
		return true, nil, fmt.Errorf("no attempt for task %q", parts[1])
	case ":changes":
		if m.current == nil {
			return false, nil, nil
		}
		text, err := m.current.Changes()
		if err == nil {
			fmt.Fprintln(m.output(), text)
		}
		return true, nil, err
	case ":apply":
		if m.current == nil {
			return false, nil, nil
		}
		c := m.current
		return true, c.Apply, nil
	case ":run-close":
		m.close()
		m.current = nil
		return true, nil, nil
	case ":reconcile-apply":
		if m.current == nil {
			return true, nil, fmt.Errorf("no orchestration run selected")
		}
		return true, nil, m.current.ReconcileApply()
	}
	return false, nil, nil
}

func (m *runManager) output() io.Writer {
	if m.out != nil {
		return m.out
	}
	return os.Stdout
}
