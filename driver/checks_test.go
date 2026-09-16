package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/belt-sh/harness-test/harness"
	"github.com/belt-sh/harness-test/server"
)

// The checks must be able to fail. Each of these used to skip, and a skip
// cannot turn a badge red.
func TestChecksFailInsteadOfSkip(t *testing.T) {
	entry := func(body string) server.LogEntry {
		return server.LogEntry{Path: "/v1/chat/completions", Body: []byte(body)}
	}
	cases := []struct {
		name    string
		h       harness.Harness
		entries []server.LogEntry
		run     func(r *TestRunner)
		wantF   int
		wantS   int
	}{
		{"no requests fails", harness.All["claude"], nil, func(r *TestRunner) { r.checkAPIRequests("headless", r.entries()) }, 1, 0},
		{"no stream fails", harness.All["claude"], []server.LogEntry{entry(`{"stream":false}`)}, func(r *TestRunner) { r.checkStreamingFormat("headless", r.entries()) }, 1, 0},
		{"a streamed answer passes", harness.All["claude"], []server.LogEntry{{Path: "/v1/messages", Streamed: true}}, func(r *TestRunner) { r.checkStreamingFormat("headless", r.entries()) }, 0, 0},
		{"no stream skips with reason", withIssue(harness.All["claude"], "headless:streaming", "x"), []server.LogEntry{entry(`{"stream":false}`)}, func(r *TestRunner) { r.checkStreamingFormat("headless", r.entries()) }, 0, 1},
		{"wrong model fails", harness.All["claude"], []server.LogEntry{entry(`{"model":"someone-elses-model"}`)}, func(r *TestRunner) { r.checkModelSelection("headless", r.entries()) }, 1, 0},
		{"wrong model skips with reason", withIssue(harness.All["claude"], "headless:model", "x"), []server.LogEntry{entry(`{"model":"someone-elses-model"}`)}, func(r *TestRunner) { r.checkModelSelection("headless", r.entries()) }, 0, 1},
		{"model named only in prompt text fails", withModel(harness.All["claude"], "mock-model"), []server.LogEntry{{Path: "/api/llm/o/v1/responses", Model: "gpt-5.6-sol", Body: []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"-m mock-model"}]}`)}}, func(r *TestRunner) { r.checkModelSelection("interactive", r.entries()) }, 1, 0},
		{"model in gemini path passes", withModel(harness.All["gemini"], "gemini-2.5-flash"), []server.LogEntry{{Path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent", Body: []byte(`{}`)}}, func(r *TestRunner) { r.checkModelSelection("headless", r.entries()) }, 0, 0},
		{"model as a path prefix of another model fails", withModel(harness.All["gemini"], "gemini-2.5"), []server.LogEntry{{Path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent", Body: []byte(`{}`)}}, func(r *TestRunner) { r.checkModelSelection("headless", r.entries()) }, 1, 0},
		{"hook silent fails", harness.All["claude"], nil, func(r *TestRunner) { r.reportEvent("headless", "prompt", TagPrompt, false, "") }, 1, 0},
		{"hook silent skips with reason", withIssue(harness.All["claude"], "headless:event:PROMPT", "x"), nil, func(r *TestRunner) { r.reportEvent("headless", "prompt", TagPrompt, false, "") }, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &TestRunner{harness: c.h, testEntries: c.entries}
			c.run(r)
			if r.result.Failed != c.wantF || r.result.Skipped != c.wantS {
				t.Errorf("failed=%d skipped=%d, want failed=%d skipped=%d", r.result.Failed, r.result.Skipped, c.wantF, c.wantS)
			}
		})
	}
}

func withIssue(h harness.Harness, key, reason string) harness.Harness {
	m := map[string]string{}
	for k, v := range h.KnownIssues {
		m[k] = v
	}
	m[key] = reason
	h.KnownIssues = m
	return h
}

func withModel(h harness.Harness, model string) harness.Harness {
	h.DefaultModel = model
	h.AcceptedModels = nil
	return h
}

// A compaction sends the thread to the model to be condensed; a slash command
// that arrives as an ordinary user message sends one more message than the
// turn before it. The probe used to tell them apart by matching English,
// which had to stay disjoint from its own filler prompts and would have
// reported a reworded agent as one that never compacted.
func TestMessageCountSeparatesCompactionFromAnOrdinaryTurn(t *testing.T) {
	turn := server.LogEntry{Body: []byte(`{"messages":[{"role":"system"},{"role":"user"},{"role":"assistant"}]}`)}
	compaction := server.LogEntry{Body: []byte(`{"messages":[{"role":"system"},{"role":"user"},{"role":"assistant"},{"role":"user"},{"role":"assistant"},{"role":"user"}]}`)}
	ordinary := server.LogEntry{Body: []byte(`{"messages":[{"role":"system"},{"role":"user"}]}`)}

	before := widestRequest([]server.LogEntry{turn})
	if widestRequest([]server.LogEntry{compaction}) < before {
		t.Error("a request carrying the whole thread should read as a compaction")
	}
	if widestRequest([]server.LogEntry{ordinary}) >= before {
		t.Error("a request carrying less than the preceding turn should not read as a compaction")
	}
}

// waitTurnSettled ends early on a stop hook that fired since the mark. The
// mark has to be taken before the prompt goes in, and the marker it counts
// differs by hook source: the mock hooks write the suite's tag, belt writes
// its own event name in brackets.
func TestStopsLoggedCountsTheHookSourcesOwnMarker(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() { os.Remove(hookLogPath) })

	os.WriteFile(hookLogPath, []byte("PROMPT\nSTOP\n"), 0644)
	os.MkdirAll(filepath.Join(home, ".belt"), 0755)
	os.WriteFile(filepath.Join(home, ".belt", "hooks.log"), []byte("[stop] done\n[stop] done\n"), 0644)

	mock := &TestRunner{harness: harness.All["claude"], home: home, hookSource: HooksMock}
	if got := mock.stopsLogged(); got != 1 {
		t.Errorf("mock source: stopsLogged = %d, want 1", got)
	}
	belt := &TestRunner{harness: harness.All["claude"], home: home, hookSource: HooksBelt}
	if got := belt.stopsLogged(); got != 2 {
		t.Errorf("belt source: stopsLogged = %d, want 2", got)
	}

	// An agent with no stop hook has no signal, and must not be reported as
	// "no stop seen yet" — that would end the wait the moment one appeared
	// from something else.
	noStop := harness.All["claude"]
	noStop.Events.Stop = ""
	if got := (&TestRunner{harness: noStop, home: home}).stopsLogged(); got != -1 {
		t.Errorf("no stop hook: stopsLogged = %d, want -1", got)
	}
}
