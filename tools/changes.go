package tools

import (
	"anthonytaha/garness/sandbox"
)

func ReviewChangesTool(s *sandbox.Sandbox) Tool {
	return Tool{
		Name:        "review_changes",
		Description: "Review isolated workspace changes before applying them.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func:        func(args map[string]any) (string, error) { return s.Changes() },
	}
}

func ApplyChangesTool(s *sandbox.Sandbox) Tool {
	return Tool{
		Name:        "apply_changes",
		Description: "Apply reviewed isolated changes to the original workspace, requiring approval of the concrete changes.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func:        func(args map[string]any) (string, error) { return s.ApplyChanges() },
	}
}
