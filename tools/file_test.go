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

func TestIncrementalFileTools(t *testing.T) {
	ws, err := sandbox.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewToolRegistry()
	r.Register(WriteFileTool(ws))
	r.Register(EditFileTool(ws))
	r.Register(AppendFileTool(ws))
	call := func(name, args string) {
		t.Helper()
		if result := r.Call(name, args); strings.HasPrefix(result, "error:") {
			t.Fatal(result)
		}
	}
	call("append_file", `{"path":"file","content":"start\n"}`)
	call("append_file", `{"path":"file","content":"middle\nend"}`)
	call("edit_file", `{"path":"file","old_text":"middle","new_text":"edited"}`)
	if body, _ := ws.ReadFile("file"); body != "start\nedited\nend" {
		t.Fatalf("lost existing text: %q", body)
	}
	call("edit_file", `{"path":"file","old_text":"edited\n","new_text":""}`)
	before, _ := ws.ReadFile("file")
	for _, args := range []string{`{"path":"file","old_text":"missing","new_text":"x"}`, `{"path":"file","old_text":"","new_text":"x"}`, `{"path":"file","old_text":"start"}`, `{"path":"file","old_text":1,"new_text":"x"}`} {
		if result := r.Call("edit_file", args); !strings.HasPrefix(result, "error:") {
			t.Fatal("accepted invalid edit")
		}
		if body, _ := ws.ReadFile("file"); body != before {
			t.Fatal("failed edit changed file")
		}
	}
	call("write_file", `{"path":"duplicates","content":"same same"}`)
	if result := r.Call("edit_file", `{"path":"duplicates","old_text":"same","new_text":"different"}`); !strings.HasPrefix(result, "error:") {
		t.Fatal("ambiguous edit accepted")
	}
	for _, name := range []string{"edit_file", "append_file"} {
		for _, path := range []string{"../outside", "AGENTS.md", ".env", ".git/config"} {
			args := `{"path":"` + path + `","old_text":"start","new_text":"x","content":"x"}`
			if result := r.Call(name, args); !strings.HasPrefix(result, "error:") {
				t.Fatalf("%s escaped boundary: %s", name, result)
			}
		}
	}
	ws.FileLimit = int64(len(before))
	if result := r.Call("append_file", `{"path":"file","content":"overflow"}`); !strings.HasPrefix(result, "error:") {
		t.Fatal("oversized append accepted")
	}
	if body, _ := ws.ReadFile("file"); body != before {
		t.Fatal("failed append changed file")
	}
}
