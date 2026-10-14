package cli

import (
	"anthonytaha/garness/orchestration"
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strconv"
	"strings"
	"time"
)

func (m *terminalModel) toolCommand(line string) tea.Cmd {
	parts := strings.Fields(line)
	if len(parts) == 2 {
		index, err := strconv.Atoi(parts[1])
		if err != nil || index < 1 || index > len(m.tools) {
			return m.print("error: unknown tool activity number")
		}
		return m.printOutput(m.toolDetails(index - 1))
	}
	if len(parts) != 1 || parts[0] != ":tools" {
		return m.print("usage: :tools [number]")
	}
	var out strings.Builder
	for i, tool := range m.tools {
		fmt.Fprintf(&out, "%d. %s\n", i+1, m.toolSummary(tool))
	}
	if len(m.tools) == 0 {
		out.WriteString("No tool activity yet")
	}
	return m.print(out.String())
}
func (m *terminalModel) toolSummary(tool toolActivity) string {
	status := "running"
	if tool.finished {
		status = "done"
		if tool.event.Failed {
			status = "failed"
		}
	}
	target := tool.event.Arguments
	var arguments map[string]any
	if json.Unmarshal([]byte(target), &arguments) == nil {
		for _, key := range []string{"path", "command", "expression"} {
			if value, ok := arguments[key].(string); ok {
				target = value
				break
			}
		}
	}
	if len([]rune(target)) > 80 {
		target = string([]rune(target)[:80]) + "…"
	}
	duration := tool.event.Duration
	if !tool.finished && !tool.started.IsZero() {
		duration = time.Since(tool.started)
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s · %s · %s", tool.task, tool.event.Name, target, status, duration.Round(time.Millisecond)))
}
func (m *terminalModel) toolDetails(index int) string {
	tool := m.tools[index]
	return fmt.Sprintf("Tool #%d · %s\nArguments:\n%s\nResult:\n%s", index+1, m.toolSummary(tool), tool.event.Arguments, tool.event.Text)
}

func (m *terminalModel) updateActivity(msg activityMessage) tea.Cmd {
	event := msg.event
	var print tea.Cmd
	switch event.Kind {
	case "context":
		if msg.task == "" {
			m.tokens = event.ContextTokens
		}
	case "request", "compaction", "retry", "quota":
		m.activity = strings.TrimSpace(msg.task + " " + event.Text)
		if event.Kind != "request" {
			print = m.print(m.activity)
		}
	case "warning", "compacted":
		print = m.print(event.Text)
	case "tool-start":
		if msg.task == "" && m.stream != "" {
			print = tea.Println(m.renderMarkdown(m.stream))
			m.stream = ""
		}
		m.tools = append(m.tools, toolActivity{task: msg.task, event: event, started: time.Now()})
		m.activity = strings.TrimSpace(msg.task + " " + event.Name)
	case "tool-finish":
		for i := len(m.tools) - 1; i >= 0; i-- {
			if m.tools[i].task == msg.task && m.tools[i].event.ID == event.ID {
				m.tools[i].event, m.tools[i].finished = event, true
				text := fmt.Sprintf("%s Tool #%d · %s", stateSymbol(orchestration.Succeeded), i+1, m.toolSummary(m.tools[i]))
				if event.Failed {
					text = "! " + text + "\n" + event.Text
				}
				print = m.print(text)
				break
			}
		}
	}
	if print != nil {
		return tea.Sequence(print, m.wait())
	}
	return m.wait()
}
