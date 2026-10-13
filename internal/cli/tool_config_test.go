package cli

import (
	"flag"
	"os"
	"testing"
)

func TestToolLimitConfig(t *testing.T) {
	for _, test := range []struct {
		name, env string
		args      []string
		want      int
		fails     bool
	}{
		{name: "default", env: "32", want: 32},
		{name: "environment", env: "48", want: 48},
		{name: "flag overrides environment", env: "48", args: []string{"--max-tool-steps", "64"}, want: 64},
		{name: "invalid environment", env: "bad", fails: true},
		{name: "zero flag", env: "32", args: []string{"--max-tool-steps", "0"}, fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldFlags, oldArgs := flag.CommandLine, os.Args
			defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
			flag.CommandLine = flag.NewFlagSet("fixture", flag.ContinueOnError)
			os.Args = append([]string{"fixture"}, test.args...)
			t.Setenv("GARNESS_MAX_TOOL_STEPS", test.env)
			t.Setenv("GARNESS_CONTEXT_BUDGET", "0")
			config, e := loadConfig()
			if (e != nil) != test.fails {
				t.Fatalf("error=%v", e)
			}
			if e == nil && config.MaxToolSteps != test.want {
				t.Fatalf("limit=%d", config.MaxToolSteps)
			}
		})
	}
}

func TestOutputTokenConfig(t *testing.T) {
	for _, test := range []struct {
		name, env string
		args      []string
		want      int
		fails     bool
	}{
		{name: "default", want: 8192}, {name: "environment", env: "4096", want: 4096}, {name: "flag", env: "4096", args: []string{"--max-output-tokens", "16384"}, want: 16384}, {name: "invalid environment", env: "bad", fails: true}, {name: "zero flag", args: []string{"--max-output-tokens", "0"}, fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldFlags, oldArgs := flag.CommandLine, os.Args
			defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
			flag.CommandLine = flag.NewFlagSet("fixture", flag.ContinueOnError)
			os.Args = append([]string{"fixture"}, test.args...)
			t.Setenv("GARNESS_MAX_TOOL_STEPS", "32")
			t.Setenv("GARNESS_CONTEXT_BUDGET", "0")
			t.Setenv("GARNESS_MAX_OUTPUT_TOKENS", test.env)
			if test.env == "" {
				os.Unsetenv("GARNESS_MAX_OUTPUT_TOKENS")
			}
			config, err := loadConfig()
			if (err != nil) != test.fails {
				t.Fatalf("error=%v", err)
			}
			if err == nil && config.MaxOutputTokens != test.want {
				t.Fatalf("limit=%d", config.MaxOutputTokens)
			}
		})
	}
}
