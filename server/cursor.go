package server

// Cursor agent CLI (`agent`, cursor.com/install) speaks Connect-protobuf to
// Cursor's backend; the model runs server-side and the client executes tools.
// This file implements just enough of that protocol for the harness. The
// message schemas were read out of the CLI bundle (agent.v1.*, aiserver.v1.*)
// with the resolver kept in the belt scratch notes, 2026-09.
//
// Flow the CLI follows against an endpoint:
//   POST /auth/exchange_user_api_key           JSON  {accessToken, refreshToken}
//   POST /aiserver.v1.DashboardService/*       proto unary (GetMe, privacy, ...)
//   POST /aiserver.v1.ServerConfigService/*    proto unary
//   POST /aiserver.v1.AiService/GetUsableModels, GetDefaultModelForCli, AvailableModels
//   POST /agent.v1.AgentService/RunSSE         connect server-stream, request = BidiRequestId
//   POST /aiserver.v1.BidiService/BidiAppend   proto unary, data = hex(AgentClientMessage)
//
// Server → client messages go back as AgentServerMessage frames on the RunSSE
// stream. The mock asks the client for its request context (rules, env), which
// is how AGENTS.md / .cursor/rules content reaches the log, optionally asks it
// to read a file (tool hooks), then streams the canned answer and ends the turn.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// --- protobuf wire helpers (no schema compiler needed) ---

func pbVarint(v uint64) []byte {
	var b [10]byte
	n := binary.PutUvarint(b[:], v)
	return b[:n]
}

func pbTag(no int, wt int) []byte { return pbVarint(uint64(no)<<3 | uint64(wt)) }

func pbBytes(no int, v []byte) []byte {
	return append(append(pbTag(no, 2), pbVarint(uint64(len(v)))...), v...)
}

func pbString(no int, s string) []byte     { return pbBytes(no, []byte(s)) }
func pbMsg(no int, parts ...[]byte) []byte { return pbBytes(no, bytes.Join(parts, nil)) }
func pbUint(no int, v uint64) []byte       { return append(pbTag(no, 0), pbVarint(v)...) }
func pbBool(no int, v bool) []byte {
	if v {
		return pbUint(no, 1)
	}
	return pbUint(no, 0)
}

type pbField struct {
	No   int
	WT   int
	Num  uint64
	Data []byte
}

