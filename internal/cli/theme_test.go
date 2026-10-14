package cli

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAccentColorConfig(t *testing.T) {
	for _, test := range []struct {
		name, env string
		args      []string
		want      string
		fails     bool
	}{
		{name: "default green", want: "82"},
		{name: "named environment", env: "amber", want: "214"},
		{name: "hex environment", env: " #00FF88 ", want: "#00ff88"},
		{name: "palette environment", env: "045", want: "45"},
		{name: "flag overrides environment", env: "amber", args: []string{"--accent-color", "blue"}, want: "75"},
		{name: "flag overrides invalid environment", env: "invalid", args: []string{"--accent-color", "green"}, want: "82"},
		{name: "invalid name", env: "invalid", fails: true},
		{name: "invalid hex", args: []string{"--accent-color", "#00gg00"}, fails: true},
		{name: "out of range", args: []string{"--accent-color", "256"}, fails: true},
		{name: "negative palette", env: "-1", fails: true},
		{name: "terminal sequence", env: "\x1b[32m", fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldFlags, oldArgs := flag.CommandLine, os.Args
			defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
			flag.CommandLine = flag.NewFlagSet("fixture", flag.ContinueOnError)
			os.Args = append([]string{"fixture"}, test.args...)
			t.Setenv("GARNESS_ACCENT_COLOR", test.env)
			t.Setenv("GARNESS_MAX_TOOL_STEPS", "32")
			t.Setenv("GARNESS_MAX_OUTPUT_TOKENS", "8192")
			t.Setenv("GARNESS_CONTEXT_BUDGET", "0")
			config, err := loadConfig()
			if (err != nil) != test.fails {
				t.Fatalf("error=%v", err)
			}
			if err == nil && config.AccentColor != test.want {
				t.Fatalf("accent=%q want=%q", config.AccentColor, test.want)
			}
		})
	}
}

func TestTerminalAccentAndNoColor(t *testing.T) {
	m := terminalFixture(t, "#00ff88")
	want := lipgloss.Color("#00ff88")
	if m.accent.GetForeground() != want || m.input.FocusedStyle.Prompt.GetForeground() != want {
		t.Fatal("configured accent did not reach the terminal and composer")
	}
	m.input.SetValue(":plan")
	if strings.Contains(m.View(), "\x1b") {
		t.Fatal("accent ignored NO_COLOR")
	}
}
