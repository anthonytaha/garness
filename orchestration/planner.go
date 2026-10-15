package orchestration

import (
	"encoding/json"
	"fmt"
	"sort"
)

func schemaObject(properties map[string]any) map[string]any {
	var required []string
	for name := range properties {
		required = append(required, name)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func schemaArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func schemaString() map[string]any { return map[string]any{"type": "string"} }

func schemaEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func resultResponseFormat() map[string]any {
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "task_result", "strict": true, "schema": schemaObject(map[string]any{
		"summary": schemaString(), "artifacts": schemaArray(schemaString()), "unresolved": schemaArray(schemaString()),
	})}}
}

func reviewResponseFormat() map[string]any {
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "task_review", "strict": true, "schema": schemaObject(map[string]any{
		"passed": map[string]any{"type": "boolean"}, "issues": schemaArray(schemaString()),
	})}}
}

func checkSchema(final bool) map[string]any {
	variants := []any{}
	for _, kind := range []string{"file_exists", "file_contains", "shell"} {
		properties := map[string]any{"kind": schemaEnum(kind)}
		if kind == "shell" {
			properties["command"] = schemaString()
			properties["domains"] = schemaArray(schemaString())
		} else {
			properties["path"] = schemaString()
			if kind == "file_contains" {
				properties["contains"] = schemaString()
			}
		}
		if final {
			properties["task_id"] = schemaString()
		}
		variants = append(variants, schemaObject(properties))
	}
	return map[string]any{"anyOf": variants}
}

// The provider constrains syntax and field shapes; Plan.Validate still enforces
// dependencies, permissions, paths, and all other execution invariants locally.
func planResponseFormat(version int, limits Limits) map[string]any {
	var names []string
	for name := range capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	task := schemaObject(map[string]any{
		"id":                  map[string]any{"type": "string", "pattern": taskIDPattern.String()},
		"role":                schemaEnum("worker", "reviewer"),
		"instructions":        schemaString(),
		"dependencies":        schemaArray(schemaString()),
		"allowed_tools":       schemaArray(schemaEnum(names...)),
		"write_scope":         schemaArray(schemaString()),
		"acceptance_criteria": schemaArray(schemaString()),
		"checks":              schemaArray(checkSchema(false)),
		"max_attempts":        map[string]any{"type": "integer", "enum": []int{1, 2, 3}},
		"review":              map[string]any{"type": "boolean"},
	})
	// Use the Limits JSON tags so wire names stay aligned with persisted plans.
	data, _ := json.Marshal(limits)
	values := map[string]int{}
	json.Unmarshal(data, &values)
	properties := map[string]any{}
	for name, value := range values {
		properties[name] = map[string]any{"type": "integer", "enum": []int{value}}
	}
	schema := schemaObject(map[string]any{
		"version":      map[string]any{"type": "integer", "enum": []int{version}},
		"tasks":        schemaArray(task),
		"final_checks": schemaArray(checkSchema(true)),
		"limits":       schemaObject(properties),
	})
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "orchestration_plan", "strict": true, "schema": schema}}
}

func plannerPrompt(objective string, version int, limits Limits, paths []string) string {
	// A valid complete example avoids teaching the model an invalid limits
	// object or checks for an unrelated language/framework.
	check := Check{Kind: "file_exists", Path: "output.txt"}
	final := check
	final.TaskID = "implement"
	example := Plan{Version: version, Limits: limits, Tasks: []Task{{
		ID: "implement", Role: "worker", Instructions: "Implement the requested behavior in output.txt and verify it.",
		Dependencies: []string{}, AllowedTools: []string{"read_file", "write_file", "edit_file"}, WriteScope: []string{"output.txt"},
		AcceptanceCriteria: []string{"Requested behavior is implemented"}, Checks: []Check{check}, MaxAttempts: 3, Review: true,
	}}, FinalChecks: []Check{final}}
	data, _ := json.Marshal(example)
	manifest, _ := json.Marshal(paths)
	return fmt.Sprintf(`Plan this objective: %s
Inspect workspace files with read_file as needed. Return only a JSON Plan matching the supplied response schema, without markdown.
This is a complete valid example of the shape, not a task to copy: %s
Use version %d and the exact limits in that example.
Task IDs must start with a lowercase letter and contain only lowercase letters, digits, and hyphens; use create-index-html, never create_index_html.
write_scope is an array of workspace-relative path STRINGS, such as ["index.html"], never objects or globs. Grant the smallest needed scope. Any worker with shell or file-writing tools needs a nonempty write_scope.
Use only tools in the schema. Reviewer tasks are read-only and may use only read_file, calculator, and review_changes with an empty write_scope.
Use the fewest cohesive tasks needed. For a single-file deliverable, prefer one implementation task with review over splitting each feature into successive edits of the same file. Each task's acceptance checks must be satisfiable when that task finishes, before its dependents run.
Checks are file_exists (path), file_contains (path and contains), or shell (command and domains). Final checks must include the ID of the task responsible for repair. Choose relevant local checks using available tools. Do not launch a server or require network access just to inspect a local HTML file. File-content checks do not prove a game runs in a browser; let review assess behavior and report any testing limitations honestly.
Keep criteria focused on behavior, not arbitrary function names or optional features the user did not request. At most 64 tasks and 3 attempts per task. No recursive delegation.
Workspace paths: %s`, objective, data, version, manifest)
}
