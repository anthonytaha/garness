package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/orchestration"
	"anthonytaha/garness/sandbox"
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

type jobResult struct {
	reply    string
	err      error
	planning bool
}

func runREPL(scanner *bufio.Scanner, agent *harness.Agent, box *sandbox.Sandbox, manager *sessionManager, limits orchestration.Limits) {
	fmt.Println("Welcome to Garness:")
	fmt.Println("Orchestration: :plan <objective>, :approve <digest>, :execute, :run-status, :cancel, :runs, :run-resume <id>, :attempt <task>, :retry <task> [evidence], :reconcile-apply, :run-close")
	broker := &approvalBroker{requests: make(chan approvalRequest)}
	events := make(chan orchestration.Event, 256)
	runs, err := newRunManager(manager, agent, limits, broker, events)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer runs.close()
	inputs := make(chan string)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		defer close(inputs)
		for scanner.Scan() {
			select {
			case inputs <- scanner.Text():
			case <-finished:
				return
			}
		}
	}()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	done := make(chan jobResult, 1)
	var cancel context.CancelFunc
	var pending *approvalRequest
	var queued []string
	start := func(operation func(context.Context) (string, error), planning bool) {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		agent.Approve = func(name, args string) bool { return broker.approve(ctx, name, args) }
		go func() { reply, err := operation(ctx); done <- jobResult{reply: reply, err: err, planning: planning} }()
	}
	printError := func(err error) {
		if err != nil {
			fmt.Println("error:", err)
		}
	}
	fmt.Print("you> ")
	for {
		if inputs == nil && cancel == nil && len(queued) == 0 {
			printError(scanner.Err())
			fmt.Println()
			return
		}
		requests := broker.requests
		if pending != nil {
			requests = nil
		}
		var line string
		var ok bool
		if cancel == nil && len(queued) > 0 {
			line, ok = queued[0], true
			queued = queued[1:]
		} else {
			select {
			case request := <-requests:
				if request.ctx.Err() != nil || inputs == nil && len(queued) == 0 {
					request.answer <- false
					continue
				}
				pending = &request
				fmt.Printf("  approve %s(%s)? [y/N] ", request.name, request.arguments)
				if len(queued) > 0 {
					answer := strings.ToLower(strings.TrimSpace(queued[0]))
					queued = queued[1:]
					pending.answer <- answer == "y" || answer == "yes"
					pending = nil
				}
				continue
			case <-interrupts:
				if cancel != nil {
					cancel()
					fmt.Println("Cancelling active work...")
				}
				if pending != nil {
					pending.answer <- false
					pending = nil
				}
				continue
			case event := <-events:
				if event.TaskID == "" {
					fmt.Println("run>", event.To, event.Detail)
				} else {
					fmt.Println("task>", event.TaskID, event.To, event.Detail)
				}
				continue
			case result := <-done:
				cancel()
				cancel = nil
				if pending != nil {
					pending.answer <- false
					pending = nil
				}
				printError(result.err)
				if result.reply != "" {
					fmt.Println("bot>", result.reply)
				}
				if result.planning && result.err == nil {
					runs.printPlan()
				}
				if inputs != nil {
					fmt.Print("you> ")
				}
				continue
			case line, ok = <-inputs:
			}
		}
		if !ok {
			inputs = nil
			if pending != nil {
				pending.answer <- false
				pending = nil
			}
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == ":cancel" {
			if cancel != nil {
				cancel()
				fmt.Println("Cancelling active work...")
			} else if runs.current != nil {
				printError(runs.current.Cancel())
			} else {
				fmt.Println("No active work")
			}
			if pending != nil {
				pending.answer <- false
				pending = nil
			}
			continue
		}
		if line == ":run-status" {
			printError(runs.status())
			continue
		}
		if pending != nil {
			answer := strings.ToLower(line)
			pending.answer <- answer == "y" || answer == "yes"
			pending = nil
			continue
		}
		if cancel != nil {
			if len(queued) < 128 {
				queued = append(queued, line)
			} else {
				fmt.Println("error: input queue is full; wait for active work to finish")
			}
			continue
		}
		handled, operation, err := runs.command(line)
		if handled {
			printError(err)
			if err == nil && operation != nil {
				start(operation, strings.HasPrefix(line, ":plan "))
			} else {
				fmt.Print("you> ")
			}
			continue
		}
		if strings.HasPrefix(line, ":") && manager.command(line, scanner) {
			fmt.Print("you> ")
			continue
		}
		if line == ":changes" {
			result, err := manager.box.Changes()
			printError(err)
			if err == nil {
				fmt.Println(result)
			}
			fmt.Print("you> ")
			continue
		}
		if line == ":apply" {
			start(func(ctx context.Context) (string, error) {
				manager.box.Approve = func(name, args string) bool { return agent.Approve != nil && agent.Approve(name, args) }
				return manager.box.ApplyChanges()
			}, false)
			continue
		}
		if strings.HasPrefix(line, ":") {
			fmt.Println("error: unknown command")
			continue
		}
		start(func(ctx context.Context) (string, error) { return agent.RunContext(ctx, line) }, false)
	}
}
