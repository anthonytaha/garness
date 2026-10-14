package cli

import (
	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
	"anthonytaha/garness/orchestration"
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
	AccentColor         string
	Plain               bool
	NoStream            bool
	OrchestrationLimits orchestration.Limits
	New                 bool
	Resume              string
	Profile             string
	ContextBudget       int
	MaxToolSteps        int
	MaxOutputTokens     int
	SkillsDir           string
}

func loadConfig() (config, error) {
	loadEnvFile(".env")
	profile := strings.TrimSpace(os.Getenv("GARNESS_SANDBOX_PROFILE"))
	if profile == "" {
		profile = "local"
	}
	flag.StringVar(&profile, "sandbox-profile", profile, "sandbox profile: local or isolated")
	accentColor := strings.TrimSpace(os.Getenv("GARNESS_ACCENT_COLOR"))
	if accentColor == "" {
		accentColor = "green"
	}
	flag.StringVar(&accentColor, "accent-color", accentColor, "terminal accent: green (default), amber, blue, cyan, magenta, red, white, #RRGGBB, or 0–255")
	var plain, noStream bool
	flag.BoolVar(&plain, "plain", false, "use the plain line-based interface")
	flag.BoolVar(&noStream, "no-stream", false, "disable streaming model responses")
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
	limits := orchestration.DefaultLimits()
	for _, setting := range []struct {
		name, flag, help string
		value            *int
	}{
		{"GARNESS_WORKERS", "workers", "maximum concurrent subagents (1–16)", &limits.Workers},
		{"GARNESS_RUN_REQUESTS", "run-requests", "maximum model HTTP attempts per orchestration run", &limits.MaxRequests},
		{"GARNESS_RUN_TOKENS", "run-tokens", "maximum reserved tokens per orchestration run", &limits.MaxReservedTokens},
		{"GARNESS_RUN_TOOL_CALLS", "run-tool-calls", "maximum worker and verification tool calls per run", &limits.MaxToolCalls},
		{"GARNESS_RUN_TIMEOUT", "run-timeout", "orchestration execution deadline in seconds", &limits.TimeoutSeconds},
	} {
		if value, exists := os.LookupEnv(setting.name); exists {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed <= 0 {
				return config{}, fmt.Errorf("%s must be a positive integer", setting.name)
			}
			*setting.value = parsed
		}
		flag.IntVar(setting.value, setting.flag, *setting.value, setting.help)
	}
	flag.Parse()
	accentColor, err := parseAccentColor(accentColor)
	if err != nil {
		return config{}, fmt.Errorf("--accent-color / GARNESS_ACCENT_COLOR: %w", err)
	}
	if maxOutputTokens <= 0 {
		return config{}, fmt.Errorf("--max-output-tokens must be a positive integer")
	}
	if maxToolSteps <= 0 {
		return config{}, fmt.Errorf("--max-tool-steps must be a positive integer")
	}
	limits.MaxToolSteps, limits.MaxOutputTokens = maxToolSteps, maxOutputTokens
	if err := limits.Validate(); err != nil {
		return config{}, err
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
	return config{AccentColor: accentColor, Plain: plain, NoStream: noStream, OrchestrationLimits: limits, New: fresh, Resume: resume, Profile: profile, ContextBudget: contextBudget, MaxToolSteps: maxToolSteps, MaxOutputTokens: maxOutputTokens, SkillsDir: strings.TrimSpace(os.Getenv("GARNESS_SKILLS_DIR"))}, nil
}
