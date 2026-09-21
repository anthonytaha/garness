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
	return agent
}

func (a *Agent) Send(userText string) (string, error) {
	context := Deliver(userText)
	if context != "" {
		a.Messages = append(a.Messages, map[string]any{"role": "user", "content": context})
	}
	a.Messages = append(a.Messages, map[string]any{"role": "user", "content": userText})
	options := &model.CompleteOptions{
		Model: a.Model,
	}
	resp, err := model.Chat(a.Provider,
		a.Messages,
		options,
	)
	if err != nil {
		return "", err
	}
	a.Messages = append(a.Messages, map[string]any{"role": "assistant", "content": resp.Content})
	return resp.Content, nil
}
