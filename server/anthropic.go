package server

import (
	"encoding/json"
	"net/http"
)

func (s *MockServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	req, _ := s.parseRequest(r)
	model := req.modelOrDefault()

	if s.shouldToolCall(req.hasTools(), r.URL.Path) {
		s.anthropicToolCall(w, model, req.Stream)
		return
	}

	text := s.getResponse()
	if req.Stream {
		streamSSEEvents(w, anthropicTextEvents(model, text))
		return
	}

	writeJSON(w, AnthropicMessage{
		ID: "mock-1", Type: "message", Role: "assistant", Model: model,
		Content:    []ContentBlock{{Type: "text", Text: text}},
		StopReason: "end_turn",
		Usage:      &AntUsage{InputTokens: 10, OutputTokens: 5},
	})
}

func (s *MockServer) anthropicToolCall(w http.ResponseWriter, model string, stream bool) {
	name, args := s.getToolCall()
	var input any
	json.Unmarshal([]byte(args), &input)

	block := ContentBlock{Type: "tool_use", ID: "toolu_mock_1", Name: name, Input: input}

	if stream {
		streamSSEEvents(w, []sseEvent{
			{"message_start", map[string]any{
				"type":    "message_start",
				"message": AnthropicMessage{ID: "mock-tc", Type: "message", Role: "assistant", Model: model},
			}},
			{"content_block_start", map[string]any{
				"type": "content_block_start", "index": 0,
				"content_block": ContentBlock{Type: "tool_use", ID: "toolu_mock_1", Name: name},
			}},
			{"content_block_delta", map[string]any{
				"type": "content_block_delta", "index": 0,
				"delta": map[string]string{"type": "input_json_delta", "partial_json": args},
			}},
			{"content_block_stop", map[string]any{
				"type": "content_block_stop", "index": 0,
			}},
			{"message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]string{"stop_reason": "tool_use"},
				"usage": AntUsage{OutputTokens: 5},
			}},
			{"message_stop", map[string]any{"type": "message_stop"}},
		})
		return
	}

	writeJSON(w, AnthropicMessage{
		ID: "mock-tc", Type: "message", Role: "assistant", Model: model,
		Content:    []ContentBlock{block},
		StopReason: "tool_use",
		Usage:      &AntUsage{InputTokens: 10, OutputTokens: 5},
	})
}

func anthropicTextEvents(model, text string) []sseEvent {
	return []sseEvent{
		{"message_start", map[string]any{
			"type":    "message_start",
			"message": AnthropicMessage{ID: "mock-1", Type: "message", Role: "assistant", Model: model},
		}},
		{"content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": ContentBlock{Type: "text"},
		}},
		{"content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]string{"type": "text_delta", "text": text},
		}},
		{"content_block_stop", map[string]any{
			"type": "content_block_stop", "index": 0,
		}},
		{"message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": AntUsage{OutputTokens: 5},
		}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}
}
