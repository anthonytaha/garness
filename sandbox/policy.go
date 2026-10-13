package sandbox

import (
	"path/filepath"
	"strings"
	"time"
)

const DEFAULT_SNAPSHOT_LIMIT int64 = 64 << 20

type SandboxPolicy struct {
	Profile       string
	FileLimit     int64
	SnapshotLimit int64
	Timeout       time.Duration
	OutputLimit   int
	ReadRoots     []string
}

func DefaultSandboxPolicy(profile string) SandboxPolicy {
	return SandboxPolicy{
		Profile:       profile,
		FileLimit:     DEFAULT_FILE_LIMIT,
		SnapshotLimit: DEFAULT_SNAPSHOT_LIMIT,
		Timeout:       2 * time.Minute,
		OutputLimit:   1 << 20,
	}
}

func protectedPath(path string, write bool) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".env" || strings.HasPrefix(part, ".env.") && part != ".env.example" {
			return true
		}
		if write && (part == ".git" || part == "AGENTS.md" || part == ".garness" || part == ".agents" || part == ".codex") {
			return true
		}
	}
	return false
}
