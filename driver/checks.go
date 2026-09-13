package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/belt-sh/harness-test/harness"
	"github.com/belt-sh/harness-test/server"
)

// runChecks runs all verification checks for a phase.
func (r *TestRunner) runChecks(phase string) {
	r.checkHookEvents(phase)
	entries := r.server.Log()
	r.checkAPIRequests(phase, entries)
	r.checkStreamingFormat(phase, entries)
	r.checkModelSelection(phase, entries)
	r.checkInstructions(phase, entries)
	r.checkHookInjection(phase, entries)
}

// checkHookInjection verifies the text the prompt hook emitted reached the
// model, i.e. the agent turns hook stdout into context. In mock mode that is
// the codename this suite made up; in belt mode it is a line belt itself
// printed, captured before the run. Agents without a context channel on that
// event skip rather than fail.
func (r *TestRunner) checkHookInjection(phase string, entries []server.LogEntry) {
	// Mock mode only: the codename is emitted by the same hook run the agent
	// makes, so finding it proves the round trip. belt's own suggestions
	// depend on the prompt matching its corpus, and the codename question
	// matches nothing, so belt mode checks the shape of belt's output
	// instead — see checkBeltHookShape.
	want := r.injectCode
	if want == "" || r.hookSource != HooksMock {
		return
	}
	fmt.Printf("[check] hook injection (%s)\n", phase)
	if !r.promptHookFired() {
		r.skip(fmt.Sprintf("%s: prompt hook did not fire, nothing to inject", phase))
		return
	}
	for _, e := range entries {
		if strings.Contains(string(e.Body), want) {
			r.pass(fmt.Sprintf("%s: prompt hook context reached the model", phase))
			return
		}
	}
	if harness.ContextChannelFor(r.harness.Name, "user-prompt-submit") == harness.ContextNone ||
		harness.ContextChannelFor(r.harness.Name, "user-prompt-submit") == harness.ContextPlugin {
		r.skip(fmt.Sprintf("%s: prompt hook context not seen (no stdout channel: %s)", phase, harness.ContextChannelFor(r.harness.Name, "user-prompt-submit")))
		return
	}
	if note := r.harness.KnownIssues[phase+":prompt-context"]; note != "" {
		r.skip(fmt.Sprintf("%s: prompt hook context not found in any request — known issue: %s", phase, note))
		return
	}
	r.fail(fmt.Sprintf("%s: prompt hook context (%s) not found in any request", phase,
		harness.ContextChannelFor(r.harness.Name, "user-prompt-submit")))
}

// checkInstructions verifies that the codename from each instruction file
// made it into a request to the model, i.e. the agent loaded that file into
// its system prompt at that scope. One check per file.
func (r *TestRunner) checkInstructions(phase string, entries []server.LogEntry) {
	if len(r.instructionCodes) == 0 {
		return
	}
	fmt.Printf("[check] instruction files (%s)\n", phase)
	if len(entries) == 0 {
		// checkAPIRequests already failed this phase; don't repeat it.
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

func (r *TestRunner) checkAPIRequests(phase string, entries []server.LogEntry) {
	fmt.Printf("[check] API requests (%s)\n", phase)

	// No requests at all means the agent never reached the mock, so nothing
	// else this phase checked was actually exercised. Always a failure.
	if len(entries) > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, len(entries)))
	} else {
		r.fail(fmt.Sprintf("%s: mock server received no requests", phase))
	}
}

func (r *TestRunner) checkStreamingFormat(phase string, entries []server.LogEntry) {
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
	if len(entries) == 0 {
		r.skip(fmt.Sprintf("%s: no requests to inspect for streaming", phase))
		return
	}
	if reason, ok := r.harness.KnownIssues[phase+":streaming"]; ok {
		r.skip(fmt.Sprintf("%s: no streaming requests observed — %s", phase, reason))
		return
	}
	r.fail(fmt.Sprintf("%s: no streaming requests observed", phase))
}

func (r *TestRunner) checkModelSelection(phase string, entries []server.LogEntry) {
	if r.harness.DefaultModel == "" {
		return
	}
	fmt.Printf("[check] model selection (%s)\n", phase)

	// Only the request's own model field (as the mock parsed it) or the URL
	// path (gemini puts the model there) counts. Searching the whole body let
	// any mention pass: droid's TUI sent "-m mock-model" as the user's prompt
	// while running Factory's default model, and this check went green.
	accepted := append([]string{r.harness.DefaultModel}, r.harness.AcceptedModels...)
	for _, e := range entries {
		for _, model := range accepted {
			if e.Model == model || pathNamesModel(e.Path, model) {
				r.pass(fmt.Sprintf("%s: model %s in request", phase, model))
				return
			}
		}
	}
	if len(entries) == 0 {
		r.skip(fmt.Sprintf("%s: no requests to inspect for model selection", phase))
		return
	}
	// The agent ignored the model this harness configures, so the run measured
	// some other model: a gap in the harness, not a detail to wave through.
	if reason, ok := r.harness.KnownIssues[phase+":model"]; ok {
		r.skip(fmt.Sprintf("%s: model %s not found in requests — %s", phase, r.harness.DefaultModel, reason))
		return
	}
	r.fail(fmt.Sprintf("%s: model %s not found in requests", phase, r.harness.DefaultModel))
}

// promptHookFired reports whether the mock prompt hook ran in this phase.
func (r *TestRunner) promptHookFired() bool {
	// The two hook sources write different things: the mock's script echoes
	// the tag, belt's own hook logs its event name. Matching only the tag made
	// belt runs report "nothing to inject" for a hook that had just fired.
	marks := []string{TagPrompt}
	if r.hookSource == HooksBelt {
		marks = []string{"[" + beltEventNames[TagPrompt] + "]"}
	}
	if data, err := os.ReadFile(hookLogPath); err == nil {
		for _, m := range marks {
			if strings.Contains(string(data), m) {
				return true
			}
		}
	}
	out := stripANSI(r.lastOutput)
	return strings.Contains(out, "hook: "+r.harness.Events.PromptSubmit) ||
		strings.Contains(out, "[belt:hook] "+beltEventNames[TagPrompt]+" done")
}

// pathNamesModel reports whether a URL path addresses the model as a path
// segment (".../models/<model>:generateContent"), not merely contains it.
func pathNamesModel(path, model string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == model || strings.HasPrefix(seg, model+":") {
			return true
		}
	}
	return false
}
