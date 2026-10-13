package sandbox

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strings"
)

type WorkspaceChange struct {
	Path   string
	Kind   string
	Before string
	After  string
}

func fileHash(file snapshotFile, exists bool) string {
	if !exists {
		return "absent"
	}
	return fmt.Sprintf("%x:%o", sha256.Sum256(file.Body), file.Mode.Type()|file.Mode.Perm())
}

func workspaceChanges(before, after map[string]snapshotFile) []WorkspaceChange {
	paths := make(map[string]bool)
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	var changes []WorkspaceChange
	for path := range paths {
		if protectedPath(path, true) {
			continue
		}
		old, oldOK := before[path]
		new, newOK := after[path]
		oldHash, newHash := fileHash(old, oldOK), fileHash(new, newOK)
		if oldHash == newHash {
			continue
		}
		kind := "modified"
		if !oldOK {
			kind = "added"
		}
		if !newOK {
			kind = "deleted"
		}
		changes = append(changes, WorkspaceChange{Path: path, Kind: kind, Before: oldHash, After: newHash})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func (s *Sandbox) Changes() (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Policy.Profile != "isolated" {
		return "local changes are applied automatically", nil
	}
	after, err := scanWorkspace(s.Workspace, s.Policy, false)
	if err != nil {
		return "", err
	}
	return changeReview(workspaceChanges(s.baseline, after), s.baseline, after), nil
}

func changeReview(changes []WorkspaceChange, before, after map[string]snapshotFile) string {
	var result strings.Builder
	for _, change := range changes {
		fmt.Fprintf(&result, "%s %q\nbefore: %s\nafter: %s\n", change.Kind, change.Path, change.Before, change.After)
		old, new := before[change.Path], after[change.Path]
		if len(old.Body)+len(new.Body) <= 32768 {
			fmt.Fprintf(&result, "--- original\n%q\n+++ sandbox\n%q\n", old.Body, new.Body)
		} else {
			result.WriteString("content too large for inline review; inspect the sandbox file with read_file before applying\n")
		}
	}
	if len(changes) == 0 {
		return "no changes"
	}
	return result.String()
}

func (s *Sandbox) ApplyChanges() (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Policy.Profile != "isolated" {
		return "", fmt.Errorf("only isolated changes require applying")
	}
	after, err := scanWorkspace(s.Workspace, s.Policy, false)
	if err != nil {
		return "", err
	}
	changes := workspaceChanges(s.baseline, after)
	if len(changes) == 0 {
		return "no changes", nil
	}
	review := changeReview(changes, s.baseline, after)
	if s.Approve == nil || !s.Approve("apply_changes", review) {
		return "", &ApprovalDeniedError{Action: "change application"}
	}
	if err = applySnapshot(s.Original, s.Policy, s.baseline, after); err != nil {
		return "", err
	}
	s.baseline = after
	return fmt.Sprintf("applied %d changes", len(changes)), nil
}

func applySnapshot(original *Workspace, policy SandboxPolicy, before, after map[string]snapshotFile) error {
	current, err := scanWorkspace(original, policy, false)
	if err != nil {
		return err
	}
	changes := workspaceChanges(before, after)
	for _, change := range changes {
		file, exists := current[change.Path]
		if fileHash(file, exists) != change.Before {
			return fmt.Errorf("original changed since snapshot: %s", change.Path)
		}
	}
	for _, change := range changes {
		old, oldOK := before[change.Path]
		new, newOK := after[change.Path]
		if oldOK && newOK && old.Mode.IsDir() != new.Mode.IsDir() {
			return fmt.Errorf("file/directory replacement requires separate commands: %s", change.Path)
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if (changes[i].Kind == "deleted") != (changes[j].Kind == "deleted") {
			return changes[i].Kind == "deleted"
		}
		left, right := strings.Count(changes[i].Path, "/"), strings.Count(changes[j].Path, "/")
		if left != right {
			if changes[i].Kind == "deleted" {
				return left > right
			}
			return left < right
		}
		return changes[i].Path < changes[j].Path
	})
	var applied []WorkspaceChange
	for _, change := range changes {
		file, exists := after[change.Path]
		err = applyFileState(original, change.Path, file, exists)
		if err != nil {
			var rollback []string
			for i := len(applied) - 1; i >= 0; i-- {
				path := applied[i].Path
				old, exists := before[path]
				var restoreErr error
				restoreErr = applyFileState(original, path, old, exists)
				if restoreErr != nil {
					rollback = append(rollback, path+": "+restoreErr.Error())
				}
			}
			return fmt.Errorf("apply %s: %w (rollback errors: %v)", change.Path, err, rollback)
		}
		applied = append(applied, change)
	}
	return nil
}

func applyFileState(ws *Workspace, path string, file snapshotFile, exists bool) error {
	if !exists {
		return ws.removePath(path)
	}
	if !file.Mode.IsDir() {
		return ws.writeBytes(path, file.Body, file.Mode, false)
	}
	root, name, err := ws.parent(path, true, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Mkdir(name, 0755); os.IsExist(err) {
		info, statErr := root.Lstat(name)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", path)
		}
		return nil
	}
	return err
}
