package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/transcript"
	"github.com/inference-sh/agentprotocol/transcript/all"
)

// The seed shapes (--probe seedkinds=shapes) are sessions of the forms an
// import has broken an agent with, one session per shape, so a shape that
// makes an agent refuse the session costs only its own checks. Each is
// written as a foreign session (Agent harness-test) through Portable and
// Lower(caps), loaded, prompted once, and read from the mock's log.
//
// Every shape reports the same way when the agent never got as far as the
// model: finding:request-failed:<reason> when the load, the prompt or the
// turn failed, finding:rejected when the turn ended and no request carried
// the prompt.

// seedShape is one regression shape.
type seedShape struct {
	name  string // the check id's part after "seedkinds."
	build func(r *TestRunner, cwd string, at time.Time) shapeSession
}

// shapeSession is a built shape: its entries, a file it needs on disk, and
// the report that reads the result.
type shapeSession struct {
	entries []transcript.Entry
	file    string // written before the load and removed after, when set
	content []byte
	report  func(r *TestRunner, run shapeRun)
}

var seedShapes = []seedShape{
	{"in-message-result", shapeInMessageResult},
	{"parallel-same-id", shapeParallelSameID},
	{"user-attachments", shapeUserAttachments},
	{"tool-after-text", shapeToolAfterText},
	{"file-uri", shapeFileURI},
	{"retired-tool", shapeRetiredTool},
	{"image-only-result", shapeImageOnlyResult},
	{"retired-answer-as-summary", shapeRetiredAnswerAsSummary},
	{"untyped-file-path", shapeUntypedFilePath},
	{"trailing-prompt", shapeTrailingPrompt},
	{"trailing-tool-result", shapeTrailingToolResult},
}

func (r *TestRunner) probeSeedShapes() {
	if r.harness.DriverKind() == "" {
		return
	}
	fmt.Println("[probe] seed shapes (import shapes that broke an agent)")
	st, ok, err := all.Open(r.harness.Name, r.agentHome())
	if !ok {
		r.skip("seedkinds.shapes:no-codec", fmt.Sprintf("seed shapes: no codec for %s", r.harness.Name))
		return
	}
	if err != nil {
		r.fail("seedkinds.shapes.open", fmt.Sprintf("seed shapes: open %s store: %v", r.harness.Name, err))
		return
	}
	for _, sh := range seedShapes {
		r.runSeedShape(st, sh)
	}
}

func (r *TestRunner) runSeedShape(st transcript.Store, sh seedShape) {
	cwd := r.workDir()
	now := time.Now().UTC()
	built := sh.build(r, cwd, now)
	if built.file != "" {
		os.WriteFile(built.file, built.content, 0o644)
		defer os.Remove(built.file)
	}
	s := &transcript.Session{Agent: seedAgent, CWD: cwd, Created: now, Updated: now, Entries: built.entries}
	id, ok := r.writeSeed(st, "shape-"+sh.name, s)
	if !ok {
		return
	}
	run := shapeRun{name: sh.name}
	run.back, _ = st.Read(context.Background(), id)
	sent, stage, msg := r.loadShape(id)
	run.req = firstTurnRequest(sent, promptText)
	if stage != "" && run.req == nil {
		run.failed, run.failMsg = failReason(stage, msg), msg
	}
	built.report(r, run)
}

// loadShape loads a written session, prompts once and returns what the agent
// sent the model. stage and msg say where and why it failed, if it did:
// "load", "prompt", "turn" (the agent reported the turn failed) or "exited".
func (r *TestRunner) loadShape(id string) (sent []server.LogEntry, stage, msg string) {
	d := r.resumeSession(id, false, false)
	if err := d.Start(); err != nil {
		d.Close()
		return nil, "load", err.Error()
	}
	defer d.Close()
	logFrom := r.server.LogCount()
	answered := r.server.AnswersServed()
	outFrom := len(d.Output())
	if err := d.SendPrompt(promptText); err != nil {
		return r.server.Log()[logFrom:], "prompt", err.Error()
	}
	// WaitAnswered waits out its whole timeout on a turn that failed before
	// the model answered; a failed turn is an answer here.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if (r.server.AnswersServed() > answered && d.TurnDone()) || !d.Alive() {
			break
		}
		if d.TurnDone() && strings.Contains(d.Output()[outFrom:], "turn failed:") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	sent = r.server.Log()[logFrom:]
	out := d.Output()[outFrom:]
	if _, m, ok := strings.Cut(out, "turn failed: "); ok {
		m, _, _ = strings.Cut(m, "\n")
		return sent, "turn", m
	}
	if !d.Alive() {
		return sent, "exited", lastLine(out)
	}
	return sent, "", ""
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// failReason is a failure's stable name: the error's kind when it is one an
// import has been measured to cause, else the stage it happened at. The
// error itself goes in the message.
func failReason(stage, msg string) string {
	l := strings.ToLower(msg)
	for _, k := range []struct{ sub, reason string }{
		{"url scheme", "url-scheme"},
		{"session not found", "session-not-found"},
		{"displayname", "display-name"},
		{"invalid image", "invalid-image"},
		{"could not process image", "invalid-image"},
	} {
		if strings.Contains(l, k.sub) {
			return k.reason
		}
	}
	return stage
}

