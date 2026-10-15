package harness

import (
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"anthonytaha/garness/tools"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DEFAULT_SYS_PROMPT string = "You are Garness, a coding agent"

const MAX_TOOL_STEPS int = 32

var ErrToolLimit = errors.New("agent tool limit reached")

type Agent struct {
	ResponseFormat       map[string]any
	OnEvent              func(Event)
	OnDelta              func(string)
	BeforeRequest        func(int) error
	OnUsage              func(map[string]any)
	OnToolResult         func(string, tools.CallResult)
	RequireCompletion    bool
	Transcript           []map[string]any
	Checkpoint           func() error
	OnRetry              func(int, time.Duration)
	OnRateLimitRemaining func(int64)
	Interrupted          bool
	Model                string
	Provider             model.Provider
	Messages             []map[string]any
	SystemPrompt         string
	AgentsDir            string
	Skills               *SkillRegistry
	activeSkills         map[string]Skill
	Tools                *tools.ToolRegistry
	Workspace            *sandbox.Workspace
	Approve              func(name, arguments string) bool

	SummaryModel    string
	ContextBudget   int
	MaxToolSteps    int
	MaxOutputTokens int

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

func NewAgent(modelName string, provider model.Provider, systemPrompt string, agentsDir string, observers ...func(Event)) *Agent {
	if systemPrompt == "" {
		systemPrompt = DEFAULT_SYS_PROMPT
	}
	agent := &Agent{SystemPrompt: systemPrompt, Model: modelName, Provider: provider, Messages: []map[string]any{}, AgentsDir: agentsDir, ContextBudget: DEFAULT_CONTEXT_BUDGET}
	if len(observers) > 0 {
		agent.OnEvent = observers[0]
	}
	sysPrompt, err := agent.systemText()
	if err != nil {
		agent.warning("Error loading system prompt: " + err.Error())
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
		agent.warning(warning.Error())
	}
	return agent
}

func (a *Agent) Run(userText string) (reply string, runErr error) {
	return a.RunContext(context.Background(), userText)
}

func (a *Agent) RunContext(ctx context.Context, userText string) (reply string, runErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	accepted := false
	defer func() {
		if accepted && a.Checkpoint != nil {
			if err := a.Checkpoint(); err != nil {
				runErr = fmt.Errorf("save session: %w", err)
			}
		}
	}()
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
		a.appendMessage(map[string]any{"role": "user", "content": context})
	}
	a.appendMessage(map[string]any{"role": "user", "content": userText})
	accepted = true
	a.Interrupted = true
	if a.Checkpoint != nil {
		if err := a.Checkpoint(); err != nil {
			return "", err
		}
	}
	specs := a.Tools.Specs()
	tools := make([]any, len(specs))
	for i, s := range specs {
		tools[i] = s
	}
	options := &model.CompleteOptions{
		ResponseFormat:       a.ResponseFormat,
		OnDelta:              a.OnDelta,
		Context:              ctx,
		BeforeRequest:        a.BeforeRequest,
		OnUsage:              a.OnUsage,
		Model:                a.Model,
		Tools:                tools,
		MaxTokens:            a.MaxOutputTokens,
		OnRetry:              a.OnRetry,
		OnRateLimitRemaining: a.OnRateLimitRemaining,
	}
	limit := a.MaxToolSteps
	if limit <= 0 {
		limit = MAX_TOOL_STEPS
	}
	for step := 0; step <= limit; step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if step == limit {
			if a.RequireCompletion {
				return "", ErrToolLimit
			}
			a.appendMessage(map[string]any{"role": "user", "content": fmt.Sprintf("The tool limit of %d rounds for this turn has been reached. Stop using tools. Summarize completed work and any remaining work accurately; do not claim unfinished tasks are complete.", limit)})
			options.Tools = nil
		}
		a.maybeCompactContext(ctx)
		if a.Checkpoint != nil {
			if err := a.Checkpoint(); err != nil {
				return "", err
			}
		}
		sent := len(a.Messages)
		a.contextEvent()
		a.emit(Event{Kind: "request", Text: "Requesting model response"})
		resp, err := model.Chat(
			a.Provider,
			a.Messages,
			options,
		)
		if err != nil {
			return "", err
		}
		a.recordUsage(resp.Usage, sent)
		a.contextEvent()
		if resp.FinishReason != nil && *resp.FinishReason == "length" {
			return "", fmt.Errorf("model output reached its token limit; increase --max-output-tokens or request smaller edits. No tools from the truncated response were executed")
		}
		if len(resp.ToolCalls) == 0 {
			a.appendMessage(map[string]any{"role": "assistant", "content": resp.Content})
			a.Interrupted = false
			if step == limit {
				return fmt.Sprintf("Tool limit (%d rounds) reached.\n%s\nAsk me to continue if there is remaining work.", limit, resp.Content), nil
			}
			return resp.Content, nil
		}
		if step == limit {
			return "", fmt.Errorf("tool limit (%d rounds) reached; provider requested more tools despite the summary-only request. Progress is saved; ask me to continue", limit)
		}
		if err := a.runToolCallsContext(ctx, resp); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("tool limit (%d rounds) reached; ask me to continue", limit)

}

func (a *Agent) maybeCompact() {
	a.maybeCompactContext(context.Background())
}

func (a *Agent) maybeCompactContext(ctx context.Context) {
	if a.ContextBudget <= 0 || a.estimatedTokens() <= a.ContextBudget {
		return
	}
	a.emit(Event{Kind: "compaction", Text: "Compacting context"})
	summaryModel := a.SummaryModel
	if summaryModel == "" {
		summaryModel = a.Model
	}
	compacted, err := compact(a.Provider, summaryModel, a.Messages, COMPACT_KEEP_HEAD, COMPACT_KEEP_TAIL, &model.CompleteOptions{Context: ctx, BeforeRequest: a.BeforeRequest, OnUsage: a.OnUsage, OnRetry: a.OnRetry, OnRateLimitRemaining: a.OnRateLimitRemaining})
	if err != nil {
		a.warning("compaction failed: " + err.Error())
		return
	}
	if len(compacted) != len(a.Messages) {
		a.usageTokens, a.usageMsgCount = 0, 0
		a.usageSystem = ""
	}
	a.Messages = compacted
	a.contextEvent()
	a.emit(Event{Kind: "compacted", Text: "Context compaction finished"})
}

func (a *Agent) runToolCalls(resp *model.LLMResponse) error {
	return a.runToolCallsContext(context.Background(), resp)
}

func (a *Agent) runToolCallsContext(ctx context.Context, resp *model.LLMResponse) error {
	a.appendMessage(map[string]any{"role": "assistant", "content": resp.Content, "tool_calls": resp.ToolCalls})
	if a.Checkpoint != nil {
		if err := a.Checkpoint(); err != nil {
			return err
		}
	}
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

		a.emit(Event{Kind: "tool-start", ID: id, Name: name, Arguments: arguments})
		started := time.Now()
		result := a.Tools.CallContext(ctx, name, arguments)
		a.emit(Event{Kind: "tool-finish", ID: id, Name: name, Arguments: arguments, Text: result.String(), Failed: result.Err != nil, Duration: time.Since(started)})
		if a.OnToolResult != nil {
			a.OnToolResult(name, result)
		}

		a.appendMessage(map[string]any{
			"role":         "tool",
			"tool_call_id": id,
			"content":      result.String(),
		})
		if a.Checkpoint != nil {
			if err := a.Checkpoint(); err != nil {
				return err
			}
		}
	}
	if a.Checkpoint != nil {
		return a.Checkpoint()
	}
	return nil
}

