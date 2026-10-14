package cli

import (
	"anthonytaha/garness/session"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type sessionPicker struct {
	sessions  []*session.Session
	query     string
	index     int
	width     int
	selected  string
	new       bool
	cancelled bool
}

func newSessionPicker(sessions []*session.Session) *sessionPicker {
	return &sessionPicker{sessions: sessions, width: 80}
}
func (p *sessionPicker) matches() []*session.Session {
	var matches []*session.Session
	for _, v := range p.sessions {
		if strings.Contains(strings.ToLower(v.Name+" "+v.ID+" "+sessionPreview(v)), strings.ToLower(p.query)) {
			matches = append(matches, v)
		}
	}
	return matches
}
func sessionPreview(v *session.Session) string {
	for _, message := range v.Transcript {
		if message["role"] == "user" {
			text, _ := message["content"].(string)
			return strings.Join(strings.Fields(terminalText(text)), " ")
		}
	}
	return ""
}
func (p *sessionPicker) key(msg tea.KeyMsg) (string, bool, string) {
	matches := p.matches()
	if msg.Paste {
		p.query += terminalText(string(msg.Runes))
		p.index = 0
		return "", false, ""
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		return "", true, "cancel"
	case "up":
		p.index = max(0, p.index-1)
	case "down":
		p.index = min(len(matches), p.index+1)
	case "enter":
		if p.index == 0 {
			return "", true, "new"
		}
		if p.index <= len(matches) {
			return matches[p.index-1].ID, true, ""
		}
	case "backspace":
		runes := []rune(p.query)
		if len(runes) > 0 {
			p.query = string(runes[:len(runes)-1])
			p.index = 0
		}
	default:
		if msg.Type == tea.KeyRunes {
			p.query += strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, string(msg.Runes))
			p.index = 0
		}
	}
	return "", false, ""
}
func (p *sessionPicker) view(width, limit int) string {
	matches := p.matches()
	p.index = min(p.index, len(matches))
	rows := []string{"◆ Workspace sessions", "Search: " + p.query}
	start := max(0, p.index-limit+1)
	for i := start; i < min(len(matches)+1, start+limit); i++ {
		row := "New session"
		if i > 0 {
			v := matches[i-1]
			name := v.Name
			if name == "" {
				name = v.ID
			}
			row = fmt.Sprintf("%s · %s · %s", name, v.Updated.Local().Format("2006-01-02 15:04"), sessionPreview(v))
		}
		prefix := "  "
		if i == p.index {
			prefix = "❯ "
		}
		rows = append(rows, ansi.Truncate(terminalText(prefix+row), max(10, width), "…"))
	}
	rows = append(rows, "↑/↓ choose · Enter open · type to search · Esc cancel")
	return strings.Join(rows, "\n")
}
func (p *sessionPicker) Init() tea.Cmd { return nil }
func (p *sessionPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = message.Width
	case tea.KeyMsg:
		id, done, action := p.key(message)
		if done {
			p.selected, p.new, p.cancelled = id, action == "new", action == "cancel"
			return p, tea.Quit
		}
	}
	return p, nil
}
func (p *sessionPicker) View() string { return p.view(p.width, 8) + "\n" }
func chooseSessionInteractive(store *session.Store) (*session.Session, error) {
	list, err := store.List()
	if err != nil {
		return nil, err
	}
	picker := newSessionPicker(list)
	if _, err := tea.NewProgram(picker).Run(); err != nil {
		return nil, err
	}
	if picker.cancelled {
		return nil, fmt.Errorf("session selection cancelled")
	}
	if picker.new {
		return nil, nil
	}
	return store.Load(picker.selected)
}
