package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/sandbox"
	"anthonytaha/garness/session"
	"bufio"
	"flag"
	"fmt"
	"os"
	"time"
)

func Run() int {
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	store, err := session.Open("", ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(flag.Args()) > 0 {
		return sessionCommand(store, flag.Args())
	}
	scanner := bufio.NewScanner(os.Stdin)
	selected, err := chooseSession(store, scanner, config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	agent := harness.NewAgent("", *model.NewMistralProvider("mistral-small-latest", os.Getenv("MISTRAL_API_KEY")), "", ".")
	agent.ContextBudget = config.ContextBudget
	agent.MaxToolSteps = config.MaxToolSteps
	agent.MaxOutputTokens = config.MaxOutputTokens
	agent.OnRetry = func(status int, delay time.Duration) {
		fmt.Fprintf(os.Stderr, "Provider returned HTTP %d; retrying in %s...\n", status, delay.Round(time.Millisecond))
	}
	if config.SkillsDir != "" {
		for _, warning := range agent.ConfigureSkills(config.SkillsDir) {
			fmt.Fprintln(os.Stderr, "WARNING:", warning)
		}
	}
	agent.OnRateLimitRemaining = func(remaining int64) {
		fmt.Fprintf(os.Stderr, "Provider X-RateLimit-Remaining: %d\n", remaining)
		if remaining == 0 {
			fmt.Fprintln(os.Stderr, "Provider reports no remaining quota; further requests may be rate-limited.")
		}
	}
	agent.Approve = terminalApproval(scanner)
	box, err := sandbox.NewSandbox(".", sandbox.DefaultSandboxPolicy(config.Profile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		return 1
	}

	agent.ConfigureSandbox(box)
	fmt.Printf("Sandbox profile: %s (shell network requires domain approval)\n", config.Profile)
	if config.Profile == "isolated" {
		fmt.Println("Review changes with :changes; apply them with :apply.")
	}
	manager := &sessionManager{store: store, agent: agent, box: box, current: selected}
	if selected != nil {
		manager.unlock, err = store.Lock(selected.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		selected, err = store.Load(selected.ID)
		if err != nil {
			manager.close()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		manager.current = selected
		for _, warning := range agent.Restore(selected) {
			fmt.Fprintln(os.Stderr, "WARNING:", warning)
		}
		fmt.Println("Resumed", selected.ID, "with a fresh sandbox")
	}
	defer manager.close()
	defer func() { manager.box.Close() }()
	agent.Checkpoint = manager.save
	runREPL(scanner, agent, box, manager)
	return 0
}