func (a *Agent) appendMessage(m map[string]any) {
	a.Messages = append(a.Messages, m)
	a.Transcript = append(a.Transcript, m)
}
func (a *Agent) Snapshot(v *session.Session) {
	v.Messages = a.Messages
	v.Transcript = a.Transcript
	v.Interrupted = a.Interrupted
	v.Skills = nil
	for name := range a.activeSkills {
		v.Skills = append(v.Skills, name)
	}
}
func (a *Agent) Restore(v *session.Session) []error {
	a.Messages = append([]map[string]any{{"role": "system", "content": a.SystemPrompt}}, v.Messages...)
	if len(v.Messages) > 0 && msgRole(v.Messages[0]) == "system" {
		a.Messages = a.Messages[1:]
	}
	a.Transcript = append([]map[string]any(nil), v.Transcript...)
	a.activeSkills = make(map[string]Skill)
	var warnings []error
	for _, name := range v.Skills {
		if _, err := a.activateSkill(name); err != nil {
			warnings = append(warnings, err)
		}
	}
	a.refreshSkillContext()
	a.usageTokens = 0
	a.usageMsgCount = 0
	a.usageSystem = ""
	// Complete unanswered tool calls with an interruption marker; never execute them.
	answered := map[string]bool{}
	for _, m := range a.Messages {
		if msgRole(m) == "tool" {
			id, _ := m["tool_call_id"].(string)
			answered[id] = true
		}
	}
	var repaired []map[string]any
	for _, m := range a.Messages {
		repaired = append(repaired, m)
		for _, call := range msgToolCalls(m) {
			tc, ok := call.(map[string]any)
			if !ok {
				continue
			}
			id, _ := tc["id"].(string)
			if !answered[id] {
				marker := map[string]any{"role": "tool", "tool_call_id": id, "content": "Interrupted before a result was saved. Execution status is unknown; inspect workspace before retrying."}
				repaired = append(repaired, marker)
				a.Transcript = append(a.Transcript, marker)
			}
		}
	}
	a.Messages = repaired
	a.appendMessage(map[string]any{"role": "user", "content": "Session resumed with a fresh sandbox from current workspace files. Previous isolated changes were not restored. Recheck files before continuing."})
	a.Interrupted = false
	return warnings
}

func (a *Agent) ResetUsage() { a.usageTokens = 0; a.usageMsgCount = 0; a.usageSystem = "" }
