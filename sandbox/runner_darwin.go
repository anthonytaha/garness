package sandbox

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func defaultReadRoots() []string {
	return []string{"/System/Library", "/System/Volumes/Preboot/Cryptexes/OS", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/libexec", "/usr/share", "/usr/local/go", "/bin", "/sbin", "/Library/Developer", "/Applications/Xcode.app", "/etc/ssl"}
}
func localReadRoots() []string {
	return []string{"/usr/local/bin", "/usr/local/lib", "/usr/local/share", "/opt/homebrew"}
}
func localCommandPaths() []string { return []string{"/usr/local/bin", "/opt/homebrew/bin"} }
func checkBackend(network bool) error {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return fmt.Errorf("shell requires sandbox-exec: %w", err)
	}
	return nil
}
func grantListener() (net.Listener, func(), error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	return listener, func() {}, err
}

func prepareCommand(ctx context.Context, workspace *Workspace, policy SandboxPolicy, request CommandRequest, grant *NetworkGrant, directory, home, temp string, roots []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	proxy := ""
	shell := "/bin/sh"
	profile := macProfile(workspace.Root, directory, roots, grant)
	cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", profile, shell, "-c", request.Command)
	cmd.Dir = filepath.Join(workspace.Root, request.Cwd)
	if grant != nil {
		proxy = grant.URL(grant.listener.Addr().String())
	}
	cmd.Env = commandEnvironment(home, temp, proxy, policy.Profile)
	return cmd, nil
}

func macProfile(workspace, runtimeDir string, roots []string, grant *NetworkGrant) string {
	var profile strings.Builder
	profile.WriteString("(version 1)\n(deny default)\n(allow signal (target same-sandbox))\n(allow sysctl-read)\n(allow file-read-metadata)\n(allow file-read* (literal \"/\"))\n")
	// Process execution and forking need unconditional rules; signals stay limited.
	profile.WriteString("(allow process-exec process-fork)\n(allow file-read* (literal \"/dev/null\") (literal \"/dev/random\") (literal \"/dev/urandom\"))\n(allow file-write* (literal \"/dev/null\"))\n")
	for _, root := range roots {
		fmt.Fprintf(&profile, "(allow file-read* file-map-executable (subpath %s))\n", strconv.Quote(root))
	}
	for _, root := range []string{workspace, runtimeDir} {
		fmt.Fprintf(&profile, "(allow file-read* file-map-executable file-write* (subpath %s))\n", strconv.Quote(root))
	}
	profile.WriteString(`(deny file-read* file-write* (regex #"(^|/)\.env($|/)"))` + "\n")
	profile.WriteString(`(deny file-write* (regex #"(^|/)(\.git|AGENTS\.md|\.garness|\.agents|\.codex)($|/)"))` + "\n")
	if grant != nil {
		_, port, _ := net.SplitHostPort(grant.listener.Addr().String())
		fmt.Fprintf(&profile, "(allow network-outbound (remote tcp \"localhost:%s\"))\n", port)
	}
	return profile.String()
}
