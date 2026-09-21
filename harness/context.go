package harness

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var attachPattern = regexp.MustCompile(`@(\S+)`)

func Deliver (userText string) (string) {
	blocks := make([]string, 0)
	for _, match := range attachPattern.FindAllStringSubmatch(userText, -1) {
		path := match[1]

		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		body, err := os.ReadFile(path)
		if err != nil {
			print("Error reading file %s: %v\n", path, err)
		}

		blocks = append(blocks, fmt.Sprintf("--- %s ---\n%s", path, body))
	}
	return strings.Join(blocks, "\n\n")

}