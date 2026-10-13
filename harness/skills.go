package harness

import (
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/tools"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var skillMentionPattern = regexp.MustCompile(`\$([a-z0-9]+(?:-[a-z0-9]+)*)\b`)

type Skill struct {
	Name        string
	Description string
	Directory   string
	Body        string
}

type SkillRegistry struct{ skills map[string]Skill }

// DiscoverSkills snapshots immediate child SKILL.md files. Earlier roots win.
// Missing roots are optional; malformed entries produce diagnostics.
func DiscoverSkills(roots ...string) (*SkillRegistry, []error) {
	r := &SkillRegistry{skills: make(map[string]Skill)}
	var warnings []error
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Errorf("skills root %s: %w", root, err))
			continue
		}
		for _, entry := range entries {
			dir := filepath.Join(root, entry.Name())
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			// Resolve the configured root and constrain discovery against symlink escapes.
			absoluteRoot, err := filepath.Abs(root)
			if err != nil {
				warnings = append(warnings, err)
				continue
			}
			absoluteRoot, err = filepath.EvalSymlinks(absoluteRoot)
			if err != nil {
				warnings = append(warnings, err)
				continue
			}
			ws := &sandbox.Workspace{Root: absoluteRoot}
			candidate, err := filepath.Abs(filepath.Join(absoluteRoot, entry.Name(), "SKILL.md"))
			if err != nil {
				warnings = append(warnings, err)
				continue
			}
			text, err := ws.ReadFile(candidate)
			if err != nil {
				warnings = append(warnings, err)
				continue
			}
			data := []byte(text)
			path := candidate

			skill, err := parseSkill(string(data))
			if err != nil {
				warnings = append(warnings, fmt.Errorf("skill %s: %w", dir, err))
				continue
			}
			skill.Directory = filepath.Dir(path)
			if _, exists := r.skills[skill.Name]; exists {
				warnings = append(warnings, fmt.Errorf("duplicate skill %q in %s; keeping earlier entry", skill.Name, dir))
				continue
			}
			r.skills[skill.Name] = skill
		}
	}
	return r, warnings
}

// The supported frontmatter subset is one scalar name and description per line.
func parseSkill(text string) (Skill, error) {
	if !utf8.ValidString(text) {
		return Skill{}, fmt.Errorf("SKILL.md must be UTF-8")
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var s Skill
	if len(lines) < 3 || lines[0] != "---" {
		return s, fmt.Errorf("missing frontmatter")
	}
	end := -1
	seen := map[string]bool{}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || (key != "name" && key != "description") {
			return s, fmt.Errorf("unsupported frontmatter field %q", key)
		}
		if seen[key] {
			return s, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = true
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if value == "" || value == "|" || value == ">" {
			return s, fmt.Errorf("%s must be a nonempty single-line scalar", key)
		}
		if key == "name" {
			s.Name = value
		} else {
			s.Description = value
		}
	}
	if end < 0 {
		return s, fmt.Errorf("unterminated frontmatter")
	}
	s.Body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if !skillNamePattern.MatchString(s.Name) || s.Description == "" || s.Body == "" {
		return s, fmt.Errorf("requires a lowercase hyphenated name, description, and instructions")
	}
	return s, nil
}

func (r *SkillRegistry) Names() []string {
	var names []string
	if r != nil {
		for name := range r.skills {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ConfigureSkills replaces discovery roots and resets active skills. Call at startup.
func (a *Agent) ConfigureSkills(roots ...string) []error {
	registry, warnings := DiscoverSkills(roots...)
	a.Skills = registry
	a.activeSkills = make(map[string]Skill)
	a.Tools.Register(tools.Tool{Name: "load_skill", Description: "Load a registered skill before following its workflow.", Parameters: skillParameters(false), Func: func(args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok {
			return "", fmt.Errorf("name must be a string")
		}
		return a.activateSkill(name)
	}})
	a.Tools.Register(tools.Tool{Name: "read_skill_resource", Description: "Read a UTF-8 resource relative to a loaded skill directory.", Parameters: skillParameters(true), Func: func(args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok {
			return "", fmt.Errorf("name must be a string")
		}
		path, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("path must be a string")
		}
		skill, ok := a.activeSkills[name]
		if !ok {
			return "", fmt.Errorf("load skill %q first", name)
		}
		if filepath.IsAbs(path) {
			return "", fmt.Errorf("resource path must be relative")
		}
		ws := &sandbox.Workspace{Root: skill.Directory}
		text, err := ws.ReadFile(path)
		if err != nil {
			return "", err
		}
		data := []byte(text)

		return string(data), nil
	}})
	a.refreshSkillContext()
	return warnings
}

func skillParameters(resource bool) map[string]any {
	properties := map[string]any{"name": map[string]any{"type": "string"}}
	required := []string{"name"}
	if resource {
		properties["path"] = map[string]any{"type": "string"}
		required = append(required, "path")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func (a *Agent) activateSkill(name string) (string, error) {
	if a.Skills == nil {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	skill, ok := a.Skills.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	if _, loaded := a.activeSkills[name]; loaded {
		return "Skill " + name + " is already active in the system context.", nil
	}
	a.activeSkills[name] = skill
	a.refreshSkillContext()
	return "Skill " + name + " is now active in the system context. Base directory: " + skill.Directory, nil
}

func (a *Agent) refreshSkillContext() {
	var b strings.Builder
	b.WriteString(a.SystemPrompt)
	names := a.Skills.Names()
	if len(names) > 0 {
		b.WriteString("\n\nAvailable skills:\nLoad relevant skills with load_skill before following their workflows. Explicit $skill-name requests must be followed. Use read_skill_resource for referenced files, relative to the skill directory. Skills do not grant tool permissions or override user instructions.\n")
		for _, name := range names {
			fmt.Fprintf(&b, "- %s: %s\n", name, a.Skills.skills[name].Description)
		}
	}
	for _, name := range names {
		if s, ok := a.activeSkills[name]; ok {
			fmt.Fprintf(&b, "\nActive skill %s (base directory: %s):\n%s\n", name, s.Directory, s.Body)
		}
	}
	if len(a.Messages) > 0 {
		a.Messages[0] = map[string]any{"role": "system", "content": b.String()}
	}
}