// shapeRun is what one shape's load came to.
type shapeRun struct {
	name    string
	back    *transcript.Session // read back after the write; nil when unreadable
	req     []server.LogEntry   // the first turn request; nil when none carried the prompt
	failed  string              // failReason, when the agent failed before a request
	failMsg string
	// codecOnly reads the codec's Context as the request: what the checks
	// would say of an agent that sends everything it is given
	// (TestSeedShapesCodecs).
	codecOnly bool
}

func (s shapeRun) ctx() []transcript.Entry {
	if s.back == nil {
		return nil
	}
	return s.back.Context()
}

// kept reports whether the codec's Context carries x anywhere: text, a
// tool's arguments, an attachment's bytes (as base64), name or URI.
func (s shapeRun) kept(x string) bool {
	b, _ := json.Marshal(s.ctx())
	return bytes.Contains(b, []byte(x))
}

func (s shapeRun) sent(x string) bool {
	if s.codecOnly {
		return s.kept(x)
	}
	return entriesContain(s.req, x)
}

// calls counts the structured tool calls whose arguments carry fact, and
// the times the fact appears as text when there are none.
func (s shapeRun) calls(fact string) (calls, text int) {
	if s.codecOnly {
		for _, e := range s.ctx() {
			for _, b := range e.Content {
				if b.Kind == transcript.BlockToolUse && strings.Contains(string(b.Input), fact) {
					calls++
				}
			}
		}
		return calls, 0
	}
	return countCalls(s.req, fact)
}

// blocked is the answer every shape shares when the agent never got as far
// as the model: "request-failed:<reason>" (reason from failReason, a fixed
// name) or "rejected". It is "" when the shape's own checks can run.
func (s shapeRun) blocked(r *TestRunner) (reason, msg string) {
	switch {
	case s.failed != "":
		return "request-failed:" + s.failed, fmt.Sprintf("seed shapes (%s): %s failed before any request carried the prompt: %s", s.name, r.harness.Name, s.failMsg)
	case s.req == nil && !s.codecOnly:
		return "rejected", fmt.Sprintf("seed shapes (%s): %s loaded the session and no request carried the prompt", s.name, r.harness.Name)
	}
	return "", ""
}

func shapeFact() string { return "SEED-" + randomHex(4) }

func shapeText(s string) transcript.Block {
	return transcript.Block{Kind: transcript.BlockText, Text: s}
}

func shapeEntry(id string, role transcript.Role, blocks ...transcript.Block) transcript.Entry {
	return transcript.Entry{ID: id, Role: role, Content: blocks}
}

// timed dates the entries one second apart in order.
func timed(at time.Time, es ...transcript.Entry) []transcript.Entry {
	for i := range es {
		es[i].Time = at.Add(time.Duration(i) * time.Second)
		if c := es[i].Compaction; c != nil {
			for j := range c.Summary {
				c.Summary[j].Time = es[i].Time
			}
		}
	}
	return es
}

// missing names the parts of want whose fact is not there.
func missing(have func(string) bool, parts ...[2]string) []string {
	var out []string
	for _, p := range parts {
		if !have(p[1]) {
			out = append(out, p[0])
		}
	}
	return out
}

// 1. opencode's layout: the tool's result and the image it returned filed
// inside the assistant message that made the call, not in a tool entry.
// Portable (agentprotocol 8cf0269) moves them into a tool entry of their own.
func shapeInMessageResult(r *TestRunner, cwd string, at time.Time) shapeSession {
	args, result := shapeFact(), shapeFact()
	tool, input := r.seedToolCall(args)
	img := shapePNG(color.RGBA{200, 30, 30, 255})
	const id = "call_inmsg_1"
	es := timed(at,
		shapeEntry("shape1-u", transcript.RoleUser, shapeText("Take a screenshot of the page.")),
		shapeEntry("shape1-a", transcript.RoleAssistant,
			shapeText("Taking the screenshot."),
			transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input},
			transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK, Text: "screenshot taken: " + result},
			transcript.Block{Kind: transcript.BlockImage, ToolID: id, MediaType: "image/png", Data: img}),
		shapeEntry("shape1-b", transcript.RoleAssistant, shapeText("The screenshot is taken.")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.in-message-result:"+reason, msg)
			return
		}
		b64 := base64.StdEncoding.EncodeToString(img)
		name := r.harness.Name
		switch {
		case !s.kept(result):
			r.finding("seedkinds.in-message-result:not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no result for the call (image kept: %v)", s.name, name, s.kept(b64)))
		case !s.sent(result):
			r.finding("seedkinds.in-message-result:result-dropped", fmt.Sprintf("seed shapes (%s): %s did not send the result filed in the assistant message (image sent: %v)", s.name, name, s.sent(b64)))
		case s.sent(b64):
			r.pass("seedkinds.in-message-result:as-image", fmt.Sprintf("seed shapes (%s): %s sent the result and the image as image data", s.name, name))
		case !s.kept(b64):
			r.finding("seedkinds.in-message-result:image-not-kept", fmt.Sprintf("seed shapes (%s): %s sent the result; its codec kept no image", s.name, name))
		default:
			r.finding("seedkinds.in-message-result:image-dropped", fmt.Sprintf("seed shapes (%s): %s sent the result and not the image its codec kept", s.name, name))
		}
	}}
}

