package cli

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"os"
	"path/filepath"
	"strings"
)

func (m *terminalModel) welcome() tea.Cmd {
	var prints []tea.Cmd
	prints = append(prints, tea.Println(m.accent.Render(" ▄▄▄  GARNESS")+"\n"+m.dim.Render("      "+filepath.Base(m.manager.store.Workspace)+" · "+m.modelName()+" · "+m.manager.box.Policy.Profile)+"\n"+m.dim.Render("      :help for commands · Ctrl+G plan actions · Ctrl+T agents")))
	transcript := m.manager.agent.Transcript
	if len(transcript) > 0 {
		prints = append(prints, m.print("Recent session conversation · full transcript available with :export"))
		start := max(0, len(transcript)-20)
		for _, message := range transcript[start:] {
			text, _ := message["content"].(string)
			if text == "" {
				continue
			}
			if message["role"] == "user" {
				prints = append(prints, m.print("❯ "+text))
			}
			if message["role"] == "assistant" {
				prints = append(prints, tea.Println(m.accent.Render("◆ Garness")+"\n"+m.renderMarkdown(text)))
			}
		}
	}
	return tea.Sequence(prints...)
}

func (m *terminalModel) modelName() string {
	if m.manager.agent.Model != "" {
		return m.manager.agent.Model
	}
	return m.manager.agent.Provider.Model
}

func (m *terminalModel) setSize(width, height int) {
	m.width, m.height = max(10, width), max(4, height)
	m.input.SetWidth(m.width)
	m.input.SetHeight(min(3, max(1, m.height/5)))
	style := "dark"
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		style = "notty"
	}
	profile := termenv.TrueColor
	if style == "notty" {
		profile = termenv.Ascii
	}
	m.markdown, _ = glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithColorProfile(profile), glamour.WithWordWrap(max(10, m.width-2)))
}

func (m *terminalModel) text(text string) string {
	return ansi.Hardwrap(terminalText(text), m.width, true)
}
func (m *terminalModel) renderMarkdown(text string) string {
	text = terminalText(text)
	if m.markdown != nil {
		if rendered, err := m.markdown.Render(text); err == nil {
			return strings.TrimSpace(rendered)
		}
	}
	return m.text(text)
}
func (m *terminalModel) print(text string) tea.Cmd { return tea.Println(m.text(text)) }
func (m *terminalModel) printOutput(text string) tea.Cmd {
	lines := strings.Split(m.text(strings.TrimRight(text, "\n")), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "+") {
			lines[i] = m.good.Render(line)
		}
		if strings.HasPrefix(line, "-") {
			lines[i] = m.bad.Render(line)
		}
		if strings.HasPrefix(line, "@@") {
			lines[i] = m.accent.Render(line)
		}
	}
	return tea.Println(strings.Join(lines, "\n"))
}

func (m *terminalModel) View() string {
	if m.closing && m.cancel == nil {
		return ""
	}
	if m.pending != nil {
		// Approval is modal; preserve the draft and keep decisions visible.
		lines := strings.Split(m.approvalView(), "\n")
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], m.width, "…")
		}
		return strings.Join(lines[:min(len(lines), m.height)], "\n")
	}
	var sections []string
	if m.picker != nil {
		sections = append(sections, m.picker.view(m.width, min(8, max(1, m.height/3))))
	} else {
		if m.snapshot != nil {
			sections = append(sections, m.runView())
		}
		if m.focus == "tools" {
			sections = append(sections, m.toolsView())
		}
		for _, tool := range m.tools {
			if !tool.finished {
				sections = append(sections, ansi.Truncate(terminalText("◆ "+m.toolSummary(tool)), m.width, "…"))
			}
		}
		if m.stream != "" {
			lines := strings.Split(m.text(m.stream), "\n")
			limit := max(1, min(6, m.height/4))
			if len(lines) > limit {
				lines = lines[len(lines)-limit:]
			}
			sections = append(sections, m.accent.Render("◆ Garness · streaming")+"\n"+strings.Join(lines, "\n"))
		} else if m.cancel != nil {
			sections = append(sections, m.accent.Render([]string{"·", "◦", "○", "◦"}[m.frame%4]+" "+m.text(m.activity)))
		}
		if m.focus == "composer" && !m.completionHidden {
			matches := commandMatches(m.input.Value())
			if len(matches) > 0 {
				selected := min(m.completion, len(matches)-1)
				start := max(0, selected-2)
				var rows []string
				for i := start; i < min(len(matches), start+min(5, max(1, m.height/5))); i++ {
					row := matches[i].name + " " + matches[i].arguments + " · " + matches[i].description
					row = ansi.Truncate(terminalText(row), m.width-2, "…")
					if i == selected {
						row = m.accent.Render("❯ " + row)
					} else {
						row = "  " + m.dim.Render(row)
					}
					rows = append(rows, row)
				}
				sections = append(sections, strings.Join(rows, "\n"))
			}
		}
	}
	rule := m.dim.Render(strings.Repeat("─", m.width))
	sections = append(sections, rule, m.input.View(), rule)
	status := m.manager.box.Policy.Profile + " · " + m.modelName() + " · " + filepath.Base(m.manager.store.Workspace)
	if budget := m.manager.agent.ContextBudget; budget > 0 {
		status += fmt.Sprintf(" · context ~%d%%", m.tokens*100/budget)
	} else {
		status += " · compaction off"
	}
	if m.cancel == nil {
		status += " · idle"
	}
	if len(m.queued) > 0 {
		status += fmt.Sprintf(" · %d queued", len(m.queued))
	}
	sections = append(sections, m.dim.Render(ansi.Truncate(terminalText(status), m.width, "…")))
	if m.height >= 12 {
		sections = append(sections, m.dim.Render(ansi.Truncate("Enter send · Ctrl+J newline · Tab complete · :help", m.width, "…")))
	}
	view := strings.Join(sections, "\n")
	// Keep the live region within the terminal so committed scrollback stays
	// outside the redraw area even on short terminals.
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	return strings.Join(lines, "\n")
}

