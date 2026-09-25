package server

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// The flags are read at the field numbers the CLI sends them on, absent
// fields read as their kind says, and a reply to request_context_args is
// read the same way as a context carried by the run request.
func TestCursorClientFlags(t *testing.T) {
	ctx := bytes.Join([][]byte{
		pbMsg(7, pbString(1, "mcp_a")), pbMsg(7, pbString(1, "mcp_b")),
		pbBool(24, true), pbBool(35, false), pbMsg(28),
		pbMsg(4, pbBool(5, true)),
	}, nil)
	run := bytes.Join([][]byte{
		pbMsg(2, pbMsg(1, pbMsg(2, ctx))),
		pbString(13, "cli"), pbBool(19, true), pbMsg(4, pbMsg(1), pbMsg(1), pbMsg(1)),
	}, nil)
	got := cursorClientFlags(pbMsg(1, run))
	want := map[string]string{
		"run_request.harness":                       "cli",
		"run_request.client_supports_inline_images": "true",
		"run_request.can_create_cloud_subagents":    "unset",
		"run_request.system_prompt_spec":            "unset",
		"run_request.mcp_tools":                     "3",
		"request_context.tools":                     "2",
		"request_context.web_fetch_enabled":         "true",
		"request_context.web_search_enabled":        "unset",
		"request_context.read_lints_enabled":        "false",
		"request_context.hooks_config":              "set",
		"request_context.env.sandbox_enabled":       "true",
		"request_context.env.sandbox_supported":     "unset",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	reply := cursorClientFlags(pbMsg(2, pbUint(1, 3), pbMsg(10, pbMsg(1, pbMsg(1, ctx)))))
	if reply["request_context.tools"] != "2" || reply["run_request.harness"] != "" {
		t.Errorf("request_context_result read as %v", reply)
	}
	if cursorClientFlags(pbMsg(5, pbMsg(1))) != nil {
		t.Error("a control message carries no flags")
	}
}

// The mock logs the flags with the message, and CursorClientFlags keeps the
// first value seen.
func TestCursorClientFlagsLogged(t *testing.T) {
	s := New()
	send := func(msg []byte) {
		body := append(pbMsg(2, pbString(1, "req-1")), pbBytes(4, msg)...)
		s.handleCursorBidiAppend(httptest.NewRecorder(), httptest.NewRequest("POST", "/aiserver.v1.BidiService/BidiAppend", bytes.NewReader(body)))
	}
	send(pbMsg(2, pbUint(1, 99), pbMsg(10, pbMsg(1, pbMsg(1, pbBool(17, true))))))
	send(pbMsg(2, pbUint(1, 98), pbMsg(10, pbMsg(1, pbMsg(1, pbBool(17, false))))))
	if got := s.CursorClientFlags()["request_context.web_search_enabled"]; got != "true" {
		t.Errorf("web_search_enabled = %q, want the first value, true", got)
	}
}
