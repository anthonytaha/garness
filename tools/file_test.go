package tools

import (
	"strings"
	"testing"

	"anthonytaha/garness/sandbox"
)

func TestFileToolsUseWorkspaceBoundary(t *testing.T) {
	workspace, err := sandbox.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewToolRegistry()
	registry.Register(ReadFileTool(workspace))
	registry.Register(WriteFileTool(workspace))
	if result := registry.Call("write_file", `{"path":"file","content":"hello"}`); result != "wrote file (5 chars)" {
		t.Fatal(result)
	}
	if result := registry.Call("read_file", `{"path":"file"}`); result != "hello" {
		t.Fatal(result)
	}
	for _, path := range []string{"../outside", "AGENTS.md", ".env", ".git/config"} {
		if result := registry.Call("write_file", `{"path":"`+path+`","content":"changed"}`); !strings.HasPrefix(result, "error:") {
			t.Fatalf("write accepted: %s", result)
		}
	}
}
