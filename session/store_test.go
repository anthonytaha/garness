package session

import (
	"strings"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	s, e := Open(t.TempDir(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	v := s.New("test", "")
	v.Messages = []map[string]any{{"role": "user", "content": "hello"}}
	v.Transcript = v.Messages
	unlock, e := s.Lock(v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Lock(v.ID); e == nil {
		t.Fatal("concurrent lock succeeded")
	}
	if e = s.Save(v); e != nil {
		t.Fatal(e)
	}
	unlock()
	loaded, e := s.Load(v.ID)
	if e != nil || loaded.Name != "test" {
		t.Fatalf("load: %v", e)
	}
	list, e := s.List()
	if e != nil || len(list) != 1 {
		t.Fatalf("list: %v", e)
	}
	for _, format := range []string{"json", "markdown"} {
		b, e := Export(loaded, format)
		if e != nil || !strings.Contains(string(b), "hello") {
			t.Fatalf("export: %v", e)
		}
	}
	child := s.New("branch", v.ID)
	child.Messages = loaded.Messages
	child.Transcript = loaded.Transcript
	if e = s.Save(child); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Load(child.ID); e != nil {
		t.Fatal("deleting parent removed branch")
	}
	if _, e = s.Load("../escape"); e == nil {
		t.Fatal("accepted invalid ID")
	}
}
func TestValidation(t *testing.T) {
	s, _ := Open(t.TempDir(), t.TempDir())
	v := s.New("test", "")
	v.Version = 99
	if e := s.Save(v); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Load(v.ID); e == nil {
		t.Fatal("accepted unknown version")
	}
	v.Workspace = "other"
	if e := s.Save(v); e == nil {
		t.Fatal("accepted another workspace")
	}
}
