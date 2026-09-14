package server

import (
	"net/http"
	"time"
)

func newResponse(id, status string, output []ResponseItem, usage *Usage) ResponseObject {
	ts := time.Now().Unix()
	if output == nil {
		output = []ResponseItem{} // clients (grok) reject "output": null
	}
	if usage != nil && usage.InputTokensDetails == nil {
		usage.InputTokensDetails = &TokenDetails{}
		usage.OutputTokensDetails = &TokenDetails{}
	}
	return ResponseObject{
		ID: id, Object: "response", Ts: ts, TsAlt: ts, Model: "mock-model",
		Status: status, Output: output, Usage: usage,
	}
}

func (s *MockServer) handleResponses(w http.ResponseWriter, r *http.Request) {
	req, body, idx := s.parseRequest(r)
	if req.Stream {
		s.markStreamed(idx)
	}

	// Side calls that ask for one specific function (grok's session_title)
	// get that function call, the way the real backend answers them.
	if name := req.singleFunction(); name != "" && name != s.currentToolName() {
		s.responsesFunctionCall(w, name, `{"`+name+`":"Mock value"}`)
		return
	}

	if s.shouldToolCall(req.hasTools() && s.bodyOffersTool(body), r.URL.Path) {
		name, args := s.getToolCall()
		s.responsesFunctionCall(w, name, args)
		return
	}

	s.responsesText(w, s.getResponse())
}

func (s *MockServer) currentToolName() string {
	name, _ := s.getToolCall()
	return name
}

func (s *MockServer) responsesText(w http.ResponseWriter, text string) {
	part := ResponsePart{Type: "output_text", Text: text, Annotations: []any{}}
	msg := ResponseItem{Type: "message", ID: "msg_mock_1", Status: "completed", Role: "assistant", Content: []ResponsePart{part}}
	emptyPart := ResponsePart{Type: "output_text", Annotations: []any{}}
	emptyMsg := ResponseItem{Type: "message", ID: "msg_mock_1", Status: "in_progress", Role: "assistant", Content: []ResponsePart{emptyPart}}

	events := []sseEvent{
		typed("response.created", map[string]any{
			"response": newResponse("mock-resp-1", "in_progress", nil, nil),
		}),
		typed("response.output_item.added", map[string]any{
			"output_index": 0, "item": emptyMsg,
		}),
		typed("response.content_part.added", map[string]any{
			"item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "part": emptyPart,
		}),
		typed("response.output_text.delta", map[string]any{
			"item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "delta": text,
		}),
		typed("response.output_text.done", map[string]any{
			"item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "text": text,
		}),
		typed("response.content_part.done", map[string]any{
			"item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "part": part,
		}),
		typed("response.output_item.done", map[string]any{
			"item_id": "msg_mock_1", "output_index": 0, "item": msg,
		}),
		typed("response.completed", map[string]any{
			"response": newResponse("mock-resp-1", "completed", []ResponseItem{msg},
				&Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}),
		}),
	}
	streamSSEEvents(w, events)
}

func (s *MockServer) responsesFunctionCall(w http.ResponseWriter, name, args string) {
	empty := ""
	fc := ResponseItem{
		Type: "function_call", ID: "fc_mock_1", CallID: "call_mock_1",
		Name: name, Arguments: &empty, Status: "in_progress",
	}
	fcDone := ResponseItem{
		Type: "function_call", ID: "fc_mock_1", CallID: "call_mock_1",
		Name: name, Arguments: &args, Status: "completed",
	}

	events := []sseEvent{
		typed("response.created", map[string]any{
			"response": newResponse("mock-resp-tc", "in_progress", nil, nil),
		}),
		typed("response.output_item.added", map[string]any{
			"response_id": "mock-resp-tc", "output_index": 0, "item": fc,
		}),
		typed("response.function_call_arguments.delta", map[string]any{
			"response_id": "mock-resp-tc", "item_id": "fc_mock_1", "output_index": 0, "delta": args,
		}),
		typed("response.function_call_arguments.done", map[string]any{
			"response_id": "mock-resp-tc", "item_id": "fc_mock_1", "output_index": 0, "arguments": args,
		}),
		typed("response.output_item.done", map[string]any{
			"response_id": "mock-resp-tc", "output_index": 0, "item": fcDone,
		}),
		typed("response.completed", map[string]any{
			"response": newResponse("mock-resp-tc", "completed", []ResponseItem{fcDone},
				&Usage{InputTokens: 10, OutputTokens: 12, TotalTokens: 22}),
		}),
	}
	streamSSEEvents(w, events)
}
