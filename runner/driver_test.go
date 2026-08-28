package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestACPRequestFormat(t *testing.T) {
	req := acpRequest{
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
	if !strings.Contains(s, `"protocolVersion"`) {
		t.Error("missing protocolVersion in params")
	}
}

func TestACPResponseParsing(t *testing.T) {
	// Response with ID (request reply)
	respJSON := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`
	var resp acpResponse
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Error("expected ID=1")
	}
	if resp.Result == nil {
		t.Error("expected non-nil result")
	}

	// Notification (no ID)
	notifJSON := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"kind":"agent_message_chunk","text":"hello"}}}`
	var notif acpResponse
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

func TestExtractUpdateText(t *testing.T) {
	tests := []struct {
		name string
		upd  acpUpdate
		want string
	}{
		{
			name: "text field",
			upd:  acpUpdate{Update: struct {
				Kind    string `json:"kind"`
				Content string `json:"content,omitempty"`
				Text    string `json:"text,omitempty"`
				Message *struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text,omitempty"`
					} `json:"content,omitempty"`
				} `json:"message,omitempty"`
			}{Text: "hello"}},
			want: "hello",
		},
		{
			name: "content field",
			upd:  acpUpdate{Update: struct {
				Kind    string `json:"kind"`
				Content string `json:"content,omitempty"`
				Text    string `json:"text,omitempty"`
				Message *struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text,omitempty"`
					} `json:"content,omitempty"`
				} `json:"message,omitempty"`
			}{Content: "world"}},
			want: "world",
		},
		{
			name: "empty",
			upd:  acpUpdate{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUpdateText(tt.upd)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDriverInterface(t *testing.T) {
	// Verify Driver interface is implementable
	var _ Driver = (*ACPDriver)(nil)
	_ = time.Second // used by Driver.WaitForResponse
}
