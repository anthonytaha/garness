package tools

import (
	"encoding/json"
	"fmt"
)

type ToolFunc func(args map[string]any) (string, error)

type Tool struct {
	Name          string
	Description   string
	Func          ToolFunc
	Parameters    map[string]any
	NeedsApproval bool
}

type ToolRegistry struct {
	tools   map[string]Tool
	Approve func(name, arguments string) bool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (tr *ToolRegistry) Register(tool Tool) {
	tr.tools[tool.Name] = tool
}

func (tr *ToolRegistry) GetTool(name string) (Tool, bool) {
	tool, exists := tr.tools[name]
	return tool, exists
}

func (r *ToolRegistry) Specs() []map[string]any {
	specs := make([]map[string]any, 0, len(r.tools))

	for _, t := range r.tools {
		specs = append(specs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	return specs
}

func (r *ToolRegistry) Call(name, arguments string) string {
	tool, ok := r.tools[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name)
	}

	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf(
				"error: could not parse arguments %q",
				arguments,
			)
		}
	}

	if tool.NeedsApproval && (r.Approve == nil || !r.Approve(name, arguments)) {
		return "error: tool access denied"
	}
	result, err := tool.Func(args)
	if err != nil {
		return "error: " + err.Error()
	}

	return result
}

func (r *ToolRegistry) Len() int {
	return len(r.tools)
}

// Workspace Agnostic tools
func DefaultTools() *ToolRegistry {
	reg := NewToolRegistry()

	reg.Register(Tool{
		Name:        "calculator",
		Description: "Evaluate an arithmetic expression like '47 * 89'.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"expression"},
		},
		Func: func(args map[string]any) (string, error) {
			expression, ok := args["expression"].(string)
			if !ok {
				return "", fmt.Errorf("expression must be a string")
			}

			return Calculator(expression)
		},
	})
	return reg
}
