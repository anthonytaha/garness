package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/orchestration"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

type outputMessage string
type deltaMessage string
type activityMessage struct {
	task  string
	event harness.Event
}
type runMessage orchestration.Event
type approvalMessage approvalRequest
type tickMessage time.Time
type queuedMessage struct{}
type operationMessage struct {
	jobResult
	tokens int
}

// Writers and observers send immutable presentation messages. The terminal is
// the only owner of input and rendering; workers never write directly to it.
type eventWriter struct {
	ctx      context.Context
	messages chan tea.Msg
}

func (w eventWriter) Write(p []byte) (int, error) {
	select {
	case w.messages <- outputMessage(string(p)):
		return len(p), nil
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

func interactiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("TERM") != "dumb"
}

type toolActivity struct {
	task     string
	event    harness.Event
	finished bool
	started  time.Time
}

type terminalModel struct {
	ctx                    context.Context
	messages               chan tea.Msg
	manager                *sessionManager
	runs                   *runManager
	broker                 *approvalBroker
	input                  textarea.Model
	width, height          int
	accent, dim, good, bad lipgloss.Style
	markdown               *glamour.TermRenderer
	history                []string
	historyIndex           int
	historyDraft           string
	completion             int
	completionHidden       bool
	cancel                 context.CancelFunc
	closing                bool
	queued                 []string
	pending                *approvalRequest
	approveChoice          bool
	approvalExpanded       bool
	approvalScroll         int
	focus                  string
	action                 int
	taskIndex              int
	toolIndex              int
	tools                  []toolActivity
	stream                 string
	activity               string
	tokens                 int
	snapshot               *orchestration.Run
	picker                 *sessionPicker
	frame                  int
	jobs                   sync.WaitGroup
}

func newTerminalModel(ctx context.Context, manager *sessionManager, limits orchestration.Limits, accentColor string) (*terminalModel, error) {
	messages := make(chan tea.Msg, 256)
	broker := &approvalBroker{requests: make(chan approvalRequest)}
	runs, err := newRunManager(manager, manager.agent, limits, broker, nil)
	if err != nil {
		return nil, err
	}
	writer := eventWriter{ctx: ctx, messages: messages}
	manager.out, runs.out, runs.rich = writer, writer, true
	m := &terminalModel{ctx: ctx, messages: messages, manager: manager, runs: runs, broker: broker, width: 80, height: 24, focus: "composer"}
	renderer := lipgloss.NewRenderer(os.Stdout)
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		renderer.SetColorProfile(termenv.Ascii)
	}
	m.accent = renderer.NewStyle().Foreground(lipgloss.Color(accentColor)).Bold(true)
	m.dim = renderer.NewStyle().Foreground(lipgloss.Color("245"))
	m.good = renderer.NewStyle().Foreground(lipgloss.Color("114"))
	m.bad = renderer.NewStyle().Foreground(lipgloss.Color("203"))
	input := textarea.New()
	input.Prompt = "❯ "
	input.Placeholder = "Describe a task or :command"
	input.ShowLineNumbers = false
	input.CharLimit = 128 << 10
	input.SetWidth(80)
	input.SetHeight(3)
	input.FocusedStyle.Prompt = m.accent
	input.FocusedStyle.CursorLine = lipgloss.NewStyle()
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "alt+enter"))
	input.Focus()
	m.input = input
	m.tokens = manager.agent.EstimatedContextTokens()
	m.setSize(80, 24)
	manager.agent.OnEvent = func(event harness.Event) { m.send(activityMessage{event: event}) }
	manager.agent.OnDelta = func(delta string) { m.send(deltaMessage(delta)) }
	manager.agent.OnRetry = func(status int, delay time.Duration) {
		m.send(activityMessage{event: harness.Event{Kind: "retry", Text: fmt.Sprintf("HTTP %d; retrying in %s", status, delay.Round(time.Millisecond))}})
	}
	manager.agent.OnRateLimitRemaining = func(remaining int64) {
		m.send(activityMessage{event: harness.Event{Kind: "quota", Text: fmt.Sprintf("Provider quota remaining: %d", remaining)}})
	}
	runs.options.OnEvent = func(event orchestration.Event) { m.send(runMessage(event)) }
	runs.options.OnAgentEvent = func(task string, event harness.Event) { m.send(activityMessage{task: task, event: event}) }
	return m, nil
}

