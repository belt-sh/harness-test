package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/transcript"
	"github.com/inference-sh/agentprotocol/transcript/all"
)

// probeSeedKinds asks which parts of an imported history an agent gives its
// model. The seed probe plants one fact in plain text; this one writes a
// session with a different fact in each kind of content a conversation holds,
// loads it, and reads the requests the agent then sent.
//
// Each kind is reported on its own, next to what the codec says the model
// should be given (Session.Context, read back after the write). A fact the
// codec keeps and the model never sees is the agent dropping it; a fact the
// codec itself leaves out of the context never had a chance.
//
// A missing kind is a finding, not a failure: an agent may drop reasoning
// from history on purpose, and that is worth knowing rather than fixing.
func (r *TestRunner) probeSeedKinds() {
	if r.harness.DriverKind() == "" {
		return
	}
	fmt.Println("[probe] seed kinds (which parts of an imported history reach the model)")
	st, ok, err := all.Open(r.harness.Name, r.agentHome())
	if !ok {
		r.skip("seedkinds:no-codec", fmt.Sprintf("seed kinds: no codec for %s", r.harness.Name))
		return
	}
	if err != nil {
		r.fail("seedkinds.open", fmt.Sprintf("seed kinds: open %s store: %v", r.harness.Name, err))
		return
	}

	facts := newSeedFacts()
	now := time.Now().UTC()
	built := &transcript.Session{
		Agent: r.harness.Name, CWD: r.workDir(), Created: now, Updated: now,
		Entries: r.seedKindsEntries(facts, now),
	}
	id, ok := r.writeSeed(st, "kinds", built)
	if !ok {
		return
	}

	// What the codec says the model will be given, from the store rather
	// than from what was handed to Write: a writer may drop or reshape a
	// block, and that is the codec's doing, not the agent's.
	kept := map[string]bool{}
	if s, err := st.Read(context.Background(), id); err != nil {
		r.fail("seedkinds.read-back", fmt.Sprintf("seed kinds: read back %s session %s: %v", r.harness.Name, id, err))
		return
	} else {
		for _, f := range facts {
			kept[f.kind] = contextHolds(s.Context(), f.fact)
		}
	}

	sent, ok := r.loadSeed("kinds", id)
	if !ok {
		return
	}
	var reached []string
	for _, f := range facts {
		switch {
		case entriesContain(sent, f.fact):
			reached = append(reached, f.kind)
			r.pass("seedkinds."+slug(f.kind), fmt.Sprintf("seed kinds: %s gave the model the %s", r.harness.Name, f.kind))
		case !kept[f.kind]:
			r.finding("seedkinds."+slug(f.kind)+":not-kept", fmt.Sprintf("seed kinds: %s's codec kept no %s in the session's context, so the model could not get it", r.harness.Name, f.kind))
		default:
			r.finding("seedkinds."+slug(f.kind)+":not-given", fmt.Sprintf("seed kinds: %s loaded the %s and did not give it to the model", r.harness.Name, f.kind))
		}
	}
	fmt.Printf("  seed kinds: %s reached the model: %d of %d (%s)\n", r.harness.Name, len(reached), len(facts), strings.Join(reached, ", "))
}

// seedFact is one planted fact and the kind of content that carries it.
type seedFact struct{ kind, fact string }

// The kinds, in the order the session holds them.
const (
	kindUser        = "first user message"
	kindAssistant   = "assistant text"
	kindReasoning   = "reasoning"
	kindToolArgs    = "tool call arguments"
	kindToolResult  = "tool result"
	kindSecondUser  = "second turn's user message"
	kindSecondReply = "second turn's answer"
)

func newSeedFacts() []seedFact {
	var out []seedFact
	for _, k := range []string{kindUser, kindAssistant, kindReasoning, kindToolArgs, kindToolResult, kindSecondUser, kindSecondReply} {
		out = append(out, seedFact{kind: k, fact: "SEED-" + randomHex(4)})
	}
	return out
}

func factFor(facts []seedFact, kind string) string {
	for _, f := range facts {
		if f.kind == kind {
			return f.fact
		}
	}
	return ""
}