// 2. Parallel calls that share an id and an input, each with its own result
// (gemini records parallel calls with the same input this way).
func shapeParallelSameID(r *TestRunner, cwd string, at time.Time) shapeSession {
	args, r1, r2 := shapeFact(), shapeFact(), shapeFact()
	tool, input := r.seedToolCall(args)
	const id = "call_dup"
	use := transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input}
	res := func(t string) transcript.Block {
		return transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK, Text: t}
	}
	es := timed(at,
		shapeEntry("shape2-u", transcript.RoleUser, shapeText("Read the file twice and compare.")),
		shapeEntry("shape2-a", transcript.RoleAssistant, use, use),
		shapeEntry("shape2-t", transcript.RoleTool, res("first read: "+r1), res("second read: "+r2)),
		shapeEntry("shape2-b", transcript.RoleAssistant, shapeText("Both reads are done.")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.parallel-same-id:"+reason, msg)
			return
		}
		name := r.harness.Name
		keptCalls, _ := shapeRun{back: s.back, codecOnly: true}.calls(args)
		keptResults := 0
		for _, f := range []string{r1, r2} {
			if s.kept(f) {
				keptResults++
			}
		}
		if parts := callParts(keptCalls, s.kept, r1, r2); parts != "" {
			r.finding("seedkinds.parallel-same-id:not-kept-"+parts,
				fmt.Sprintf("seed shapes (%s): %s's codec kept %d of 2 calls and %d of 2 results", s.name, name, keptCalls, keptResults))
			return
		}
		calls, text := s.calls(args)
		results := 0
		for _, f := range []string{r1, r2} {
			if s.sent(f) {
				results++
			}
		}
		msg := fmt.Sprintf("seed shapes (%s): %s sent %d structured calls (%d text mentions) and %d of 2 results", s.name, name, calls, text, results)
		n := calls
		if n == 0 {
			n = text
		}
		switch parts := callParts(n, s.sent, r1, r2); {
		case parts != "":
			r.finding("seedkinds.parallel-same-id:dropped-"+parts, msg)
		case calls >= 2:
			r.pass("seedkinds.parallel-same-id:as-tool", msg)
		default:
			r.pass("seedkinds.parallel-same-id:as-text", msg)
		}
	}}
}

// callParts names what is missing of two identical calls and their two
// results: "one-call" or "both-calls", "first-result", "second-result".
func callParts(calls int, has func(string) bool, r1, r2 string) string {
	var out []string
	switch {
	case calls == 0:
		out = append(out, "both-calls")
	case calls == 1:
		out = append(out, "one-call")
	}
	if !has(r1) {
		out = append(out, "first-result")
	}
	if !has(r2) {
		out = append(out, "second-result")
	}
	return strings.Join(out, "-")
}

