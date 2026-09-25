package runner

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/transcript"
)

// The seed kinds session also carries what only another agent's session
// would: tool calls under other agents' tool names, a compaction in the form
// Session.Portable gives a writer, and an image returned by a tool. The
// session is written as a foreign one (Agent is not the target), so the
// writer takes it through Portable and Lower(caps) exactly as it takes an
// import.
//
// An undone turn is not seeded: Portable drops every AudienceNone entry
// before any writer sees one (agentprotocol TestPortable, TestAudience), so
// there is nothing a writer could do with it.

// seedAgent is the session's Agent: not any target, so every writer lowers it.
const seedAgent = "harness-test"

// foreignTool is a tool call as another agent records it. args has one %s,
// where the call's fact goes.
type foreignTool struct {
	kind, name, args string
}

// foreignTools are calls no target declares under that name and shape: two
// of claude's, codex's freeform apply_patch (its input is the patch text, a
// JSON string) and its shell, gemini's write, and a nested-argument read
// (kiro's shape).
var foreignTools = []foreignTool{
	{"claude Edit", "Edit", `{"file_path":"notes.md","old_string":"draft %s","new_string":"final"}`},
	{"claude Read", "Read", `{"file_path":"docs/%s.md"}`},
	{"codex apply_patch", "apply_patch", `"*** Begin Patch\n*** Update File: notes.md\n@@\n-draft\n+final %s\n*** End Patch\n"`},
	{"codex exec_command", "exec_command", `{"cmd":"grep -rn %s .","workdir":"."}`},
	{"gemini write_file", "write_file", `{"file_path":"out.txt","content":"release %s"}`},
	{"nested arguments", "read", `{"operations":[{"mode":"Line","path":"src/%s.go","start_line":1}]}`},
}

// seedPNG is a 1x1 PNG, the image a tool returns.
var seedPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC")

// foreignSeed is everything the foreign part of the session planted.
type foreignSeed struct {
	retiredUser, retiredReply string // before the compaction, retired by it
	keptUser, keptReply       string // before it, kept (Compaction.Keep)
	shownOnly                 string // before it, shown and never sent
	summary                   string // the compaction's summary
	imageResult               string // the text of the result carrying the image
	tools                     []foreignCall
}

type foreignCall struct {
	foreignTool
	fact, resultFact string
}

func newForeignSeed() foreignSeed {
	f := foreignSeed{
		retiredUser: "SEED-" + randomHex(4), retiredReply: "SEED-" + randomHex(4),
		keptUser: "SEED-" + randomHex(4), keptReply: "SEED-" + randomHex(4),
		shownOnly: "SEED-" + randomHex(4), summary: "SEED-" + randomHex(4),
		imageResult: "SEED-" + randomHex(4),
	}
	for _, t := range foreignTools {
		f.tools = append(f.tools, foreignCall{t, "SEED-" + randomHex(4), "SEED-" + randomHex(4)})
	}
	return f
}

// compactedHistory is what comes before the conversation: two turns, the
// first retired by a compaction and the second kept, a shown-only entry,
// and the compaction marker as Portable builds it: an opaque entry (no role,
// no content) whose Summary holds entries for everyone and whose Keep names
// the first kept entry.
func (f foreignSeed) compactedHistory(at time.Time) []transcript.Entry {
	text := func(s string) []transcript.Block { return []transcript.Block{{Kind: transcript.BlockText, Text: s}} }
	tick := func(n int) time.Time { return at.Add(time.Duration(n) * time.Second) }
	return []transcript.Entry{
		{ID: "seed-r1", Role: transcript.RoleUser, Time: tick(0), Content: text("Before we start: the archive label is " + f.retiredUser + ".")},
		{ID: "seed-r2", Role: transcript.RoleAssistant, Time: tick(1), Content: text("Filed under " + f.retiredReply + ".")},
		{ID: "seed-p1", Role: transcript.RoleUser, Time: tick(2), Content: text("Keep this in mind: the branch is " + f.keptUser + ".")},
		{ID: "seed-p2", Role: transcript.RoleAssistant, Time: tick(3), Content: text("Branch noted as " + f.keptReply + ".")},
		{ID: "seed-p3", Role: transcript.RoleUser, Time: tick(4), Audience: transcript.AudienceUser, Content: text("$ ls\nlocal output " + f.shownOnly)},
		{ID: "seed-c1", Time: tick(5), Compaction: &transcript.Compaction{
			Summary: []transcript.Entry{{ID: "seed-c1-summary", Role: transcript.RoleUser, Time: tick(5),
				Content: text("Summary of the conversation so far: the archive was labelled and filed; the summary tag is " + f.summary + ".")}},
			Keep: "seed-p1",
		}},
	}
}

