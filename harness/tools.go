package harness

import (
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/tools"
)

func (a *Agent) ConfigureSandbox(s *sandbox.Sandbox) {
	a.Workspace = s.Workspace
	s.Approve = a.requestApproval
	a.Tools.Approve = a.requestApproval
	a.Tools.Register(tools.ReadFileTool(s.Workspace))
	a.Tools.Register(tools.WriteFileTool(s.Workspace))
	a.Tools.Register(tools.ShellTool(s))
	a.Tools.Register(tools.ReviewChangesTool(s))
	a.Tools.Register(tools.ApplyChangesTool(s))
}
