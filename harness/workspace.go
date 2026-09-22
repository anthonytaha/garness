package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Workspace struct {
	Root string
}

// New creates a workspace. An empty root creates a fresh temporary directory.
func NewWorkspace(root string) (*Workspace, error) {
	var err error

	if root == "" {
		root, err = os.MkdirTemp("", "workspace-")
		if err != nil {
			return nil, fmt.Errorf("create temporary workspace: %w", err)
		}
	} else {
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	// Resolve symlinks in the workspace root itself.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	return &Workspace{Root: filepath.Clean(root)}, nil
}

// safe resolves path and verifies that it remains inside the workspace.
func (ws *Workspace) safe(path string) (string, error) {
	var candidate string

	if filepath.IsAbs(path) {
		candidate = filepath.Clean(path)
	} else {
		candidate = filepath.Join(ws.Root, path)
	}

	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	// Resolve existing symlinks. This is especially important if a directory
	// inside the workspace is a symlink pointing outside it.
	resolved, err := resolveExistingPath(candidate)
	if err != nil {
		return "", err
	}

	relative, err := filepath.Rel(ws.Root, resolved)
	if err != nil {
		return "", fmt.Errorf("compare path %q with workspace: %w", path, err)
	}

	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}

	return resolved, nil
}

// resolveExistingPath resolves symlinks even when the final file or some
// trailing directories do not exist yet.
func resolveExistingPath(path string) (string, error) {
	current := path
	var missing []string

	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect path %q: %w", path, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot resolve path: %s", path)
		}

		missing = append(missing, filepath.Base(current))
		current = parent
	}

	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}

	return filepath.Clean(resolved), nil
}

func (ws *Workspace) Write(path, content string) string {
	resolved, err := ws.safe(path)
	if err != nil {
		return "error: " + err.Error()
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Sprintf("error: create parent directories: %v", err)
	}

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("error: write %s: %v", path, err)
	}
	count := utf8.RuneCountInString(content)

	return fmt.Sprintf("wrote %s (%d chars)", path, count)
}

func (ws *Workspace) Read(path string) string {
	resolved, err := ws.safe(path)
	if err != nil {
		return "error: " + err.Error()
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Sprintf("error: no such file: %s", path)
	}

	body, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("error: read %s: %v", path, err)
	}

	return string(body)
}

func (ws *Workspace) Edit(path, old, new string) string {
	resolved, err := ws.safe(path)
	if err != nil {
		return "error: " + err.Error()
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Sprintf("error: no such file: %s", path)
	}

	body, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("error: read %s: %v", path, err)
	}

	text := string(body)
	if !strings.Contains(text, old) {
		return fmt.Sprintf("error: text to replace not found in %s", path)
	}

	// The final argument limits replacement to the first occurrence.
	edited := strings.Replace(text, old, new, 1)

	if err := os.WriteFile(resolved, []byte(edited), info.Mode().Perm()); err != nil {
		return fmt.Sprintf("error: edit %s: %v", path, err)
	}

	return fmt.Sprintf("edited %s", path)
}
