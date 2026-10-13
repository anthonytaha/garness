package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedReviewAndConflict(t *testing.T) {
	ws := testWorkspace(t)
	ws.WriteFile("file", "original")
	os.WriteFile(filepath.Join(ws.Root, ".env"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(ws.Root, "AGENTS.md"), []byte("instructions"), 0644)
	sandbox, err := NewSandbox(ws.Root, DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	if _, err := os.Stat(filepath.Join(sandbox.Workspace.Root, ".env")); !os.IsNotExist(err) {
		t.Fatal("secret copied")
	}
	sandbox.Workspace.WriteFile("file", "modified")
	sandbox.Workspace.WriteFile("new", "added")
	review, err := sandbox.Changes()
	if err != nil || !strings.Contains(review, "original") || !strings.Contains(review, "modified") {
		t.Fatalf("%s %v", review, err)
	}
	if _, err = sandbox.ApplyChanges(); err == nil {
		t.Fatal("apply bypassed approval")
	}
	sandbox.Approve = func(name, args string) bool { return name == "apply_changes" && strings.Contains(args, "modified") }
	ws.WriteFile("file", "concurrent edit")
	if _, err = sandbox.ApplyChanges(); err == nil {
		t.Fatal("overwrote concurrent change")
	}
	if _, err := ws.ReadFile("new"); err == nil {
		t.Fatal("partial apply on conflict")
	}
	ws.WriteFile("file", "original")
	if _, err = sandbox.ApplyChanges(); err != nil {
		t.Fatal(err)
	}
	if body, _ := ws.ReadFile("file"); body != "modified" {
		t.Fatal(body)
	}
	if result, _ := sandbox.Changes(); result != "no changes" {
		t.Fatal(result)
	}
}

func TestSnapshotDirectoryChangesAndRollback(t *testing.T) {
	ws := testWorkspace(t)
	ws.WriteFile("old/file", "content")
	sandbox, err := NewSandbox(ws.Root, DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	os.RemoveAll(filepath.Join(sandbox.Workspace.Root, "old"))
	os.MkdirAll(filepath.Join(sandbox.Workspace.Root, "new/empty"), 0755)
	sandbox.Approve = func(string, string) bool { return true }
	if _, err = sandbox.ApplyChanges(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(ws.Root, "old")); !os.IsNotExist(err) {
		t.Fatal("directory deletion lost")
	}
	if info, err := os.Stat(filepath.Join(ws.Root, "new/empty")); err != nil || !info.IsDir() {
		t.Fatal("empty directory lost", err)
	}

	ws.WriteFile("keep/file", "content")
	os.WriteFile(filepath.Join(ws.Root, "keep/.env"), []byte("secret"), 0600)
	other, err := NewSandbox(ws.Root, DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	os.RemoveAll(filepath.Join(other.Workspace.Root, "keep"))
	other.Approve = func(string, string) bool { return true }
	if _, err = other.ApplyChanges(); err == nil {
		t.Fatal("removed directory containing protected secret")
	}
	if body, err := ws.ReadFile("keep/file"); err != nil || body != "content" {
		t.Fatalf("rollback failed: %q %v", body, err)
	}
}