// pbDecode splits a message into fields. Returns ok=false on malformed input.
func pbDecode(b []byte) ([]pbField, bool) {
	var out []pbField
	i := 0
	for i < len(b) {
		key, n := binary.Uvarint(b[i:])
		if n <= 0 {
			return nil, false
		}
		i += n
		f := pbField{No: int(key >> 3), WT: int(key & 7)}
		if f.No == 0 {
			return nil, false
		}
		switch f.WT {
		case 0:
			v, n := binary.Uvarint(b[i:])
			if n <= 0 {
				return nil, false
			}
			f.Num = v
			i += n
		case 1:
			if i+8 > len(b) {
				return nil, false
			}
			f.Data = b[i : i+8]
			i += 8
		case 2:
			l, n := binary.Uvarint(b[i:])
			if n <= 0 || i+n+int(l) > len(b) {
				return nil, false
			}
			f.Data = b[i+n : i+n+int(l)]
			i += n + int(l)
		case 5:
			if i+4 > len(b) {
				return nil, false
			}
			f.Data = b[i : i+4]
			i += 4
		default:
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

// pbGet returns the first field with the given number.
func pbGet(fields []pbField, no int) (pbField, bool) {
	for _, f := range fields {
		if f.No == no {
			return f, true
		}
	}
	return pbField{}, false
}

// pbPath follows a chain of length-delimited fields.
func pbPath(b []byte, path ...int) ([]byte, bool) {
	for _, no := range path {
		fields, ok := pbDecode(b)
		if !ok {
			return nil, false
		}
		f, ok := pbGet(fields, no)
		if !ok || f.WT != 2 {
			return nil, false
		}
		b = f.Data
	}
	return b, true
}

// pbToJSON renders a message as a loose JSON tree for the request log: field
// numbers as keys, printable strings kept as text, nested messages recursed.
// It is a heuristic (bytes that happen to parse as a message are recursed),
// good enough for humans and for grepping codenames out of the log.
func pbToJSON(b []byte, depth int) any {
	fields, ok := pbDecode(b)
	if !ok {
		return hex.EncodeToString(b)
	}
	out := map[string]any{}
	for _, f := range fields {
		key := fmt.Sprint(f.No)
		var v any
		switch f.WT {
		case 0:
			v = f.Num
		case 2:
			printable := utf8.Valid(f.Data) && isMostlyText(f.Data)
			if depth < 12 && len(f.Data) > 0 {
				if _, ok := pbDecode(f.Data); ok && !printable {
					v = pbToJSON(f.Data, depth+1)
				}
			}
			if v == nil {
				if printable {
					v = string(f.Data)
				} else {
					v = hex.EncodeToString(f.Data)
				}
			}
		default:
			v = hex.EncodeToString(f.Data)
		}
		if prev, dup := out[key]; dup {
			if arr, isArr := prev.([]any); isArr {
				out[key] = append(arr, v)
			} else {
				out[key] = []any{prev, v}
			}
		} else {
			out[key] = v
		}
	}
	return out
}

func isMostlyText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, r := range string(b) {
		if r < 0x20 && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}

// --- Connect framing ---

func connectFrame(flag byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func readConnectFrames(body []byte) [][]byte {
	var frames [][]byte
	for len(body) >= 5 {
		l := binary.BigEndian.Uint32(body[1:5])
		if int(l)+5 > len(body) {
			break
		}
		frames = append(frames, body[5:5+l])
		body = body[5+l:]
	}
	return frames
}

// --- aiserver.v1 unary responses ---

func cursorModelDetails(id string) []byte {
	return bytes.Join([][]byte{pbString(1, id), pbString(3, id), pbString(4, id), pbString(5, id)}, nil)
}

var cursorModels = []string{"gpt-4o-mini", "mock-model"}

func (s *MockServer) cursorUnary(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	s.recordJSON(r, map[string]any{"rpc": r.URL.Path, "request": pbToJSON(body, 0)}, "")

	var out []byte
	switch {
	case strings.HasSuffix(r.URL.Path, "/GetUsableModels"):
		for _, m := range cursorModels {
			out = append(out, pbMsg(1, cursorModelDetails(m))...)
		}
	case strings.HasSuffix(r.URL.Path, "/GetDefaultModelForCli"):
		out = pbMsg(1, cursorModelDetails(cursorModels[0]))
	case strings.HasSuffix(r.URL.Path, "/AvailableModels"):
		for _, m := range cursorModels {
			out = append(out, pbMsg(2, pbString(1, m), pbBool(2, true), pbBool(5, true), pbString(17, m), pbString(18, m))...)
		}
		for _, m := range cursorModels {
			out = append(out, pbString(1, m)...)
		}
	case strings.HasSuffix(r.URL.Path, "/GetServerConfig"):
		// http2_config = FORCE_ALL_DISABLED (1): keep the agent stream on
		// HTTP/1.1 (RunSSE + BidiAppend) instead of an h2c bidi stream.
		out = pbUint(7, 1)
	case strings.HasSuffix(r.URL.Path, "/GetMe"):
		out = bytes.Join([][]byte{pbString(1, "mock-auth"), pbUint(2, 1), pbString(3, "mock@test.invalid")}, nil)
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, map[string]any{})
		return
	}
	w.Header().Set("Content-Type", "application/proto")
	w.WriteHeader(200)
	w.Write(out)
}

// readBody returns the request body, gunzipped when the client compressed it
// (connect clients gzip anything above a few hundred bytes).
func readBody(r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)
	if r.Header.Get("Content-Encoding") == "gzip" {
		if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			if out, err := io.ReadAll(zr); err == nil {
				return out
			}
		}
	}
	return body
}

func (s *MockServer) recordJSON(r *http.Request, v any, model string) int {
	data, _ := json.Marshal(v)
	return s.record(r, data, model)
}

// --- agent.v1 run stream ---

type cursorSession struct {
	frames   chan []byte
	closed   chan struct{}
	once     sync.Once
	stage    string // "", "context", "tool", "done"
	prompt   string
	model    string
	toolPath string // path the mock asked the client to read, "" when no tool was served
	toolCmd  string // command the mock asked the client to run, "" when it served a read
	// priorRoot and priorTurns are the blob ids of the conversation so far,
	// as the client sent them in run_request.conversation_state. A resumed
	// session sends its history this way, by reference.
	priorRoot  [][]byte
	priorTurns [][]byte
	userMsgID  string
	toolID     string // its call id, in real cursor's form
	toolOut    string // what the client returned for it
	execID     uint64
	pending    uint64 // exec id whose reply advances the current stage
	lastSeen   time.Time
}

func (cs *cursorSession) send(msg []byte) {
	select {
	case cs.frames <- connectFrame(0, msg):
	case <-cs.closed:
	case <-time.After(10 * time.Second):
	}
}

func (cs *cursorSession) close() { cs.once.Do(func() { close(cs.closed) }) }

func (s *MockServer) cursorSessions() map[string]*cursorSession {
	if s.cursor == nil {
		s.cursor = map[string]*cursorSession{}
	}
	return s.cursor
}

func (s *MockServer) cursorSession(id string) *cursorSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.cursorSessions()
	cs, ok := m[id]
	if !ok {
		cs = &cursorSession{frames: make(chan []byte, 16), closed: make(chan struct{}), lastSeen: time.Now()}
		m[id] = cs
	}
	return cs
}

