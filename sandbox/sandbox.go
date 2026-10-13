package sandbox

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Sandbox struct {
	Policy    SandboxPolicy
	Original  *Workspace
	Workspace *Workspace
	Approve   func(name, arguments string) bool
	Runner    CommandRunner
	mutex     sync.Mutex
	baseline  map[string]snapshotFile
	directory string
}

func NewSandbox(root string, policy SandboxPolicy) (*Sandbox, error) {
	if policy.Profile != "local" && policy.Profile != "isolated" {
		return nil, fmt.Errorf("sandbox profile must be local or isolated")
	}
	if policy.FileLimit <= 0 || policy.SnapshotLimit < policy.FileLimit || policy.Timeout <= 0 || policy.Timeout > 10*time.Minute || policy.OutputLimit <= 0 {
		return nil, fmt.Errorf("invalid sandbox limits")
	}
	original, err := NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	original.FileLimit = policy.FileLimit
	sandbox := &Sandbox{Policy: policy, Original: original, Workspace: original, Runner: OSCommandRunner{}}
	if policy.Profile == "isolated" {
		sandbox.directory, sandbox.Workspace, sandbox.baseline, err = createSnapshot(original, policy)
		if err != nil {
			return nil, err
		}
	}
	return sandbox, nil
}

func (s *Sandbox) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.directory != "" {
		return os.RemoveAll(s.directory)
	}
	return nil
}