func (m *terminalModel) runView() string {
	run := m.snapshot
	var rows []string
	heading := fmt.Sprintf("◆ Plan / agents · %s", run.State)
	if run.Plan != nil {
		heading = fmt.Sprintf("◆ Plan v%d / agents · %s · %d workers", run.Plan.Version, run.State, run.Plan.Limits.Workers)
	}
	rows = append(rows, m.accent.Render(ansi.Truncate(heading, m.width, "…")))
	limit := min(5, max(1, m.height/5))
	start := 0
	if m.focus == "tasks" {
		start = max(0, m.taskIndex-limit+1)
	}
	for i := start; i < min(len(run.Tasks), start+limit); i++ {
		task := run.Tasks[i]
		row := fmt.Sprintf("%s %s · %s · %d/%d attempts · depends %s", stateSymbol(task.State), task.Task.ID, task.State, len(task.Attempts), task.Task.MaxAttempts, joined(task.Task.Dependencies))
		if m.focus == "tasks" && i == m.taskIndex {
			row = "❯ " + row
		}
		rows = append(rows, ansi.Truncate(terminalText(row), m.width, "…"))
	}
	if len(run.Tasks) > limit {
		rows = append(rows, m.dim.Render(fmt.Sprintf("%d tasks · Ctrl+T browse · Enter inspect · Esc return", len(run.Tasks))))
	}
	rows = append(rows, m.dim.Render(ansi.Truncate(fmt.Sprintf("Requests %d · reserved tokens %d · tool calls %d", run.Usage.Requests, run.Usage.ReservedTokens, run.Usage.ToolCalls), m.width, "…")))
	if run.Plan != nil {
		var actions []string
		for i, label := range planActions {
			label = "[ " + label + " ]"
			if m.focus == "plan" && i == m.action {
				label = m.accent.Render(label)
			}
			actions = append(actions, label)
		}
		if m.width < 72 && m.focus == "plan" {
			rows = append(rows, m.accent.Render("[ "+planActions[m.action]+" ]"))
		} else {
			rows = append(rows, ansi.Truncate(strings.Join(actions, " "), m.width, "…"))
		}
		if m.focus == "plan" {
			rows = append(rows, m.dim.Render("←/→ choose · Enter activate · Esc return"))
		}
	}
	return strings.Join(rows, "\n")
}
func (m *terminalModel) toolsView() string {
	if len(m.tools) == 0 {
		return "No tool activity yet · Esc return"
	}
	m.toolIndex = min(m.toolIndex, len(m.tools)-1)
	tool := m.tools[m.toolIndex]
	text := m.text(m.toolDetails(m.toolIndex))
	lines := strings.Split(text, "\n")
	limit := max(1, min(6, m.height/4))
	if len(lines) > limit {
		lines = append(lines[:limit], "… Enter prints full details to scrollback")
	}
	if !tool.finished {
		lines = append(lines, "Running")
	}
	return strings.Join(lines, "\n") + "\n" + m.dim.Render("↑/↓ choose tool · Enter full details · Esc return")
}
func (m *terminalModel) approvalView() string {
	request := m.pending
	lines := strings.Split(m.text(request.arguments), "\n")
	limit := max(1, min(5, m.height-3))
	if m.approvalExpanded {
		limit = max(1, m.height-3)
	}
	start := min(m.approvalScroll, max(0, len(lines)-limit))
	m.approvalScroll = start
	detail := strings.Join(lines[start:min(len(lines), start+limit)], "\n")
	deny, approve := "[ Deny ]", "[ Approve once ]"
	if m.approveChoice {
		approve = m.accent.Render(approve)
	} else {
		deny = m.accent.Render(deny)
	}
	actions := deny + " " + approve
	if m.width < 30 {
		choice := "DENY"
		if m.approveChoice {
			choice = "APPROVE"
		}
		actions = m.accent.Render("[ " + choice + " ]")
	}
	return m.accent.Render("◆ Approval · "+terminalText(request.name)) + "\n" + detail + "\n" + actions + "\n" + m.dim.Render(fmt.Sprintf("←/→ choose · Enter confirm · Esc deny · Ctrl+O expand · ↑/↓ scroll (%d/%d)", start+1, len(lines)))
}
