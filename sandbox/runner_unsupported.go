//go:build !darwin && !linux

package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
)

func unsupportedPlatform() error {
	return fmt.Errorf("shell sandbox is unsupported on %s", runtime.GOOS)
}
func defaultReadRoots() []string                   { return nil }
func localReadRoots() []string                     { return nil }
func localCommandPaths() []string                  { return nil }
func checkBackend(network bool) error              { return unsupportedPlatform() }
func grantListener() (net.Listener, func(), error) { return nil, nil, unsupportedPlatform() }
func prepareCommand(ctx context.Context, workspace *Workspace, policy SandboxPolicy, request CommandRequest, grant *NetworkGrant, directory, home, temp string, roots []string) (*exec.Cmd, error) {
	return nil, unsupportedPlatform()
}
func configureProcess(cmd *exec.Cmd)                               {}
func killProcessGroup(cmd *exec.Cmd) error                         { return unsupportedPlatform() }
func openDirectory(root *os.Root, name string) (*os.File, error)   { return nil, unsupportedPlatform() }
func openRegularFile(root *os.Root, name string) (*os.File, error) { return nil, unsupportedPlatform() }
func openTemporaryFile(root *os.Root, name string, mode os.FileMode) (*os.File, error) {
	return nil, unsupportedPlatform()
}
func multipleLinks(info os.FileInfo) bool { return false }
