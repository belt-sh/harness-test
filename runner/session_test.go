package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inference-sh/agentprotocol/harness"
	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/acp"
)

// ACP session/request_permission must be answered with
// {"outcome":{"outcome":"selected","optionId":...}}. The driver used to send
// {"outcome":"approved"}; gemini validates the reply with a schema, rejected it,
// and failed every tool call, which the registry then mistook for gemini
// running no tool hooks over ACP. The nesting now comes from the library, so
// this test holds the suite's choice of option and the shape it serialises to.
func TestPermissionReplyFollowsSpec(t *testing.T) {
	cases := []struct {
		name    string
		options string
		want    string // expected optionId, or "" for cancelled
	}{
		{"prefers allow_once", `[{"optionId":"no","kind":"reject_once"},{"optionId":"always","kind":"allow_always"},{"optionId":"once","kind":"allow_once"}]`, "once"},
		{"falls back to allow_always", `[{"optionId":"no","kind":"reject_once"},{"optionId":"always","kind":"allow_always"}]`, "always"},
		{"gemini's proceed_once", `[{"optionId":"proceed_once","kind":"allow_once"},{"optionId":"cancel","kind":"reject_once"}]`, "proceed_once"},
		{"unknown kinds are approved", `[{"optionId":"yes","kind":"weird_kind"}]`, "yes"},
		{"an unknown kind that reads as a refusal is passed over", `[{"optionId":"no","kind":"weird_reject"},{"optionId":"yes","kind":"weird_kind"}]`, "yes"},
		{"only refusals cancels rather than guessing", `[{"optionId":"no","kind":"weird_decline"}]`, ""},
		{"no options cancels", `[]`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req acp.PermissionRequest
			if err := json.Unmarshal([]byte(`{"options":`+c.options+`}`), &req); err != nil {
				t.Fatal(err)
			}
			d := NewACPDriver("agent", nil, t.TempDir(), nil)
			res, err := d.handler().OnPermission(context.Background(), req)
			if err != nil {
				t.Fatalf("OnPermission: %v", err)
			}
			data, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatalf("reply is not the spec shape: %v\n%s", err, data)
			}
			if c.want == "" {
				if wire.Outcome.Outcome != "cancelled" {
					t.Errorf("outcome = %q, want cancelled: %s", wire.Outcome.Outcome, data)
				}
				return
			}
			if wire.Outcome.Outcome != "selected" || wire.Outcome.OptionID != c.want {
				t.Errorf("outcome = %+v, want selected/%s: %s", wire.Outcome, c.want, data)
			}
		})
	}
}

type fakeAgentOpts struct {
	errorOnClose   bool
	askPermission  bool
	refuseLoadOnce string // marker path: refuse the first session/load, accept later ones
}

func withErrorOnClose(o *fakeAgentOpts) { o.errorOnClose = true }

// withPermissionRequest makes the agent ask before it does anything else in
// the turn, so a client that holds the answer holds the whole turn.
func withPermissionRequest(o *fakeAgentOpts) { o.askPermission = true }

// withRefusedFirstLoad models the agent this probe was rebuilt for: one whose
// session is not loadable the instant its process ends, and which answers the
// first session/load with an error and later ones with the conversation.
func withRefusedFirstLoad(marker string) func(*fakeAgentOpts) {
	return func(o *fakeAgentOpts) { o.refuseLoadOnce = marker }
}

// fakeACPAgent writes a shell agent that speaks just enough ACP to answer a
// turn: it replies to initialize and session/new, and on session/prompt streams
// one content chunk, then a turn_complete update, then the prompt response.
// Driving a real process keeps the test honest about the parts this file still
// owns — accumulating updates, matching patterns, ending the wait.
func fakeACPAgent(t *testing.T, replyToPrompt bool, opts ...func(*fakeAgentOpts)) string {
	t.Helper()
	cfg := fakeAgentOpts{}
	for _, o := range opts {
		o(&cfg)
	}
	closeReply := "      exit 0\n"
	if cfg.errorOnClose {
		// kiro-cli 2.21.4 answers the session/close notification with an
		// error carrying no id, which is legal JSON-RPC and which crashed
		// the client before agentprotocol v0.2.1.
		closeReply = `      printf '%s\n' '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found","data":"session/close"}}'
      exit 0
`
	}
	prompt := `      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CODENAME-OK"}}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"turn_complete"}}}'
`
	if cfg.askPermission {
		ask := `      printf '%s\n' '{"jsonrpc":"2.0","id":9001,"method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"tc-1","title":"Read README.md"},"options":[{"optionId":"no","kind":"reject_once"},{"optionId":"once","kind":"allow_once"}]}}'
`
		prompt = `      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
` + ask + strings.TrimPrefix(prompt, `      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
`)
	}
	if replyToPrompt {
		prompt += `      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
`
	}
	load := ""
	if cfg.refuseLoadOnce != "" {
		load = `    *'"method":"session/load"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      if [ -f ` + cfg.refuseLoadOnce + ` ]; then
        printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"the earlier turn"}}}}'
        printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      else
        : > ` + cfg.refuseLoadOnce + `
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error"}}\n' "$id"
      fi
      ;;
`
	}
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
` + load + `    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1"}}\n' "$id"
      ;;
    *'"method":"session/close"'*)
` + closeReply + `      ;;
    *'"method":"session/prompt"'*)
` + prompt + `      ;;
  esac
