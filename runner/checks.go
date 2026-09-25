package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/harness"
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
	// Belt runs only: reports once, on the first phase that reaches it.
	r.checkBeltHookShape()
	r.dumpDeclaredTools(phase)
	if r.probes.DumpTools {
		r.probeCursorTools(phase)
	}
}

// dumpDeclaredTools prints what the agent offered the model in this phase.
// It answers why a prepared tool call was never served: the mock only serves
// it to a request that declares that tool, so a name this list does not
// contain can never fire the tool hooks.
func (r *TestRunner) dumpDeclaredTools(phase string) {
	if !r.probes.DumpTools || r.server == nil {
		return
	}
	fmt.Printf("  [tools] %s/%s wanted %q, declared: %s\n",
		r.harness.Name, phase, r.toolMatcher(), strings.Join(r.server.DeclaredTools(), " "))
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
		r.skip("hook-injection:no-prompt-hook", fmt.Sprintf("%s: prompt hook did not fire, nothing to inject", phase))
		return
	}
	wantBytes := []byte(want)
	for _, e := range entries {
		if bytes.Contains(e.Body, wantBytes) {
			r.pass("hook-injection", fmt.Sprintf("%s: prompt hook context reached the model", phase))
			return
		}
	}
	// ContextPlugin used to skip here too, from when those agents had no way
	// to return context at all. They do now — the generated plugin file reads
	// the hook's stdout and hands it to the agent — so a codename that never
	// arrives is a failure, not a trait. While it skipped, the plugin agents
	// could inject nothing and the run stayed green.
	channel := harness.ContextChannelFor(r.harness.Name, "user-prompt-submit")
	if channel == harness.ContextNone {
		r.skip("hook-injection:no-channel", fmt.Sprintf("%s: prompt hook context not seen (no stdout channel: %s)", phase, channel))
		return
	}
	if note := r.harness.KnownIssues[phase+":prompt-context"]; note != "" {
		r.skip("hook-injection:known-issue", fmt.Sprintf("%s: prompt hook context not found in any request — known issue: %s", phase, note))
		return
	}
	r.fail("hook-injection", fmt.Sprintf("%s: prompt hook context (%s) not found in any request", phase, channel))
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
		r.skip("instructions:no-requests", fmt.Sprintf("%s: no requests to inspect for instruction files", phase))
		return
	}
	names := make([]string, 0, len(r.instructionCodes))
	for name := range r.instructionCodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		code := []byte(r.instructionCodes[name])
		found := false
		for _, e := range entries {
			if bytes.Contains(e.Body, code) {
				found = true
				break
			}
		}
		if found {
			r.pass("instructions."+name, fmt.Sprintf("%s: %s loaded into context", phase, name))
		} else {
			r.fail("instructions."+name, fmt.Sprintf("%s: %s not found in any request", phase, name))
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
		r.pass("requests", fmt.Sprintf("%s: mock server received %d request(s)", phase, len(entries)))
	} else {
		r.fail("requests", fmt.Sprintf("%s: mock server received no requests", phase))
	}
}

func (r *TestRunner) checkStreamingFormat(phase string, entries []server.LogEntry) {
	fmt.Printf("[check] streaming (%s)\n", phase)

	// The mock records whether it answered with a stream, because it is the
	// only thing that knows. This used to be five per-protocol guesses at
	// paths and headers, tried against every entry regardless of the agent's
	// API format, and one of them — a query-string match on a field that only
	// ever holds a path — could not fire at all.
	for _, e := range entries {
		if e.Streamed {
			r.pass("streaming", fmt.Sprintf("%s: streaming enabled in request", phase))
			return
		}
	}
	if len(entries) == 0 {
		r.skip("streaming:no-requests", fmt.Sprintf("%s: no requests to inspect for streaming", phase))
		return
	}
	if reason, ok := r.harness.KnownIssues[phase+":streaming"]; ok {
		r.skip("streaming:known-issue", fmt.Sprintf("%s: no streaming requests observed — %s", phase, reason))
		return
	}
	r.fail("streaming", fmt.Sprintf("%s: no streaming requests observed", phase))
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
				r.pass("model", fmt.Sprintf("%s: model %s in request", phase, model))
				return
			}
		}
	}
	if len(entries) == 0 {
		r.skip("model:no-requests", fmt.Sprintf("%s: no requests to inspect for model selection", phase))
		return
	}
	// The agent ignored the model this harness configures, so the run measured
	// some other model: a gap in the harness, not a detail to wave through.
	if reason, ok := r.harness.KnownIssues[phase+":model"]; ok {
		r.skip("model:known-issue", fmt.Sprintf("%s: model %s not found in requests — %s", phase, r.harness.DefaultModel, reason))
		return
	}
	r.fail("model", fmt.Sprintf("%s: model %s not found in requests", phase, r.harness.DefaultModel))
}

// promptHookFired reports whether the mock prompt hook ran in this phase. Its
// only caller is the injection check, which is mock-only, so it matches what
// the mock's script writes.
func (r *TestRunner) promptHookFired() bool {
	if data, err := os.ReadFile(hookLogPath); err == nil && strings.Contains(string(data), TagPrompt) {
		return true
	}
	return strings.Contains(r.strippedOutput(), "hook: "+r.harness.Events.PromptSubmit)
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

// checkBeltHookShape verifies belt hands the agent the shape that agent's
// channel takes, and hands it text rather than another envelope.
//
// `belt suggest --json` printed a hook envelope and belt's hook shaped one
// again around it, so every JSON-channel agent received an additionalContext
// whose value was a second envelope, and the plain-stdout agents (kimi, kiro)
// were given raw JSON as their context. Both still passed every hook check,
// because a hook firing says nothing about what it hands over.
func (r *TestRunner) checkBeltHookShape() {
	if r.beltProbe == nil {
		return
	}
	probe := <-r.beltProbe
	r.beltProbe = nil

	fmt.Println("[check] belt hook output")
	channel := harness.ContextChannelFor(r.harness.Name, "user-prompt-submit")
	switch {
	case probe.err != nil:
		r.skip("belt-hook-shape:no-output", "belt prompt hook produced no output to inspect: "+probe.err.Error()+" "+probe.stderr)
		return
	case strings.TrimSpace(probe.stdout) == "":
		r.skip("belt-hook-shape:no-suggestions", "belt had no suggestions for the probe prompt, so there is nothing to inspect "+probe.stderr)
		return
	}

	text, ok := harness.HookContextText(r.harness.Name, "user-prompt-submit", probe.stdout)
	if !ok {
		r.fail("belt-hook-shape:unshaped", fmt.Sprintf("belt did not print %s's hook shape (%s): %s",
			r.harness.Name, channel, truncate(strings.TrimSpace(probe.stdout), 100)))
		return
	}
	if _, wrapped := harness.AnyHookContext(text); wrapped {
		r.fail("belt-hook-shape:double-wrapped", fmt.Sprintf("belt wrapped the context twice for %s (%s): the text it handed over is itself an envelope: %s",
			r.harness.Name, channel, truncate(text, 100)))
		return
	}
	r.pass("belt-hook-shape", fmt.Sprintf("belt prompt hook output is shaped for %s (%s)", r.harness.Name, channel))
}
