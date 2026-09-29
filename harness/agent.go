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
	Approve      func(name, arguments string) bool

	SummaryModel string
	ContextBudget int

	usageTokens   int
	usageMsgCount int
}

func (a *Agent) requestApproval(name, arguments string) bool {
	if a.Approve == nil {
		return false
	}
	return a.Approve(name, arguments)
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
	agent := &Agent{Model: modelName, Provider: provider, Messages: []map[string]any{}, AgentsDir: agentsDir, ContextBudget: DEFAULT_CONTEXT_BUDGET}
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
		a.maybeCompact()
		sent := len(a.Messages)
		resp, err := model.Chat(
			a.Provider,
			a.Messages,
			options,
		)
		if err != nil {
			return "", err
		}
		a.recordUsage(resp.Usage, sent)
		if len(resp.ToolCalls) == 0 {
			a.Messages = append(a.Messages, map[string]any{"role": "assistant", "content": resp.Content})
			return resp.Content, nil
		}
		a.runToolCalls(resp)
	}
	return "", fmt.Errorf("exceeded tool-step budget")

}

func (a *Agent) maybeCompact() {
	if a.ContextBudget <= 0 || a.estimatedTokens() <= a.ContextBudget {
		return
	}
	summaryModel := a.SummaryModel
	if summaryModel == "" {
		summaryModel = a.Model
	}
	compacted, err := compact(a.Provider, summaryModel, a.Messages, COMPACT_KEEP_HEAD, COMPACT_KEEP_TAIL)
	if err != nil {
		fmt.Println("WARNING: compaction failed: ", err)
		return
	}
	a.Messages = compacted
	a.usageTokens, a.usageMsgCount = 0, 0
}

func (a *Agent) estimatedTokens() int {
	if a.usageTokens > 0 && a.usageMsgCount <= len(a.Messages) {
		return a.usageTokens + estimateTokens(a.Messages[a.usageMsgCount:])
	}
	return estimateTokens(a.Messages)
}

func (a *Agent) recordUsage(usage map[string]any, sent int) {
	prompt, ok := usage["prompt_tokens"].(float64)
	if !ok || prompt <= 0 {
		a.usageTokens, a.usageMsgCount = 0, 0
		return
	}
	a.usageTokens, a.usageMsgCount = int(prompt), sent
}


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

		tool, _ := a.Tools.GetTool(name)
		var result string
		if tool.NeedsApproval && !a.requestApproval(name, arguments) {
			result = "[tool ()" + name + ") access denied]"
		} else {
			result = a.Tools.Call(name, arguments)
		}

		a.Messages = append(a.Messages, map[string]any{
			"role":         "tool",
			"tool_call_id": id,
			"content":      result,
		})
	}
}
