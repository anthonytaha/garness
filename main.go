package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"anthonytaha/garness/harness"
	"anthonytaha/garness/model"
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
}

func main() {
	loadEnvFile(".env")
	agent := harness.NewAgent("", *model.NewGeminiProvider("gemini-3.5-flash", os.Getenv("GOOGLE_AI_STUDIO_KEY")), "", ".")
	fmt.Println("Welcome to Garness:")
	fmt.Println("System prompt: ", agent.SystemPrompt)
	scanner := bufio.NewScanner(os.Stdin)
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
		reply, err := agent.Run(user)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Println("bot>", reply)
	}
}
