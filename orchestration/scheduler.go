package orchestration

import (
	"context"
	"fmt"
	"time"
)

func (c *Coordinator) dependenciesDone(task *TaskRun) bool {
	for _, dep := range task.Task.Dependencies {
		for _, other := range c.Run.Tasks {
			if other.Task.ID == dep && other.State != Succeeded {
				return false
			}
		}
	}
	return true
}

func (c *Coordinator) task(id string) *TaskRun {
	for i := range c.Run.Tasks {
		if c.Run.Tasks[i].Task.ID == id {
			return &c.Run.Tasks[i]
		}
	}
	return nil
}

func (c *Coordinator) Execute(parent context.Context) error {
	if c.Run.Plan == nil || c.Run.Approval == nil || !c.Run.Approval.Approved || c.Run.Approval.Digest != c.Run.Plan.Digest() {
		return fmt.Errorf("execution requires approval of the exact current plan")
	}
	if c.Run.State != AwaitingApproval && c.Run.State != Blocked {
		return fmt.Errorf("run cannot execute from %s", c.Run.State)
	}
	for _, task := range c.Run.Tasks {
		if task.State == Blocked {
			return fmt.Errorf("reconcile blocked task %s with Retry before execution", task.Task.ID)
		}
	}
	if c.Run.Deadline.IsZero() {
		c.Run.Deadline = time.Now().Add(time.Duration(c.Run.Plan.Limits.TimeoutSeconds) * time.Second).UTC()
	}
	ctx, cancel := context.WithDeadline(parent, c.Run.Deadline)
	defer cancel()
	if err := c.transition(Executing, "approved plan dispatched"); err != nil {
		return err
	}
	c.Run.Failure = nil
	if err := c.save(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return c.stop(err)
	}
	events := make(chan outcome, c.Run.Plan.Limits.Workers*2)
	active := 0
	var fatal error
	for {
		if fatal == nil {
			if err := ctx.Err(); err != nil {
				fatal = err
				cancel()
			}
		}
		if fatal == nil {
			for i := range c.Run.Tasks {
				task := &c.Run.Tasks[i]
				if (task.State == Pending || task.State == RetryWait) && c.dependenciesDone(task) {
					if err := c.taskTransition(task, Ready, "dependencies satisfied"); err != nil {
						fatal = err
						break
					}
				}
				if active >= c.Run.Plan.Limits.Workers || task.State != Ready {
					continue
				}
				if len(task.Attempts) >= task.Task.MaxAttempts {
					fatal = &Failure{Kind: "attempts", Message: "attempt limit exhausted for " + task.Task.ID}
					break
				}
				job, err := c.prepare(task)
				if err != nil {
					fatal = err
					break
				}
				// Dispatch intent and the initial workspace are durable before a
				// worker can issue its first request or perform any tool action.
				state, err := job.box.ExportState()
				if err == nil {
					err = c.Store.SaveArtifact(c.Run.ID, job.attempt.ID, &Artifact{Sandbox: state})
				}
				if err == nil {
					err = c.save()
				}
				if err != nil {
					job.box.Close()
					fatal = err
					break
				}
				active++
				go func(job *work) { events <- c.perform(ctx, job, events) }(job)
			}
		}
		if fatal != nil {
			cancel()
		}
		if active == 0 {
			if fatal != nil {
				return c.stop(fatal)
			}
			allDone := true
			for _, task := range c.Run.Tasks {
				if task.State != Succeeded {
					allDone = false
				}
			}
			if !allDone {
				return c.stop(&Failure{Kind: "dependency", Message: "no runnable tasks; inspect blocked dependencies"})
			}
			if err := c.transition(Verifying, "checking combined workspace"); err != nil {
				return err
			}
			if err := c.save(); err != nil {
				return err
			}
			checks, err := c.verify(ctx, c.box, c.Run.Plan.FinalChecks)
			c.Run.FinalChecks = checks
			if err != nil {
				failure := classify(err)
				if failure.Repairable && len(checks) > 0 {
					task := c.task(checks[len(checks)-1].Check.TaskID)
					if task != nil && len(task.Attempts) < task.Task.MaxAttempts {
						if err = c.invalidate(task.Task.ID, failure.Message); err != nil {
							return c.stop(err)
						}
						if err = c.transition(Executing, "repair combined verification failure"); err != nil {
							return err
						}
						if err = c.save(); err != nil {
							return err
						}
						continue
					}
					return c.stop(&Failure{Kind: "attempts", Message: "final verification failed and repair limit exhausted: " + failure.Message})
				}
				return c.stop(err)
			}
			if err = ctx.Err(); err != nil {
				return c.stop(err)
			}
			if err = c.transition(Completed, "all tasks and combined checks passed; changes await final review"); err != nil {
				return err
			}
			return c.save()
		}
		var out outcome
		// Once cancellation is recorded, drain worker outcomes instead of
		// repeatedly selecting an already-closed context channel.
		if fatal != nil {
			out = <-events
		} else {
			select {
			case out = <-events:
			case <-ctx.Done():
				fatal = ctx.Err()
				cancel()
				continue
			}
		}
		task := c.task(out.taskID)
		if out.verifying {
			err := c.taskTransition(task, Verifying, "worker output ready for checks")
			if err == nil {
				task.Attempts[len(task.Attempts)-1].State = Verifying
				err = c.save()
			}
			out.ack <- err
			if err != nil {
				fatal = err
			}
			continue
		}
		active--
		if fatal != nil && out.err == nil {
			out.err = ctx.Err()
		}
		if err := c.accept(task, out); err != nil && fatal == nil {
			fatal = err
		}
		if err := c.save(); err != nil && fatal == nil {
			fatal = err
		}
	}
}