// foreignTurns are the other agents' tool calls, each answered, then the
// agent's own tool returning an image.
func (f foreignSeed) foreignTurns(at time.Time, ownTool string, ownArgs json.RawMessage) []transcript.Entry {
	var out []transcript.Entry
	for i, t := range f.tools {
		id := fmt.Sprintf("call_seed_%d", i+1)
		tm := at.Add(time.Duration(2*i) * time.Second)
		out = append(out,
			transcript.Entry{ID: "seed-f" + fmt.Sprint(2*i+1), Role: transcript.RoleAssistant, Time: tm,
				Content: []transcript.Block{{Kind: transcript.BlockToolUse, ToolID: id, Name: t.name, Input: json.RawMessage(fmt.Sprintf(t.args, t.fact))}}},
			transcript.Entry{ID: "seed-f" + fmt.Sprint(2*i+2), Role: transcript.RoleTool, Time: tm.Add(time.Second),
				Content: []transcript.Block{{Kind: transcript.BlockToolResult, ToolID: id, Name: t.name, Status: transcript.StatusOK, Text: "done: " + t.resultFact}}},
		)
	}
	tm := at.Add(time.Duration(2*len(f.tools)) * time.Second)
	out = append(out,
		transcript.Entry{ID: "seed-i1", Role: transcript.RoleAssistant, Time: tm,
			Content: []transcript.Block{{Kind: transcript.BlockToolUse, ToolID: "call_seed_image", Name: ownTool, Input: ownArgs}}},
		transcript.Entry{ID: "seed-i2", Role: transcript.RoleTool, Time: tm.Add(time.Second),
			Content: []transcript.Block{
				{Kind: transcript.BlockToolResult, ToolID: "call_seed_image", Name: ownTool, Status: transcript.StatusOK, Text: "screenshot " + f.imageResult},
				{Kind: transcript.BlockImage, ToolID: "call_seed_image", MediaType: "image/png", Data: seedPNG},
			}},
	)
	return out
}

// reportForeignCodec is what the target's codec made of the foreign part,
// read back after the write: the compaction's context and history.
func (r *TestRunner) reportForeignCodec(f foreignSeed, back *transcript.Session) {
	ctx, lin := back.Context(), back.Linearize()
	compacted := false
	for _, e := range back.Entries {
		compacted = compacted || e.Compaction != nil
	}
	has := func(es []transcript.Entry, s string) bool { return contextHolds(es, s) }

	// (a) Context: the summary and the kept turn, and nothing retired.
	switch {
	case has(ctx, f.retiredUser) || has(ctx, f.retiredReply):
		r.finding("seedkinds.compaction.context:retired-kept", fmt.Sprintf("seed kinds: %s's Context() after the write still holds the retired turn", r.harness.Name))
	case has(ctx, f.shownOnly):
		r.finding("seedkinds.compaction.context:shown-only-kept", fmt.Sprintf("seed kinds: %s's Context() holds the shown-only entry", r.harness.Name))
	case !has(ctx, f.summary):
		r.finding("seedkinds.compaction.context:no-summary", fmt.Sprintf("seed kinds: %s's Context() after the write has no compaction summary", r.harness.Name))
	case !has(ctx, f.keptUser) || !has(ctx, f.keptReply):
		r.finding("seedkinds.compaction.context:no-kept", fmt.Sprintf("seed kinds: %s's Context() after the write lost the turn the compaction kept (user %v, answer %v)",
			r.harness.Name, has(ctx, f.keptUser), has(ctx, f.keptReply)))
	default:
		r.pass("seedkinds.compaction.context", fmt.Sprintf("seed kinds: %s's Context() holds the summary and the kept turn, and no retired turn", r.harness.Name))
	}

	// (b) Linearize: the retired turn stays as history.
	u, a := has(lin, f.retiredUser), has(lin, f.retiredReply)
	shown := "the shown-only entry is not kept"
	if has(lin, f.shownOnly) {
		shown = "the shown-only entry is kept"
	}
	switch {
	case u && a:
		r.pass("seedkinds.compaction.history", fmt.Sprintf("seed kinds: %s's Linearize() keeps the retired turn (%s)", r.harness.Name, shown))
	case !u && !a && !compacted:
		r.finding("seedkinds.compaction.history:summary-only", fmt.Sprintf("seed kinds: %s's session has no compaction and no retired turn: the writer applied the compaction (Lower without Compaction); %s", r.harness.Name, shown))
	case !u && !a:
		r.finding("seedkinds.compaction.history:not-kept", fmt.Sprintf("seed kinds: %s recorded the compaction and its Linearize() lost the retired turn; %s", r.harness.Name, shown))
	default:
		r.finding("seedkinds.compaction.history:partly", fmt.Sprintf("seed kinds: %s's Linearize() keeps part of the retired turn (user %v, answer %v); %s", r.harness.Name, u, a, shown))
	}
}

