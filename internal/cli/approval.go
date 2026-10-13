package cli

import (
	"bufio"
	"fmt"
	"strings"
)

func terminalApproval(scanner *bufio.Scanner) func(string, string) bool {
	return func(name string, arguments string) bool {
		fmt.Printf("  approve %s(%s)? [y/N] ", name, arguments)
		if !scanner.Scan() {
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
}
