package sandbox

import (
	"context"
	"testing"
)

func testWorkspace(t *testing.T) *Workspace {
	t.Helper()
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

type fixtureCommandRunner struct {
	ran   bool
	grant *NetworkGrant
}

func (r *fixtureCommandRunner) Run(ctx context.Context, ws *Workspace, policy SandboxPolicy, request CommandRequest, grant *NetworkGrant) (CommandResult, error) {
	r.ran = true
	r.grant = grant
	if err := ws.WriteFile("result", "created"); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Output: "fixture", ExitCode: 0}, nil
}
