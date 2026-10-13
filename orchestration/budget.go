package orchestration

import (
	"encoding/json"
	"sync"
)

type budget struct {
	mutex  sync.Mutex
	store  *Store
	id     string
	limits Limits
	usage  Usage
}

func (b *budget) reserve(tokens int, tool bool) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	next := b.usage
	if tool {
		next.ToolCalls++
	} else {
		next.Requests++
		next.ReservedTokens += tokens
	}
	if next.Requests > b.limits.MaxRequests || tokens > b.limits.MaxReservedTokens-b.usage.ReservedTokens || next.ToolCalls > b.limits.MaxToolCalls {
		return &Failure{Kind: "budget", Message: "shared request, token reservation, or tool budget exhausted"}
	}
	if err := b.store.saveUsage(b.id, next); err != nil {
		return err
	}
	b.usage = next
	return nil
}

func (b *budget) beforeRequest(tokens int) error { return b.reserve(tokens, false) }

func (b *budget) checkpoint() (Usage, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.usage, b.store.saveUsage(b.id, b.usage)
}

func (b *budget) record(usage map[string]any) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	data, _ := json.Marshal(usage["total_tokens"])
	var tokens int
	if json.Unmarshal(data, &tokens) == nil && tokens > 0 {
		b.usage.ReportedTokens += tokens
	}
	// Reservations are persisted before requests. Reported usage is advisory;
	// the next reservation or coordinator checkpoint also saves this count.
}
