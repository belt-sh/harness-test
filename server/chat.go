package server

import (
	"fmt"
	"net/http"
	"time"
)

func newChat(id, object, model string, choices []ChatChoice, usage *Usage) ChatCompletion {
	return ChatCompletion{
		ID: id, Object: object, Created: time.Now().Unix(),
		Model: model, Choices: choices, Usage: usage,
	}
}

func (s *MockServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	req, _ := s.parseRequest(r)
	model := req.modelOrDefault()

	if s.shouldToolCall(req.hasTools(), r.URL.Path) {
		s.chatToolCall(w, model, req.Stream)
		return
	}

	text := s.getResponse()
	if req.Stream {
		s.chatStream(w, model, text)
		return
	}

	writeJSON(w, newChat("mock-1", "chat.completion", model,
		[]ChatChoice{{
			Message:      &ChatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		&Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	))
}

func (s *MockServer) chatStream(w http.ResponseWriter, model, text string) {
	f := beginSSE(w)
	streamData(w, f, newChat("mock-1", "chat.completion.chunk", model,
		[]ChatChoice{{Delta: &ChatMessage{Role: "assistant", Content: text}}}, nil))
	streamData(w, f, newChat("mock-1", "chat.completion.chunk", model,
		[]ChatChoice{{Delta: &ChatMessage{}, FinishReason: "stop"}}, nil))
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f != nil {
		f.Flush()
	}
}

func (s *MockServer) chatToolCall(w http.ResponseWriter, model string, stream bool) {
	name, args := s.getToolCall()
	tc := ToolCall{
		Index:    0,
		ID:       "call_mock_1",
		Type:     "function",
		Function: FunctionCall{Name: name, Arguments: args},
	}

	if stream {
		f := beginSSE(w)
		streamData(w, f, newChat("mock-tc", "chat.completion.chunk", model,
			[]ChatChoice{{Delta: &ChatMessage{Role: "assistant", ToolCalls: []ToolCall{tc}}}}, nil))
		streamData(w, f, newChat("mock-tc", "chat.completion.chunk", model,
			[]ChatChoice{{Delta: &ChatMessage{}, FinishReason: "tool_calls"}}, nil))
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f != nil {
			f.Flush()
		}
		return
	}

	writeJSON(w, newChat("mock-tc", "chat.completion", model,
		[]ChatChoice{{
			Message:      &ChatMessage{Role: "assistant", ToolCalls: []ToolCall{tc}},
			FinishReason: "tool_calls",
		}},
		&Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	))
}
