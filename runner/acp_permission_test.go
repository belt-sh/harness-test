package runner

import (
	"bytes"
	"encoding/json"
	"testing"
)

type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

// ACP session/request_permission must be answered with
// {"outcome":{"outcome":"selected","optionId":...}}. The driver used to send
// {"outcome":"approved"}; gemini validates the reply with a schema, rejected it,
// and failed every tool call, which the registry then mistook for gemini
// running no tool hooks over ACP.
func TestPermissionReplyFollowsSpec(t *testing.T) {
	cases := []struct {
		name    string
		options string
		want    string // expected optionId, or "" for cancelled
	}{
		{"prefers allow_once", `[{"optionId":"no","kind":"reject_once"},{"optionId":"always","kind":"allow_always"},{"optionId":"once","kind":"allow_once"}]`, "once"},
		{"falls back to allow_always", `[{"optionId":"no","kind":"reject_once"},{"optionId":"always","kind":"allow_always"}]`, "always"},
		{"gemini's proceed_once", `[{"optionId":"proceed_once","kind":"allow_once"},{"optionId":"cancel","kind":"reject_once"}]`, "proceed_once"},
		{"no options cancels", `[]`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			d := &ACPDriver{stdin: nopCloser{&out}}
			id := 7
			d.handlePermission(rpcMessage{ID: &id, Method: "session/request_permission", Params: json.RawMessage(`{"options":` + c.options + `}`)})
			var resp struct {
				ID     int `json:"id"`
				Result struct {
					Outcome struct {
						Outcome  string `json:"outcome"`
						OptionID string `json:"optionId"`
					} `json:"outcome"`
				} `json:"result"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
				t.Fatalf("reply is not the spec shape: %v\n%s", err, out.String())
			}
			if resp.ID != 7 {
				t.Errorf("id = %d", resp.ID)
			}
			if c.want == "" {
				if resp.Result.Outcome.Outcome != "cancelled" {
					t.Errorf("outcome = %q, want cancelled", resp.Result.Outcome.Outcome)
				}
				return
			}
			if resp.Result.Outcome.Outcome != "selected" || resp.Result.Outcome.OptionID != c.want {
				t.Errorf("outcome = %+v, want selected/%s", resp.Result.Outcome, c.want)
			}
		})
	}
}

// What the driver actually writes for session/prompt, read back off its stdin:
// the ACP method, the session id it was given, and the prompt as a text block.
func TestSendPromptWritesSpecRequest(t *testing.T) {
	var out bytes.Buffer
	d := &ACPDriver{stdin: nopCloser{&out}, sessionID: "sess-1"}
	if err := d.SendPrompt("hello world"); err != nil {
		t.Fatal(err)
	}
	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"prompt"`
		} `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &req); err != nil {
		t.Fatalf("not JSON-RPC: %v\n%s", err, out.String())
	}
	if req.JSONRPC != "2.0" || req.Method != "session/prompt" || req.ID == 0 {
		t.Errorf("envelope = %+v", req)
	}
	if req.Params.SessionID != "sess-1" {
		t.Errorf("sessionId = %q", req.Params.SessionID)
	}
	if len(req.Params.Prompt) != 1 || req.Params.Prompt[0].Type != "text" || req.Params.Prompt[0].Text != "hello world" {
		t.Errorf("prompt = %+v", req.Params.Prompt)
	}
}
