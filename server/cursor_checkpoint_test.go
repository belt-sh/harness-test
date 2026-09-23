package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
)

// cursor's transcript is built from a conversation checkpoint, not from the
// streamed text: it reads blob ids out of root_prompt_messages_json and fetches
// each from its own blob store, skipping any it cannot find. So every id the
// checkpoint names must have been stored first, by a kv set_blob sent before
// it. Get the order or the ids wrong and cursor writes nothing but
// turn_ended — silently, which is what it did before this existed.
func TestCursorCheckpointStoresBlobsBeforeNamingThem(t *testing.T) {
	s := &MockServer{}
	cs := &cursorSession{frames: make(chan []byte, 8), closed: make(chan struct{}), prompt: "what is the codename"}
	s.cursorCheckpoint(cs)

	stored := map[string][]byte{}
	var named [][]byte
	for len(cs.frames) > 0 {
		msg := readFrame(t, cs)
		if id, ok := pbPath(msg, 4, 3, 1); ok {
			data, _ := pbPath(msg, 4, 3, 2)
			if named != nil {
				t.Fatal("a blob was stored after the checkpoint that names it")
			}
			stored[string(id)] = data
			continue
		}
		fields, _ := pbDecode(msg)
		for _, f := range fields {
			if f.No != 3 {
				continue
			}
			inner, _ := pbDecode(f.Data)
			for _, g := range inner {
				if g.No == 1 {
					named = append(named, g.Data)
				}
			}
		}
	}

	if len(named) != 2 {
		t.Fatalf("checkpoint names %d messages, want the user turn and the answer", len(named))
	}
	for i, id := range named {
		data, ok := stored[string(id)]
		if !ok {
			t.Fatalf("message %d is named but was never stored; cursor would skip it", i)
		}
		if sum := sha256.Sum256(data); !bytes.Equal(sum[:], id) {
			t.Errorf("message %d id is not the sha256 of its content", i)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("message %d is not JSON: %v", i, err)
		}
		if want := []string{"user", "assistant"}[i]; m["role"] != want {
			t.Errorf("message %d role %v, want %s", i, m["role"], want)
		}
	}
}
