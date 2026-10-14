package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func printSessions(store *session.Store) error {
	return printSessionsTo(os.Stdout, store)
}

func printSessionsTo(out io.Writer, store *session.Store) error {
	list, e := store.List()
	if e != nil {
		return e
	}
	for i, v := range list {
		preview := ""
		for _, m := range v.Transcript {
			if m["role"] == "user" {
				preview, _ = m["content"].(string)
				break
			}
		}
		preview = strings.Join(strings.Fields(preview), " ")
		if len(preview) > 70 {
			preview = preview[:70]
		}
		fmt.Fprintf(out, "%d. %s  %s  %s  %s\n", i+1, v.ID, v.Name, v.Updated.Local().Format("2006-01-02 15:04"), preview)
	}
	return nil
}
func chooseSession(store *session.Store, scanner *bufio.Scanner, c config) (*session.Session, error) {
	if c.New {
		return nil, nil
	}
	if c.Resume != "" {
		return store.Load(c.Resume)
	}
	info, e := os.Stdin.Stat()
	if e != nil {
		return nil, e
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("noninteractive input requires --new or --resume <id>")
	}
	list, e := store.List()
	if e != nil {
		return nil, e
	}
	if e = printSessions(store); e != nil {
		return nil, e
	}
	fmt.Print("Session number (or n for new)> ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("no session selected")
	}
	choice := strings.TrimSpace(scanner.Text())
	if choice == "n" || choice == "new" {
		return nil, nil
	}
	index, e := strconv.Atoi(choice)
	if e != nil || index < 1 || index > len(list) {
		return nil, fmt.Errorf("invalid session selection")
	}
	return list[index-1], nil
}
func sessionCommand(store *session.Store, args []string) int {
	e := manage(store, args)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	return 0
}
func manage(store *session.Store, args []string) error {
	return manageTo(os.Stdout, store, args)
}

