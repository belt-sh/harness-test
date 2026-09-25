package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/transcript"
)

// The session is what a writer is handed for an import: through Portable,
// the compaction gives the model the summary and the kept turn and never the
// retired one; the person still sees the retired turn and the shown-only
// entry. Every planted fact is in the session once.
func TestSeedKindsSessionShape(t *testing.T) {
	r := &TestRunner{}
	facts, foreign := newSeedFacts(), newForeignSeed()
	s := &transcript.Session{Agent: seedAgent, Entries: r.seedKindsSession(facts, foreign, time.Now())}
	p := s.Portable()
	ctx, lin := p.Context(), p.Linearize()

	for _, fact := range []string{foreign.summary, foreign.keptUser, foreign.keptReply, foreign.imageResult} {
		if !contextHolds(ctx, fact) {
			t.Errorf("portable Context() lacks %s", fact)
		}
	}
	for _, fact := range []string{foreign.retiredUser, foreign.retiredReply, foreign.shownOnly} {
		if contextHolds(ctx, fact) {
			t.Errorf("portable Context() holds %s", fact)
		}
	}
	for _, fact := range []string{foreign.retiredUser, foreign.retiredReply, foreign.shownOnly} {
		if !contextHolds(lin, fact) {
			t.Errorf("portable Linearize() lacks %s", fact)
		}
	}
	for _, f := range facts {
		if !contextHolds(ctx, f.fact) {
			t.Errorf("portable Context() lacks the %s", f.kind)
		}
	}
	for _, c := range foreign.tools {
		if !contextHolds(ctx, c.fact) || !contextHolds(ctx, c.resultFact) {
			t.Errorf("portable Context() lacks the %s call or result", c.kind)
		}
	}

	all, _ := json.Marshal(s.Entries)
	planted := []string{foreign.retiredUser, foreign.retiredReply, foreign.keptUser, foreign.keptReply, foreign.shownOnly, foreign.summary, foreign.imageResult}
	for _, c := range foreign.tools {
		planted = append(planted, c.fact, c.resultFact)
	}
	for _, f := range facts {
		planted = append(planted, f.fact)
	}
	for _, fact := range planted {
		if n := strings.Count(string(all), fact); n != 1 {
			t.Errorf("%s planted %d times", fact, n)
		}
	}
	for i := 1; i < len(s.Entries); i++ {
		if !s.Entries[i].Time.After(s.Entries[i-1].Time) {
			t.Errorf("entry %d is not later than the one before", i)
		}
	}
}

func TestForeignForm(t *testing.T) {
	const fact = "SEED-ABCD1234"
	req := func(body string) []server.LogEntry { return []server.LogEntry{{Body: json.RawMessage(body)}} }
	blob, _ := json.Marshal(map[string]any{"history_blob": `{"role":"assistant","content":[{"type":"tool-call","toolName":"Edit","args":{"old_string":"draft SEED-ABCD1234"}}]}`})
	for _, c := range []struct{ name, body, want string }{
		{"anthropic", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"Edit","input":{"old_string":"draft SEED-ABCD1234"}}]}]}`, "tool"},
		{"chat", `{"messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"Edit","arguments":"{\"old_string\":\"draft SEED-ABCD1234\"}"}}]}]}`, "tool"},
		{"responses custom", `{"input":[{"type":"custom_tool_call","name":"Edit","input":"draft SEED-ABCD1234"}]}`, "tool"},
		{"gemini", `{"contents":[{"parts":[{"functionCall":{"name":"Edit","args":{"old_string":"draft SEED-ABCD1234"}}}]}]}`, "tool"},
		{"cursor blob", string(blob), "tool"},
		{"renamed", `{"input":[{"type":"function_call","name":"edit","arguments":"{\"old\":\"draft SEED-ABCD1234\"}"}]}`, "renamed"},
		{"text", `{"messages":[{"role":"assistant","content":"[called Edit with draft SEED-ABCD1234]"}]}`, "text"},
		{"same name, other call", `{"tools":[{"name":"Edit"}],"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"x":"y"}}]}]}`, ""},
	} {
		if got, _ := foreignForm(req(c.body), "Edit", fact); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
