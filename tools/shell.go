package tools

import (
	"anthonytaha/garness/sandbox"
	"context"
	"fmt"
	"strings"
	"time"
)

func ShellTool(s *sandbox.Sandbox) Tool {
	t := Tool{
		Name:        "shell",
		Description: "Run a shell command in the selected sandbox. Network is disabled unless exact domains are requested and approved. Local changes apply automatically; isolated changes require apply_changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"cwd":     map[string]any{"type": "string"},
				"domains": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"timeout_seconds": map[string]any{
					"type":    "integer",
					"minimum": 1,
					"maximum": int(s.Policy.Timeout / time.Second),
				},
			},
			"required": []string{"command"},
		},
		ContextFunc: func(ctx context.Context, args map[string]any) (string, error) {
			command, ok := args["command"].(string)
			if !ok || strings.TrimSpace(command) == "" {
				return "", fmt.Errorf("command must be a non-empty string")
			}
			request := sandbox.CommandRequest{Command: command, Timeout: s.Policy.Timeout}
			if value, exists := args["cwd"]; exists {
				request.Cwd, ok = value.(string)
				if !ok {
					return "", fmt.Errorf("cwd must be a string")
				}
			}
			if value, exists := args["domains"]; exists {
				values, ok := value.([]any)
				if !ok {
					return "", fmt.Errorf("domains must be an array")
				}
				for _, value := range values {
					domain, ok := value.(string)
					if !ok {
						return "", fmt.Errorf("domain must be a string")
					}
					request.Domains = append(request.Domains, domain)
				}
			}
			if value, exists := args["timeout_seconds"]; exists {
				seconds, ok := value.(float64)
				if !ok || seconds < 1 || seconds > 600 || seconds != float64(int(seconds)) {
					return "", fmt.Errorf("timeout_seconds must be an integer between 1 and 600")
				}
				request.Timeout = time.Duration(seconds) * time.Second
			}
			result, err := s.RunResultContext(ctx, request)
			if err != nil {
				return "", err
			}
			if result.ExitCode != 0 {
				return "", &ShellExitError{Result: result}
			}
			suffix := ""
			if result.Truncated {
				suffix = "\n[output truncated]"
			}
			return fmt.Sprintf("exit code: %d\n%s%s", result.ExitCode, result.Output, suffix), nil
		},
	}
	t.Func = func(args map[string]any) (string, error) { return t.ContextFunc(context.Background(), args) }
	return t
}

type ShellExitError struct{ Result sandbox.CommandResult }

func (e *ShellExitError) Error() string {
	return fmt.Sprintf("exit code: %d\n%s", e.Result.ExitCode, e.Result.Output)
}
