package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRestorationIntegrationAndApply(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("instructions"), 0600)
	os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(root, "file"), []byte("before"), 0644)
	policy := DefaultSandboxPolicy("isolated")
	coordinator, err := NewSandbox(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	worker, err := NewSandbox(coordinator.Workspace.Root, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if err = worker.Workspace.WriteFile("file", "after"); err != nil {
		t.Fatal(err)
	}
	state, err := worker.ExportState()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Files[".env"]; ok {
		t.Fatal("secret persisted")
	}
	restored, err := RestoreSandbox(coordinator.Workspace.Root, policy, state)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if text, _ := restored.Workspace.ReadFile("file"); text != "after" {
		t.Fatal("lost worker changes")
	}
	if err = coordinator.Integrate(state); err != nil {
		t.Fatal(err)
	}
	if text, _ := os.ReadFile(filepath.Join(root, "file")); string(text) != "before" {
		t.Fatal("integration changed original")
	}
	if err = coordinator.Integrate(state); err == nil {
		t.Fatal("stale patch applied twice")
	}
	coordinator.Approve = func(string, string) bool { return true }
	if _, err = coordinator.ApplyChanges(); err != nil {
		t.Fatal(err)
	}
	state, err = coordinator.ExportState()
	if err != nil {
		t.Fatal(err)
	}
	afterApply, err := RestoreSandbox(root, policy, state)
	if err != nil {
		t.Fatal("could not resume applied snapshot", err)
	}
	defer afterApply.Close()
	if text, err := afterApply.Changes(); err != nil || text != "no changes" {
		t.Fatal("applied state has pending changes", text, err)
	}
}

func TestStateRejectsEscapesSecretsAndChangedInstructions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("instructions"), 0600)
	policy := DefaultSandboxPolicy("isolated")
	box, err := NewSandbox(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	for _, path := range []string{"../outside", "/absolute", "dir/../file", ".env", ".git/config"} {
		state, err := box.ExportState()
		if err != nil {
			t.Fatal(err)
		}
		state.Files[path] = StateFile{Body: []byte("bad"), Mode: 0644}
		if restored, err := RestoreSandbox(root, policy, state); err == nil {
			restored.Close()
			t.Fatal("invalid snapshot accepted", path)
		}
	}
	state, _ := box.ExportState()
	state.Files["AGENTS.md"] = StateFile{Body: []byte("changed"), Mode: 0644}
	if restored, err := RestoreSandbox(root, policy, state); err == nil {
		restored.Close()
		t.Fatal("changed instructions accepted")
	}
}

func TestInterruptedCopyBackReconciliation(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "one"), []byte("before"), 0644)
	os.WriteFile(filepath.Join(root, "two"), []byte("before"), 0644)
	box, err := NewSandbox(root, DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	box.Workspace.WriteFile("one", "after")
	box.Workspace.WriteFile("two", "after")
	if applied, err := box.ReconcileApply(); err != nil || applied {
		t.Fatal("unchanged original not recognized", err)
	}
	os.WriteFile(filepath.Join(root, "one"), []byte("after"), 0644)
	if _, err := box.ReconcileApply(); err == nil {
		t.Fatal("partial copy-back accepted")
	}
	os.WriteFile(filepath.Join(root, "two"), []byte("after"), 0644)
	if applied, err := box.ReconcileApply(); err != nil || !applied {
		t.Fatal("completed copy-back not recognized", err)
	}
	if review, err := box.Changes(); err != nil || review != "no changes" {
		t.Fatal("reconciled patch still pending", review, err)
	}
}
