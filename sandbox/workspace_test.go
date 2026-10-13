package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceConfinement(t *testing.T) {
	ws := testWorkspace(t)
	if err := ws.WriteFile("nested/file", "hello"); err != nil {
		t.Fatal(err)
	}
	if body, err := ws.ReadFile("nested/file"); err != nil || body != "hello" {
		t.Fatalf("%q %v", body, err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Symlink(outside, filepath.Join(ws.Root, "escape"))
	os.Symlink(filepath.Join(ws.Root, "nested"), filepath.Join(ws.Root, "alias"))
	os.Link(outside, filepath.Join(ws.Root, "hardlink"))
	for _, path := range []string{"../secret", outside, "escape", "alias/file", "hardlink", ".env", "nested/.env.local"} {
		if _, err := ws.ReadFile(path); err == nil {
			t.Errorf("accepted read %s", path)
		}
	}
	for _, path := range []string{"../secret", outside, "escape", "alias/file", ".env", ".git/config", "AGENTS.md", ".garness/config.json"} {
		if err := ws.WriteFile(path, "changed"); err == nil {
			t.Errorf("accepted write %s", path)
		}
	}
	if err := ws.WriteFile("hardlink", "replacement"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(outside)
	if string(body) != "secret" {
		t.Fatal("write followed hard link")
	}
	ws.FileLimit = 2
	if _, err := ws.ReadFile("nested/file"); err == nil {
		t.Fatal("oversized read accepted")
	}
	if err := ws.WriteFile("large", "123"); err == nil {
		t.Fatal("oversized write accepted")
	}
}

func TestWorkspaceSymlinkSwap(t *testing.T) {
	ws := testWorkspace(t)
	ws.WriteFile("changing/file", "safe")
	outside := testWorkspace(t)
	outside.WriteFile("file", "secret")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			os.Rename(filepath.Join(ws.Root, "changing"), filepath.Join(ws.Root, "parked"))
			os.Symlink(outside.Root, filepath.Join(ws.Root, "changing"))
			os.Remove(filepath.Join(ws.Root, "changing"))
			os.Rename(filepath.Join(ws.Root, "parked"), filepath.Join(ws.Root, "changing"))
		}
	}()
	for i := 0; i < 200; i++ {
		body, err := ws.ReadFile("changing/file")
		if err == nil && body != "safe" && body != "written" {
			t.Errorf("symlink race escaped: %q", body)
		}
		ws.WriteFile("changing/file", "written")
	}
	<-done
	if body, err := outside.ReadFile("file"); err != nil || body != "secret" {
		t.Fatalf("outside file modified: %q %v", body, err)
	}
}

func TestIncrementalWritesConfinementAndMode(t *testing.T) {
	ws := testWorkspace(t)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("original"), 0600)
	os.Symlink(outside, filepath.Join(ws.Root, "symlink"))
	os.Link(outside, filepath.Join(ws.Root, "hardlink"))
	for _, path := range []string{"symlink", "hardlink"} {
		if err := ws.AppendFile(path, "changed"); err == nil {
			t.Fatal("unsafe append accepted")
		}
		if err := ws.EditFile(path, "original", "changed"); err == nil {
			t.Fatal("unsafe edit accepted")
		}
	}
	if body, _ := os.ReadFile(outside); string(body) != "original" {
		t.Fatal("outside file modified")
	}
	ws.WriteFile("mode", "original")
	os.Chmod(filepath.Join(ws.Root, "mode"), 0755)
	if err := ws.AppendFile("mode", " suffix"); err != nil {
		t.Fatal(err)
	}
	if err := ws.EditFile("mode", "original", "replacement"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(ws.Root, "mode"))
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("mode not preserved")
	}
}
