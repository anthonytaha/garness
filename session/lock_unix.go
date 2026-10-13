//go:build darwin || linux

package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

func lockSessionFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open session lock: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("session lock is not a regular file")
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("session is in use by another Garness process")
		}
		return nil, fmt.Errorf("lock session: %w", err)
	}
	// Older releases recorded a PID but did not hold an OS lock. Respect an
	// old live writer and recover its marker only after that process exits.
	legacy, err := io.ReadAll(io.LimitReader(file, 64))
	if err != nil {
		file.Close()
		return nil, err
	}
	if text := strings.TrimSpace(string(legacy)); text != "" {
		pid, parseErr := strconv.Atoi(text)
		if parseErr != nil || pid <= 0 {
			file.Close()
			return nil, fmt.Errorf("invalid legacy session lock: %s", path)
		}
		probe := syscall.Kill(pid, 0)
		if !errors.Is(probe, syscall.ESRCH) {
			file.Close()
			return nil, fmt.Errorf("session is in use by process %d (legacy lock)", pid)
		}
	}
	if err = file.Truncate(0); err != nil {
		file.Close()
		return nil, err
	}
	var once sync.Once
	// Keep the lock file: removing it permits different processes to lock
	// different inodes for the same session while a writer is still active.
	return func() { once.Do(func() { file.Close() }) }, nil
}