func manageTo(out io.Writer, store *session.Store, args []string) error {
	if len(args) == 0 || args[0] != "sessions" {
		return fmt.Errorf("usage: garness sessions list|inspect|rename|delete|export|branch")
	}
	if len(args) < 2 {
		return fmt.Errorf("missing session operation")
	}
	op := args[1]
	if op == "list" {
		return printSessionsTo(out, store)
	}
	if len(args) < 3 {
		return fmt.Errorf("missing session ID")
	}
	id := args[2]
	if op == "delete" {
		return store.Delete(id)
	}
	v, e := store.Load(id)
	if e != nil {
		return e
	}
	switch op {
	case "inspect":
		fmt.Fprintf(out, "%s (%s)\nWorkspace: %s\nCreated: %s\nUpdated: %s\nParent: %s\nMessages: %d; transcript: %d; interrupted: %t\n", v.Name, v.ID, v.Workspace, v.Created, v.Updated, v.Parent, len(v.Messages), len(v.Transcript), v.Interrupted)
		return nil
	case "rename":
		if len(args) < 4 {
			return fmt.Errorf("missing name")
		}
		unlock, e := store.Lock(id)
		if e != nil {
			return e
		}
		defer unlock()
		v, e = store.Load(id)
		if e != nil {
			return e
		}
		v.Name = strings.Join(args[3:], " ")
		return store.Save(v)
	case "branch":
		child := store.New(v.Name+" (branch)", id)
		child.Messages = v.Messages
		child.Transcript = v.Transcript
		child.Skills = v.Skills
		child.Interrupted = v.Interrupted
		unlock, e := store.Lock(child.ID)
		if e != nil {
			return e
		}
		defer unlock()
		if e = store.Save(child); e == nil {
			fmt.Fprintln(out, child.ID)
		}
		return e
	case "export":
		format := "markdown"
		if len(args) > 3 {
			format = args[3]
		}
		b, e := session.Export(v, format)
		if e != nil {
			return e
		}
		if len(args) > 4 {
			f, e := os.OpenFile(args[4], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			_, e = f.Write(b)
			ce := f.Close()
			if e != nil {
				return e
			}
			return ce
		}
		fmt.Fprintln(out, string(b))
		return nil
	}
	return fmt.Errorf("unknown session operation %q", op)
}

type sessionManager struct {
	out     io.Writer
	store   *session.Store
	agent   *harness.Agent
	box     *sandbox.Sandbox
	current *session.Session
	unlock  func()
}

func (m *sessionManager) close() {
	if m.unlock != nil {
		m.unlock()
	}
}
func (m *sessionManager) save() error {
	if m.current == nil {
		m.current = m.store.New("", "")
		var e error
		m.unlock, e = m.store.Lock(m.current.ID)
		if e != nil {
			m.current = nil
			return e
		}
		m.println("Session:", m.current.ID)
	}
	m.agent.Snapshot(m.current)
	return m.store.Save(m.current)
}
func (m *sessionManager) switchSession(v *session.Session) error {
	var unlock func()
	var e error
	if v != nil {
		unlock, e = m.store.Lock(v.ID)
		if e != nil {
			return e
		}
	}
	if v != nil {
		if _, statErr := os.Stat(m.store.Directory + "/" + v.ID + ".json"); statErr == nil {
			v, e = m.store.Load(v.ID)
			if e != nil {
				unlock()
				return e
			}
		}
	}
	box, e := sandbox.NewSandbox(m.store.Workspace, m.box.Policy)
	if e != nil {
		if unlock != nil {
			unlock()
		}
		return e
	}
	if m.current != nil {
		if e = m.save(); e != nil {
			box.Close()
			if unlock != nil {
				unlock()
			}
			return e
		}
	}
	m.close()
	m.box.Close()
	m.box = box
	m.current = v
	m.unlock = unlock
	m.agent.Messages = []map[string]any{{"role": "system", "content": m.agent.SystemPrompt}}
	m.agent.Transcript = nil
	m.agent.ResetUsage()
	m.agent.ConfigureSkills(m.agent.AgentsDir + "/.garness/skills")
	if dir := os.Getenv("GARNESS_SKILLS_DIR"); dir != "" {
		m.agent.ConfigureSkills(dir)
	}
	m.agent.ConfigureSandbox(m.box)
	if v != nil {
		for _, w := range m.agent.Restore(v) {
			m.println("WARNING:", w)
		}
		return m.save()
	}
	m.agent.Interrupted = false
	return nil
}
func (m *sessionManager) command(line string, scanner *bufio.Scanner) bool {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false
	}
	var e error
	switch parts[0] {
	case ":session":
		if m.current == nil {
			m.println("New session (not saved yet)")
		} else {
			m.println(m.current.ID, m.current.Name)
		}
	case ":sessions":
		e = printSessionsTo(m.output(), m.store)
	case ":new":
		e = m.switchSession(nil)
	case ":resume":
		if len(parts) != 2 {
			e = fmt.Errorf("usage: :resume <id>")
		} else if m.current != nil && parts[1] == m.current.ID {
			e = fmt.Errorf("session already active")
		} else {
			var v *session.Session
			v, e = m.store.Load(parts[1])
			if e == nil {
				e = m.switchSession(v)
			}
		}
	case ":branch":
		if e = m.save(); e == nil {
			v := m.store.New(m.current.Name+" (branch)", m.current.ID)
			m.agent.Snapshot(v)
			e = m.switchSession(v)
		}
	case ":rename":
		if len(parts) < 2 {
			e = fmt.Errorf("usage: :rename <name>")
		} else if e = m.save(); e == nil {
			m.current.Name = strings.Join(parts[1:], " ")
			e = m.save()
		}
	case ":export":
		if e = m.save(); e == nil {
			e = manageTo(m.output(), m.store, append([]string{"sessions", "export", m.current.ID}, parts[1:]...))
		}
	case ":delete":
		if len(parts) != 2 {
			e = fmt.Errorf("usage: :delete <id>")
		} else if m.current != nil && parts[1] == m.current.ID {
			e = fmt.Errorf("switch sessions before deleting the active session")
		} else {
			e = m.store.Delete(parts[1])
		}
	case ":inspect":
		if m.current == nil {
			m.println("No saved session")
		} else {
			e = manageTo(m.output(), m.store, []string{"sessions", "inspect", m.current.ID})
		}
	default:
		return false
	}
	if e != nil {
		m.println("error:", e)
	}
	return true
}

func (m *sessionManager) output() io.Writer {
	if m.out != nil {
		return m.out
	}
	return os.Stdout
}

func (m *sessionManager) println(values ...any) { fmt.Fprintln(m.output(), values...) }
