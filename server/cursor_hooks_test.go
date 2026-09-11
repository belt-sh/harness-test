package server

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"
)

// readFrame takes one connect frame off the session and returns its message.
func readFrame(t *testing.T, cs *cursorSession) []byte {
	t.Helper()
	select {
	case fr := <-cs.frames:
		if len(fr) < 5 {
			t.Fatalf("short frame")
		}
		return fr[5:]
	case <-time.After(time.Second):
		t.Fatal("no frame sent")
	}
	return nil
}

// A hook request must be AgentServerMessage.exec_server_message (2) carrying
// execute_hook_args (27) { request (1): ExecuteHookRequest { <hook>: ... } },
// the shape the Cursor CLI dispatches to its hook executor.
func TestCursorHookRequestShape(t *testing.T) {
	s := &MockServer{}
	cs := &cursorSession{frames: make(chan []byte, 4), closed: make(chan struct{})}
	s.cursorHook(cs, "hook:stop", cursorHookStop, pbMsg(cursorHookStop, pbString(1, "completed")))
	msg := readFrame(t, cs)
	status, ok := pbPath(msg, 2, 27, 1, cursorHookStop, 1)
	if !ok || string(status) != "completed" {
		t.Fatalf("stop request not at 2.27.1.11: %v", pbToJSON(msg, 0))
	}
	if cs.pending == 0 || cs.stage != "hook:stop" {
		t.Errorf("session not waiting on the request: pending=%d stage=%q", cs.pending, cs.stage)
	}
}

// Only the hook the runner asked for is requested.
func TestCursorRequestsOnlyConfiguredHooks(t *testing.T) {
	s := &MockServer{}
	s.SetRequestedHooks([]string{"STOP"})
	if !s.requestsHook("STOP") || s.requestsHook("PROMPT") || s.requestsHook("PRE_COMPACT") {
		t.Errorf("requested set = %v", s.requestedHooks)
	}
}

// A reply to an earlier request must not advance a later stage. The 4-second
// fallback can move the session on, and the real reply then arriving late used
// to advance it again, skipping the compaction step under belt's slower hooks.
func TestCursorLateReplyDoesNotAdvance(t *testing.T) {
	s := New()
	cs := s.cursorSession("req-1")
	cs.stage = "hook:stop"
	cs.pending = 5
	reply := func(id uint64) {
		body := append(pbMsg(2, pbString(1, "req-1")), pbBytes(4, pbMsg(2, pbUint(1, id)))...)
		req := httptest.NewRequest("POST", "/aiserver.v1.BidiService/BidiAppend", bytes.NewReader(body))
		s.handleCursorBidiAppend(httptest.NewRecorder(), req)
	}
	reply(4) // stale: answers the request before the current one
	if cs.stage != "hook:stop" || cs.pending != 5 {
		t.Fatalf("stale reply advanced the session: stage=%q pending=%d", cs.stage, cs.pending)
	}
	reply(5)
	if cs.stage != "done" {
		t.Fatalf("current reply did not advance: stage=%q", cs.stage)
	}
}