// 3. A user prompt with an image and a PDF attached.
func shapeUserAttachments(r *TestRunner, cwd string, at time.Time) shapeSession {
	pdfText, pdfName := shapeFact(), shapeFact()
	img := shapePNG(color.RGBA{30, 160, 60, 255})
	pdf := shapePDF("Brief tag " + pdfText)
	es := timed(at,
		shapeEntry("shape3-u", transcript.RoleUser,
			shapeText("Here are the mockup and the brief."),
			transcript.Block{Kind: transcript.BlockImage, MediaType: "image/png", Data: img},
			transcript.Block{Kind: transcript.BlockFile, MediaType: "application/pdf", Name: "brief-" + pdfName + ".pdf", Data: pdf}),
		shapeEntry("shape3-a", transcript.RoleAssistant, shapeText("I have the mockup and the brief.")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.user-attachments.image:"+reason, msg)
			r.finding("seedkinds.user-attachments.pdf:"+reason, msg)
			return
		}
		name := r.harness.Name
		b64 := base64.StdEncoding.EncodeToString(img)
		switch {
		case !s.kept(b64):
			r.finding("seedkinds.user-attachments.image:not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no attached image", s.name, name))
		case s.sent(b64):
			r.pass("seedkinds.user-attachments.image:as-image", fmt.Sprintf("seed shapes (%s): %s sent the attached image as image data", s.name, name))
		default:
			r.finding("seedkinds.user-attachments.image:dropped", fmt.Sprintf("seed shapes (%s): %s did not send the attached image its codec kept", s.name, name))
		}
		p64 := base64.StdEncoding.EncodeToString(pdf)
		switch {
		case !s.kept(p64) && !s.kept(pdfName):
			r.finding("seedkinds.user-attachments.pdf:not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no attached PDF", s.name, name))
		case s.sent(p64):
			r.pass("seedkinds.user-attachments.pdf:as-document", fmt.Sprintf("seed shapes (%s): %s sent the PDF's bytes (name sent: %v)", s.name, name, s.sent(pdfName)))
		case s.sent(pdfText) && s.sent("%PDF-"):
			r.pass("seedkinds.user-attachments.pdf:as-raw-text", fmt.Sprintf("seed shapes (%s): %s sent the PDF's raw bytes as text", s.name, name))
		case s.sent(pdfText):
			r.pass("seedkinds.user-attachments.pdf:as-text", fmt.Sprintf("seed shapes (%s): %s sent the PDF's text", s.name, name))
		case s.sent(pdfName):
			r.pass("seedkinds.user-attachments.pdf:as-reference", fmt.Sprintf("seed shapes (%s): %s sent the PDF's name and not its content", s.name, name))
		default:
			r.finding("seedkinds.user-attachments.pdf:dropped", fmt.Sprintf("seed shapes (%s): %s did not send the PDF its codec kept (bytes kept: %v)", s.name, name, s.kept(p64)))
		}
	}}
}

// 4. A tool turn directly after an assistant text message: two assistant
// messages in a row (kiro 2.22 loaded this and sent no request).
func shapeToolAfterText(r *TestRunner, cwd string, at time.Time) shapeSession {
	text, args, result := shapeFact(), shapeFact(), shapeFact()
	tool, input := r.seedToolCall(args)
	const id = "call_after_text"
	es := timed(at,
		shapeEntry("shape4-u", transcript.RoleUser, shapeText("Look at the config.")),
		shapeEntry("shape4-a", transcript.RoleAssistant, shapeText("Sure, the config tag is "+text+".")),
		shapeEntry("shape4-b", transcript.RoleAssistant, transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input}),
		shapeEntry("shape4-t", transcript.RoleTool, transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK, Text: "config: " + result}),
		shapeEntry("shape4-c", transcript.RoleAssistant, shapeText("The config is fine.")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.tool-after-text:"+reason, msg)
			return
		}
		want := [][2]string{{"text", text}, {"call", args}, {"result", result}}
		if m := missing(s.kept, want...); len(m) > 0 {
			parts := strings.Join(m, "-")
			r.finding("seedkinds.tool-after-text:not-kept-"+parts, fmt.Sprintf("seed shapes (%s): %s's codec lost the %s", s.name, r.harness.Name, strings.Join(m, ", ")))
			return
		}
		if m := missing(s.sent, want...); len(m) > 0 {
			parts := strings.Join(m, "-")
			r.finding("seedkinds.tool-after-text:dropped-"+parts, fmt.Sprintf("seed shapes (%s): %s did not send the %s", s.name, r.harness.Name, strings.Join(m, ", ")))
			return
		}
		r.pass("seedkinds.tool-after-text", fmt.Sprintf("seed shapes (%s): %s sent the text, the call after it and its result", s.name, r.harness.Name))
	}}
}

// 5. An image attached by path alone: a file: URL, no bytes, as codex
// records a local image. The file exists, so an agent or a writer that reads
// it can; opencode and kilo failed the whole request on a file: URL ("URL
// scheme must be http, https, or data").
func shapeFileURI(r *TestRunner, cwd string, at time.Time) shapeSession {
	fileName := shapeFact()
	path := filepath.Join(cwd, "screen-"+fileName+".png")
	img := shapePNG(color.RGBA{30, 60, 200, 255})
	es := timed(at,
		shapeEntry("shape5-u", transcript.RoleUser,
			shapeText("Look at the attached screenshot."),
			transcript.Block{Kind: transcript.BlockImage, MediaType: "image/png", URI: "file://" + path}),
		shapeEntry("shape5-a", transcript.RoleAssistant, shapeText("I looked at the screenshot.")),
	)
	return shapeSession{entries: es, file: path, content: img, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.file-uri:"+reason, msg)
			return
		}
		name := r.harness.Name
		b64 := base64.StdEncoding.EncodeToString(img)
		switch {
		case s.sent(b64) && !s.codecOnly:
			r.pass("seedkinds.file-uri:as-image", fmt.Sprintf("seed shapes (%s): %s sent the image's bytes (codec kept them inline: %v)", s.name, name, s.kept(b64)))
		case s.sent(fileName) && !s.codecOnly:
			r.pass("seedkinds.file-uri:as-reference", fmt.Sprintf("seed shapes (%s): %s sent the image's path and not its bytes", s.name, name))
		case !s.kept(fileName) && !s.kept(b64):
			r.finding("seedkinds.file-uri:not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no image", s.name, name))
		case s.codecOnly:
			r.pass("seedkinds.file-uri:kept", fmt.Sprintf("seed shapes (%s): %s's codec kept the image (bytes inline: %v)", s.name, name, s.kept(b64)))
		default:
			r.finding("seedkinds.file-uri:dropped", fmt.Sprintf("seed shapes (%s): %s accepted the session and did not send the image its codec kept", s.name, name))
		}
	}}
}

