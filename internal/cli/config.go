package cli

import (
	"anthonytaha/garness/harness"
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("error reading %s: %v\n", path, err)
	}
}

type config struct {
	Profile       string
	ContextBudget int
	SkillsDir     string
}

func loadConfig() (config, error) {
	loadEnvFile(".env")
	profile := strings.TrimSpace(os.Getenv("GARNESS_SANDBOX_PROFILE"))
	if profile == "" {
		profile = "local"
	}
	flag.StringVar(&profile, "sandbox-profile", profile, "sandbox profile: local or isolated")
	flag.Parse()
	contextBudget := harness.DEFAULT_CONTEXT_BUDGET
	if value, exists := os.LookupEnv("GARNESS_CONTEXT_BUDGET"); exists {
		budget, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || budget < 0 {
			return config{}, fmt.Errorf("GARNESS_CONTEXT_BUDGET must be a non-negative integer (0 disables compaction)")
		}
		contextBudget = budget
	}
	return config{Profile: profile, ContextBudget: contextBudget, SkillsDir: strings.TrimSpace(os.Getenv("GARNESS_SKILLS_DIR"))}, nil
}
