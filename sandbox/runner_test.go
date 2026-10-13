package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellApprovalAndLocalCopyBack(t *testing.T) {
	ws := testWorkspace(t)
	sandbox, err := NewSandbox(ws.Root, DefaultSandboxPolicy("local"))
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	runner := &fixtureCommandRunner{}
	sandbox.Runner = runner
	if _, err := sandbox.Run(CommandRequest{Command: "fixture", Domains: []string{"example.com"}}); err == nil {
		t.Fatal("network approval bypassed")
	}
	if runner.ran {
		t.Fatal("denied command ran")
	}
	result, err := sandbox.Run(CommandRequest{Command: "fixture"})
	if err != nil || !strings.Contains(result, "fixture") {
		t.Fatal(result, err)
	}
	if body, _ := ws.ReadFile("result"); body != "created" {
		t.Fatal("local changes not copied back")
	}
	approvals := 0
	sandbox.Approve = func(name, args string) bool {
		approvals++
		return name == "shell_network" && strings.Contains(args, "example.com") && strings.Contains(args, "fixture")
	}
	if _, err := sandbox.Run(CommandRequest{Command: "fixture", Domains: []string{"example.com"}}); err != nil {
		t.Fatal(err)
	}
	if approvals != 1 || runner.grant == nil || !runner.grant.closed {
		t.Fatal("grant was not revoked")
	}
	if _, err := sandbox.Run(CommandRequest{Command: "fixture", Domains: []string{"example.com"}}); err != nil {
		t.Fatal(err)
	}
	if approvals != 2 {
		t.Fatal("network approval persisted across invocations")
	}
}

func TestFileModesAndCancellation(t *testing.T) {
	ws := testWorkspace(t)
	ws.WriteFile("executable", "original")
	os.Chmod(filepath.Join(ws.Root, "executable"), 0755)
	if err := ws.WriteFile("executable", "changed"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(ws.Root, "executable")); err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("executable mode lost", err)
	}
	sandbox, err := NewSandbox(ws.Root, DefaultSandboxPolicy("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	if changes, err := sandbox.Changes(); err != nil || changes != "no changes" {
		t.Fatal(changes, err)
	}
	runner := &fixtureCommandRunner{}
	sandbox.Runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sandbox.RunContext(ctx, CommandRequest{Command: "fixture"}); err == nil || runner.ran {
		t.Fatal("cancelled command ran")
	}
}