// firstTurnRequest is the first request carrying the prompt: the turn's own,
// where the history goes (agents send title and routing requests beside
// it). cursor's history does not travel in that request: the mock fetches
// each message blob the client names and logs it on its own, so for cursor
// every request after the prompt is read.
func firstTurnRequest(sent []server.LogEntry, prompt string) []server.LogEntry {
	for _, e := range sent {
		if strings.Contains(e.Path, "agent.v1.") {
			return sent
		}
	}
	for i, e := range sent {
		if bytes.Contains(e.Body, []byte(prompt)) {
			return sent[i : i+1]
		}
	}
	return nil
}

// reportForeignRequest is what reached the model. req is the first turn
// request; nil when none carried the prompt, which is what "rejected" means
// here: the agent loaded the session and sent the model nothing with it.
func (r *TestRunner) reportForeignRequest(f foreignSeed, back *transcript.Session, req []server.LogEntry) {
	name := r.harness.Name
	var ctx []transcript.Entry
	if back != nil {
		ctx = back.Context()
	}
	if req == nil {
		msg := fmt.Sprintf("seed kinds: %s loaded the session and no request carried the prompt", name)
		r.finding("seedkinds.compaction.request:rejected", msg)
		r.finding("seedkinds.shown-only:rejected", msg)
		r.finding("seedkinds.tool-image:rejected", msg)
		for _, t := range f.tools {
			label := t.kind
			r.finding("seedkinds.foreign."+slug(label)+":rejected", msg)
		}
		return
	}

	// (c) the request: summary and kept turn, nothing retired.
	sent := func(s string) bool { return entriesContain(req, s) }
	switch {
	case sent(f.retiredUser) || sent(f.retiredReply):
		r.finding("seedkinds.compaction.request:retired-sent", fmt.Sprintf("seed kinds: %s sent the model the retired turn", name))
	case !sent(f.summary):
		r.finding("seedkinds.compaction.request:no-summary", fmt.Sprintf("seed kinds: %s did not send the compaction summary (the codec kept it: %v)", name, contextHolds(ctx, f.summary)))
	case !sent(f.keptUser) || !sent(f.keptReply):
		r.finding("seedkinds.compaction.request:no-kept", fmt.Sprintf("seed kinds: %s did not send the kept turn (user %v, answer %v)", name, sent(f.keptUser), sent(f.keptReply)))
	default:
		r.pass("seedkinds.compaction.request", fmt.Sprintf("seed kinds: %s sent the summary and the kept turn, and no retired turn", name))
	}
	if sent(f.shownOnly) {
		r.finding("seedkinds.shown-only:sent", fmt.Sprintf("seed kinds: %s sent the model an entry only the person was shown", name))
	} else {
		r.pass("seedkinds.shown-only", fmt.Sprintf("seed kinds: %s did not send the shown-only entry", name))
	}

	// The image a tool returned.
	b64 := base64.StdEncoding.EncodeToString(seedPNG)
	kept := false
	for _, e := range ctx {
		for _, b := range e.Content {
			kept = kept || (b.Kind == transcript.BlockImage && (len(b.Data) > 0 || b.URI != ""))
		}
	}
	result := sent(f.imageResult)
	switch {
	case entriesContain(req, b64):
		r.pass("seedkinds.tool-image:as-image", fmt.Sprintf("seed kinds: %s sent the tool's image as image data (result text sent: %v)", name, result))
	case !kept:
		r.finding("seedkinds.tool-image:not-kept", fmt.Sprintf("seed kinds: %s's codec kept no image in the session's context (result text sent: %v)", name, result))
	case entriesContain(req, ".png") || entriesContain(req, "file://"):
		r.pass("seedkinds.tool-image:as-reference", fmt.Sprintf("seed kinds: %s sent a reference to the tool's image, not its data (result text sent: %v)", name, result))
	default:
		r.finding("seedkinds.tool-image:not-given", fmt.Sprintf("seed kinds: %s's codec kept the image and the agent did not send it (result text sent: %v)", name, result))
	}

	// Other agents' tool calls.
	for _, t := range f.tools {
		label := t.kind
		form, renamedTo := foreignForm(req, t.name, t.fact)
		how := formPhrase(form)
		if form == "renamed" {
			how = fmt.Sprintf("as a tool call named %q", renamedTo)
		}
		msg := fmt.Sprintf("seed kinds: %s gave the model %s (%s) %s; its result %s", name, label, t.name, how, sentPhrase(sent(t.resultFact)))
		switch {
		case form == "tool":
			r.pass("seedkinds.foreign."+slug(label)+":as-tool", msg)
		case form == "renamed":
			r.pass("seedkinds.foreign."+slug(label)+":as-tool-renamed", msg)
		case form == "text":
			r.pass("seedkinds.foreign."+slug(label)+":as-text", msg)
		case !contextHolds(ctx, t.fact):
			r.finding("seedkinds.foreign."+slug(label)+":not-kept", fmt.Sprintf("seed kinds: %s's codec kept no %s call in the session's context; its result %s", name, label, sentPhrase(sent(t.resultFact))))
		default:
			r.finding("seedkinds.foreign."+slug(label)+":dropped", msg)
		}
	}
}