func (m *terminalModel) send(message tea.Msg) {
	select {
	case m.messages <- message:
	case <-m.ctx.Done():
	}
}

func runTerminal(manager *sessionManager, limits orchestration.Limits, noStream bool, accentColor string) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	m, err := newTerminalModel(ctx, manager, limits, accentColor)
	if err != nil {
		return err
	}
	defer m.runs.close()
	if noStream {
		manager.agent.OnDelta = nil
	}
	program := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	_, err = program.Run()
	if m.cancel != nil {
		m.cancel()
	}
	stop()
	m.jobs.Wait()
	return err
}

func (m *terminalModel) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case message := <-m.messages:
			return message
		case request := <-m.broker.requests:
			return approvalMessage(request)
		case <-m.ctx.Done():
			return nil
		}
	}
}
func terminalTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMessage(t) })
}

func (m *terminalModel) dequeue() tea.Cmd {
	if m.cancel != nil || m.pending != nil || len(m.queued) == 0 {
		return nil
	}
	return func() tea.Msg { return queuedMessage{} }
}

func (m *terminalModel) Init() tea.Cmd {
	return tea.Batch(m.wait(), terminalTick(), textarea.Blink, m.welcome())
}

func (m *terminalModel) loadSnapshot() {
	if m.runs.current == nil {
		m.snapshot = nil
		return
	}
	if run, err := m.runs.store.Load(m.runs.current.Run.ID); err == nil {
		m.snapshot = run
	}
}

func (m *terminalModel) start(operation func(context.Context) (string, error), planning bool) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.activity, m.stream = cancel, "Working", ""
	m.jobs.Add(1)
	m.manager.agent.Approve = func(name, args string) bool { return m.broker.approve(ctx, name, args) }
	return func() tea.Msg {
		defer m.jobs.Done()
		reply, err := operation(ctx)
		// Read agent state only on its owner goroutine, then pass a value to UI.
		m.send(operationMessage{jobResult: jobResult{reply: reply, err: err, planning: planning}, tokens: m.manager.agent.EstimatedContextTokens()})
		return nil
	}
}

func (m *terminalModel) submit(line string) tea.Cmd {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if line == ":cancel" {
		return m.cancelWork()
	}
	if line == ":quit" {
		return m.quit()
	}
	if line == ":help" {
		return m.print(commandHelp())
	}
	if line == ":tools" || strings.HasPrefix(line, ":tools ") {
		return m.toolCommand(line)
	}
	if line == ":run-status" {
		m.loadSnapshot()
		if m.snapshot == nil {
			return m.print("No orchestration run selected")
		}
		return m.print(formatRun(m.snapshot))
	}
	if m.cancel != nil {
		if len(m.queued) >= 128 {
			return m.print("error: input queue is full; wait for active work to finish")
		}
		m.queued = append(m.queued, line)
		return m.print(fmt.Sprintf("Queued (%d): %s", len(m.queued), line))
	}
	if line == ":sessions" {
		list, err := m.manager.store.List()
		if err != nil {
			return m.print("error: " + err.Error())
		}
		m.picker = newSessionPicker(list)
		return nil
	}
	handled, operation, err := m.runs.command(line)
	if handled {
		m.loadSnapshot()
		if err != nil {
			return m.print("error: " + err.Error())
		}
		if operation != nil {
			return m.start(operation, strings.HasPrefix(line, ":plan "))
		}
		return nil
	}
	if strings.HasPrefix(line, ":") && m.manager.command(line, nil) {
		m.tokens = m.manager.agent.EstimatedContextTokens()
		if line == ":new" || line == ":branch" || strings.HasPrefix(line, ":resume ") {
			return m.welcome()
		}
		return nil
	}
	if line == ":changes" {
		text, err := m.manager.box.Changes()
		if err != nil {
			return m.print("error: " + err.Error())
		}
		return m.printOutput(text)
	}
	if line == ":apply" {
		return m.start(func(ctx context.Context) (string, error) {
			m.manager.box.Approve = func(name, args string) bool { return m.manager.agent.Approve(name, args) }
			return m.manager.box.ApplyChanges()
		}, false)
	}
	if strings.HasPrefix(line, ":") {
		return m.print("error: unknown command; use :help")
	}
	return m.start(func(ctx context.Context) (string, error) { return m.manager.agent.RunContext(ctx, line) }, false)
}