// handleCursorRunSSE keeps the server stream open and relays frames queued by
// BidiAppend until the turn ends.
func (s *MockServer) handleCursorRunSSE(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	reqID := ""
	for _, fr := range readConnectFrames(body) {
		if id, ok := pbPath(fr, 1); ok {
			reqID = string(id)
		}
	}
	// RunSSE is a server stream by definition.
	s.markStreamed(s.recordJSON(r, map[string]any{"rpc": "RunSSE", "request_id": reqID}, ""))
	cs := s.cursorSession(reqID)

	w.Header().Set("Content-Type", "application/connect+proto")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	if f != nil {
		f.Flush()
	}
	timeout := time.After(120 * time.Second)
	for {
		select {
		case fr := <-cs.frames:
			w.Write(fr)
			if f != nil {
				f.Flush()
			}
		case <-cs.closed:
			// drain anything queued before the close
			for {
				select {
				case fr := <-cs.frames:
					w.Write(fr)
				default:
					w.Write(connectFrame(2, []byte("{}")))
					if f != nil {
						f.Flush()
					}
					return
				}
			}
		case <-timeout:
			w.Write(connectFrame(2, []byte(`{"error":{"code":"deadline_exceeded","message":"mock timeout"}}`)))
			return
		case <-r.Context().Done():
			return
		}
	}
}

