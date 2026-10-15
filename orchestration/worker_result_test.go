package orchestration

import (
	"anthonytaha/garness/sandbox"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDecodeJSONCodeFences(t *testing.T) {
	const body = `{"summary":"done","artifacts":["index.html"],"unresolved":[]}`
	for _, text := range []string{body, "```json\n" + body + "\n```", "```\n" + body + "\n```", " \n```json\r\n" + body + "\r\n```\n "} {
		var result Result
		if err := decode(text, &result); err != nil || result.Summary != "done" {
			t.Fatal("valid wrapped result rejected", err)
		}
	}
	for _, text := range []string{
		"Here is the result:\n```json\n" + body + "\n```",
		"```json\n" + body + "\n```\nExplanation",
		"```json\n" + body + " {}\n```",
		"```json\n{\"unknown\":true}\n```",
		"```json\n{broken}\n```",
	} {
		var result Result
		if err := decode(text, &result); err == nil {
			t.Fatal("invalid or ambiguous result accepted", text)
		}
	}
}

func TestFencedWorkerAndReviewerResultsAreVerified(t *testing.T) {
	var writes, reviews atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		messages, text := payload(t, r)
		if strings.Contains(text, "Review correctness") {
			reviews.Add(1)
			response(w, "```json\n{\"passed\":true,\"issues\":[]}\n```")
			return
		}
		if messages[len(messages)-1]["role"] == "tool" {
			response(w, "```json\n{\"summary\":\"done\",\"artifacts\":[\"index.html\"],\"unresolved\":[]}\n```")
			return
		}
		writes.Add(1)
		response(w, "", writeCall("index.html", "good"))
	})
	task := fixtureTask("create-index-html", "index.html")
	task.Review = true
	approve(t, c, planFor(task))
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 || reviews.Load() != 1 || c.Run.State != Completed || len(c.Run.Tasks[0].Attempts[0].Result.Checks) == 0 {
		t.Fatal("wrapped report bypassed checks or repeated work")
	}
	if _, err := os.Stat(filepath.Join(c.Store.Workspace, "index.html")); !os.IsNotExist(err) {
		t.Fatal("changes applied without final approval")
	}
}

func TestResultCorrectionUsesSchemaWithoutReplayingTools(t *testing.T) {
	var writes, corrections atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages       []map[string]any `json:"messages"`
			ResponseFormat map[string]any   `json:"response_format"`
			Tools          []any            `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.ResponseFormat != nil {
			corrections.Add(1)
			if len(body.Tools) != 0 || body.ResponseFormat["type"] != "json_schema" {
				t.Error("result correction lacks tool-free schema")
			}
			response(w, `{"summary":"done","artifacts":["index.html"],"unresolved":[]}`)
			return
		}
		if body.Messages[len(body.Messages)-1]["role"] == "tool" {
			response(w, "Done, see index.html.")
			return
		}
		writes.Add(1)
		response(w, "", writeCall("index.html", "good"))
	})
	approve(t, c, planFor(fixtureTask("create-index-html", "index.html")))
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 || corrections.Load() != 1 {
		t.Fatal("result correction replayed writes")
	}
}

func TestFailedReportRecoveryPreservesFilesWithoutReexecutingWorker(t *testing.T) {
	for _, content := range []string{"good", "bad"} {
		t.Run(content, func(t *testing.T) {
			var requests atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				response(w, "unexpected worker invocation")
			})
			plannedTask := fixtureTask("create-index-html", "index.html")
			plannedTask.MaxAttempts = 2
			approve(t, c, planFor(plannedTask))
			// Reproduce a persisted run from the earlier parser: file writes
			// completed, but three Markdown-wrapped reports were rejected.
			c.Run.State = Failed
			c.Run.Failure = &Failure{Kind: "attempts", Message: "task result could not be decoded"}
			task := &c.Run.Tasks[0]
			task.State = Failed
			id := c.Store.NewID()
			task.Attempts = []Attempt{{ID: id, Number: 1, State: Failed, Failure: &Failure{Kind: "format", Message: "invalid character '`'"}}}
			artifact := &Artifact{Sandbox: c.Run.Working}
			// Copy the map so failed worker edits are not accepted by the run.
			data, _ := json.Marshal(artifact)
			var copy Artifact
			json.Unmarshal(data, &copy)
			artifact = &copy
			artifact.Sandbox.Files["index.html"] = sandboxFile(content)
			artifact.Session.Transcript = []map[string]any{{"role": "assistant", "content": "```json\n{\"summary\":\"created game\",\"artifacts\":[\"index.html\"],\"unresolved\":[]}\n```"}}
			if err := c.Store.SaveArtifact(c.Run.ID, id, artifact); err != nil {
				t.Fatal(err)
			}
			if err := c.save(); err != nil {
				t.Fatal(err)
			}
			c.Close()
			restored, err := Resume(c.Store, c.Run.ID, c.Options)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err = restored.Retry("create-index-html", ""); err != nil {
				t.Fatal(err)
			}
			err = restored.Execute(context.Background())
			if content == "good" && (err != nil || restored.Run.State != Completed) {
				t.Fatal("saved file not recovered", err)
			}
			if content == "bad" && (err == nil || restored.Run.State != Failed) {
				t.Fatal("report recovery bypassed verification")
			}
			if requests.Load() != 0 {
				t.Fatal("report recovery reran worker", requests.Load())
			}
			if len(restored.Run.Tasks[0].Attempts) != 2 {
				t.Fatal("attempt counter was reset")
			}
		})
	}
}

func sandboxFile(content string) sandbox.StateFile {
	return sandbox.StateFile{Body: []byte(content), Mode: 0644}
}

func TestFailedExecutionCannotUseReportOnlyRetry(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { response(w, "unused") })
	approve(t, c, planFor(fixtureTask("one", "file")))
	c.Run.State = Failed
	c.Run.Tasks[0].State = Failed
	c.Run.Tasks[0].Attempts = []Attempt{{ID: c.Store.NewID(), Number: 1, State: Failed, Failure: &Failure{Kind: "verification", Message: "test failed"}}}
	if err := c.Retry("one", ""); err == nil {
		t.Fatal("failed execution treated as reporting-only retry")
	}
	if c.Run.State != Failed {
		t.Fatal("failed state mutated on rejected retry")
	}
}
