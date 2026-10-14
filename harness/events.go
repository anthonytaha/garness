package harness

import (
	"fmt"
	"time"
)

// Event contains presentation data only. Observers do not control execution,
// permissions, checkpoints, or accounting. Callbacks run on the agent goroutine.
type Event struct {
	Kind          string
	ID            string
	Name          string
	Arguments     string
	Text          string
	Failed        bool
	Duration      time.Duration
	ContextTokens int
	ContextBudget int
}

func (a *Agent) emit(event Event) {
	if a.OnEvent != nil {
		a.OnEvent(event)
	}
}

func (a *Agent) contextEvent() {
	if a.OnEvent == nil {
		return
	}
	a.emit(Event{Kind: "context", ContextTokens: a.estimatedTokens(), ContextBudget: a.ContextBudget})
}

func (a *Agent) warning(text string) {
	if a.OnEvent != nil {
		a.emit(Event{Kind: "warning", Text: text, Failed: true})
	} else {
		fmt.Println("WARNING:", text)
	}
}
