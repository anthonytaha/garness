package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
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
	New             bool
	Resume          string
	Profile         string
	ContextBudget   int
	MaxToolSteps    int
	MaxOutputTokens int
	SkillsDir       string
}

func loadConfig() (config, error) {
	loadEnvFile(".env")
	profile := strings.TrimSpace(os.Getenv("GARNESS_SANDBOX_PROFILE"))
	if profile == "" {
		profile = "local"
	}
	flag.StringVar(&profile, "sandbox-profile", profile, "sandbox profile: local or isolated")
	var fresh bool
	var resume string
	flag.BoolVar(&fresh, "new", false, "start a new session")
	flag.StringVar(&resume, "resume", "", "resume session ID")
	maxToolSteps := harness.MAX_TOOL_STEPS
	if value, exists := os.LookupEnv("GARNESS_MAX_TOOL_STEPS"); exists {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return config{}, fmt.Errorf("GARNESS_MAX_TOOL_STEPS must be a positive integer")
		}
		maxToolSteps = parsed
	}
	flag.IntVar(&maxToolSteps, "max-tool-steps", maxToolSteps, "maximum tool rounds per turn")
	maxOutputTokens := model.DefaultMaxOutputTokens
	if value, exists := os.LookupEnv("GARNESS_MAX_OUTPUT_TOKENS"); exists {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return config{}, fmt.Errorf("GARNESS_MAX_OUTPUT_TOKENS must be a positive integer")
		}
		maxOutputTokens = parsed
	}
	flag.IntVar(&maxOutputTokens, "max-output-tokens", maxOutputTokens, "maximum output tokens per model response")
	flag.Parse()
	if maxOutputTokens <= 0 {
		return config{}, fmt.Errorf("--max-output-tokens must be a positive integer")
	}
	if maxToolSteps <= 0 {
		return config{}, fmt.Errorf("--max-tool-steps must be a positive integer")
	}
	if fresh && resume != "" {
		return config{}, fmt.Errorf("--new and --resume are mutually exclusive")
	}
	contextBudget := harness.DEFAULT_CONTEXT_BUDGET
	if value, exists := os.LookupEnv("GARNESS_CONTEXT_BUDGET"); exists {
		budget, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || budget < 0 {
			return config{}, fmt.Errorf("GARNESS_CONTEXT_BUDGET must be a non-negative integer (0 disables compaction)")
		}
		contextBudget = budget
	}
	return config{New: fresh, Resume: resume, Profile: profile, ContextBudget: contextBudget, MaxToolSteps: maxToolSteps, MaxOutputTokens: maxOutputTokens, SkillsDir: strings.TrimSpace(os.Getenv("GARNESS_SKILLS_DIR"))}, nil
}
