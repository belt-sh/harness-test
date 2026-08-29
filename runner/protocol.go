package runner

import "encoding/json"

// JSON-RPC 2.0 base types used by ACP (Agent Client Protocol).

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ACP session/update notification payload.

type sessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

type sessionUpdate struct {
	Kind string `json:"sessionUpdate"` // discriminator: content_chunk, tool_call, usage

	Content   json.RawMessage `json:"content,omitempty"`
	MessageID string          `json:"messageId,omitempty"`

	ToolCallID string `json:"toolCallId,omitempty"`
	Status     string `json:"status,omitempty"`
	Title      string `json:"title,omitempty"`
}

// ACP filesystem request payloads.

type fsWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fsReadRequest struct {
	Path string `json:"path"`
}
