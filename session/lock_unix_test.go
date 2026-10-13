//go:build darwin || linux

package session

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLockCrashRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockChild$")
	cmd.Env = append(os.Environ(), "GARNESS_TEST_LOCK="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("child failed to acquire lock")
	}
	if unlock, err := lockSessionFile(path); err == nil {
		unlock()
		t.Fatal("locked active writer")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	unlock, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("crash left lock: %v", err)
	}
	unlock()
	unlock()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("lock target removed")
	}
	// Reproduce a legacy crash marker using the PID of the reaped child.
	if err = os.WriteFile(path, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err = lockSessionFile(path)
	if err != nil {
		t.Fatalf("legacy crash left lock: %v", err)
	}
	unlock()
	if err = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err = lockSessionFile(path); err == nil {
		unlock()
		t.Fatal("ignored live legacy writer")
	}
}

func TestLockChild(t *testing.T) {
	path := os.Getenv("GARNESS_TEST_LOCK")
	if path == "" {
		return
	}
	unlock, err := lockSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("locked")
	bufio.NewReader(os.Stdin).ReadByte()
}
