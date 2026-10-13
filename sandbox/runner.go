package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type CommandRequest struct {
	Command string
	Cwd     string
	Domains []string
	Timeout time.Duration
}

type CommandResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}

type CommandRunner interface {
	Run(context.Context, *Workspace, SandboxPolicy, CommandRequest, *NetworkGrant) (CommandResult, error)
}

type OSCommandRunner struct{}

type limitedOutput struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	count := len(data)
	remaining := w.limit - w.buffer.Len()
	if count > remaining {
		data = data[:remaining]
		w.truncated = true
	}
	w.buffer.Write(data)
	return count, nil
}

func (OSCommandRunner) Check(network bool) error { return checkBackend(network) }

func systemReadRoots(policy SandboxPolicy) ([]string, error) {
	roots := defaultReadRoots()
	if policy.Profile == "local" {
		roots = append(roots, localReadRoots()...)
	}
	roots = append(roots, policy.ReadRoots...)
	var existing []string
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("toolchain read roots must be absolute")
		}
		resolved, err := filepath.EvalSymlinks(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if resolved == "/" {
			return nil, fmt.Errorf("the filesystem root cannot be a toolchain read root")
		}
		existing = append(existing, resolved)
	}
	return existing, nil
}

func (OSCommandRunner) Run(ctx context.Context, workspace *Workspace, policy SandboxPolicy, request CommandRequest, grant *NetworkGrant) (CommandResult, error) {
	directory, err := os.MkdirTemp("", "garness-command-")
	if err != nil {
		return CommandResult{}, err
	}
	defer os.RemoveAll(directory)
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return CommandResult{}, err
	}
	home := filepath.Join(directory, "home")
	temp := filepath.Join(directory, "tmp")
	for _, path := range []string{home, temp} {
		if err = os.Mkdir(path, 0700); err != nil {
			return CommandResult{}, err
		}
	}
	roots, err := systemReadRoots(policy)
	if err != nil {
		return CommandResult{}, err
	}
	cmd, err := prepareCommand(ctx, workspace, policy, request, grant, directory, home, temp, roots)
	if err != nil {
		return CommandResult{}, err
	}

	output := &limitedOutput{limit: policy.OutputLimit}
	cmd.Stdout, cmd.Stderr = output, output
	configureProcess(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessGroup(cmd)
	}
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("sandbox could not start: %w", err)
	}
	err = cmd.Wait()
	killProcessGroup(cmd)
	if ctx.Err() != nil {
		return CommandResult{}, fmt.Errorf("command cancelled: %w; output: %s", ctx.Err(), output.buffer.String())
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return CommandResult{}, fmt.Errorf("sandbox command failed: %w", err)
		}
	}
	return CommandResult{Output: output.buffer.String(), ExitCode: cmd.ProcessState.ExitCode(), Truncated: output.truncated}, nil
}

func commandEnvironment(home, temp, proxy, profile string) []string {
	environment := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/go/bin", "HOME=" + home, "TMPDIR=" + temp, "TMP=" + temp, "TEMP=" + temp, "LANG=C", "GARNESS_SANDBOX_PROFILE=" + profile, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if profile == "local" {
		for _, path := range localCommandPaths() {
			environment[0] += ":" + path
		}
	}

	if proxy != "" {
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"} {
			environment = append(environment, name+"="+proxy)
		}
		environment = append(environment, "NO_PROXY=", "no_proxy=")
	}
	return environment
}
