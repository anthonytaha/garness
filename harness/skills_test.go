package harness

import (
	"anthonytaha/garness/model"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureSkill(t *testing.T, root, dir, name string) {
	t.Helper()
	p := filepath.Join(root, dir)
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Review code carefully\n---\nExact review instructions."), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillParsingAndDiscovery(t *testing.T) {
	for _, input := range []string{"no frontmatter", "---\nname: x\n---\nbody", "---\nname: x\nname: y\ndescription: d\n---\nbody", "---\nname: x\ndescription: |\n---\nbody"} {
		if _, err := parseSkill(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	root := t.TempDir()
	fixtureSkill(t, root, "b", "review")
	fixtureSkill(t, root, "a", "review")
	fixtureSkill(t, root, "c", "build")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	r, warnings := DiscoverSkills(filepath.Join(root, "missing"), relativeRoot)
	if len(warnings) != 1 || strings.Join(r.Names(), ",") != "build,review" {
		t.Fatalf("names=%v warnings=%v", r.Names(), warnings)
	}
	if filepath.Base(r.skills["review"].Directory) != "a" {
		t.Fatal("duplicate precedence is unstable")
	}
}

func TestSkillResourceBoundaries(t *testing.T) {
	root := t.TempDir()
	fixtureSkill(t, root, "review", "review")
	a := NewAgent("", model.Provider{}, "custom prompt", "")
	a.ConfigureSkills(root)
	if _, err := a.activateSkill("review"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "review", "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../../secret", outside, "escape"} {
		args, _ := json.Marshal(map[string]string{"name": "review", "path": path})
		if result := a.Tools.Call("read_skill_resource", string(args)); !strings.HasPrefix(result, "error:") {
			t.Fatalf("read escaped: %s", result)
		}
	}
	if result := a.Tools.Call("read_skill_resource", `{"name":"review","path":"SKILL.md"}`); !strings.Contains(result, "Exact review") {
		t.Fatal(result)
	}
	load, _ := a.Tools.GetTool("load_skill")
	resource, _ := a.Tools.GetTool("read_skill_resource")
	if load.NeedsApproval || resource.NeedsApproval {
		t.Fatal("skill reads require approval")
	}
	if _, err := a.activateSkill("unknown"); err == nil {
		t.Fatal("unknown accepted")
	}
	a.activateSkill("review")
	if strings.Count(a.Messages[0]["content"].(string), "Exact review instructions.") != 1 {
		t.Fatal("duplicate activation")
	}
}

func TestSkillActivationAndCompaction(t *testing.T) {
	root := t.TempDir()
	fixtureSkill(t, root, "review", "review")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		system := request.Messages[0]["content"].(string)
		if strings.Contains(system, "You are compacting") {
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Summary"}}}})
			return
		}
		calls++
		if calls == 1 {
			if strings.Contains(system, "Exact review instructions.") {
				t.Error("eager body loading")
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"load_skill","arguments":"{\"name\":\"review\"}"}}]}}]}`))
			return
		}
		if !strings.Contains(system, "custom prompt") || !strings.Contains(system, "Exact review instructions.") {
			t.Error("missing persistent instructions")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Done"}}}})
	}))
	defer server.Close()
	a := NewAgent("test", *model.NewProvider(server.URL, "test", ""), "custom prompt", "")
	a.ConfigureSkills(root)
	if _, err := a.Run("Review this"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		a.Messages = append(a.Messages, map[string]any{"role": "user", "content": strings.Repeat("context", 100)})
	}
	a.ContextBudget = 1
	before := len(a.Messages)
	a.maybeCompact()
	if len(a.Messages) >= before {
		t.Fatal("did not compact")
	}
	if !strings.Contains(a.Messages[0]["content"].(string), "Exact review instructions.") {
		t.Fatal("compaction lost skill")
	}
	a.ConfigureSkills(root)
	a.ContextBudget = 0
	if _, err := a.Run("Use $review"); err != nil {
		t.Fatal(err)
	}
	before = len(a.Messages)
	if _, err := a.Run("Use $missing"); err == nil || len(a.Messages) != before {
		t.Fatal("unknown skill mutated conversation")
	}
}

func TestDiscoveryRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	fixtureSkill(t, outside, "review", "review")
	if err := os.Symlink(filepath.Join(outside, "review"), filepath.Join(root, "review")); err != nil {
		t.Fatal(err)
	}
	r, warnings := DiscoverSkills(root)
	if len(r.Names()) != 0 || len(warnings) != 1 {
		t.Fatalf("escaped discovery: %v %v", r.Names(), warnings)
	}
}
