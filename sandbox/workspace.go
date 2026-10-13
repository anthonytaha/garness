package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const DEFAULT_FILE_LIMIT int64 = 4 << 20

var nextFileID atomic.Uint64

type Workspace struct {
	Root      string
	FileLimit int64
	mutation  sync.Mutex
}

func NewWorkspace(root string) (*Workspace, error) {
	var err error
	if root == "" {
		root, err = os.MkdirTemp("", "workspace-")
		if err != nil {
			return nil, err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: root, FileLimit: DEFAULT_FILE_LIMIT}, nil
}

func (ws *Workspace) relative(path string, write bool) (string, error) {
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(ws.Root, path)
		if err != nil {
			return "", err
		}
	}
	path = filepath.Clean(path)
	if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace or names its root: %s", path)
	}
	if protectedPath(path, write) {
		return "", fmt.Errorf("protected path: %s", path)
	}
	return path, nil
}

// parent pins each directory before continuing. O_NOFOLLOW rejects symlink
// aliases, including aliases to protected files, without a check/open race.
func (ws *Workspace) parent(path string, write, create bool) (*os.Root, string, error) {
	path, err := ws.relative(path, write)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(ws.Root)
	if err != nil {
		return nil, "", err
	}
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		if create {
			if err = root.Mkdir(part, 0755); err != nil && !os.IsExist(err) {
				root.Close()
				return nil, "", err
			}
		}
		dir, openErr := openDirectory(root, part)
		if openErr != nil {
			root.Close()
			return nil, "", openErr
		}
		next, openErr := os.OpenRoot(fmt.Sprintf("/dev/fd/%d", dir.Fd()))
		dir.Close()
		root.Close()
		if openErr != nil {
			return nil, "", openErr
		}
		root = next
	}
	return root, parts[len(parts)-1], nil
}

func (ws *Workspace) readBytes(path string) ([]byte, os.FileMode, error) {
	root, name, err := ws.parent(path, false, false)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	file, err := openRegularFile(root, name)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("not a regular file: %s", path)
	}
	if multipleLinks(info) {
		return nil, 0, fmt.Errorf("hard-linked files are not allowed: %s", path)
	}
	limit := ws.FileLimit
	if limit <= 0 {
		limit = DEFAULT_FILE_LIMIT
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > limit {
		return nil, 0, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return body, info.Mode().Perm(), nil
}

func (ws *Workspace) ReadFile(path string) (string, error) {
	body, _, err := ws.readBytes(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("file must be UTF-8")
	}
	return string(body), nil
}

func (ws *Workspace) writeBytes(path string, content []byte, requestedMode os.FileMode, preserveMode bool) error {
	limit := ws.FileLimit
	if limit <= 0 {
		limit = DEFAULT_FILE_LIMIT
	}
	if int64(len(content)) > limit {
		return fmt.Errorf("file exceeds %d bytes", limit)
	}
	root, name, err := ws.parent(path, true, true)
	if err != nil {
		return err
	}
	defer root.Close()
	mode := requestedMode.Perm() & 0777
	if info, statErr := root.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", path)
		}
		if preserveMode {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	// Replace instead of truncating: a hard link must never modify another file.
	temp := fmt.Sprintf(".garness-write-%d", nextFileID.Add(1))
	file, err := openTemporaryFile(root, temp, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = file.Write(content)
	if err == nil {
		err = file.Chmod(mode)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, name)
}

// String-returning wrappers preserve the existing Workspace API.
func (ws *Workspace) Read(path string) string {
	body, err := ws.ReadFile(path)
	if err != nil {
		return "error: " + err.Error()
	}
	return body
}

func (ws *Workspace) Write(path, content string) string {
	if err := ws.WriteFile(path, content); err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("wrote %s (%d chars)", path, utf8.RuneCountInString(content))
}

func (ws *Workspace) Edit(path, oldData, newData string) string {
	if err := ws.EditFile(path, oldData, newData); err != nil {
		return "error: " + err.Error()
	}
	return "edited " + path
}

// EditFile requires one exact match so an ambiguous edit cannot silently
// replace the wrong occurrence. Read and replacement retain workspace checks.
func (ws *Workspace) EditFile(path, oldData, newData string) error {
	ws.mutation.Lock()
	defer ws.mutation.Unlock()
	if oldData == "" {
		return fmt.Errorf("old_text must not be empty")
	}
	body, err := ws.ReadFile(path)
	if err != nil {
		return err
	}
	count := strings.Count(body, oldData)
	if count != 1 {
		return fmt.Errorf("old_text must match exactly once (found %d matches); read the file and provide a unique selection", count)
	}
	return ws.writeBytes(path, []byte(strings.Replace(body, oldData, newData, 1)), 0644, true)
}

// AppendFile preserves existing text, inserts no implicit newline, and creates
// missing files. Use atomic replacement so hard links cannot affect other files.
func (ws *Workspace) AppendFile(path, content string) error {
	ws.mutation.Lock()
	defer ws.mutation.Unlock()
	body, err := ws.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return ws.writeBytes(path, []byte(body+content), 0644, true)
}

func (ws *Workspace) WriteFile(path, content string) error {
	ws.mutation.Lock()
	defer ws.mutation.Unlock()
	return ws.writeBytes(path, []byte(content), 0644, true)
}

func (ws *Workspace) removePath(path string) error {
	root, name, err := ws.parent(path, true, false)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("not a regular file or directory: %s", path)
	}
	return root.Remove(name)
}
