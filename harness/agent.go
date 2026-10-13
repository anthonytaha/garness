package harness

import (
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/tools"
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
	Skills       *SkillRegistry
	activeSkills map[string]Skill
	Tools        *tools.ToolRegistry
	Workspace    *sandbox.Workspace
	Approve      func(name, arguments string) bool

	SummaryModel  string
	ContextBudget int

	usageTokens   int
	usageMsgCount int
	usageSystem   string
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
	if systemPrompt == "" {
		systemPrompt = DEFAULT_SYS_PROMPT
	}
	agent := &Agent{SystemPrompt: systemPrompt, Model: modelName, Provider: provider, Messages: []map[string]any{}, AgentsDir: agentsDir, ContextBudget: DEFAULT_CONTEXT_BUDGET}
	sysPrompt, err := agent.systemText()
	if err != nil {
		fmt.Println("WARNING: Error loading system prompt: ", err)
		sysPrompt = systemPrompt
	}
	agent.SystemPrompt = sysPrompt
	agent.Messages = append(agent.Messages, map[string]any{"role": "system", "content": agent.SystemPrompt})
	agent.Tools = tools.DefaultTools()
	agent.Tools.Approve = agent.requestApproval
	root := agentsDir
	if root == "" {
		root = "."
	}
	for _, warning := range agent.ConfigureSkills(filepath.Join(root, ".garness", "skills")) {
		fmt.Println("WARNING:", warning)
	}
	return agent
}

func (a *Agent) Run(userText string) (string, error) {
	// Validate every explicit reference before changing session state.
	mentions := skillMentionPattern.FindAllStringSubmatch(userText, -1)
	for _, mention := range mentions {
		if a.Skills == nil {
			return "", fmt.Errorf("unknown skill %q", mention[1])
		}
		if _, ok := a.Skills.skills[mention[1]]; !ok {
			return "", fmt.Errorf("unknown skill %q", mention[1])
		}
	}
	for _, mention := range mentions {
		if _, err := a.activateSkill(mention[1]); err != nil {
			return "", err
		}
	}
	context := ""
	if a.Workspace != nil {
		var err error
		context, err = DeliverWorkspace(a.Workspace, userText)
		if err != nil {
			return "", err
		}
	} else if attachPattern.MatchString(userText) {
		return "", fmt.Errorf("attachments require a configured workspace")
	}
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
	if len(compacted) != len(a.Messages) {
		a.usageTokens, a.usageMsgCount = 0, 0
		a.usageSystem = ""
	}
	a.Messages = compacted
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

		result := a.Tools.Call(name, arguments)

		a.Messages = append(a.Messages, map[string]any{
			"role":         "tool",
			"tool_call_id": id,
			"content":      result,
		})
	}
}
