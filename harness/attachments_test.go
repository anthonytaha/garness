package harness

import (
	"anthonytaha/garness/sandbox"
	"strings"
	"testing"
)

func TestAttachmentConfinement(t *testing.T) {
	ws, err := sandbox.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws.WriteFile("file", "hello")
	if body, err := DeliverWorkspace(ws, "see @file"); err != nil || !strings.Contains(body, "hello") {
		t.Fatalf("%q %v", body, err)
	}
	if _, err := DeliverWorkspace(ws, "see @../secret"); err == nil {
		t.Fatal("attachment escaped")
	}
}