func (c *Coordinator) accept(task *TaskRun, out outcome) error {
	attempt := &task.Attempts[len(task.Attempts)-1]
	attempt.Result = out.result
	if out.err == nil {
		if task.State == Running {
			if err := c.taskTransition(task, Verifying, "accepting worker result"); err != nil {
				return err
			}
		}
		if err := c.box.Integrate(out.state); err != nil {
			out.err = &Failure{Kind: "conflict", Message: err.Error(), Repairable: true}
		}
	}
	if out.err == nil {
		attempt.State = Succeeded
		return c.taskTransition(task, Succeeded, "verified changes integrated")
	}
	failure := classify(out.err)
	attempt.Failure = failure
	task.Feedback = failure.Error()
	if failure.Repairable && len(task.Attempts) < task.Task.MaxAttempts {
		attempt.State = Failed
		return c.taskTransition(task, RetryWait, failure.Message)
	}
	if failure.Repairable || failure.Kind == "format" {
		attempt.State = Failed
		if err := c.taskTransition(task, Failed, failure.Message); err != nil {
			return err
		}
		return &Failure{Kind: "attempts", Message: "task " + task.Task.ID + " failed: " + failure.Message}
	}
	attempt.State = Blocked
	if err := c.taskTransition(task, Blocked, failure.Message); err != nil {
		return err
	}
	return failure
}

func (c *Coordinator) invalidate(id, feedback string) error {
	invalid := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, task := range c.Run.Tasks {
			for _, dep := range task.Task.Dependencies {
				if invalid[dep] && !invalid[task.Task.ID] {
					invalid[task.Task.ID] = true
					changed = true
				}
			}
		}
	}
	for i := range c.Run.Tasks {
		task := &c.Run.Tasks[i]
		if !invalid[task.Task.ID] {
			continue
		}
		if len(task.Attempts) >= task.Task.MaxAttempts {
			return &Failure{Kind: "attempts", Message: "dependent repair limit exhausted for " + task.Task.ID}
		}
	}
	for i := range c.Run.Tasks {
		task := &c.Run.Tasks[i]
		if invalid[task.Task.ID] {
			task.Feedback = "Combined verification failed; repair the current accepted workspace and recheck dependent work: " + feedback
			if err := c.taskTransition(task, Pending, task.Feedback); err != nil {
				return err
			}
		}
	}
	return nil
}
