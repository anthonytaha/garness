package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementAndSwitch(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("instructions"), 0600)
	s, e := session.Open(t.TempDir(), root)
	if e != nil {
		t.Fatal(e)
	}
	a := harness.NewAgent("", model.Provider{}, "", root)
	box, e := sandbox.NewSandbox(root, sandbox.DefaultSandboxPolicy("isolated"))
	if e != nil {
		t.Fatal(e)
	}
	m := &sessionManager{store: s, agent: a, box: box}
	defer m.close()
	defer func() { m.box.Close() }()
	a.Checkpoint = m.save
	if !m.command(":rename example name", bufio.NewScanner(strings.NewReader(""))) {
		t.Fatal("command not handled")
	}
	id := m.current.ID
	if e = manage(s, []string{"sessions", "rename", id, "locked"}); e == nil {
		t.Fatal("renamed active session")
	}
	os.WriteFile(filepath.Join(m.box.Workspace.Root, "pending"), []byte("change"), 0600)
	if e = m.switchSession(nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(m.box.Workspace.Root, "pending")); !os.IsNotExist(e) {
		t.Fatal("retained isolated change")
	}
	if e = manage(s, []string{"sessions", "rename", id, "renamed"}); e != nil {
		t.Fatal(e)
	}
	v, e := s.Load(id)
	if e != nil || v.Name != "renamed" {
		t.Fatal("rename failed")
	}
	if e = m.switchSession(v); e != nil {
		t.Fatal(e)
	}
	m.command(":branch", nil)
	if m.current.ID == id || m.current.Parent != id {
		t.Fatal("branch failed")
	}
	output := filepath.Join(t.TempDir(), "export.json")
	if e = manage(s, []string{"sessions", "export", m.current.ID, "json", output}); e != nil {
		t.Fatal(e)
	}
	if e = manage(s, []string{"sessions", "export", m.current.ID, "json", output}); e == nil {
		t.Fatal("overwrote export")
	}
}