// seedKindsEntries is a two-turn conversation: the user asks, the assistant
// reasons, answers and calls a tool, the tool returns, the assistant sums up,
// and a second exchange follows. The tool is the one this agent calls in the
// session phase, so the call is one the agent would have made itself.
func (r *TestRunner) seedKindsEntries(facts []seedFact, at time.Time) []transcript.Entry {
	f := func(k string) string { return factFor(facts, k) }
	name, args := r.seedToolCall(f(kindToolArgs))
	const toolID = "seed-tool-1"
	tick := func(n int) time.Time { return at.Add(time.Duration(n) * time.Second) }
	text := func(s string) transcript.Block { return transcript.Block{Kind: transcript.BlockText, Text: s} }

	return []transcript.Entry{
		{ID: "seed-k1", Role: transcript.RoleUser, Time: tick(0),
			Content: []transcript.Block{text("Remember this for later: the project codename is " + f(kindUser) + ". Then look at the notes file.")}},
		{ID: "seed-k2", ParentID: "seed-k1", Role: transcript.RoleAssistant, Time: tick(1),
			Content: []transcript.Block{
				{Kind: transcript.BlockReasoning, Text: "The user wants the notes read. Their private tag is " + f(kindReasoning) + "."},
				text("Noted. My reference for this task is " + f(kindAssistant) + ". Reading the notes now."),
				{Kind: transcript.BlockToolUse, ToolID: toolID, Name: name, Input: args},
			}},
		{ID: "seed-k3", ParentID: "seed-k2", Role: transcript.RoleTool, Time: tick(2),
			Content: []transcript.Block{{Kind: transcript.BlockToolResult, ToolID: toolID, Name: name, Status: transcript.StatusOK,
				Text: "notes: the deploy key is " + f(kindToolResult)}}},
		{ID: "seed-k4", ParentID: "seed-k3", Role: transcript.RoleAssistant, Time: tick(3),
			Content: []transcript.Block{text("I read the notes.")}},
		{ID: "seed-k5", ParentID: "seed-k4", Role: transcript.RoleUser, Time: tick(4),
			Content: []transcript.Block{text("One more thing: the release name is " + f(kindSecondUser) + ".")}},
		{ID: "seed-k6", ParentID: "seed-k5", Role: transcript.RoleAssistant, Time: tick(5),
			Content: []transcript.Block{text("Understood, the release ticket is " + f(kindSecondReply) + ".")}},
	}
}

// seedToolCall is the agent's own session-phase tool with the fact added to
// every string argument, at any depth, so the fact sits in the arguments
// whatever the tool's schema calls them (kiro's read nests them:
// {"operations":[{"path":...}]}). Arguments with no string at all get the
// fact under a key of its own, so the tool call always carries it.
func (r *TestRunner) seedToolCall(fact string) (string, json.RawMessage) {
	name, args := r.harness.ToolCallName, r.harness.ToolCallArgs
	if o, ok := r.harness.ToolCallByMode[ModeACP]; ok {
		name, args = o.Name, o.Args
	}
	if name == "" {
		name, args = server.DefaultToolName, server.DefaultToolArgs
	}
	var v any
	if json.Unmarshal([]byte(r.expand(args)), &v) != nil {
		v = map[string]any{}
	}
	v, planted := plantInStrings(v, fact)
	if !planted {
		m, ok := v.(map[string]any)
		if !ok {
			m = map[string]any{}
		}
		m["note"] = fact
		v = m
	}
	out, _ := json.Marshal(v)
	return name, out
}

// plantInStrings appends fact to every string in v and reports whether there
// was one.
func plantInStrings(v any, fact string) (any, bool) {
	switch x := v.(type) {
	case string:
		return x + "-" + fact, true
	case map[string]any:
		planted := false
		for k, e := range x {
			var p bool
			x[k], p = plantInStrings(e, fact)
			planted = planted || p
		}
		return x, planted
	case []any:
		planted := false
		for i, e := range x {
			var p bool
			x[i], p = plantInStrings(e, fact)
			planted = planted || p
		}
		return x, planted
	}
	return v, false
}

// contextHolds reports whether any block of the context carries s, in text,
// reasoning, a tool result or a tool call's arguments.
func contextHolds(ctx []transcript.Entry, s string) bool {
	for _, e := range ctx {
		for _, b := range e.Content {
			if strings.Contains(b.Text, s) || strings.Contains(string(b.Input), s) {
				return true
			}
		}
	}
	return false
}
