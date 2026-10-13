package tools

import (
	"anthonytaha/garness/sandbox"
	"fmt"
)

func ReadFileTool(ws *sandbox.Workspace) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a UTF-8 text file from the workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
		Func: func(args map[string]any) (string, error) {
			path, ok := args["path"].(string)
			if !ok {
				return "", fmt.Errorf("path must be a string")
			}
			return ws.ReadFile(path)
		},
		NeedsApproval: false,
	}
}

func WriteFileTool(ws *sandbox.Workspace) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Write UTF-8 text to a file in the workspace, creating or overwriting it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Func: func(args map[string]any) (string, error) {
			path, ok := args["path"].(string)
			if !ok {
				return "", fmt.Errorf("path must be a string")
			}
			content, ok := args["content"].(string)
			if !ok {
				return "", fmt.Errorf("content must be a string")
			}
			if err := ws.WriteFile(path, content); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %s (%d chars)", path, len([]rune(content))), nil
		},
		NeedsApproval: false,
	}
}
