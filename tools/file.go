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
		Description: "Create or replace the ENTIRE UTF-8 file with content. Supply the complete file, not a fragment. Use edit_file for targeted changes and append_file to add content at the end.",
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

func EditFileTool(ws *sandbox.Workspace) Tool {
	return Tool{Name: "edit_file", Description: "Replace exactly one occurrence of old_text with new_text in an existing UTF-8 file, preserving all other content. Read the file first; old_text must be nonempty and uniquely match. new_text may be empty to delete the selection.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "required": []string{"path", "old_text", "new_text"}}, Func: func(args map[string]any) (string, error) {
		path, err := fileString(args, "path")
		if err != nil {
			return "", err
		}
		old, err := fileString(args, "old_text")
		if err != nil {
			return "", err
		}
		replacement, err := fileString(args, "new_text")
		if err != nil {
			return "", err
		}
		if err = ws.EditFile(path, old, replacement); err != nil {
			return "", err
		}
		return "edited " + path, nil
	}}
}
func AppendFileTool(ws *sandbox.Workspace) Tool {
	return Tool{Name: "append_file", Description: "Append content to the END of a UTF-8 file, preserving existing content. Creates a missing file. No newline is inserted automatically; include any needed newline in content.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}, Func: func(args map[string]any) (string, error) {
		path, err := fileString(args, "path")
		if err != nil {
			return "", err
		}
		content, err := fileString(args, "content")
		if err != nil {
			return "", err
		}
		if err = ws.AppendFile(path, content); err != nil {
			return "", err
		}
		return fmt.Sprintf("appended to %s (%d chars)", path, len([]rune(content))), nil
	}}
}
func fileString(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return value, nil
}
