package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Version = 1

type Session struct {
	Version     int              `json:"version"`
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Workspace   string           `json:"workspace"`
	Created     time.Time        `json:"created"`
	Updated     time.Time        `json:"updated"`
	Parent      string           `json:"parent,omitempty"`
	Messages    []map[string]any `json:"messages"`
	Transcript  []map[string]any `json:"transcript"`
	Skills      []string         `json:"skills,omitempty"`
	Interrupted bool             `json:"interrupted"`
}

type Store struct{ Directory, Workspace string }

func Open(root, workspace string) (*Store, error) {
	path, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = os.Getenv("GARNESS_SESSION_DIR")
		if root == "" {
			base := os.Getenv("XDG_DATA_HOME")
			if base == "" {
				home, e := os.UserHomeDir()
				if e != nil {
					return nil, e
				}
				base = filepath.Join(home, ".local", "share")
			}
			root = filepath.Join(base, "garness", "sessions")
		}
	}
	hash := sha256.Sum256([]byte(path))
	dir := filepath.Join(root, hex.EncodeToString(hash[:]))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{dir, path}, nil
}
func (s *Store) New(name, parent string) *Session {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	if name == "" {
		name = "Untitled"
	}
	return &Session{Version: Version, ID: hex.EncodeToString(b), Name: name, Workspace: s.Workspace, Created: now, Updated: now, Parent: parent}
}
func (s *Store) path(id string) (string, error) {
	b, e := hex.DecodeString(id)
	if e != nil || len(b) != 16 {
		return "", fmt.Errorf("invalid session ID")
	}
	return filepath.Join(s.Directory, id+".json"), nil
}
func (s *Store) Lock(id string) (func(), error) {
	p, e := s.path(id)
	if e != nil {
		return nil, e
	}
	return lockSessionFile(p + ".lock")
}

// Save requires the caller to hold the session lock.
func (s *Store) Save(v *Session) error {
	p, e := s.path(v.ID)
	if e != nil {
		return e
	}
	if v.Workspace != s.Workspace {
		return fmt.Errorf("session workspace mismatch")
	}
	v.Updated = time.Now().UTC()
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.Directory, ".session-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), p)
}
func (s *Store) Load(id string) (*Session, error) {
	p, e := s.path(id)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var v Session
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	if v.Version != Version || v.ID != id || v.Workspace != s.Workspace {
		return nil, fmt.Errorf("unsupported or mismatched session")
	}
	for _, m := range v.Messages {
		r, _ := m["role"].(string)
		if r != "system" && r != "user" && r != "assistant" && r != "tool" {
			return nil, fmt.Errorf("invalid message role")
		}
	}
	return &v, nil
}
func (s *Store) List() ([]*Session, error) {
	entries, e := os.ReadDir(s.Directory)
	if e != nil {
		return nil, e
	}
	var out []*Session
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		v, e := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), e)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}
func (s *Store) Delete(id string) error {
	unlock, e := s.Lock(id)
	if e != nil {
		return e
	}
	defer unlock()
	p, e := s.path(id)
	if e != nil {
		return e
	}
	return os.Remove(p)
}
func Export(v *Session, format string) ([]byte, error) {
	if format == "json" {
		return json.MarshalIndent(v, "", "  ")
	}
	if format != "markdown" {
		return nil, fmt.Errorf("format must be json or markdown")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nSession: %s\n\n", v.Name, v.ID)
	for _, m := range v.Transcript {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", role, content)
		if calls, ok := m["tool_calls"]; ok {
			data, _ := json.MarshalIndent(calls, "", "  ")
			fmt.Fprintf(&b, "```json\n%s\n```\n\n", data)
		}
	}
	return []byte(b.String()), nil
}
