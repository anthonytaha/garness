package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/sandbox"
	"bufio"
	"fmt"
	"strings"
)

func runREPL(scanner *bufio.Scanner, agent *harness.Agent, box *sandbox.Sandbox, manager *sessionManager) {
	fmt.Println("Welcome to Garness:")

	for {
		fmt.Print("you> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		user := scanner.Text()
		if strings.TrimSpace(user) == "" {
			continue
		}
		if strings.HasPrefix(user, ":") && manager.command(user, scanner) {
			continue
		}
		if user == ":changes" || user == ":apply" {
			var result string
			var err error
			if user == ":changes" {
				result, err = manager.box.Changes()
			} else {
				result, err = manager.box.ApplyChanges()
			}
			if err != nil {
				fmt.Println("error:", err)
			} else {
				fmt.Println(result)
			}
			continue
		}
		reply, err := agent.Run(user)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Println("bot>", reply)
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("error reading input:", err)
	}

}
