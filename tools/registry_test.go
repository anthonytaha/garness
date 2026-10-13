package tools

import (
	"testing"
)

func TestRegistryApprovalCannotBeBypassed(t *testing.T) {
	registry := NewToolRegistry()
	ran := false
	registry.Register(Tool{Name: "dangerous", NeedsApproval: true, Func: func(map[string]any) (string, error) { ran = true; return "ok", nil }})
	registry.Call("dangerous", "{}")
	if ran {
		t.Fatal("registry bypassed approval")
	}
	approvals := 0
	registry.Approve = func(name, args string) bool { approvals++; return true }
	registry.Call("dangerous", "invalid")
	if approvals != 0 {
		t.Fatal("approved invalid arguments")
	}
	if result := registry.Call("dangerous", "{}"); result != "ok" || !ran || approvals != 1 {
		t.Fatal(result)
	}
}
