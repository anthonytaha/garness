package cli

import (
	"anthonytaha/garness/orchestration"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

type commandInfo struct{ name, arguments, description string }

var commands = []commandInfo{
	{":plan", "<objective>", "Plan work with read-only agents; no argument reviews the plan"},
	{":approve", "<digest>", "Approve this exact plan version"},
	{":reject", "<digest>", "Reject this exact plan version"},
	{":execute", "", "Execute the approved plan with parallel agents"},
	{":run-status", "", "Show tasks, attempts, and run budgets"},
	{":cancel", "", "Cancel active work"},
	{":changes", "", "Review sandbox changes"},
	{":apply", "", "Request approval to copy changes back"},
	{":runs", "", "List saved orchestration runs"},
	{":run-resume", "<id>", "Restore an orchestration run"},
	{":attempt", "<task>", "Inspect a task's saved transcript and changes"},
	{":retry", "<task> <evidence>", "Reconcile and retry a blocked task"},
	{":reconcile-apply", "", "Reconcile interrupted copy-back"},
	{":run-close", "", "Close the selected run"},
	{":session", "", "Show the active session"},
	{":sessions", "", "Choose a workspace session"},
	{":new", "", "Start a new session"},
	{":resume", "<id>", "Resume a saved session"},
	{":branch", "", "Create an independent session branch"},
	{":rename", "<name>", "Rename the active session"},
	{":export", "[json|markdown] [path]", "Export the full session transcript"},
	{":delete", "<id>", "Delete an inactive session"},
	{":inspect", "", "Inspect session metadata"},
	{":tools", "[number]", "List tool activity or inspect an earlier result"},
	{":help", "", "Show commands and keyboard shortcuts"},
	{":quit", "", "Exit after cancelling active work"},
}

func commandMatches(input string) []commandInfo {
	if !strings.HasPrefix(input, ":") || strings.ContainsAny(input, " \n\t") {
		return nil
	}
	var matches []commandInfo
	var related []commandInfo
	for _, command := range commands {
		if strings.HasPrefix(command.name, input) {
			matches = append(matches, command)
		} else if strings.Contains(strings.ToLower(command.name+" "+command.description), strings.ToLower(strings.TrimPrefix(input, ":"))) {
			related = append(related, command)
		}
	}
	return append(matches, related...)
}

func commandHelp() string {
	var out strings.Builder
	out.WriteString("GARNESS commands\n\n")
	for _, command := range commands {
		fmt.Fprintf(&out, "%s %s\n  %s\n", command.name, command.arguments, command.description)
	}
	out.WriteString("\nEnter submit · Ctrl+J newline · Tab complete · Alt+Up/Down history\nCtrl+G plan actions · Ctrl+T tasks · Ctrl+O tool details · Esc return/deny\nCtrl+C cancel work or clear draft · Ctrl+D exit when idle\n")
	return out.String()
}

func formatPlan(run *orchestration.Run) string {
	if run == nil || run.Plan == nil {
		return "No plan selected"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "◆ Plan v%d · %s\n%s\nRun: %s\n", run.Plan.Version, run.State, run.Objective, run.ID)
	for _, task := range run.Plan.Tasks {
		fmt.Fprintf(&out, "\n%s [%s]\n  %s\n  depends: %s\n  tools: %s\n  write scope: %s\n", task.ID, task.Role, task.Instructions, joined(task.Dependencies), joined(task.AllowedTools), joined(task.WriteScope))
		for _, criterion := range task.AcceptanceCriteria {
			fmt.Fprintf(&out, "  ✓ required: %s\n", criterion)
		}
		for _, check := range task.Checks {
			fmt.Fprintf(&out, "  check: %s\n", formatCheck(check))
		}
		fmt.Fprintf(&out, "  attempts: %d · review: %t\n", task.MaxAttempts, task.Review)
	}
	out.WriteString("\nFinal verification:\n")
	for _, check := range run.Plan.FinalChecks {
		fmt.Fprintf(&out, "  %s · repair: %s\n", formatCheck(check), check.TaskID)
	}
	l := run.Plan.Limits
	fmt.Fprintf(&out, "\nLimits: %d workers · %d requests · %d reserved tokens · %d tool calls · %ds\nPlan digest: %s\nApprove with :approve <digest>; execute separately with :execute.\n", l.Workers, l.MaxRequests, l.MaxReservedTokens, l.MaxToolCalls, l.TimeoutSeconds, run.Plan.Digest())
	return out.String()
}

func joined(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func formatCheck(check orchestration.Check) string {
	if check.Kind == "shell" {
		return check.Command + " · domains: " + joined(check.Domains)
	}
	if check.Kind == "file_contains" {
		return check.Kind + " " + check.Path + " contains " + fmt.Sprintf("%q", check.Contains)
	}
	return check.Kind + " " + check.Path
}

func formatRun(run *orchestration.Run) string {
	var out strings.Builder
	fmt.Fprintf(&out, "◆ Run %s · %s\n%s\n", run.ID, run.State, run.Objective)
	for _, task := range run.Tasks {
		fmt.Fprintf(&out, "  %s %s · %s · attempts %d/%d · depends %s\n", stateSymbol(task.State), task.Task.ID, task.State, len(task.Attempts), task.Task.MaxAttempts, joined(task.Task.Dependencies))
		if len(task.Attempts) > 0 {
			a := task.Attempts[len(task.Attempts)-1]
			if a.Failure != nil {
				fmt.Fprintf(&out, "    %s\n", a.Failure)
			}
			if a.Result != nil {
				fmt.Fprintf(&out, "    %s\n", a.Result.Summary)
			}
		}
	}
	fmt.Fprintf(&out, "Requests %d · reserved tokens %d · reported tokens %d · tool calls %d\n", run.Usage.Requests, run.Usage.ReservedTokens, run.Usage.ReportedTokens, run.Usage.ToolCalls)
	if run.Failure != nil {
		fmt.Fprintln(&out, run.Failure)
	}
	if run.Applying {
		out.WriteString("Copy-back interrupted: inspect original files and :reconcile-apply before applying again.\n")
	}
	return out.String()
}

func formatTask(task orchestration.TaskRun) string {
	var out strings.Builder
	fmt.Fprintf(&out, "◆ %s · %s · %s\n%s\nDepends: %s\nTools: %s\nWrite scope: %s\nAttempts: %d/%d\n", task.Task.ID, task.Task.Role, task.State, task.Task.Instructions, joined(task.Task.Dependencies), joined(task.Task.AllowedTools), joined(task.Task.WriteScope), len(task.Attempts), task.Task.MaxAttempts)
	for _, criterion := range task.Task.AcceptanceCriteria {
		fmt.Fprintf(&out, "Required: %s\n", criterion)
	}
	for _, check := range task.Task.Checks {
		fmt.Fprintf(&out, "Check: %s\n", formatCheck(check))
	}
	for _, attempt := range task.Attempts {
		fmt.Fprintf(&out, "Attempt %d (%s): %s\n", attempt.Number, attempt.ID, attempt.State)
		if attempt.Failure != nil {
			fmt.Fprintln(&out, attempt.Failure)
		}
		if attempt.Result != nil {
			fmt.Fprintln(&out, attempt.Result.Summary)
		}
	}
	return out.String()
}

func stateSymbol(state orchestration.State) string {
	switch state {
	case orchestration.Succeeded, orchestration.Completed:
		return "✓"
	case orchestration.Running, orchestration.Executing, orchestration.Planning, orchestration.Verifying:
		return "◆"
	case orchestration.Failed, orchestration.Blocked:
		return "!"
	case orchestration.Cancelled:
		return "×"
	default:
		return "·"
	}
}

// Model text, file contents, and command output must not control the terminal.
// Keep newlines and tabs, but discard escape sequences and control characters.
func terminalText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}
