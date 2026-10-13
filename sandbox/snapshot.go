package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type snapshotFile struct {
	Body []byte
	Mode os.FileMode
}

// Snapshots contain regular files and directories only. Secrets and agent configuration never
// enter the command's filesystem; readable AGENTS.md files are copied read-only.
func scanWorkspace(ws *Workspace, policy SandboxPolicy, copyInstructions bool) (map[string]snapshotFile, error) {
	root, err := os.OpenRoot(ws.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := make(map[string]snapshotFile)
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		protected := protectedPath(path, true)
		if protected && !(copyInstructions && filepath.Base(path) == "AGENTS.md" && !protectedPath(filepath.Dir(path), true)) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot does not accept symlinks: %s", path)
		}
		if entry.IsDir() {
			files[path] = snapshotFile{Mode: os.ModeDir | 0755}
			if len(files) > 10000 {
				return fmt.Errorf("workspace exceeds 10000 entries")
			}
			return nil
		}
		body, mode, err := ws.readBytes(path)
		if err != nil {
			return err
		}
		total += int64(len(body))
		if total > policy.SnapshotLimit {
			return fmt.Errorf("workspace exceeds snapshot limit of %d bytes", policy.SnapshotLimit)
		}
		files[path] = snapshotFile{Body: body, Mode: mode}
		if len(files) > 10000 {
			return fmt.Errorf("workspace exceeds 10000 entries")
		}
		return nil
	})
	return files, err
}

func createSnapshot(original *Workspace, policy SandboxPolicy) (string, *Workspace, map[string]snapshotFile, error) {
	baseline, err := scanWorkspace(original, policy, true)
	if err != nil {
		return "", nil, nil, err
	}
	directory, err := os.MkdirTemp("", "garness-sandbox-")
	if err != nil {
		return "", nil, nil, err
	}
	workspace, err := NewWorkspace(directory)
	if err != nil {
		os.RemoveAll(directory)
		return "", nil, nil, err
	}
	workspace.FileLimit = policy.FileLimit
	for path, file := range baseline {
		destination := filepath.Join(workspace.Root, path)
		if file.Mode.IsDir() {
			if err = os.MkdirAll(destination, 0755); err != nil {
				os.RemoveAll(directory)
				return "", nil, nil, err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err == nil {
			mode := file.Mode & 0777
			if protectedPath(path, true) {
				mode = 0444
			}
			err = os.WriteFile(destination, file.Body, mode)
			if err == nil {
				err = os.Chmod(destination, mode)
			}
		}
		if err != nil {
			os.RemoveAll(directory)
			return "", nil, nil, err
		}
	}
	return directory, workspace, baseline, nil
}
