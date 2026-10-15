package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSingleFilePlannerUsesSchemaAndCorrectsResponse(t *testing.T) {
	var requests atomic.Int32
	plan := planFor(fixtureTask("create-index-html", "index.html"))
	plan.Tasks[0].Instructions = "Implement a self-contained Flappy Bird clone using inline Canvas, JavaScript, and CSS."
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages       []map[string]any `json:"messages"`
			ResponseFormat map[string]any   `json:"response_format"`
			Tools          []any            `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ResponseFormat == nil {
			response(w, "Create a self-contained index.html; no existing implementation.")
			return
		}
		if len(body.Tools) != 0 {
			t.Error("structured plan generation still exposes tools")
		}
		data, _ := json.Marshal(planResponseFormat(1, DefaultLimits()))
		var expected map[string]any
		json.Unmarshal(data, &expected)
		if !reflect.DeepEqual(body.ResponseFormat, expected) {
			t.Error("planner schema missing or changed on correction")
		}
		if requests.Add(1) == 1 {
			// Reproduce the field-shape error in the saved Flappy Bird plan.
			data, _ := json.Marshal(plan)
			text := strings.Replace(string(data), `"write_scope":["index.html"]`, `"write_scope":[{"path":"index.html","type":"file"}]`, 1)
			response(w, text)
			return
		}
		last, _ := body.Messages[len(body.Messages)-1]["content"].(string)
		if !strings.Contains(last, "cannot unmarshal object") || !strings.Contains(last, "string write_scope") {
			t.Error("correction lacks actionable field error", last)
		}
		data, _ = json.Marshal(plan)
		response(w, string(data))
	})
	c.Run.Objective = "Create in a single index.html file a flappy bird clone"
	if err := c.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || c.Run.State != AwaitingApproval || c.Run.Approval != nil {
		t.Fatal("plan was not corrected and held for approval")
	}
	if c.Run.Plan.Tasks[0].WriteScope[0] != "index.html" {
		t.Fatal("write scope expanded")
	}
	if len(c.Run.Events) < 2 || !strings.Contains(c.Run.Events[len(c.Run.Events)-2].Detail, "planner response 1 rejected") {
		t.Fatal("validation error not recorded")
	}
}

func TestPlannerFailureReportsSpecificValidationError(t *testing.T) {
	for _, test := range []struct{ name, text, want string }{
		{"malformed JSON", `{"version":1,"checks":[{"contains\":\"Restart\"}]}`, "unexpected EOF"},
		{"invalid task ID", "", `invalid or duplicate task ID "create_index_html"`},
		{"invalid scope shape", "", "cannot unmarshal object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := test.text
			if text == "" {
				id := "create-index-html"
				if test.name == "invalid task ID" {
					id = "create_index_html"
				}
				data, _ := json.Marshal(planFor(fixtureTask(id, "index.html")))
				text = string(data)
				if test.name == "invalid scope shape" {
					text = strings.Replace(text, `"write_scope":["index.html"]`, `"write_scope":[{"path":"index.html"}]`, 1)
				}
			}
			var requests atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					ResponseFormat map[string]any `json:"response_format"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if body.ResponseFormat == nil {
					response(w, "No inspection needed.")
					return
				}
				requests.Add(1)
				response(w, text)
			})
			err := c.Plan(context.Background())
			var failure *Failure
			if !errors.As(err, &failure) || failure.Kind != "format" || !strings.Contains(failure.Message, test.want) || requests.Load() != 3 {
				t.Fatalf("unhelpful or unbounded failure: %v; requests=%d", err, requests.Load())
			}
			loaded, err := c.Store.Load(c.Run.ID)
			if err != nil || loaded.Failure == nil || !strings.Contains(loaded.Failure.Message, test.want) {
				t.Fatal("validation failure was not persisted", err)
			}
		})
	}
}

func TestPlannerExampleMatchesConfiguredPlan(t *testing.T) {
	limits := DefaultLimits()
	limits.Workers = 1
	limits.MaxOutputTokens = 4096
	prompt := plannerPrompt("single file game", 2, limits, []string{"README.md"})
	const prefix = "This is a complete valid example of the shape, not a task to copy: "
	_, tail, ok := strings.Cut(prompt, prefix)
	if !ok {
		t.Fatal("no example")
	}
	line, _, _ := strings.Cut(tail, "\n")
	var example Plan
	if err := decode(line, &example); err != nil {
		t.Fatal(err)
	}
	if err := example.Validate(); err != nil {
		t.Fatal("prompt teaches an invalid plan", err)
	}
	if example.Version != 2 || example.Limits != limits {
		t.Fatal("example ignores configured version or limits")
	}
	format := planResponseFormat(2, limits)
	schema := format["json_schema"].(map[string]any)["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	version := properties["version"].(map[string]any)["enum"]
	if fmt.Sprint(version) != "[2]" {
		t.Fatal("version not constrained")
	}
	task := properties["tasks"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if task["id"].(map[string]any)["pattern"] != taskIDPattern.String() {
		t.Fatal("ID pattern differs from validator")
	}
	if task["write_scope"].(map[string]any)["items"].(map[string]any)["type"] != "string" {
		t.Fatal("scope paths not constrained to strings")
	}
}

func TestPlannerInspectionIsBoundedAndGenerationHasNoTools(t *testing.T) {
	var inspectionRequests, planRequests atomic.Int32
	plan := planFor(fixtureTask("create-index-html", "index.html"))
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ResponseFormat map[string]any `json:"response_format"`
			Tools          []any          `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ResponseFormat != nil {
			planRequests.Add(1)
			if len(body.Tools) != 0 {
				t.Error("plan generation can still request file reads")
			}
			data, _ := json.Marshal(plan)
			response(w, string(data))
			return
		}
		inspectionRequests.Add(1)
		if len(body.Tools) == 0 {
			response(w, "Enough context gathered.")
			return
		}
		response(w, "", map[string]any{"id": "inspect", "function": map[string]any{"name": "read_file", "arguments": `{"path":"AGENTS.md"}`}})
	})
	if err := c.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inspectionRequests.Load() != 5 || planRequests.Load() != 1 || c.Run.Usage.ToolCalls != 4 {
		t.Fatal("inspection was not bounded", inspectionRequests.Load(), planRequests.Load(), c.Run.Usage)
	}
}
