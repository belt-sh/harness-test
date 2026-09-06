package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/belt-sh/harness-test/server"
)

// runChecks runs all verification checks for a phase.
func (r *Runner) runChecks(phase string) {
	r.checkHookEvents(phase)
	entries := r.server.Log()
	r.checkAPIRequests(phase, entries)
	r.checkStreamingFormat(phase, entries)
	r.checkModelSelection(phase, entries)
	r.checkInstructions(phase, entries)
}

// checkInstructions verifies that the codename from each instruction file
// made it into a request to the model, i.e. the agent loaded that file into
// its system prompt at that scope. One check per file.
func (r *Runner) checkInstructions(phase string, entries []server.LogEntry) {
	if len(r.instructionCodes) == 0 {
		return
	}
	fmt.Printf("[check] instruction files (%s)\n", phase)
	if len(entries) == 0 {
		r.skip(fmt.Sprintf("%s: no requests to inspect for instruction files", phase))
		return
	}
	names := make([]string, 0, len(r.instructionCodes))
	for name := range r.instructionCodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		code := r.instructionCodes[name]
		found := false
		for _, e := range entries {
			if strings.Contains(string(e.Body), code) {
				found = true
				break
			}
		}
		if found {
			r.pass(fmt.Sprintf("%s: %s loaded into context", phase, name))
		} else {
			r.fail(fmt.Sprintf("%s: %s not found in any request", phase, name))
			if os.Getenv("HARNESS_DEBUG") != "" {
				dump := fmt.Sprintf("/tmp/harness-%s-%s-requests.json", r.harness.Name, phase)
				if data, err := json.MarshalIndent(entries, "", "  "); err == nil {
					os.WriteFile(dump, data, 0644)
					fmt.Printf("    [debug] request bodies written to %s\n", dump)
				}
			}
		}
	}
}

func (r *Runner) checkAPIRequests(phase string, entries []server.LogEntry) {
	fmt.Printf("[check] API requests (%s)\n", phase)

	if len(entries) > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, len(entries)))
	} else {
		r.skip(fmt.Sprintf("%s: mock server received no requests", phase))
	}
}

func (r *Runner) checkStreamingFormat(phase string, entries []server.LogEntry) {
	fmt.Printf("[check] streaming (%s)\n", phase)

	for _, e := range entries {
		// OpenAI/Anthropic: "stream": true in body
		var req map[string]any
		if json.Unmarshal(e.Body, &req) == nil {
			if stream, ok := req["stream"].(bool); ok && stream {
				r.pass(fmt.Sprintf("%s: streaming enabled in request", phase))
				return
			}
		}
		// Cursor: connect server-stream
		if strings.HasSuffix(e.Path, "/RunSSE") {
			r.pass(fmt.Sprintf("%s: streaming enabled in request", phase))
			return
		}
		// Gemini: ?alt=sse in URL path
		if strings.Contains(e.Path, "alt=sse") || strings.Contains(e.Path, "streamGenerateContent") {
			r.pass(fmt.Sprintf("%s: streaming enabled in request", phase))
			return
		}
		// Bedrock/Q: converse-stream, invoke-with-response-stream, or GenerateAssistantResponse
		if strings.Contains(e.Path, "converse-stream") || strings.Contains(e.Path, "invoke-with-response-stream") ||
			strings.Contains(string(e.Headers["x-amz-target"]), "GenerateAssistantResponse") {
			r.pass(fmt.Sprintf("%s: streaming enabled in request", phase))
			return
		}
	}
	r.skip(fmt.Sprintf("%s: no streaming requests observed", phase))
}

func (r *Runner) checkModelSelection(phase string, entries []server.LogEntry) {
	if r.harness.DefaultModel == "" {
		return
	}
	fmt.Printf("[check] model selection (%s)\n", phase)

	accepted := append([]string{r.harness.DefaultModel}, r.harness.AcceptedModels...)
	for _, e := range entries {
		body := string(e.Body)
		for _, model := range accepted {
			if strings.Contains(body, model) || strings.Contains(e.Path, model) || e.Model == model {
				r.pass(fmt.Sprintf("%s: model %s in request", phase, model))
				return
			}
		}
	}
	r.skip(fmt.Sprintf("%s: model %s not found in requests", phase, r.harness.DefaultModel))
}