func formPhrase(form string) string {
	switch form {
	case "tool":
		return "as a tool call"
	case "renamed":
		return "as a tool call under another name"
	case "text":
		return "as text"
	}
	return "not at all"
}

func sentPhrase(sent bool) string {
	if sent {
		return "was sent"
	}
	return "was not sent"
}

// foreignForm says how a tool call carrying fact in its arguments reached
// the model: "tool", a structured call under its own name (anthropic
// tool_use, chat tool_calls[].function, responses function_call or
// custom_tool_call, gemini functionCall, cursor's tool-call parts);
// "renamed", a structured call under another name, which is returned; "text", only inside a
// message string; "" not at all. The declarations are removed first, and a
// call counts only if its arguments carry the fact, since the target may
// have a tool of the same name.
func foreignForm(req []server.LogEntry, name, fact string) (string, string) {
	best, renamedTo := "", ""
	rank := map[string]int{"": 0, "text": 1, "renamed": 2, "tool": 3}
	for _, e := range req {
		var v any
		if json.Unmarshal(e.Body, &v) != nil {
			if bytes.Contains(e.Body, []byte(fact)) && rank["text"] > rank[best] {
				best = "text"
			}
			continue
		}
		if m, ok := v.(map[string]any); ok {
			for _, k := range []string{"tools", "functions", "tool_choice", "toolConfig", "tool_config"} {
				delete(m, k)
			}
		}
		form := ""
		note := func(f string) {
			if rank[f] > rank[form] {
				form = f
			}
		}
		call := func(n, args string) {
			if !strings.Contains(args, fact) {
				return
			}
			if n == name {
				note("tool")
			} else {
				note("renamed")
				renamedTo = n
			}
		}
		var onObj func(map[string]any)
		var onStr func(string)
		onObj = func(o map[string]any) {
			if fn, ok := o["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok {
					call(n, jsonText(fn["arguments"]))
				}
			}
			if fc, ok := o["functionCall"].(map[string]any); ok {
				if n, ok := fc["name"].(string); ok {
					call(n, jsonText(fc["args"]))
				}
			}
			for _, nk := range []string{"name", "toolName"} {
				n, ok := o[nk].(string)
				if !ok {
					continue
				}
				for _, k := range []string{"input", "arguments", "args"} {
					if a, ok := o[k]; ok {
						call(n, jsonText(a))
						break
					}
				}
			}
		}
		onStr = func(s string) {
			if strings.Contains(s, fact) {
				note("text")
			}
			// JSON inside a string: cursor's history blobs, arguments
			// encoded as a string.
			if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				var inner any
				if json.Unmarshal([]byte(t), &inner) == nil {
					walkJSON(inner, onObj, onStr)
				}
			}
		}
		walkJSON(v, onObj, onStr)
		// A structured call's arguments are also strings the walk saw, so
		// "text" is only the answer when no call carried the fact.
		if rank[form] > rank[best] {
			best = form
		}
	}
	return best, renamedTo
}

func jsonText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func walkJSON(v any, obj func(map[string]any), str func(string)) {
	switch x := v.(type) {
	case map[string]any:
		obj(x)
		for _, e := range x {
			walkJSON(e, obj, str)
		}
	case []any:
		for _, e := range x {
			walkJSON(e, obj, str)
		}
	case string:
		str(x)
	}
}
