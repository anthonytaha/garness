package tools

import (
	"context"
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
	ContextFunc   func(context.Context, map[string]any) (string, error)
}

type ToolRegistry struct {
	tools      map[string]Tool
	Approve    func(name, arguments string) bool
	BeforeCall func(context.Context, string, map[string]any) error
}

type CallResult struct {
	Content string
	Err     error
}

func (r CallResult) String() string {
	if r.Err != nil {
		return "error: " + r.Err.Error()
	}
	return r.Content
}

type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string { return e.Reason }

func (r *ToolRegistry) Restrict(names []string) {
	allowed := make(map[string]bool)
	for _, name := range names {
		allowed[name] = true
	}
	for name := range r.tools {
		if !allowed[name] {
			delete(r.tools, name)
		}
	}
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
	return r.CallContext(context.Background(), name, arguments).String()
}

func (r *ToolRegistry) CallContext(ctx context.Context, name, arguments string) CallResult {
	if err := ctx.Err(); err != nil {
		return CallResult{Err: err}
	}
	tool, ok := r.tools[name]
	if !ok {
		return CallResult{Err: fmt.Errorf("unknown tool %q", name)}
	}

	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return CallResult{Err: fmt.Errorf("could not parse arguments %q", arguments)}
		}
	}
	if r.BeforeCall != nil {
		if err := r.BeforeCall(ctx, name, args); err != nil {
			return CallResult{Err: err}
		}
	}

	if tool.NeedsApproval && (r.Approve == nil || !r.Approve(name, arguments)) {
		return CallResult{Err: &DeniedError{Reason: "tool access denied"}}
	}
	if err := ctx.Err(); err != nil {
		return CallResult{Err: err}
	}
	var result string
	var err error
	if tool.ContextFunc != nil {
		result, err = tool.ContextFunc(ctx, args)
	} else {
		result, err = tool.Func(args)
	}
	if err != nil {
		return CallResult{Content: result, Err: err}
	}

	return CallResult{Content: result}
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