// handleCursorBidiAppend receives one AgentClientMessage and drives the turn.
func (s *MockServer) handleCursorBidiAppend(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	fields, _ := pbDecode(body)
	var msg []byte
	reqID := ""
	for _, f := range fields {
		switch f.No {
		case 1: // data: hex-encoded message
			if b, err := hex.DecodeString(string(f.Data)); err == nil {
				msg = b
			}
		case 4: // data_binary
			msg = f.Data
		case 2: // request_id { request_id }
			if id, ok := pbPath(f.Data, 1); ok {
				reqID = string(id)
			}
		}
	}
	cs := s.cursorSession(reqID)
	top, _ := pbDecode(msg)

	entry := map[string]any{"rpc": "BidiAppend", "request_id": reqID, "message": pbToJSON(msg, 0)}
	model := ""
	kind := ""
	if len(top) > 0 {
		kind = fmt.Sprint(top[0].No)
	}
	switch kind {
	case "1": // run_request
		if t, ok := pbPath(msg, 1, 2, 1, 1, 1); ok {
			cs.prompt = string(t)
			entry["prompt"] = cs.prompt
		}
		if m, ok := pbPath(msg, 1, 9, 1); ok {
			cs.model = string(m)
			model = cs.model
		}
		// AgentRunRequest.conversation_state: ConversationStateStructure
		// { 1 root_prompt_messages_json (blob ids), 8 turns (blob ids) }.
		if st, ok := pbPath(msg, 1, 1); ok {
			if fields, ok := pbDecode(st); ok {
				for _, f := range fields {
					switch {
					case f.No == 1 && f.WT == 2:
						cs.priorRoot = append(cs.priorRoot, f.Data)
					case f.No == 8 && f.WT == 2:
						cs.priorTurns = append(cs.priorTurns, f.Data)
					}
				}
			}
			entry["history_blob_ids"] = len(cs.priorRoot)
		}
	case "2": // exec_client_message: tool / context / hook result
		entry["exec_result"] = true
		if ctx, ok := pbPath(msg, 2, 10); ok {
			entry["request_context"] = pbToJSON(ctx, 0)
		}
		if hook, ok := pbPath(msg, 2, 27); ok {
			entry["hook_result"] = pbToJSON(hook, 0)
		}
		// The read result comes back on the field its request went out on:
		// ReadResult { 1 success: ReadSuccess { 1 path, 2 content } }. Read
		// the content by field number. An earlier version took the longest
		// string in the reply, which was the path — longer than a four-byte
		// file — and stored it as the result.
		if content, ok := pbPath(msg, 2, 7, 1, 2); ok {
			entry["read_result"] = string(content)
			s.mu.Lock()
			cs.toolOut = string(content)
			s.mu.Unlock()
		}
		// ShellResult { 1 success, 2 failure, 4 rejected, 7 permission_denied, ... }.
		if res, ok := pbPath(msg, 2, 2); ok {
			entry["shell_result"] = pbToJSON(res, 0)
		}
	case "3": // kv_client_message { 1 id, 2 get_blob_result { 1 blob_data } }
		if data, ok := pbPath(msg, 3, 2, 1); ok {
			entry["history_blob"] = string(data)
		}
	case "5": // exec_client_control_message: heartbeat / stream_close / throw
		entry["exec_control"] = true
	}
	s.recordJSON(r, entry, model)

	w.Header().Set("Content-Type", "application/proto")
	w.WriteHeader(200)

	switch kind {
	case "1":
		s.cursorFetchHistory(cs)
		// The backend asks the client to run its prompt hook before the model
		// sees the turn; the reply's additional_context is recorded in the log,
		// the way the real backend would hand it to the model.
		if s.requestsHook("PROMPT") {
			s.cursorHook(cs, "hook:prompt", cursorHookBeforeSubmitPrompt,
				pbMsg(cursorHookBeforeSubmitPrompt, pbString(1, cs.prompt), pbString(3, "agent"), pbString(4, reqID), pbString(6, cs.model)))
		} else {
			cs.stage = "hook:prompt"
			s.cursorAdvance(cs)
		}
	case "2":
		id := uint64(0)
		if inner, ok := pbPath(msg, 2); ok {
			if fields, ok := pbDecode(inner); ok {
				if f, ok := pbGet(fields, 1); ok {
					id = f.Num
				}
			}
		}
		s.mu.Lock()
		current := cs.pending != 0 && id == cs.pending
		if current {
			cs.pending = 0
		}
		s.mu.Unlock()
		if current {
			s.cursorAdvance(cs)
		}
	}
}

// cursorShellParse is ShellArgs.parsing_result for a plain command:
// ShellCommandParsingResult { 1 parsing_failed, 2 executable_commands:
// ExecutableCommand { 1 name, 2 args: ExecutableCommandArg { 1 type, 2 value },
// 3 full_text } }. The mock's gated commands are single words separated by
// spaces, so splitting on whitespace is the whole parse.
func cursorShellParse(command string) []byte {
	words := strings.Fields(command)
	if len(words) == 0 {
		return pbMsg(8, pbBool(1, true))
	}
	cmd := [][]byte{pbString(1, words[0])}
	for _, w := range words[1:] {
		cmd = append(cmd, pbMsg(2, pbString(1, "word"), pbString(2, w)))
	}
	cmd = append(cmd, pbString(3, command))
	return pbMsg(8, pbBool(1, false), pbMsg(2, cmd...))
}

