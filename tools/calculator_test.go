package tools

import (
	"strings"
	"testing"
)

func TestDefaultCalculatorTool(t *testing.T) {
	registry := DefaultTools()
	for _, item := range []struct{ Expression, Result string }{
		{"47 * 89", "4183"},
		{"2 ** 3 ** 2", "512"},
		{"(4 + 6) / 2", "5"},
	} {
		if result := registry.Call("calculator", `{"expression":"`+item.Expression+`"}`); result != item.Result {
			t.Fatalf("%s: %s", item.Expression, result)
		}
	}
	if result := registry.Call("calculator", `{"expression":"1 / 0"}`); !strings.HasPrefix(result, "error:") {
		t.Fatal(result)
	}
}
