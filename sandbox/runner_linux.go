package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func defaultReadRoots() []string {
	return []string{"/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64", "/usr/libexec", "/usr/share", "/usr/local/go", "/bin", "/sbin", "/lib", "/lib64", "/etc/ssl", "/etc/pki", "/etc/ld.so.cache"}
}
func localReadRoots() []string {
	return []string{"/usr/local/bin", "/usr/local/lib", "/usr/local/share"}
}
func localCommandPaths() []string { return []string{"/usr/local/bin"} }
func checkBackend(network bool) error {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return fmt.Errorf("shell requires bubblewrap: %w", err)
	}
	if network {
		if _, err := exec.LookPath("socat"); err != nil {
			return fmt.Errorf("approved network access requires socat: %w", err)
		}
	}
	return nil
}
func grantListener() (net.Listener, func(), error) {
	directory, err := os.MkdirTemp("", "garness-network-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(directory) }
	listener, err := net.Listen("unix", filepath.Join(directory, "proxy.sock"))
	if err != nil {
		cleanup()
	}
	return listener, cleanup, err
}

func prepareCommand(ctx context.Context, workspace *Workspace, policy SandboxPolicy, request CommandRequest, grant *NetworkGrant, directory, home, temp string, roots []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	proxy := ""
	shell := "/bin/sh"
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv"}
	seen := make(map[string]bool)
	for _, root := range roots {
		if !seen[root] {
			args = append(args, "--ro-bind", root, root)
			seen[root] = true
		}
	}
	// /bin and /lib may resolve to /usr on merged-/usr distributions.
	for _, path := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		if target, linkErr := os.Readlink(path); linkErr == nil {
			args = append(args, "--symlink", target, path)
		}
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/home", "--dir", "/home/garness", "--bind", workspace.Root, "/workspace", "--chdir", filepath.Join("/workspace", request.Cwd))
	// Preserve copied instructions as read-only mount points.
	root, openErr := os.OpenRoot(workspace.Root)
	if openErr != nil {
		return nil, openErr
	}
	walkErr := filepath.WalkDir(workspace.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "AGENTS.md" {
			relative, _ := filepath.Rel(workspace.Root, path)
			args = append(args, "--ro-bind", path, filepath.Join("/workspace", relative))
		}
		return nil
	})
	root.Close()
	if walkErr != nil {
		return nil, walkErr
	}
	home, temp = "/home/garness", "/tmp"
	if grant != nil {
		args = append(args, "--ro-bind", grant.listener.Addr().String(), "/run/garness-proxy.sock")
		proxy = grant.URL("127.0.0.1:18080")
	}
	environment := commandEnvironment(home, temp, proxy, policy.Profile)
	for _, value := range environment {
		name, value, _ := strings.Cut(value, "=")
		args = append(args, "--setenv", name, value)
	}
	args = append(args, "--", shell, "-c")
	if grant == nil {
		args = append(args, request.Command)
	} else {
		relay, _ := exec.LookPath("socat")
		args = append(args, `"$1" TCP4-LISTEN:18080,bind=127.0.0.1,reuseaddr,fork UNIX-CONNECT:/run/garness-proxy.sock &
relay=$!
trap 'kill "$relay" 2>/dev/null' EXIT
attempt=0
while ! "$1" -u /dev/null TCP4:127.0.0.1:18080,connect-timeout=1 >/dev/null 2>&1; do
 kill -0 "$relay" 2>/dev/null || exit 125
 attempt=$((attempt+1))
 test "$attempt" -lt 40 || exit 125
 sleep 0.05
done
/bin/sh -c "$2"
exit $?`, "garness", relay, request.Command)
	}
	cmd = exec.CommandContext(ctx, "bwrap", args...)
	cmd.Env = commandEnvironment(home, temp, proxy, policy.Profile)
	return cmd, nil
}
