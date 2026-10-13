package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShellSandboxIntegration(t *testing.T) {
	if os.Getenv("GARNESS_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set GARNESS_SANDBOX_INTEGRATION=1 to test the installed OS backend")
	}
	if err := checkBackend(false); err != nil {
		t.Fatal(err)
	}
	ws := testWorkspace(t)
	ws.WriteFile("file", "original")
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.WriteFile(filepath.Join(ws.Root, ".env"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(ws.Root, "AGENTS.md"), []byte("instructions"), 0644)
	sandbox, err := NewSandbox(ws.Root, DefaultSandboxPolicy("local"))
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	quoted, _ := json.Marshal(outside)
	command := fmt.Sprintf(`printf changed > file; test ! -e .env; ! cat %s; ! printf hacked > AGENTS.md; printf 'network: '; curl --noproxy '*' --connect-timeout 1 -s http://1.1.1.1; test $? -ne 0`, quoted)
	result, err := sandbox.Run(CommandRequest{Command: command})
	if err != nil || !strings.Contains(result, "exit code: 0") {
		t.Fatalf("%s %v", result, err)
	}
	for _, path := range []string{outside, "/System/Volumes/Data" + outside} {
		quoted, _ := json.Marshal(path)
		result, err = sandbox.Run(CommandRequest{Command: fmt.Sprintf("if cat %s >/dev/null 2>&1; then exit 9; fi", quoted)})
		if err != nil || !strings.Contains(result, "exit code: 0") {
			t.Fatalf("outside read succeeded: %s %v", result, err)
		}
	}
	t.Setenv("GARNESS_TEST_SECRET", "secret")
	result, err = sandbox.Run(CommandRequest{Command: `test -z "${GARNESS_TEST_SECRET:-}" || exit 9; go version`})
	if err != nil || !strings.Contains(result, "go version") {
		t.Fatalf("Go toolchain unavailable: %s %v", result, err)
	}
	if body, _ := ws.ReadFile("file"); body != "changed" {
		t.Fatal(body)
	}
	result, err = sandbox.Run(CommandRequest{Command: "if printf hacked > AGENTS.md; then exit 9; fi"})
	if err != nil || !strings.Contains(result, "exit code: 0") {
		t.Fatalf("instruction write permitted: %s %v", result, err)
	}
	if body, _ := os.ReadFile(filepath.Join(ws.Root, "AGENTS.md")); string(body) != "instructions" {
		t.Fatal("modified instructions")
	}
	sandbox.Policy.OutputLimit = 20
	result, err = sandbox.Run(CommandRequest{Command: "printf '%0100d' 0"})
	if err != nil || !strings.Contains(result, "output truncated") {
		t.Fatalf("%s %v", result, err)
	}
	started := time.Now()
	_, err = sandbox.Run(CommandRequest{Command: "sleep 30 & wait", Timeout: 100 * time.Millisecond})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatal("timeout failed", err)
	}
	if runtime.GOOS == "linux" {
		t.Log("Linux namespace backend tested")
	}
}

func TestShellProxyIntegration(t *testing.T) {
	if os.Getenv("GARNESS_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set GARNESS_SANDBOX_INTEGRATION=1 to test the installed OS backend")
	}
	if err := checkBackend(true); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Errorf("unexpected host %s", r.Host)
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://other.com", http.StatusFound)
			return
		}
		fmt.Fprint(w, "approved response")
	}))
	defer upstream.Close()
	var listener net.Listener
	var err error
	if runtime.GOOS == "linux" {
		listener, err = net.Listen("unix", filepath.Join(t.TempDir(), "proxy.sock"))
	} else {
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	grant, err := newNetworkGrant(listener, map[string]bool{"example.com": true})
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	// A local fixture replaces public DNS/dialing; the sandbox must still use
	// the authenticated filtering proxy to reach it.
	grant.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(upstream.URL, "http://"))
	}
	ws := testWorkspace(t)
	result, err := (OSCommandRunner{}).Run(context.Background(), ws, DefaultSandboxPolicy("local"), CommandRequest{Command: `curl -fsS http://example.com; printf '\ndenied: '; curl -s -o /dev/null -w '%{http_code}' http://other.com; printf '\nredirect: '; curl -L -s -o /dev/null -w '%{http_code}' http://example.com/redirect; printf '\ndirect: '; curl --noproxy '*' --connect-timeout 1 -s http://1.1.1.1; test $? -ne 0`, Cwd: "."}, grant)
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "approved response") || !strings.Contains(result.Output, "denied: 403") || !strings.Contains(result.Output, "redirect: 403") {
		t.Fatalf("%+v %v", result, err)
	}
}