done
`
	path := filepath.Join(t.TempDir(), "fake-agent.sh")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestACPDriverRunsATurn(t *testing.T) {
	d := NewACPDriver("sh", []string{fakeACPAgent(t, true)}, t.TempDir(), os.Environ())
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Close()

	if err := d.SendPrompt("What is the project codename?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	out, err := d.WaitForResponse([]string{"CODENAME-OK"}, 10*time.Second)
	if err != nil {
		t.Fatalf("wait: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CODENAME-OK") {
		t.Errorf("agent text missing from output:\n%s", out)
	}
	if !strings.Contains(out, "[acp] session: s1") {
		t.Errorf("session id missing from output:\n%s", out)
	}
}

// An agent that ends the turn without answering session/prompt must not hang
// the suite: the turn-done update still releases the wait. The library's
// Prompt call blocks on the response, so this is the case worth pinning —
// it is how the driver behaved before the library, and it is what would
// otherwise turn a library bug into a suite-wide timeout.
func TestACPDriverSurvivesUnansweredPrompt(t *testing.T) {
	d := NewACPDriver("sh", []string{fakeACPAgent(t, false)}, t.TempDir(), os.Environ())
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Close()

	if err := d.SendPrompt("What is the project codename?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	out, err := d.WaitForResponse([]string{"CODENAME-OK"}, 10*time.Second)
	if err != nil {
		t.Fatalf("wait: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CODENAME-OK") {
		t.Errorf("agent text missing from output:\n%s", out)
	}
}

// An agent may report an error it cannot attribute to any request. kiro does
// exactly this for session/close. The library used to route that line into
// handleRequest and panic on its nil id; now it is delivered, and the driver
// records it so an agent's refusal shows up as a trait rather than silence.
func TestACPDriverRecordsUnattributedError(t *testing.T) {
	d := NewACPDriver("sh", []string{fakeACPAgent(t, true, withErrorOnClose)}, t.TempDir(), os.Environ())
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := d.SendPrompt("hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.WaitForResponse([]string{"CODENAME-OK"}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	d.Close()

	out := d.Output()
	if !strings.Contains(out, "unattributed error") || !strings.Contains(out, "-32601") {
		t.Errorf("the agent's error should be recorded:\n%s", out)
	}
}

// Parking is how this suite manufactures a tool call in flight: hold the
// approval, kill the client, and see what the agent says when a new process
// attaches to the session.
//
// Holding one answer must not hold the connection. Until agentprotocol v0.4.0
// it did — agent-initiated requests were answered on the read loop, so a
// blocked handler stopped every update and every pending reply behind it, and
// a human taking a minute over an approval froze the session for that minute.
// This pins the fix from the client's side: the turn's own text keeps arriving
// while the answer is held.
func TestACPDriverParksAPermissionRequest(t *testing.T) {
	d := NewACPDriver("sh", []string{fakeACPAgent(t, true, withPermissionRequest)}, t.TempDir(), os.Environ())
	d.ParkPermission = true
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := d.SendPrompt("read the readme"); err != nil {
		t.Fatal(err)
	}
	if !d.WaitParked(10 * time.Second) {
		t.Fatalf("the request was never parked:\n%s", d.Output())
	}

	seen := d.Permissions()
	if len(seen) != 1 {
		t.Fatalf("permissions = %+v, want exactly the parked one", seen)
	}
	if seen[0].ToolCallID != "tc-1" || seen[0].Answer != "parked" {
		t.Errorf("parked observation = %+v, want tc-1 parked", seen[0])
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(d.Output(), "CODENAME-OK") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(d.Output(), "CODENAME-OK") {
		t.Errorf("holding one approval stopped the rest of the connection:\n%s", d.Output())
	}

	// Kill must return rather than wait on the handler it is about to strand.
	done := make(chan struct{})
	go func() { d.Kill(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Kill blocked on the parked handler")
	}
}

// Not parking: the request is answered and recorded, with the agent's own
// toolCallId, which is what lets a resume be correlated to the call it
// interrupted.
func TestACPDriverRecordsAnAnsweredPermission(t *testing.T) {
	d := NewACPDriver("sh", []string{fakeACPAgent(t, true, withPermissionRequest)}, t.TempDir(), os.Environ())
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Close()

	if err := d.SendPrompt("read the readme"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.WaitForResponse([]string{"CODENAME-OK"}, 10*time.Second); err != nil {
		t.Fatalf("wait: %v\n%s", err, d.Output())
	}
	// Since v0.4.0 the approval is answered on its own goroutine, so it races
	// the turn's text rather than preceding it. Both orders are correct; only
	// a request that never arrives is a bug.
	var seen []PermissionObservation
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if seen = d.Permissions(); len(seen) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(seen) != 1 || seen[0].ToolCallID != "tc-1" || seen[0].Answer != "approved" {
		t.Fatalf("permissions = %+v, want tc-1 approved", seen)
	}
	if seen[0].DuringLoad {
		t.Error("a live request was marked DuringLoad")
	}
}

// A probe that attempts session/load once, whenever the phase happens to reach
// it, cannot tell an agent that will not resume from one that had not finished
// writing its session yet. That mistake recorded gemini as refusing
// session/load for weeks. The probe now retries on a schedule and reports the
// wait, and this is the agent that distinguishes the two: it refuses the first
// load and serves the conversation on the next.
func TestResumeRetriesUntilTheSessionIsWritten(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "written")
	agent := fakeACPAgent(t, true, withRefusedFirstLoad(marker))

	r := &TestRunner{
		harness:     harness.All["goose"],
		testEntries: []server.LogEntry{},
	}
	r.attemptResume("s1", "sh", []string{agent}, dir, "closed", time.Now(), nil)

	// One pass for the load, one for having needed the wait, one for the
	// replayed user turn. A single-attempt probe would have skipped instead.
	if r.result.Passed < 3 {
		t.Errorf("passed=%d skipped=%d, want the retry to resume and say it needed the wait",
			r.result.Passed, r.result.Skipped)
	}
}
