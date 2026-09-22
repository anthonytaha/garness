package harness

import (
	"anthonytaha/garness/model"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DEFAULT_SYS_PROMPT string = "You are Garness, a coding agent"
const MAX_TOOL_STEPS int = 6

type Agent struct {
	Model        string
	Provider     model.Provider
	Messages     []map[string]any
	SystemPrompt string
	AgentsDir    string
	Tools        *ToolRegistry
	Workspace    *Workspace
}

func loadAgentsMD(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}

	path := filepath.Join(dir, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}
func (a *Agent) systemText() (string, error) {
	agentsMD, err := loadAgentsMD(a.AgentsDir)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, p := range []string{a.SystemPrompt, agentsMD} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
func NewAgent(modelName string, provider model.Provider, systemPrompt string, agentsDir string) *Agent {
	agent := &Agent{Model: modelName, Provider: provider, Messages: []map[string]any{}, AgentsDir: agentsDir}
	sysPrompt, err := agent.systemText()
	if err != nil {
		fmt.Println("WARNING: Error loading system prompt: ", err)
		sysPrompt = DEFAULT_SYS_PROMPT
	}
	agent.SystemPrompt = sysPrompt
	agent.Messages = append(agent.Messages, map[string]any{"role": "system", "content": agent.SystemPrompt})
	agent.Tools = DefaultTools()
	return agent
}

func (a *Agent) Run(userText string) (string, error) {
	context := Deliver(userText)
	if context != "" {
		a.Messages = append(a.Messages, map[string]any{"role": "user", "content": context})
	}
	a.Messages = append(a.Messages, map[string]any{"role": "user", "content": userText})
	specs := a.Tools.Specs()
	tools := make([]any, len(specs))
	for i, s := range specs {
		tools[i] = s
	}
	options := &model.CompleteOptions{
		Model: a.Model,
		Tools: tools,
	}
	for range MAX_TOOL_STEPS {
		resp, err := model.Chat(
			a.Provider,
			a.Messages,
			options,
		)
		if err != nil {
			return "", err
		}
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}
		a.runToolCalls(resp)
	}
	return "", fmt.Errorf("exceeded tool-step budget")

}

// runToolCalls appends the assistant's tool-call message, executes each call
// against the registry, and appends a tool result message per call.
func (a *Agent) runToolCalls(resp *model.LLMResponse) {
	a.Messages = append(a.Messages, map[string]any{"role": "assistant", "content": resp.Content, "tool_calls": resp.ToolCalls})
	for _, call := range resp.ToolCalls {
		tc, ok := call.(map[string]any)
		if !ok {
			continue
		}

		id, _ := tc["id"].(string)

		fn, ok := tc["function"].(map[string]any)
		if !ok {
			continue
		}

		name, _ := fn["name"].(string)
		arguments, _ := fn["arguments"].(string)

		result := a.Tools.Call(name, arguments)

		a.Messages = append(a.Messages, map[string]any{
			"role":         "tool",
			"tool_call_id": id,
			"content":      result,
		})
	}
}
