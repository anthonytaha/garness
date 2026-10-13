package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"bufio"
	"fmt"
	"os"
)

func Run() int {
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	scanner := bufio.NewScanner(os.Stdin)
	agent := harness.NewAgent("", *model.NewMistralProvider("mistral-small-latest", os.Getenv("MISTRAL_API_KEY")), "", ".")
	agent.ContextBudget = config.ContextBudget
	if config.SkillsDir != "" {
		for _, warning := range agent.ConfigureSkills(config.SkillsDir) {
			fmt.Fprintln(os.Stderr, "WARNING:", warning)
		}
	}
	agent.Approve = terminalApproval(scanner)
	box, err := sandbox.NewSandbox(".", sandbox.DefaultSandboxPolicy(config.Profile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		return 1
	}
	defer box.Close()
	agent.ConfigureSandbox(box)
	fmt.Printf("Sandbox profile: %s (shell network requires domain approval)\n", config.Profile)
	if config.Profile == "isolated" {
		fmt.Println("Review changes with :changes; apply them with :apply.")
	}
	runREPL(scanner, agent, box)
	return 0
}
