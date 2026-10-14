package cli

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

func (m *terminalModel) updateKey(msg tea.KeyMsg) tea.Cmd {
	name := msg.String()
	if name == "ctrl+c" && !msg.Paste {
		if m.cancel != nil {
			return m.cancelWork()
		}
		if m.input.Value() != "" {
			m.input.Reset()
			return nil
		}
		return m.quit()
	}
	if name == "ctrl+d" && m.input.Value() == "" && !msg.Paste {
		return m.quit()
	}
	if m.pending != nil {
		if msg.Paste {
			return nil
		}
		switch name {
		case "left", "right", "tab":
			m.approveChoice = !m.approveChoice
		case "ctrl+o":
			m.approvalExpanded = !m.approvalExpanded
		case "up":
			m.approvalScroll = max(0, m.approvalScroll-1)
		case "down":
			m.approvalScroll++
		case "pgup":
			m.approvalScroll = max(0, m.approvalScroll-5)
		case "pgdown":
			m.approvalScroll += 5
		case "esc", "n":
			m.answerApproval(false)
		case "enter":
			m.answerApproval(m.approveChoice)
		}
		return nil
	}
	if m.picker != nil {
		selected, done, cmd := m.picker.key(msg)
		if done {
			m.picker = nil
			if selected != "" {
				return m.submit(":resume " + selected)
			}
			if cmd == "new" {
				return m.submit(":new")
			}
		}
		return nil
	}
	if msg.Paste {
		m.focus = "composer"
	}
	if !msg.Paste {
		switch name {
		case "ctrl+g":
			if m.snapshot != nil && m.snapshot.Plan != nil {
				m.focus, m.action = "plan", 0
			}
			return nil
		case "ctrl+t":
			m.focus, m.taskIndex = "tasks", 0
			return nil
		case "ctrl+o":
			m.focus, m.toolIndex = "tools", max(0, len(m.tools)-1)
			return nil
		case "esc":
			m.focus = "composer"
			m.completionHidden = true
			return nil
		}
		if m.focus == "plan" {
			return m.planKey(name)
		}
		if m.focus == "tasks" {
			return m.taskKey(name)
		}
		if m.focus == "tools" {
			switch name {
			case "up":
				m.toolIndex = max(0, m.toolIndex-1)
			case "down":
				m.toolIndex = min(max(0, len(m.tools)-1), m.toolIndex+1)
			case "enter":
				if len(m.tools) > 0 {
					return m.printOutput(m.toolDetails(m.toolIndex))
				}
			}
			return nil
		}
		matches := commandMatches(m.input.Value())
		if len(matches) > 0 && !m.completionHidden {
			switch name {
			case "down":
				m.completion = (m.completion + 1) % len(matches)
				return nil
			case "up":
				m.completion = (m.completion + len(matches) - 1) % len(matches)
				return nil
			case "tab":
				command := matches[min(m.completion, len(matches)-1)]
				value := command.name
				if command.arguments != "" {
					value += " "
				}
				m.input.SetValue(value)
				m.input.CursorEnd()
				m.completionHidden = true
				return nil
			}
		}
		switch name {
		case "alt+up", "alt+down":
			if len(m.history) == 0 {
				return nil
			}
			if m.historyIndex == len(m.history) {
				m.historyDraft = m.input.Value()
			}
			if name == "alt+up" {
				m.historyIndex = max(0, m.historyIndex-1)
			} else {
				m.historyIndex = min(len(m.history), m.historyIndex+1)
			}
			value := m.historyDraft
			if m.historyIndex < len(m.history) {
				value = m.history[m.historyIndex]
			}
			m.input.SetValue(value)
			m.input.CursorEnd()
			return nil
		case "enter":
			line := m.input.Value()
			m.input.Reset()
			m.completion, m.completionHidden = 0, false
			if strings.TrimSpace(line) == "" {
				return nil
			}
			m.history = append(m.history, line)
			m.historyIndex = len(m.history)
			return tea.Sequence(m.print("❯ "+line), m.submit(line))
		}
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if before != m.input.Value() {
		m.completion, m.completionHidden = 0, false
	}
	return cmd
}

var planActions = []string{"Review", "Approve", "Reject", "Execute", "Changes", "Apply"}

func (m *terminalModel) planKey(name string) tea.Cmd {
	if name == "left" {
		m.action = (m.action + len(planActions) - 1) % len(planActions)
	}
	if name == "right" || name == "tab" {
		m.action = (m.action + 1) % len(planActions)
	}
	if name != "enter" || m.snapshot == nil || m.snapshot.Plan == nil {
		return nil
	}
	if m.cancel != nil {
		return m.print("Wait for active work to finish; Ctrl+C cancels")
	}
	digest := m.snapshot.Plan.Digest()
	command := []string{":plan", ":approve " + digest, ":reject " + digest, ":execute", ":changes", ":apply"}[m.action]
	m.focus = "composer"
	return m.submit(command)
}
func (m *terminalModel) taskKey(name string) tea.Cmd {
	if m.snapshot == nil || len(m.snapshot.Tasks) == 0 {
		return nil
	}
	if name == "up" {
		m.taskIndex = max(0, m.taskIndex-1)
	}
	if name == "down" {
		m.taskIndex = min(len(m.snapshot.Tasks)-1, m.taskIndex+1)
	}
	if name == "enter" {
		task := m.snapshot.Tasks[m.taskIndex]
		if m.cancel == nil && len(task.Attempts) > 0 {
			return tea.Batch(m.print(formatTask(task)), m.submit(":attempt "+task.Task.ID))
		}
		return m.print(formatTask(task) + "\nInspect saved artifacts with :attempt " + task.Task.ID + " after active work finishes.")
	}
	return nil
}
