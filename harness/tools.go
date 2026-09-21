package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type ToolFunc func(args map[string]any) (string, error)

type Tool struct {
	Name        string
	Description string
	Func        ToolFunc
	Parameters  map[string]string
}

type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (tr *ToolRegistry) RegisterTool(tool Tool) {
	tr.tools[tool.Name] = tool
}

func (tr *ToolRegistry) GetTool(name string) (Tool, bool) {
	tool, exists := tr.tools[name]
	return tool, exists
}

func (r *ToolRegistry) Specs() []map[string]any {
	specs := make([]map[string]any, 0, len(r.tools))

	for _, t := range r.tools {
		specs = append(specs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	return specs
}

func (r *ToolRegistry) Call(name, arguments string) string {
	tool, ok := r.tools[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name)
	}

	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf(
				"error: could not parse arguments %q",
				arguments,
			)
		}
	}

	result, err := tool.Func(args)
	if err != nil {
		return "error: " + err.Error()
	}

	return result
}

func (r *ToolRegistry) Len() int {
	return len(r.tools)
}

func Calculator(expression string) (string, error) {
	p := parser{
		input: expression,
	}

	result, err := p.parseExpression()
	if err != nil {
		return "", err
	}

	p.skipSpace()
	if !p.atEnd() {
		return "", fmt.Errorf(
			"unsupported expression near %q",
			p.input[p.pos:],
		)
	}

	if math.IsNaN(result) || math.IsInf(result, 0) {
		return "", fmt.Errorf("invalid arithmetic result")
	}

	if result == math.Trunc(result) {
		return strconv.FormatInt(int64(result), 10), nil
	}

	return strconv.FormatFloat(result, 'g', -1, 64), nil
}

func ReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("error: no such file: %s", path)
		}
		return fmt.Sprintf("error: %v", err)
	}

	return string(data)
}

func DefaultTools() *ToolRegistry {
	reg := NewToolRegistry()

	reg.Register(Tool{
		Name:        "calculator",
		Description: "Evaluate an arithmetic expression like '47 * 89'.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"expression"},
		},
		Func: func(args map[string]any) (string, error) {
			expression, ok := args["expression"].(string)
			if !ok {
				return "", fmt.Errorf("expression must be a string")
			}

			return Calculator(expression)
		},
	})

	reg.Register(Tool{
		Name:        "read_file",
		Description: "Read a UTF-8 text file from disk and return its contents.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"path"},
		},
		Func: func(args map[string]any) (string, error) {
			path, ok := args["path"].(string)
			if !ok {
				return "", fmt.Errorf("path must be a string")
			}
			return ReadFile(path), nil
		},
	})

	return reg
}

// -----------------------------------------------------------------------------
// Arithmetic parser
// -----------------------------------------------------------------------------

type parser struct {
	input string
	pos   int
}

// Grammar:
//
//	expression = term { ("+" | "-") term }
//	term       = power { ("*" | "/" | "%") power }
//	power      = unary [ "**" power ]
//	unary      = "-" unary | primary
//	primary    = number | "(" expression ")"
//
// Exponentiation is right-associative, so:
//
//	2 ** 3 ** 2 == 2 ** (3 ** 2)

func (p *parser) parseExpression() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpace()

		switch {
		case p.consume("+"):
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left += right

		case p.consume("-"):
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left -= right

		default:
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpace()

		// Don't consume the first "*" of "**".
		if strings.HasPrefix(p.input[p.pos:], "**") {
			return left, nil
		}

		switch {
		case p.consume("*"):
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			left *= right

		case p.consume("/"):
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right

		case p.consume("%"):
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			left = pythonMod(left, right)

		default:
			return left, nil
		}
	}
}

func (p *parser) parsePower() (float64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}

	p.skipSpace()

	if p.consume("**") {
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}

		left = math.Pow(left, right)
	}

	return left, nil
}

func (p *parser) parseUnary() (float64, error) {
	p.skipSpace()

	if p.consume("-") {
		value, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return -value, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (float64, error) {
	p.skipSpace()

	if p.consume("(") {
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}

		p.skipSpace()
		if !p.consume(")") {
			return 0, fmt.Errorf("expected ')'")
		}

		return value, nil
	}

	return p.parseNumber()
}

func (p *parser) parseNumber() (float64, error) {
	p.skipSpace()

	start := p.pos
	seenDigit := false

	for !p.atEnd() && unicode.IsDigit(rune(p.input[p.pos])) {
		seenDigit = true
		p.pos++
	}

	if !p.atEnd() && p.input[p.pos] == '.' {
		p.pos++

		for !p.atEnd() && unicode.IsDigit(rune(p.input[p.pos])) {
			seenDigit = true
			p.pos++
		}
	}

	if !seenDigit {
		return 0, fmt.Errorf("unsupported expression")
	}

	// Optional scientific notation, e.g. 1e3 or 2.5E-4.
	if !p.atEnd() && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
		expStart := p.pos
		p.pos++

		if !p.atEnd() && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}

		digitStart := p.pos
		for !p.atEnd() && unicode.IsDigit(rune(p.input[p.pos])) {
			p.pos++
		}

		if digitStart == p.pos {
			p.pos = expStart
		}
	}

	value, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number")
	}

	return value, nil
}

func (p *parser) skipSpace() {
	for !p.atEnd() && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *parser) consume(s string) bool {
	if strings.HasPrefix(p.input[p.pos:], s) {
		p.pos += len(s)
		return true
	}

	return false
}

func (p *parser) atEnd() bool {
	return p.pos >= len(p.input)
}

func pythonMod(a, b float64) float64 {
	r := math.Mod(a, b)

	if r != 0 && ((r < 0) != (b < 0)) {
		r += b
	}

	return r
}