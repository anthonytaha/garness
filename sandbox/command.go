package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Sandbox) Run(request CommandRequest) (string, error) {
	return s.RunContext(context.Background(), request)
}

func (s *Sandbox) RunContext(parent context.Context, request CommandRequest) (string, error) {
	result, err := s.RunResultContext(parent, request)
	if err != nil {
		return "", err
	}
	suffix := ""
	if result.Truncated {
		suffix = "\n[output truncated]"
	}
	return fmt.Sprintf("exit code: %d\n%s%s", result.ExitCode, result.Output, suffix), nil
}

func (s *Sandbox) RunResultContext(parent context.Context, request CommandRequest) (CommandResult, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := parent.Err(); err != nil {
		return CommandResult{}, err
	}
	if request.Timeout <= 0 {
		request.Timeout = s.Policy.Timeout
	}
	if request.Timeout > s.Policy.Timeout {
		return CommandResult{}, fmt.Errorf("timeout exceeds sandbox policy")
	}
	domains, err := approvedDomains(request.Domains)
	if err != nil {
		return CommandResult{}, err
	}
	if strings.TrimSpace(request.Command) == "" {
		return CommandResult{}, fmt.Errorf("command must not be empty")
	}
	if s.Runner == nil {
		return CommandResult{}, fmt.Errorf("command runner is not configured")
	}
	if runner, ok := s.Runner.(interface{ Check(bool) error }); ok {
		if err = runner.Check(len(domains) > 0); err != nil {
			return CommandResult{}, err
		}
	}
	workspace := s.Workspace
	var baseline map[string]snapshotFile
	if s.Policy.Profile == "local" {
		var directory string
		directory, workspace, baseline, err = createSnapshot(s.Original, s.Policy)
		if err != nil {
			return CommandResult{}, err
		}
		defer os.RemoveAll(directory)
	}
	cwd := filepath.Clean(request.Cwd)
	if filepath.IsAbs(cwd) || cwd == ".." || strings.HasPrefix(cwd, ".."+string(filepath.Separator)) || protectedPath(cwd, true) {
		return CommandResult{}, fmt.Errorf("cwd must be an unprotected workspace-relative directory")
	}
	request.Cwd = cwd
	cwdRoot, _, err := workspace.parent(filepath.Join(cwd, ".garness-cwd"), false, false)
	if err != nil {
		return CommandResult{}, fmt.Errorf("cwd is not a workspace directory: %w", err)
	}
	cwdRoot.Close()

	var grant *NetworkGrant
	if len(domains) > 0 {
		if s.Approve == nil || !s.Approve("shell_network", networkApproval(request)) {
			return CommandResult{}, &ApprovalDeniedError{Action: "network access"}
		}
		listener, cleanup, listenErr := grantListener()
		if listenErr != nil {
			return CommandResult{}, listenErr
		}
		defer cleanup()

		grant, err = newNetworkGrant(listener, domains)
		if err != nil {
			return CommandResult{}, err
		}
		defer grant.Close()
	}
	ctx, cancel := context.WithTimeout(parent, request.Timeout)
	defer cancel()
	result, err := s.Runner.Run(ctx, workspace, s.Policy, request, grant)
	if grant != nil {
		grant.Close()
	}
	if ctx.Err() != nil {
		return CommandResult{}, fmt.Errorf("command cancelled: %w", ctx.Err())
	}
	if err != nil {
		return CommandResult{}, err
	}
	if s.Policy.Profile == "local" {
		after, scanErr := scanWorkspace(workspace, s.Policy, false)
		if scanErr != nil {
			return CommandResult{}, fmt.Errorf("command changes rejected: %w", scanErr)
		}
		if err = applySnapshot(s.Original, s.Policy, baseline, after); err != nil {
			return CommandResult{}, err
		}
	}
	return result, nil
}

func networkApproval(request CommandRequest) string {
	data, _ := json.Marshal(struct {
		Command string   `json:"command"`
		Cwd     string   `json:"cwd"`
		Domains []string `json:"domains"`
	}{request.Command, request.Cwd, request.Domains})
	return string(data)
}
