package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIProfiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "garness")
	build := exec.Command("go", "build", "-o", binary, "./cmd/garness")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s\n%v", output, err)
	}
	for _, item := range []struct {
		Name     string
		Profile  string
		Args     []string
		Input    string
		Expected string
		Fails    bool
	}{
		{Name: "local", Profile: "local", Expected: "Sandbox profile: local"},
		{Name: "isolated", Profile: "isolated", Input: ":changes\n:apply\n", Expected: "no changes"},
		{Name: "flag overrides environment", Profile: "isolated", Args: []string{"-sandbox-profile", "local"}, Expected: "Sandbox profile: local"},
		{Name: "invalid profile", Profile: "invalid", Expected: "sandbox profile must be local or isolated", Fails: true},
	} {
		t.Run(item.Name, func(t *testing.T) {
			workspace := t.TempDir()
			for path, body := range map[string]string{"AGENTS.md": "Test instructions", "file": "original", ".env": "SECRET=fixture"} {
				if err := os.WriteFile(filepath.Join(workspace, path), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command(binary, item.Args...)
			command.Dir = workspace
			command.Stdin = strings.NewReader(item.Input)
			command.Env = append(os.Environ(), "GARNESS_SANDBOX_PROFILE="+item.Profile, "GARNESS_SKILLS_DIR=", "GARNESS_CONTEXT_BUDGET=0", "MISTRAL_API_KEY=")
			output, err := command.CombinedOutput()
			if (err != nil) != item.Fails || !strings.Contains(string(output), item.Expected) {
				t.Fatalf("%s\n%v", output, err)
			}
			if strings.Contains(string(output), "SECRET=fixture") {
				t.Fatal("CLI printed workspace secret")
			}
			if body, _ := os.ReadFile(filepath.Join(workspace, "file")); string(body) != "original" {
				t.Fatal("startup modified original workspace")
			}
		})
	}
}
