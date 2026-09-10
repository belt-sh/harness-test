package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	Tools             []any           `json:"tools,omitempty"`
	GenerationConfig  map[string]any  `json:"generationConfig,omitempty"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     *geminiFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResp `json:"functionResponse,omitempty"`
}

type geminiFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (s *MockServer) handleGemini(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req geminiRequest
	json.Unmarshal(body, &req)

	model := "gemini-2.5-flash"
	if parts := strings.Split(r.URL.Path, "/"); len(parts) >= 4 {
		model = strings.TrimSuffix(parts[3], ":streamGenerateContent")
		model = strings.TrimSuffix(model, ":generateContent")
	}
	s.record(r, body, model)

	hasTools := len(req.Tools) > 0
	if s.shouldToolCall(hasTools && s.bodyOffersTool(body), r.URL.Path) {
		s.geminiToolCall(w, model)
		return
	}

	// Structured output: gemini's model router asks for JSON against a schema
	// and re-routes the whole turn when the answer does not parse, which loses
	// the prepared tool call and with it the tool hooks. Answer the schema.
	text := s.getResponse()
	if mime, _ := req.GenerationConfig["responseMimeType"].(string); mime == "application/json" {
		text = `{"choice":0,"confidence":1.0}`
		for _, key := range []string{"responseJsonSchema", "responseSchema"} {
			if schema, ok := req.GenerationConfig[key].(map[string]any); ok {
				if b, err := json.Marshal(synthFromSchema(schema)); err == nil {
					text = string(b)
				}
				break
			}
		}
	}

	if strings.Contains(r.URL.RawQuery, "alt=sse") {
		s.geminiStream(w, model, text)
		return
	}

	writeJSON(w, geminiResponse{
		Candidates: []geminiCandidate{{
			Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: text}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &geminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
		ModelVersion:  model,
	})
}

func streamDataOnly(w http.ResponseWriter, events []any) {
	f := beginSSE(w)
	for _, data := range events {
		streamData(w, f, data)
	}
}

func (s *MockServer) geminiStream(w http.ResponseWriter, model, text string) {
	streamDataOnly(w, []any{
		geminiResponse{
			Candidates:   []geminiCandidate{{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: text}}}}},
			ModelVersion: model,
		},
		geminiResponse{
			Candidates:    []geminiCandidate{{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: ""}}}, FinishReason: "STOP"}},
			UsageMetadata: &geminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
			ModelVersion:  model,
		},
	})
}

func (s *MockServer) geminiToolCall(w http.ResponseWriter, model string) {
	name, parsed := s.getToolCallParsed()
	args, _ := parsed.(map[string]any)
	fc := geminiPart{FunctionCall: &geminiFuncCall{Name: name, Args: args}}
	streamDataOnly(w, []any{
		geminiResponse{
			Candidates:   []geminiCandidate{{Content: geminiContent{Role: "model", Parts: []geminiPart{fc}}}},
			ModelVersion: model,
		},
		geminiResponse{
			Candidates:    []geminiCandidate{{Content: geminiContent{Role: "model", Parts: []geminiPart{fc}}, FinishReason: "STOP"}},
			UsageMetadata: &geminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 12, TotalTokenCount: 22},
			ModelVersion:  model,
		},
	})
}

// synthFromSchema builds the smallest value satisfying a JSON schema, so the
// mock can answer a structured-output request instead of returning prose.
func synthFromSchema(schema map[string]any) any {
	t, _ := schema["type"].(string)
	switch strings.ToUpper(t) {
	case "OBJECT":
		out := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		for name, raw := range props {
			if sub, ok := raw.(map[string]any); ok {
				out[name] = synthFromSchema(sub)
			}
		}
		return out
	case "ARRAY":
		if items, ok := schema["items"].(map[string]any); ok {
			return []any{synthFromSchema(items)}
		}
		return []any{}
	case "INTEGER", "NUMBER":
		// Deliberately high. The one structured request agents make of a mock
		// is a routing or complexity score, and a low answer sends the turn to
		// a cheaper model with a smaller toolset — gemini then rejects the
		// prepared tool call as "Tool not found" and no tool hook can fire.
		// A mock must not decide the agent under test into a degraded path.
		if max, ok := schema["maximum"].(float64); ok {
			return max
		}
		return 100
	case "BOOLEAN":
		return true
	default:
		if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
			return enum[0]
		}
		return "mock"
	}
}