// 6. Before a compaction that keeps nothing: a tool call and its result,
// and an assistant answer right before the marker, all shown-only. The person still sees
// them (Linearize); the model is given the summary alone (Context and the
// first request).
func shapeRetiredTool(r *TestRunner, cwd string, at time.Time) shapeSession {
	user, args, result, answer, summary := shapeFact(), shapeFact(), shapeFact(), shapeFact(), shapeFact()
	tool, input := r.seedToolCall(args)
	const id = "call_retired"
	es := timed(at,
		shapeEntry("shape6-u", transcript.RoleUser, shapeText("Check the build log; its tag is "+user+".")),
		shapeEntry("shape6-a", transcript.RoleAssistant, transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input}),
		shapeEntry("shape6-t", transcript.RoleTool, transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK, Text: "log: " + result}),
		shapeEntry("shape6-b", transcript.RoleAssistant, shapeText("The build log is clean, reference "+answer+".")),
		transcript.Entry{ID: "shape6-c", Compaction: &transcript.Compaction{
			Summary: []transcript.Entry{shapeEntry("shape6-c-summary", transcript.RoleUser,
				shapeText("Summary of the conversation so far: the build log was checked; the summary tag is "+summary+"."))},
		}},
		shapeEntry("shape6-v", transcript.RoleUser, shapeText("Carry on with the review.")),
		shapeEntry("shape6-w", transcript.RoleAssistant, shapeText("Carrying on.")),
	)
	// The retired entries as a reader gives them: shown and no longer sent
	// (AudienceUser), as agents that keep displaying retired history mark
	// it. Lower hands them to a writer as history before its marker.
	for i := range es[:4] {
		es[i].Audience = transcript.AudienceUser
	}
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		name := r.harness.Name
		want := [][2]string{{"user", user}, {"call", args}, {"result", result}, {"answer", answer}}
		// The codec's half needs no request.
		if s.back != nil {
			lin := s.back.Linearize()
			compacted := false
			for _, e := range s.back.Entries {
				compacted = compacted || e.Compaction != nil
			}
			inLin := func(f string) bool { return contextHolds(lin, f) }
			switch m := missing(inLin, want...); {
			case len(m) == 0:
				r.pass("seedkinds.retired-tool.history", fmt.Sprintf("seed shapes (%s): %s's Linearize() keeps the retired call, result and answer", s.name, name))
			case len(m) == len(want) && !compacted:
				r.finding("seedkinds.retired-tool.history:summary-only", fmt.Sprintf("seed shapes (%s): %s's writer applied the compaction; no retired entry is kept", s.name, name))
			default:
				parts := strings.Join(m, "-")
				r.finding("seedkinds.retired-tool.history:dropped-"+parts, fmt.Sprintf("seed shapes (%s): %s's Linearize() lost the retired %s", s.name, name, strings.Join(m, ", ")))
			}
			held := func(f string) bool { return contextHolds(s.back.Context(), f) }
			switch in := present(held, want...); {
			case len(in) > 0:
				parts := strings.Join(in, "-")
				r.finding("seedkinds.retired-tool.context:retired-kept-"+parts, fmt.Sprintf("seed shapes (%s): %s's Context() holds the retired %s", s.name, name, strings.Join(in, ", ")))
			case !held(summary):
				r.finding("seedkinds.retired-tool.context:no-summary", fmt.Sprintf("seed shapes (%s): %s's Context() has no summary", s.name, name))
			default:
				r.pass("seedkinds.retired-tool.context", fmt.Sprintf("seed shapes (%s): %s's Context() holds the summary and nothing retired", s.name, name))
			}
		} else {
			r.fail("seedkinds.retired-tool.read-back", fmt.Sprintf("seed shapes (%s): %s's session could not be read back", s.name, name))
		}
		if s.codecOnly {
			return
		}
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.retired-tool.request:"+reason, msg)
			return
		}
		switch in := present(s.sent, want...); {
		case len(in) > 0:
			parts := strings.Join(in, "-")
			r.finding("seedkinds.retired-tool.request:retired-sent-"+parts, fmt.Sprintf("seed shapes (%s): %s sent the model the retired %s", s.name, name, strings.Join(in, ", ")))
		case !s.sent(summary):
			r.finding("seedkinds.retired-tool.request:no-summary", fmt.Sprintf("seed shapes (%s): %s did not send the summary", s.name, name))
		default:
			r.pass("seedkinds.retired-tool.request", fmt.Sprintf("seed shapes (%s): %s sent the summary and nothing retired", s.name, name))
		}
	}}
}