// cursorFetchHistory asks the client for every message blob its run_request
// named, the way the backend reads a conversation it holds only by reference.
// The replies are logged with the blob's text, so a resumed session's history
// is visible in the request log if, and only if, the client kept it and can
// hand it back. Without this a cursor resume could never be seen to carry the
// earlier turn: the ids are hashes, and the text never travels unasked.
func (s *MockServer) cursorFetchHistory(cs *cursorSession) {
	for i, id := range cs.priorRoot {
		// AgentServerMessage.kv_server_message { 1 id, 2 get_blob_args { 1 blob_id } }
		cs.send(pbMsg(4, pbUint(1, uint64(2000+i)), pbMsg(2, pbBytes(1, id))))
	}
}

// cursorFallback moves on if the client never answers an exec request.
// cursorFallback advances a stage whose reply never came. It must not fire
// once the reply has been handled, and a reply that arrives after the fallback
// must not advance the next stage: slow hooks (belt's take seconds) made a late
// tool reply skip the compaction step entirely.
func (s *MockServer) cursorFallback(cs *cursorSession, stage string) {
	s.mu.Lock()
	waitingOn := cs.pending
	s.mu.Unlock()
	time.Sleep(4 * time.Second)
	s.mu.Lock()
	stale := cs.stage != stage || cs.pending != waitingOn
	if !stale {
		cs.pending = 0
	}
	s.mu.Unlock()
	if !stale {
		s.cursorAdvance(cs)
	}
}

func (s *MockServer) cursorAdvance(cs *cursorSession) {
	s.mu.Lock()
	stage := cs.stage
	s.mu.Unlock()
	switch stage {
	case "hook:prompt":
		// Then ask for the request context: rules (AGENTS.md, .cursor/rules),
		// env, repo info. That reply carries the instruction-file content.
		cs.stage = "context"
		cs.execID++
		cs.pending = cs.execID
		cs.send(pbMsg(2, pbUint(1, cs.execID), pbString(15, fmt.Sprintf("exec-%d", cs.execID)), pbMsg(10)))
		go s.cursorFallback(cs, "context")
	case "hook:compact":
		cs.send(pbMsg(1, pbMsg(1, pbString(1, s.getResponse())))) // interaction_update.text_delta
		if s.requestsHook("STOP") {
			s.cursorHook(cs, "hook:stop", cursorHookStop,
				pbMsg(cursorHookStop, pbString(1, "completed"), pbUint(2, 0), pbString(3, cs.convID())))
			return
		}
		cs.stage = "hook:stop"
		s.cursorAdvance(cs)
	case "hook:stop":
		cs.stage = "done"
		s.cursorCheckpoint(cs)
		cs.send(pbMsg(1, pbMsg(14, pbUint(1, 10), pbUint(2, 5)))) // interaction_update.turn_ended
		cs.close()
	case "context":
		if s.shouldToolCall(true, "/agent.v1.AgentService/RunSSE") {
			cs.stage = "tool"
			cs.execID++
			cs.pending = cs.execID
			name, args := s.getToolCall()
			var a struct {
				Path    string `json:"path"`
				Command string `json:"command"`
			}
			json.Unmarshal([]byte(args), &a)
			cs.toolID = "tool_" + uuid.NewString()
			if strings.EqualFold(name, "shell") {
				// ExecServerMessage.shell_args: ShellArgs { 1 command,
				// 2 working_directory, 3 timeout (ms), 4 tool_call_id,
				// 8 parsing_result }. A shell command is what cursor asks its
				// client about; a read it runs without asking. The backend
				// parses the command: without parsing_result the client
				// answers spawn_error "Parsing result is required" and never
				// reaches its permission check.
				cs.toolCmd = a.Command
				s.cursorPendingCheckpoint(cs)
				cs.send(pbMsg(2, pbUint(1, cs.execID), pbString(15, fmt.Sprintf("exec-%d", cs.execID)),
					pbMsg(2, pbString(1, a.Command), pbUint(3, 30000), pbString(4, cs.toolID),
						cursorShellParse(a.Command))))
				go s.cursorFallback(cs, "tool")
				return
			}
			path := "README.md"
			if a.Path != "" {
				path = a.Path
			}
			cs.toolPath = path
			s.cursorPendingCheckpoint(cs)
			cs.send(pbMsg(2, pbUint(1, cs.execID), pbString(15, fmt.Sprintf("exec-%d", cs.execID)),
				pbMsg(7, pbString(1, path), pbString(2, cs.toolID))))
			go s.cursorFallback(cs, "tool")
			return
		}
		s.cursorFinish(cs)
	case "tool":
		s.cursorFinish(cs)
	}
}