func (m *terminalModel) cancelWork() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
		m.activity = "Cancelling"
	} else if m.runs.current != nil {
		if err := m.runs.current.Cancel(); err != nil {
			return m.print("error: " + err.Error())
		}
		m.loadSnapshot()
	} else {
		return m.print("No active work")
	}
	m.answerApproval(false)
	return m.print("Cancelling active work...")
}
func (m *terminalModel) quit() tea.Cmd {
	if m.cancel != nil {
		m.closing = true
		return m.cancelWork()
	}
	return tea.Quit
}
func (m *terminalModel) answerApproval(approved bool) {
	if m.pending == nil {
		return
	}
	approved = approved && m.pending.ctx.Err() == nil
	select {
	case m.pending.answer <- approved:
	default:
	}
	m.pending = nil
	m.approveChoice, m.approvalExpanded, m.approvalScroll = false, false, 0
}

func (m *terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)
	case tickMessage:
		m.frame++
		if m.cancel != nil || m.frame%4 == 0 {
			m.loadSnapshot()
		}
		if m.pending != nil && m.pending.ctx.Err() != nil {
			m.answerApproval(false)
		}
		return m, terminalTick()
	case outputMessage:
		return m, tea.Sequence(m.printOutput(string(msg)), m.wait())
	case deltaMessage:
		m.stream += string(msg)
		return m, m.wait()
	case runMessage:
		m.loadSnapshot()
		label := "run"
		if msg.TaskID != "" {
			label = msg.TaskID
		}
		return m, tea.Sequence(m.print(fmt.Sprintf("%s %s · %s · %s", stateSymbol(msg.To), label, msg.To, msg.Detail)), m.wait())
	case approvalMessage:
		request := approvalRequest(msg)
		if request.ctx.Err() == nil {
			m.pending, m.approveChoice = &request, false
		} else {
			request.answer <- false
		}
		return m, m.wait()
	case activityMessage:
		return m, m.updateActivity(msg)
	case operationMessage:
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.answerApproval(false)
		m.tokens, m.activity = msg.tokens, ""
		m.loadSnapshot()
		var prints []tea.Cmd
		if msg.err != nil {
			if m.stream != "" {
				prints = append(prints, m.print("Partial response (interrupted):\n"+m.stream))
			}
			prints = append(prints, m.print("error: "+msg.err.Error()))
		}
		m.stream = ""
		if msg.reply != "" {
			prints = append(prints, tea.Println(m.accent.Render("◆ Garness")+"\n"+m.renderMarkdown(msg.reply)))
		}
		if msg.planning && msg.err == nil && m.snapshot != nil {
			prints = append(prints, m.print(formatPlan(m.snapshot)))
			m.focus, m.action = "plan", 0
		}
		if m.closing {
			prints = append(prints, tea.Quit)
			return m, tea.Sequence(prints...)
		}
		return m, tea.Batch(tea.Sequence(prints...), m.wait(), m.dequeue())
	case queuedMessage:
		if m.cancel != nil || m.pending != nil || len(m.queued) == 0 {
			return m, nil
		}
		line := m.queued[0]
		m.queued = m.queued[1:]
		command := m.submit(line)
		return m, tea.Batch(command, m.dequeue())
	case tea.KeyMsg:
		return m, m.updateKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	return m, cmd
}

var _ io.Writer = eventWriter{}