// present names the parts of want whose fact is there.
func present(have func(string) bool, parts ...[2]string) []string {
	var out []string
	for _, p := range parts {
		if have(p[1]) {
			out = append(out, p[0])
		}
	}
	return out
}

// countCalls counts the structured tool calls in req whose arguments carry
// fact: an object with a name (or toolName) and input, arguments or args.
// That is anthropic's tool_use, chat's tool_calls[].function, responses'
// function_call and gemini's functionCall, each once. Tool declarations are
// removed first. When no call carries it, text counts the fact's
// occurrences.
func countCalls(req []server.LogEntry, fact string) (calls, text int) {
	for _, e := range req {
		text += bytes.Count(e.Body, []byte(fact))
		var v any
		if json.Unmarshal(e.Body, &v) != nil {
			continue
		}
		if m, ok := v.(map[string]any); ok {
			for _, k := range []string{"tools", "functions", "tool_choice", "toolConfig", "tool_config"} {
				delete(m, k)
			}
		}
		var onObj func(map[string]any)
		var onStr func(string)
		onObj = func(o map[string]any) {
			for _, nk := range []string{"name", "toolName"} {
				if _, ok := o[nk].(string); !ok {
					continue
				}
				for _, k := range []string{"input", "arguments", "args"} {
					if a, ok := o[k]; ok {
						if strings.Contains(jsonText(a), fact) {
							calls++
						}
						return
					}
				}
			}
		}
		onStr = func(s string) {
			if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				var inner any
				if json.Unmarshal([]byte(t), &inner) == nil {
					walkJSON(inner, onObj, onStr)
				}
			}
		}
		walkJSON(v, onObj, onStr)
	}
	if calls > 0 {
		text = 0
	}
	return calls, text
}

// shapePNG is a 64x64 PNG shaded from c, so each shape's image has bytes of
// its own and is an image an agent's own validation takes (grok replaced a
// 4x4 one with "[image removed — invalid data]").
func shapePNG(c color.RGBA) []byte {
	const n = 64
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for x := 0; x < n; x++ {
		for y := 0; y < n; y++ {
			img.Set(x, y, color.RGBA{c.R ^ uint8(x*2), c.G ^ uint8(y*2), c.B, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// shapePDF is a one-page PDF showing text.
func shapePDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 20 50 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 400 100] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// 7. A tool result that is only an image: the result block has no text.
// goose's, pi's and omp's formats need a text field on every text item; an
// empty one written without it failed goose's load ("Session not found")
// and crashed pi and omp.
func shapeImageOnlyResult(r *TestRunner, cwd string, at time.Time) shapeSession {
	args := shapeFact()
	tool, input := r.seedToolCall(args)
	img := shapePNG(color.RGBA{220, 160, 20, 255})
	const id = "call_image_only"
	es := timed(at,
		shapeEntry("shape7-u", transcript.RoleUser, shapeText("Show me the chart.")),
		shapeEntry("shape7-a", transcript.RoleAssistant, transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input}),
		shapeEntry("shape7-t", transcript.RoleTool,
			transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK},
			transcript.Block{Kind: transcript.BlockImage, ToolID: id, MediaType: "image/png", Data: img}),
		shapeEntry("shape7-b", transcript.RoleAssistant, shapeText("That is the chart.")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.image-only-result:"+reason, msg)
			return
		}
		name := r.harness.Name
		b64 := base64.StdEncoding.EncodeToString(img)
		switch {
		case !s.kept(args):
			r.finding("seedkinds.image-only-result:call-not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no call", s.name, name))
		case !s.sent(args):
			r.finding("seedkinds.image-only-result:call-dropped", fmt.Sprintf("seed shapes (%s): %s did not send the call (image sent: %v)", s.name, name, s.sent(b64)))
		case s.sent(b64):
			r.pass("seedkinds.image-only-result:as-image", fmt.Sprintf("seed shapes (%s): %s sent the call and the image as image data", s.name, name))
		case !s.kept(b64):
			r.finding("seedkinds.image-only-result:image-not-kept", fmt.Sprintf("seed shapes (%s): %s sent the call; its codec kept no image", s.name, name))
		default:
			r.finding("seedkinds.image-only-result:image-dropped", fmt.Sprintf("seed shapes (%s): %s sent the call and not the image its codec kept", s.name, name))
		}
	}}
}

