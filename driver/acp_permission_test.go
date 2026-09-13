package driver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

type fakeAgentOpts struct{ errorOnClose bool }

func withErrorOnClose(o *fakeAgentOpts) { o.errorOnClose = true }

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
	if replyToPrompt {
		prompt += `      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
`
	}
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
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
