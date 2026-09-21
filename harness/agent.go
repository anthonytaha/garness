package harness

import (
	"anthonytaha/garness/model"
)

type Agent struct {
	model    string
	provider model.Provider
	messages []map[string]any
}

func NewAgent(modelName string, provider model.Provider) *Agent {
	return &Agent{model: modelName, provider: provider, messages: []map[string]any{}}
}

func (a *Agent) Send(userText string) (string, error) {
	a.messages = append(a.messages, map[string]any{"role": "user", "content": userText})
	options := &model.CompleteOptions{
		Model: a.model,
	}
	resp, err := model.Chat(a.provider,
		a.messages,
		options,
	)
	if err != nil {
		return "", err
	}
	a.messages = append(a.messages, map[string]any{"role": "assistant", "content": resp.Content})
	return resp.Content, nil
}