// 8. A compaction whose summary has the same text as the retired answer
// right before it (an agent that summarises with its last answer). The
// answer stays as history; the model gets the text once, as the summary.
func shapeRetiredAnswerAsSummary(r *TestRunner, cwd string, at time.Time) shapeSession {
	user, same := shapeFact(), shapeFact()
	text := "The review is finished; its reference is " + same + "."
	es := timed(at,
		shapeEntry("shape8-u", transcript.RoleUser, shapeText("Finish the review; its label is "+user+".")),
		shapeEntry("shape8-a", transcript.RoleAssistant, shapeText(text)),
		transcript.Entry{ID: "shape8-c", Compaction: &transcript.Compaction{
			Summary: []transcript.Entry{shapeEntry("shape8-c-summary", transcript.RoleUser, shapeText(text))},
		}},
		shapeEntry("shape8-v", transcript.RoleUser, shapeText("Carry on.")),
		shapeEntry("shape8-w", transcript.RoleAssistant, shapeText("Carrying on.")),
	)
	for i := range es[:2] {
		es[i].Audience = transcript.AudienceUser
	}
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		name := r.harness.Name
		if s.back == nil {
			r.fail("seedkinds.retired-answer-as-summary.read-back", fmt.Sprintf("seed shapes (%s): %s's session could not be read back", s.name, name))
			return
		}
		answerShown, userShown := false, false
		for _, e := range s.back.Linearize() {
			userShown = userShown || contextHolds([]transcript.Entry{e}, user)
			answerShown = answerShown || e.Role == transcript.RoleAssistant && contextHolds([]transcript.Entry{e}, same)
		}
		switch {
		case answerShown && userShown:
			r.pass("seedkinds.retired-answer-as-summary.history", fmt.Sprintf("seed shapes (%s): %s's Linearize() keeps the retired prompt and answer", s.name, name))
		case userShown:
			r.finding("seedkinds.retired-answer-as-summary.history:dropped-answer", fmt.Sprintf("seed shapes (%s): %s's Linearize() lost the retired answer that matches the summary", s.name, name))
		default:
			r.finding("seedkinds.retired-answer-as-summary.history:dropped-user", fmt.Sprintf("seed shapes (%s): %s's Linearize() lost the retired prompt (answer kept: %v)", s.name, name, answerShown))
		}
		ctx := s.back.Context()
		switch {
		case contextHolds(ctx, user):
			r.finding("seedkinds.retired-answer-as-summary.context:retired-kept", fmt.Sprintf("seed shapes (%s): %s's Context() holds the retired prompt", s.name, name))
		case !contextHolds(ctx, same):
			r.finding("seedkinds.retired-answer-as-summary.context:no-summary", fmt.Sprintf("seed shapes (%s): %s's Context() has no summary", s.name, name))
		default:
			r.pass("seedkinds.retired-answer-as-summary.context", fmt.Sprintf("seed shapes (%s): %s's Context() holds the summary and not the retired prompt", s.name, name))
		}
		if s.codecOnly {
			return
		}
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.retired-answer-as-summary.request:"+reason, msg)
			return
		}
		n := 0
		for _, e := range s.req {
			c := bytes.Count(e.Body, []byte(same))
			// The mock logs a cursor history blob twice in one entry: the
			// blob and the message that carries it.
			if c > 1 && bytes.Contains(e.Body, []byte(`"history_blob"`)) {
				c = 1
			}
			n += c
		}
		switch {
		case s.sent(user):
			r.finding("seedkinds.retired-answer-as-summary.request:retired-sent", fmt.Sprintf("seed shapes (%s): %s sent the retired prompt", s.name, name))
		case n == 0:
			r.finding("seedkinds.retired-answer-as-summary.request:no-summary", fmt.Sprintf("seed shapes (%s): %s did not send the summary", s.name, name))
		case n > 1:
			r.finding("seedkinds.retired-answer-as-summary.request:sent-twice", fmt.Sprintf("seed shapes (%s): %s sent the summary's text %d times", s.name, name, n))
		default:
			r.pass("seedkinds.retired-answer-as-summary.request", fmt.Sprintf("seed shapes (%s): %s sent the summary once and nothing retired", s.name, name))
		}
	}}
}

