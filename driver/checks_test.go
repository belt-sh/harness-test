package driver

import (
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
