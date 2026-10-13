package harness

import (
	"context"
	"strings"
	"testing"

	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
)

type fixtureRunner struct{}

func (fixtureRunner) Run(ctx context.Context, workspace *sandbox.Workspace, policy sandbox.SandboxPolicy, request sandbox.CommandRequest, grant *sandbox.NetworkGrant) (sandbox.CommandResult, error) {
	if err := workspace.WriteFile("file", "command change"); err != nil {
		return sandbox.CommandResult{}, err
	}
	return sandbox.CommandResult{Output: "command output"}, nil
}

func TestSandboxToolsWiring(t *testing.T) {
	for _, profile := range []string{"local", "isolated"} {
		t.Run(profile, func(t *testing.T) {
			box, err := sandbox.NewSandbox(t.TempDir(), sandbox.DefaultSandboxPolicy(profile))
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			box.Runner = fixtureRunner{}
			agent := NewAgent("", model.Provider{}, "custom prompt", "")
			agent.ConfigureSandbox(box)
			if agent.Workspace != box.Workspace {
				t.Fatal("attachments use a different workspace")
			}
			if result := agent.Tools.Call("write_file", `{"path":"file","content":"initial"}`); !strings.HasPrefix(result, "wrote") {
				t.Fatal(result)
			}
			if result := agent.Tools.Call("shell", `{"command":"fixture"}`); !strings.Contains(result, "command output") {
				t.Fatal(result)
			}
			if result := agent.Tools.Call("read_file", `{"path":"file"}`); result != "command change" {
				t.Fatal(result)
			}
			if profile == "local" {
				if body, _ := box.Original.ReadFile("file"); body != "command change" {
					t.Fatal("local changes not applied")
				}
				return
			}
			if result := agent.Tools.Call("apply_changes", `{}`); !strings.HasPrefix(result, "error:") {
				t.Fatal("direct call bypassed approval")
			}
			if review := agent.Tools.Call("review_changes", `{}`); !strings.Contains(review, "command change") {
				t.Fatal(review)
			}
			approved := false
			agent.Approve = func(name, args string) bool {
				approved = name == "apply_changes" && strings.Contains(args, "command change")
				return approved
			}
			if result := agent.Tools.Call("apply_changes", `{}`); !strings.HasPrefix(result, "applied") || !approved {
				t.Fatal(result)
			}
			if body, _ := box.Original.ReadFile("file"); body != "command change" {
				t.Fatal("isolated changes not applied")
			}
		})
	}
}