// Cursor runs every hook on a backend request: ExecServerMessage field 27
// execute_hook_args { request = 1: ExecuteHookRequest }, whose oneof names the
// hook, and the client answers with ExecClientMessage field 27
// execute_hook_result (agent.v1 schema, Cursor CLI 2026.09). A mock that never
// asks can never see prompt, stop or compaction hooks, which is why those
// were once recorded as things Cursor "does not do" in headless mode.
const (
	cursorHookPreCompact         = 1
	cursorHookBeforeSubmitPrompt = 7
	cursorHookStop               = 11
)

// cursorHook sends one hook request and moves the session to stage; the
// client's execute_hook_result (or the fallback timer) advances it.
func (s *MockServer) cursorHook(cs *cursorSession, stage string, _ int, query []byte) {
	cs.stage = stage
	cs.execID++
	cs.pending = cs.execID
	cs.send(pbMsg(2, pbUint(1, cs.execID), pbString(15, fmt.Sprintf("exec-%d", cs.execID)),
		pbBool(55, true), // accept_hook_additional_contexts
		pbMsg(27, pbMsg(1, query))))
	go s.cursorFallback(cs, stage)
}

func (cs *cursorSession) convID() string { return "mock-conversation" }

// cursorFinish ends the turn the way the backend does: it compacts (the mock
// always does once, standing in for a full context window, so the client's
// preCompact hook is exercised), streams the answer, runs the stop hook, and
// only then ends the turn.
func (s *MockServer) cursorFinish(cs *cursorSession) {
	if s.requestsHook("PRE_COMPACT") {
		s.cursorHook(cs, "hook:compact", cursorHookPreCompact,
			pbMsg(cursorHookPreCompact, pbString(1, "auto"), pbBool(7, true), pbString(8, cs.convID()), pbString(10, cs.model)))
		return
	}
	cs.stage = "hook:compact"
	s.cursorAdvance(cs)
}

// cursorCheckpoint does what cursor's backend does at the end of a turn so the
// client persists the conversation.
//
// Without it cursor writes one line per session — {"type":"turn_ended"} — and
// nothing else, in every mode. Its transcript is not built from the streamed
// text: TranscriptStore.writeFromStateIncremental reads a
// ConversationStateStructure, takes root_prompt_messages_json as a list of
// blob ids, fetches each from the client's local blob store, and writes one row
// per message. The backend fills that store through kv_server_message
// set_blob_args and then names the ids in a conversation_checkpoint_update. The
// mock sent neither, so there was never anything to write.
//
// A blob is the UTF-8 JSON of an AI SDK message, {role, content}. The client
// skips an id it cannot find rather than failing, so the blobs go first and the
// checkpoint that names them after a pause for the blob manager, which runs on
// its own consumer.
func (s *MockServer) cursorCheckpoint(cs *cursorSession) {
	msgs := []map[string]any{{"role": "user", "content": cs.prompt}}
	if cs.toolPath != "" {
		// Names and ids follow a real cursor session's store.db: tools are
		// PascalCase ("Read", "Write", "StrReplace", "Grep") and call ids are
		// "tool_" plus a UUID. The mock used "read_file" and "call-1" until
		// that was checked, which made samples look plausible and wrong.
		//
		// A tool turn is three messages in the AI SDK shape cursor stores: the
		// call, the result, then the answer. Cursor's own transcript writer
		// renders the call as {"type":"tool_use","name","input"} with no id and
		// writes nothing for the result — a tool-result part yields no text, so
		// the tool message produces no row. The blob is still stored, which is
		// where a reader that wants results has to look.
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": []map[string]any{{
				"type": "tool-call", "toolCallId": cs.toolID, "toolName": "Read",
				"args": map[string]any{"path": cs.toolPath},
			}}},
			map[string]any{"role": "tool", "content": []map[string]any{{
				"type": "tool-result", "toolCallId": cs.toolID, "toolName": "Read",
				"result": cs.toolOut,
			}}},
		)
	}
	if cs.toolCmd != "" {
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": []map[string]any{{
				"type": "tool-call", "toolCallId": cs.toolID, "toolName": "Shell",
				"args": map[string]any{"command": cs.toolCmd},
			}}},
			map[string]any{"role": "tool", "content": []map[string]any{{
				"type": "tool-result", "toolCallId": cs.toolID, "toolName": "Shell",
				"result": cs.toolOut,
			}}},
		)
	}
	msgs = append(msgs, map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": s.getResponse()}}})
	s.cursorSendCheckpoint(cs, msgs, s.getResponse(), "")
}