// 9. A prompt with a local text file attached by bare path, no media type
// (copilot records attachments this way; opencode and kilo need a type).
func shapeUntypedFilePath(r *TestRunner, cwd string, at time.Time) shapeSession {
	fileName, content := shapeFact(), shapeFact()
	path := filepath.Join(cwd, "notes-"+fileName+".txt")
	es := timed(at,
		shapeEntry("shape9-u", transcript.RoleUser,
			shapeText("Read the attached notes."),
			transcript.Block{Kind: transcript.BlockFile, Name: filepath.Base(path), URI: path}),
		shapeEntry("shape9-a", transcript.RoleAssistant, shapeText("I read the notes.")),
	)
	body := []byte("notes: " + content + "\n")
	b64 := base64.StdEncoding.EncodeToString(body)
	return shapeSession{entries: es, file: path, content: body, report: func(r *TestRunner, s shapeRun) {
		if reason, msg := s.blocked(r); reason != "" {
			r.finding("seedkinds.untyped-file-path:"+reason, msg)
			return
		}
		name := r.harness.Name
		switch {
		case s.sent(content) && !s.codecOnly:
			r.pass("seedkinds.untyped-file-path:as-text", fmt.Sprintf("seed shapes (%s): %s sent the file's content", s.name, name))
		case s.sent(b64) && !s.codecOnly:
			r.pass("seedkinds.untyped-file-path:as-document", fmt.Sprintf("seed shapes (%s): %s sent the file's bytes", s.name, name))
		case s.sent(fileName) && !s.codecOnly:
			r.pass("seedkinds.untyped-file-path:as-reference", fmt.Sprintf("seed shapes (%s): %s sent the file's path and not its content", s.name, name))
		case !s.kept(fileName) && !s.kept(content) && !s.kept(b64):
			r.finding("seedkinds.untyped-file-path:not-kept", fmt.Sprintf("seed shapes (%s): %s's codec kept no file", s.name, name))
		case s.codecOnly:
			r.pass("seedkinds.untyped-file-path:kept", fmt.Sprintf("seed shapes (%s): %s's codec kept the file (bytes inline: %v)", s.name, name, s.kept(b64) || s.kept(content)))
		default:
			r.finding("seedkinds.untyped-file-path:dropped", fmt.Sprintf("seed shapes (%s): %s did not send the file its codec kept", s.name, name))
		}
	}}
}

// 10. A session that ends on a prompt no one answered.
func shapeTrailingPrompt(r *TestRunner, cwd string, at time.Time) shapeSession {
	last := shapeFact()
	es := timed(at,
		shapeEntry("shape10-u", transcript.RoleUser, shapeText("Start the migration.")),
		shapeEntry("shape10-a", transcript.RoleAssistant, shapeText("The migration is started.")),
		shapeEntry("shape10-v", transcript.RoleUser, shapeText("Also rename the table to "+last+".")),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		reportTrailing(r, s, "prompt", last)
	}}
}

// 11. A session that ends on a tool result, before the answer to it.
func shapeTrailingToolResult(r *TestRunner, cwd string, at time.Time) shapeSession {
	args, result := shapeFact(), shapeFact()
	tool, input := r.seedToolCall(args)
	const id = "call_trailing"
	es := timed(at,
		shapeEntry("shape11-u", transcript.RoleUser, shapeText("Check the schema.")),
		shapeEntry("shape11-a", transcript.RoleAssistant, transcript.Block{Kind: transcript.BlockToolUse, ToolID: id, Name: tool, Input: input}),
		shapeEntry("shape11-t", transcript.RoleTool, transcript.Block{Kind: transcript.BlockToolResult, ToolID: id, Name: tool, Status: transcript.StatusOK, Text: "schema: " + result}),
	)
	return shapeSession{entries: es, report: func(r *TestRunner, s shapeRun) {
		reportTrailing(r, s, "tool result", result)
	}}
}

// reportTrailing is the trailing shapes' checks: the session loads, the
// last message is still shown (Linearize), and whether the first request
// carries it. Sent and not sent both pass: kiro records it as cancelled
// and does not send it; an agent that sends it answers it.
func reportTrailing(r *TestRunner, s shapeRun, label, fact string) {
	name := r.harness.Name
	if s.back != nil && contextHolds(s.back.Linearize(), fact) {
		r.pass("seedkinds.trailing-"+slug(label)+".history", fmt.Sprintf("seed shapes (%s): %s's Linearize() shows the last %s", s.name, name, label))
	} else {
		r.finding("seedkinds.trailing-"+slug(label)+".history:dropped", fmt.Sprintf("seed shapes (%s): %s's Linearize() lost the last %s", s.name, name, label))
	}
	if s.codecOnly {
		return
	}
	if reason, msg := s.blocked(r); reason != "" {
		r.finding("seedkinds.trailing-"+slug(label)+".request:"+reason, msg)
		return
	}
	if s.sent(fact) {
		r.pass("seedkinds.trailing-"+slug(label)+".request:sent", fmt.Sprintf("seed shapes (%s): %s loaded the session and sent the last %s", s.name, name, label))
	} else {
		r.pass("seedkinds.trailing-"+slug(label)+".request:not-sent", fmt.Sprintf("seed shapes (%s): %s loaded the session and did not send the last %s", s.name, name, label))
	}
}
