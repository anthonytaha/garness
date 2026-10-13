package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State preserves an isolated sandbox's baseline and uncommitted files. It
// excludes secrets and protected configuration, except read-only AGENTS.md.
type State struct {
	Baseline map[string]StateFile `json:"baseline"`
	Files    map[string]StateFile `json:"files"`
}

type StateFile struct {
	Body []byte      `json:"body,omitempty"`
	Mode os.FileMode `json:"mode"`
}

type ApprovalDeniedError struct{ Action string }

func (e *ApprovalDeniedError) Error() string { return e.Action + " denied" }

func stateFiles(files map[string]snapshotFile) map[string]StateFile {
	out := make(map[string]StateFile, len(files))
	for path, file := range files {
		out[path] = StateFile{Body: append([]byte(nil), file.Body...), Mode: file.Mode}
	}
	return out
}

func validateState(files map[string]StateFile, policy SandboxPolicy) (map[string]snapshotFile, error) {
	if files == nil || len(files) > 10000 {
		return nil, fmt.Errorf("invalid snapshot entries")
	}
	out := make(map[string]snapshotFile, len(files))
	var total int64
	for path, file := range files {
		if path == "." || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("invalid snapshot path: %s", path)
		}
		instruction := filepath.Base(path) == "AGENTS.md" && !protectedPath(filepath.Dir(path), true)
		if protectedPath(path, true) && !instruction {
			return nil, fmt.Errorf("protected snapshot path: %s", path)
		}
		if !file.Mode.IsRegular() && !file.Mode.IsDir() {
			return nil, fmt.Errorf("invalid snapshot file: %s", path)
		}
		if file.Mode.IsDir() && len(file.Body) != 0 {
			return nil, fmt.Errorf("directory has content: %s", path)
		}
		if int64(len(file.Body)) > policy.FileLimit {
			return nil, fmt.Errorf("snapshot file exceeds limit: %s", path)
		}
		total += int64(len(file.Body))
		if total > policy.SnapshotLimit {
			return nil, fmt.Errorf("snapshot exceeds limit")
		}
		mode := file.Mode.Type() | file.Mode.Perm()
		if instruction {
			mode = 0444
		}
		out[path] = snapshotFile{Body: append([]byte(nil), file.Body...), Mode: mode}
	}
	return out, nil
}

func (s *Sandbox) ExportState() (*State, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Policy.Profile != "isolated" {
		return nil, fmt.Errorf("state requires an isolated sandbox")
	}
	files, err := scanWorkspace(s.Workspace, s.Policy, true)
	if err != nil {
		return nil, err
	}
	baseline := stateFiles(s.baseline)
	// ApplyChanges refreshes its baseline using writable files only. Keep
	// read-only instructions in exported states after successful copy-back.
	for path, file := range files {
		if filepath.Base(path) == "AGENTS.md" {
			if _, ok := baseline[path]; !ok {
				baseline[path] = StateFile{Body: append([]byte(nil), file.Body...), Mode: 0444}
			}
		}
	}
	return &State{Baseline: baseline, Files: stateFiles(files)}, nil
}

// RestoreSandbox restores files in a fresh temporary directory. The original
// workspace is untouched until an approved ApplyChanges call.
func RestoreSandbox(root string, policy SandboxPolicy, state *State) (*Sandbox, error) {
	if state == nil {
		return nil, fmt.Errorf("missing sandbox state")
	}
	policy.Profile = "isolated"
	if policy.FileLimit <= 0 || policy.SnapshotLimit < policy.FileLimit || policy.Timeout <= 0 || policy.Timeout > 10*time.Minute || policy.OutputLimit <= 0 {
		return nil, fmt.Errorf("invalid sandbox limits")
	}
	baseline, err := validateState(state.Baseline, policy)
	if err != nil {
		return nil, err
	}
	files, err := validateState(state.Files, policy)
	if err != nil {
		return nil, err
	}
	// Instructions are immutable, including across artifact restoration.
	for path, file := range baseline {
		if filepath.Base(path) == "AGENTS.md" {
			current, exists := files[path]
			if !exists || string(current.Body) != string(file.Body) {
				return nil, fmt.Errorf("snapshot instructions changed: %s", path)
			}
		}
	}
	for path := range files {
		if filepath.Base(path) == "AGENTS.md" {
			if _, ok := baseline[path]; !ok {
				return nil, fmt.Errorf("snapshot instructions added: %s", path)
			}
		}
	}
	original, err := NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	original.FileLimit = policy.FileLimit
	directory, err := os.MkdirTemp("", "garness-restored-")
	if err != nil {
		return nil, err
	}
	ws, err := NewWorkspace(directory)
	if err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	ws.FileLimit = policy.FileLimit
	for path, file := range files {
		destination := filepath.Join(ws.Root, path)
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err == nil {
			if file.Mode.IsDir() {
				err = os.MkdirAll(destination, 0755)
			} else {
				err = os.WriteFile(destination, file.Body, file.Mode.Perm())
			}
		}
		if err != nil {
			os.RemoveAll(directory)
			return nil, err
		}
	}
	return &Sandbox{Policy: policy, Original: original, Workspace: ws, Runner: OSCommandRunner{}, baseline: baseline, directory: directory}, nil
}

func StateChanges(state *State, policy SandboxPolicy) ([]WorkspaceChange, error) {
	if state == nil {
		return nil, fmt.Errorf("missing sandbox state")
	}
	before, err := validateState(state.Baseline, policy)
	if err != nil {
		return nil, err
	}
	after, err := validateState(state.Files, policy)
	if err != nil {
		return nil, err
	}
	return workspaceChanges(before, after), nil
}

// Integrate merges a worker delta into the coordinator workspace. It retains
// the coordinator baseline for final user review and rejects stale file hashes.
func (s *Sandbox) Integrate(state *State) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Policy.Profile != "isolated" || state == nil {
		return fmt.Errorf("integration requires isolated state")
	}
	before, err := validateState(state.Baseline, s.Policy)
	if err != nil {
		return err
	}
	after, err := validateState(state.Files, s.Policy)
	if err != nil {
		return err
	}
	return applySnapshot(s.Workspace, s.Policy, before, after)
}

// ReconcileApply compares only the reviewed delta. Unrelated original edits
// are permitted, but mixed or divergent copy-back results remain blocked.
func (s *Sandbox) ReconcileApply() (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	after, err := scanWorkspace(s.Workspace, s.Policy, false)
	if err != nil {
		return false, err
	}
	current, err := scanWorkspace(s.Original, s.Policy, false)
	if err != nil {
		return false, err
	}
	allBefore, allAfter := true, true
	for _, change := range workspaceChanges(s.baseline, after) {
		file, exists := current[change.Path]
		hash := fileHash(file, exists)
		allBefore = allBefore && hash == change.Before
		allAfter = allAfter && hash == change.After
	}
	if allAfter {
		s.baseline = after
		return true, nil
	}
	if allBefore {
		return false, nil
	}
	return false, fmt.Errorf("copy-back is partial or original files diverged; reconcile files to the reviewed baseline or result")
}