// cursorPendingCheckpoint checkpoints a turn whose tool call is still out:
// the user's message and the call id in pending_tool_calls. Cursor writes its
// ACP session store (~/.cursor/acp-sessions/<id>/store.db) only from a
// checkpoint, so without one a client killed mid-turn left meta.json and no
// store, and session/load answered "Session not found" — about a turn this
// mock had never let it save. The state schema has a pending_tool_calls field,
// which exists only for a backend that checkpoints while a call is out.
func (s *MockServer) cursorPendingCheckpoint(cs *cursorSession) {
	msgs := []map[string]any{{"role": "user", "content": cs.prompt}}
	s.cursorSendCheckpoint(cs, msgs, "", cs.toolID)
}

// cursorSendCheckpoint stores each message as a blob and then names them in
// a conversation_checkpoint_update, after the history the client arrived
// with. answer is the assistant step of the structured turn, "" for none;
// pending is a tool call id still waiting on the client, "" for none.
func (s *MockServer) cursorSendCheckpoint(cs *cursorSession, msgs []map[string]any, answer, pending string) {
	n := 0
	setBlob := func(data []byte) []byte {
		sum := sha256.Sum256(data)
		// AgentServerMessage.kv_server_message { id, set_blob_args { blob_id, blob_data } }
		cs.send(pbMsg(4, pbUint(1, uint64(1000+n)), pbMsg(3, pbBytes(1, sum[:]), pbBytes(2, data))))
		n++
		return sum[:]
	}
	var ids [][]byte
	for _, m := range msgs {
		data, _ := json.Marshal(m)
		ids = append(ids, setBlob(data))
	}
	// The structured turn is what cursor reads back to show a conversation:
	// ACP's session/load replays it, and without it a resumed session
	// replays nothing. ConversationTurnStructure { 1 agent_conversation_turn:
	// AgentConversationTurnStructure { 1 user_message, 2 steps } }, each a
	// blob id; UserMessage { 1 text, 2 message_id }; ConversationStep
	// { 1 assistant_message: AssistantMessage { 1 text } }. Tool steps are
	// left out: the mock's tool calls exist only as exec requests.
	if cs.userMsgID == "" {
		cs.userMsgID = uuid.NewString()
	}
	user := setBlob(bytes.Join([][]byte{pbString(1, cs.prompt), pbString(2, cs.userMsgID)}, nil))
	turnParts := [][]byte{pbBytes(1, user)}
	if answer != "" {
		turnParts = append(turnParts, pbBytes(2, setBlob(pbMsg(1, pbString(1, answer)))))
	}
	turn := setBlob(pbMsg(1, turnParts...))
	time.Sleep(300 * time.Millisecond)
	var state [][]byte
	// A checkpoint is the whole conversation, so the history the client
	// arrived with comes first.
	for _, id := range append(append([][]byte{}, cs.priorRoot...), ids...) {
		state = append(state, pbBytes(1, id)) // root_prompt_messages_json, repeated
	}
	for _, id := range append(append([][]byte{}, cs.priorTurns...), turn) {
		state = append(state, pbBytes(8, id)) // turns, repeated
	}
	if pending != "" {
		state = append(state, pbString(4, pending)) // pending_tool_calls, repeated
	}
	cs.send(pbMsg(3, state...)) // AgentServerMessage.conversation_checkpoint_update
}
