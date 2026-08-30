package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *MockServer) parseBedrockRequest(r *http.Request) (string, bool) {
	_, body := s.parseRequest(r)
	var conv bedrockConverseRequest
	json.Unmarshal(body, &conv)
	return conv.ModelID, len(conv.ToolConfig.Tools) > 0
}

func (s *MockServer) handleBedrockConverse(w http.ResponseWriter, r *http.Request) {
	_, hasTools := s.parseBedrockRequest(r)

	if s.shouldToolCall(hasTools, r.URL.Path) {
		s.bedrockToolCall(w)
		return
	}

	writeJSON(w, bedrockConverseResponse{
		Output: bedrockOutput{
			Message: &bedrockMessage{
				Role:    "assistant",
				Content: []bedrockContent{{Text: s.getResponse()}},
			},
		},
		StopReason: "end_turn",
		Usage:      bedrockUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})
}

func (s *MockServer) handleBedrockConverseStream(w http.ResponseWriter, r *http.Request) {
	_, hasTools := s.parseBedrockRequest(r)

	if s.shouldToolCall(hasTools, r.URL.Path) {
		s.bedrockToolCallStream(w)
		return
	}

	s.bedrockStream(w, s.getResponse())
}

func (s *MockServer) bedrockStream(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.WriteHeader(200)

	writeBedrockEvent(w, "messageStart", map[string]any{
		"role": "assistant",
	})
	writeBedrockEvent(w, "contentBlockStart", map[string]any{
		"contentBlockIndex": 0,
		"start":            map[string]any{"text": ""},
	})
	writeBedrockEvent(w, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0,
		"delta":            map[string]any{"text": text},
	})
	writeBedrockEvent(w, "contentBlockStop", map[string]any{
		"contentBlockIndex": 0,
	})
	writeBedrockEvent(w, "messageStop", map[string]any{
		"stopReason": "end_turn",
	})
	writeBedrockEvent(w, "metadata", map[string]any{
		"usage":   map[string]any{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15},
		"metrics": map[string]any{"latencyMs": 100},
	})
}

func (s *MockServer) bedrockToolCall(w http.ResponseWriter) {
	name, input := s.getToolCallParsed()

	writeJSON(w, bedrockConverseResponse{
		Output: bedrockOutput{
			Message: &bedrockMessage{
				Role: "assistant",
				Content: []bedrockContent{{
					ToolUse: &bedrockToolUse{
						ToolUseId: "tooluse_mock_1",
						Name:      name,
						Input:     input,
					},
				}},
			},
		},
		StopReason: "tool_use",
		Usage:      bedrockUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})
}

func (s *MockServer) bedrockToolCallStream(w http.ResponseWriter) {
	name, args := s.getToolCall()
	var input any
	json.Unmarshal([]byte(args), &input)

	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.WriteHeader(200)

	writeBedrockEvent(w, "messageStart", map[string]any{"role": "assistant"})
	writeBedrockEvent(w, "contentBlockStart", map[string]any{
		"contentBlockIndex": 0,
		"start": map[string]any{
			"toolUse": map[string]any{
				"toolUseId": "tooluse_mock_1",
				"name":      name,
			},
		},
	})
	writeBedrockEvent(w, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0,
		"delta":            map[string]any{"toolUse": map[string]any{"input": args}},
	})
	writeBedrockEvent(w, "contentBlockStop", map[string]any{"contentBlockIndex": 0})
	writeBedrockEvent(w, "messageStop", map[string]any{"stopReason": "tool_use"})
	writeBedrockEvent(w, "metadata", map[string]any{
		"usage":   map[string]any{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15},
		"metrics": map[string]any{"latencyMs": 100},
	})
}

func writeBedrockEvent(w http.ResponseWriter, eventType string, payload any) {
	data := mustJSON(payload)
	fmt.Fprintf(w, ":event-type %s\n%s\n", eventType, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// Bedrock Converse types

type bedrockConverseRequest struct {
	ModelID    string            `json:"modelId"`
	Messages   []bedrockMessage  `json:"messages"`
	System     []bedrockContent  `json:"system,omitempty"`
	ToolConfig bedrockToolConfig `json:"toolConfig,omitempty"`
}

type bedrockMessage struct {
	Role    string           `json:"role"`
	Content []bedrockContent `json:"content"`
}

type bedrockContent struct {
	Text      string           `json:"text,omitempty"`
	ToolUse   *bedrockToolUse  `json:"toolUse,omitempty"`
	ToolResult *bedrockToolResult `json:"toolResult,omitempty"`
}

type bedrockToolUse struct {
	ToolUseId string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     any    `json:"input"`
}

type bedrockToolResult struct {
	ToolUseId string           `json:"toolUseId"`
	Content   []bedrockContent `json:"content"`
	Status    string           `json:"status"`
}

type bedrockToolConfig struct {
	Tools []any `json:"tools"`
}

type bedrockConverseResponse struct {
	Output     bedrockOutput `json:"output"`
	StopReason string        `json:"stopReason"`
	Usage      bedrockUsage  `json:"usage"`
}

type bedrockOutput struct {
	Message *bedrockMessage `json:"message,omitempty"`
}

type bedrockUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}
