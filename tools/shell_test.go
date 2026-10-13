package tools

import (
	"strings"
	"testing"

	"anthonytaha/garness/sandbox"
)

func TestShellToolRejectsInvalidArguments(t *testing.T) {
	box, err := sandbox.NewSandbox(t.TempDir(), sandbox.DefaultSandboxPolicy("local"))
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	registry := NewToolRegistry()
	registry.Register(ShellTool(box))
	for _, arguments := range []string{
		`{}`,
		`{"command":1}`,
		`{"command":"echo hello","cwd":1}`,
		`{"command":"echo hello","domains":"example.com"}`,
		`{"command":"echo hello","domains":[1]}`,
		`{"command":"echo hello","timeout_seconds":1.5}`,
	} {
		if result := registry.Call("shell", arguments); !strings.HasPrefix(result, "error:") {
			t.Fatalf("invalid arguments accepted: %s", result)
		}
	}
}
