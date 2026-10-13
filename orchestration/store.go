package orchestration

import (
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	sessions             *session.Store
	Directory, Workspace string
}

func Open(root, workspace string) (*Store, error) {
	s, err := session.Open(root, workspace)
	if err != nil {
		return nil, err
	}
	return OpenRuns(s)
}

func OpenRuns(s *session.Store) (*Store, error) {
	dir := filepath.Join(s.Directory, "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	locks := &session.Store{Directory: dir, Workspace: s.Workspace}
	return &Store{sessions: locks, Directory: dir, Workspace: s.Workspace}, nil
}

func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && id == hex.EncodeToString(b)
}
func (s *Store) NewID() string                  { return s.sessions.New("", "").ID }
func (s *Store) Lock(id string) (func(), error) { return s.sessions.Lock(id) }

func (s *Store) path(id, suffix string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("invalid run ID")
	}
	return filepath.Join(s.Directory, id+suffix), nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Save requires the caller to hold the run lock.
func (s *Store) Save(run *Run) error {
	path, err := s.path(run.ID, ".json")
	if err != nil {
		return err
	}
	if run.Workspace != s.Workspace {
		return fmt.Errorf("run workspace mismatch")
	}
	return writeJSON(path, run)
}

func (s *Store) Load(id string) (*Run, error) {
	path, err := s.path(id, ".json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run Run
	if err = json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	if run.Version != 1 || run.ID != id || run.Workspace != s.Workspace || run.Working == nil {
		return nil, fmt.Errorf("invalid or mismatched run")
	}
	if run.Plan != nil {
		if err = run.Plan.Validate(); err != nil {
			return nil, err
		}
		if len(run.Tasks) != len(run.Plan.Tasks) {
			return nil, fmt.Errorf("invalid persisted tasks")
		}
		for i, task := range run.Tasks {
			a, _ := json.Marshal(task.Task)
			b, _ := json.Marshal(run.Plan.Tasks[i])
			if string(a) != string(b) {
				return nil, fmt.Errorf("persisted task differs from plan")
			}
		}
	}
	if _, ok := runTransitions[run.State]; !ok && run.State != Completed {
		return nil, fmt.Errorf("invalid run state")
	}
	if usage, err := s.loadUsage(id); err != nil {
		return nil, err
	} else {
		// The reservation ledger can be newer than the last scheduler snapshot.
		run.Usage = usage
	}
	return &run, nil
}

func (s *Store) List() ([]*Run, error) {
	entries, err := os.ReadDir(s.Directory)
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".json" || !validID(name[:len(name)-5]) {
			continue
		}
		run, err := s.Load(name[:len(name)-5])
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Created.After(runs[j].Created) })
	return runs, nil
}

type Artifact struct {
	Session session.Session `json:"session"`
	Sandbox *sandbox.State  `json:"sandbox"`
}

func (s *Store) artifactPath(runID, attemptID string) (string, error) {
	if !validID(attemptID) {
		return "", fmt.Errorf("invalid attempt ID")
	}
	return s.path(runID, "-"+attemptID+".artifact")
}
func (s *Store) SaveArtifact(runID, attemptID string, artifact *Artifact) error {
	path, err := s.artifactPath(runID, attemptID)
	if err != nil {
		return err
	}
	return writeJSON(path, artifact)
}
func (s *Store) LoadArtifact(runID, attemptID string) (*Artifact, error) {
	path, err := s.artifactPath(runID, attemptID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var artifact Artifact
	if err = json.Unmarshal(data, &artifact); err != nil {
		return nil, err
	}
	if artifact.Sandbox == nil {
		return nil, fmt.Errorf("missing attempt sandbox")
	}
	return &artifact, nil
}

func (s *Store) saveUsage(id string, usage Usage) error {
	path, err := s.path(id, ".usage")
	if err != nil {
		return err
	}
	return writeJSON(path, usage)
}
func (s *Store) loadUsage(id string) (Usage, error) {
	path, err := s.path(id, ".usage")
	if err != nil {
		return Usage{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Usage{}, nil
	}
	if err != nil {
		return Usage{}, err
	}
	var usage Usage
	err = json.Unmarshal(data, &usage)
	if err == nil && (usage.Requests < 0 || usage.ReservedTokens < 0 || usage.ReportedTokens < 0 || usage.ToolCalls < 0) {
		err = fmt.Errorf("invalid budget ledger")
	}
	return usage, err
}
