package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestACPRequestFormat(t *testing.T) {
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": 1,
			"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"jsonrpc":"2.0"`) {
		t.Error("missing jsonrpc field")
	}
	if !strings.Contains(s, `"method":"initialize"`) {
		t.Error("missing method field")
	}
}

func TestACPResponseParsing(t *testing.T) {
	respJSON := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`
	var resp rpcMessage
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Error("expected ID=1")
	}

	notifJSON := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"content_chunk","content":{"type":"text","text":"hello"}}}}`
	var notif rpcMessage
	if err := json.Unmarshal([]byte(notifJSON), &notif); err != nil {
		t.Fatal(err)
	}
	if notif.ID != nil {
		t.Error("notification should have no ID")
	}
	if notif.Method != "session/update" {
		t.Errorf("expected method=session/update, got %s", notif.Method)
	}
}

func TestACPSessionNewParams(t *testing.T) {
	params := map[string]any{
		"cwd":        "/tmp/test",
		"mcpServers": []any{},
	}
	data, _ := json.Marshal(params)
	s := string(data)
	if !strings.Contains(s, `"cwd"`) {
		t.Error("missing cwd")
	}
	if !strings.Contains(s, `"mcpServers"`) {
		t.Error("missing mcpServers")
	}
}

func TestACPPromptFormat(t *testing.T) {
	params := map[string]any{
		"sessionId": "test-session",
		"prompt": []map[string]any{
			{"type": "text", "text": "hello world"},
		},
	}
	data, _ := json.Marshal(params)
	s := string(data)
	if !strings.Contains(s, `"prompt"`) {
		t.Error("missing prompt field")
	}
	if strings.Contains(s, `"turns"`) {
		t.Error("should use prompt not turns")
	}
}

func TestACPPermissionResponse(t *testing.T) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"result":  map[string]any{"outcome": "approved"},
	}
	data, _ := json.Marshal(resp)
	if !strings.Contains(string(data), `"outcome":"approved"`) {
		t.Error("missing approved outcome")
	}
}

func TestACPExtractText(t *testing.T) {
	d := &ACPDriver{}
	tests := []struct {
		name string
		upd  sessionUpdate
		want string
	}{
		{
			name: "text content block",
			upd:  sessionUpdate{Content: json.RawMessage(`{"type":"text","text":"hello"}`)},
			want: "hello",
		},
		{
			name: "empty content",
			upd:  sessionUpdate{},
			want: "",
		},
		{
			name: "non-text block",
			upd:  sessionUpdate{Content: json.RawMessage(`{"type":"image","url":"x"}`)},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.extractText(tt.upd)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDriverInterface(t *testing.T) {
	var _ Driver = (*ACPDriver)(nil)
	_ = time.Second
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Error("long string should be truncated")
	}
}
