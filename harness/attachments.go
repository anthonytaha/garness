package harness

import (
	"anthonytaha/garness/sandbox"
	"fmt"
	"regexp"
	"strings"
)

var attachPattern = regexp.MustCompile(`@(\S+)`)

func DeliverWorkspace(ws *sandbox.Workspace, userText string) (string, error) {
	var blocks []string
	for _, match := range attachPattern.FindAllStringSubmatch(userText, -1) {
		body, err := ws.ReadFile(match[1])
		if err != nil {
			return "", fmt.Errorf("attachment %s: %w", match[1], err)
		}
		blocks = append(blocks, fmt.Sprintf("--- %s ---\n%s", match[1], body))
	}
	return strings.Join(blocks, "\n\n"), nil
}

func Deliver(userText string) string {
	ws, err := sandbox.NewWorkspace(".")
	if err != nil {
		return "error: " + err.Error()
	}
	body, err := DeliverWorkspace(ws, userText)
	if err != nil {
		return "error: " + err.Error()
	}
	return body
}
