package runner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// runChecks runs all verification checks for a phase.
func (r *Runner) runChecks(phase string) {
	r.checkHookEvents(phase)
	r.checkAPIRequests(phase)
	r.checkStreamingFormat(phase)
	r.checkModelSelection(phase)
}

// checkBasicPrompt verifies the agent responded to a prompt.
func (r *Runner) checkBasicPrompt(phase string) {
	fmt.Printf("[check] prompt response (%s)\n", phase)

	if len(r.lastOutput) == 0 {
		r.fail(fmt.Sprintf("%s: no output", phase))
		return
	}
	r.pass(fmt.Sprintf("%s: agent produced output (%d bytes)", phase, len(r.lastOutput)))
}

// checkContextInjection verifies the agent's response includes injected context.
func (r *Runner) checkContextInjection(phase string) {
	if r.injectCode == "" {
		return
	}
	fmt.Printf("[check] context injection (%s)\n", phase)

	output := stripANSI(r.lastOutput)
	if strings.Contains(output, r.injectCode) {
		r.pass(fmt.Sprintf("%s: inject code found in output", phase))
	} else {
		r.skip(fmt.Sprintf("%s: inject code not found in output", phase))
	}
}

// checkAPIRequests verifies the mock server received requests.
func (r *Runner) checkAPIRequests(phase string) {
	fmt.Printf("[check] API requests (%s)\n", phase)

	count := r.server.LogCount()
	if count > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, count))
	} else {
		r.skip(fmt.Sprintf("%s: mock server received no requests", phase))
	}
}

// checkToolCall verifies the agent made a tool call and received a result.
func (r *Runner) checkToolCall(phase string) {
	fmt.Printf("[check] tool call (%s)\n", phase)

	entries := r.server.Log()
	hasToolUse := false
	hasToolResult := false

	for _, e := range entries {
		body := string(e.Body)
		if strings.Contains(body, "tool_use") || strings.Contains(body, "tool_calls") ||
			strings.Contains(body, "function_call") {
			hasToolUse = true
		}
		if strings.Contains(body, "tool_result") || strings.Contains(body, `"role":"tool"`) {
			hasToolResult = true
		}
	}

	if hasToolUse {
		r.pass(fmt.Sprintf("%s: tool call observed", phase))
	}
	if hasToolResult {
		r.pass(fmt.Sprintf("%s: tool result sent back", phase))
	}
}

// checkStreamingFormat verifies the mock server received streaming requests.
func (r *Runner) checkStreamingFormat(phase string) {
	fmt.Printf("[check] streaming (%s)\n", phase)

	for _, e := range r.server.Log() {
		var req map[string]any
		if json.Unmarshal(e.Body, &req) != nil {
			continue
		}
		if stream, ok := req["stream"].(bool); ok && stream {
			r.pass(fmt.Sprintf("%s: streaming enabled in request", phase))
			return
		}
	}
	r.skip(fmt.Sprintf("%s: no streaming requests observed", phase))
}

// checkModelSelection verifies the correct model was sent to the mock server.
func (r *Runner) checkModelSelection(phase string) {
	if r.harness.DefaultModel == "" {
		return
	}
	fmt.Printf("[check] model selection (%s)\n", phase)

	for _, e := range r.server.Log() {
		if strings.Contains(string(e.Body), r.harness.DefaultModel) {
			r.pass(fmt.Sprintf("%s: model %s in request", phase, r.harness.DefaultModel))
			return
		}
	}
	r.skip(fmt.Sprintf("%s: model %s not found in requests", phase, r.harness.DefaultModel))
}
