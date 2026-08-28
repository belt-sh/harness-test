package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const DefaultToolName = "Read"
const DefaultToolArgs = `{"file_path":"README.md"}`

type LogEntry struct {
	Timestamp time.Time         `json:"ts"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
	Model     string            `json:"model,omitempty"`
}

type MockServer struct {
	srv      *http.Server
	listener net.Listener

	mu           sync.Mutex
	log          []LogEntry
	response     string
	toolCallMode bool
	toolName     string
	toolArgs     string
	toolCallPath  string
}

func New() *MockServer {
	s := &MockServer{
		response: "Hello from mock server.",
	}
	mux := http.NewServeMux()

	// LLM APIs — each registered with and without /v1/ prefix
	for _, prefix := range []string{"/v1", ""} {
		mux.HandleFunc("POST "+prefix+"/chat/completions", s.handleChatCompletions)
		mux.HandleFunc("POST "+prefix+"/responses", s.handleResponses)
		mux.HandleFunc("POST "+prefix+"/messages", s.handleMessages)
		mux.HandleFunc("GET "+prefix+"/models", s.handleModels)
	}

	// Gemini API
	mux.HandleFunc("POST /v1beta/models/", s.handleGemini)
	mux.HandleFunc("POST /v1alpha/models/", s.handleGemini)

	// Factory proxy paths (Droid TUI routes LLM calls through /api/llm/{provider}/...)
	mux.HandleFunc("POST /api/llm/a/v1/messages", s.handleMessages)
	mux.HandleFunc("POST /api/llm/o/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /api/llm/o/v1/responses", s.handleResponses)

	// Harness management endpoints (Grok auth, settings, sessions)
	stub := s.handleGrokJSON(map[string]any{})
	settings := s.handleGrokJSON(map[string]any{"models": map[string]any{"default": "mock-model"}})
	user := s.handleGrokJSON(map[string]any{"userId": "mock-user", "email": "mock@test.invalid"})
	privacy := s.handleGrokJSON(map[string]any{"opted_out": false})
	for _, prefix := range []string{"/v1", ""} {
		mux.HandleFunc("GET "+prefix+"/user", user)
		mux.HandleFunc("GET "+prefix+"/settings", settings)
		mux.HandleFunc("GET "+prefix+"/privacy/coding-data-retention", privacy)
	}
	mux.HandleFunc("GET /api-key", stub)
	mux.HandleFunc("GET /billing", stub)
	mux.HandleFunc("GET /feedback/config", stub)
	mux.HandleFunc("GET /bundle/archive", stub)
	mux.HandleFunc("POST /sessions/{id}/data", s.handleGrokRecord)
	mux.HandleFunc("PUT /sessions/{id}", s.handleGrokRecord)

	// Droid (Factory) auth stubs
	mux.HandleFunc("GET /api/cli/whoami", s.handleGrokJSON(map[string]any{
		"userId": "mock-user-001", "orgId": "mock-org-001", "region": "global",
	}))
	mux.HandleFunc("GET /api/cli/org", s.handleGrokJSON(map[string]any{
		"id": "mock-org-001", "name": "Mock Org",
	}))

	// Kimi managed API stubs
	mux.HandleFunc("GET /coding/v1/me", s.handleGrokJSON(map[string]any{
		"id": "mock-user", "email": "mock@test.invalid",
	}))
	mux.HandleFunc("GET /coding/v1/models", s.handleGrokJSON(map[string]any{
		"models": []map[string]any{
			{"id": "mock-model", "name": "Mock Model", "max_context_size": 128000},
		},
	}))
	mux.HandleFunc("GET /coding/v1/usages", s.handleGrokJSON(map[string]any{
		"used": 0, "limit": 1000000,
	}))

	// Test utilities
	mux.HandleFunc("GET /log", s.handleGetLog)
	mux.HandleFunc("GET /log/count", s.handleLogCount)
	mux.HandleFunc("DELETE /log", s.handleClearLog)
	mux.HandleFunc("POST /response", s.handleSetResponse)

	// Catch-all: return 200 for any unhandled path
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})

	s.srv = &http.Server{Handler: mux}
	return s
}

// --- Public API ---

func (s *MockServer) SetResponse(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = text
}

func (s *MockServer) PrepareToolCall(name, args, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolName = name
	s.toolArgs = args
	s.toolCallPath = path
	s.toolCallMode = true
}

func (s *MockServer) LogCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.log)
}

func (s *MockServer) Log() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]LogEntry, len(s.log))
	copy(cp, s.log)
	return cp
}

func (s *MockServer) ClearLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = nil
}

func (s *MockServer) Start() (string, error) {
	var err error
	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	go s.srv.Serve(s.listener)
	return fmt.Sprintf("http://%s", s.listener.Addr()), nil
}

func (s *MockServer) Close() {
	if s.srv != nil {
		s.srv.Close()
	}
}

// --- Internal helpers ---

func (s *MockServer) getResponse() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.response
}

func (s *MockServer) getToolCall() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolName != "" {
		return s.toolName, s.toolArgs
	}
	return DefaultToolName, DefaultToolArgs
}

func (s *MockServer) shouldToolCall(hasTools bool, requestPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !hasTools {
		return false
	}
	if s.toolCallMode {
		if s.toolCallPath != "" && !strings.HasSuffix(requestPath, s.toolCallPath) {
			return false
		}
		s.toolCallMode = false
		return true
	}
	return false
}

// parseRequest reads the body, records the request, and returns the parsed fields.
func (s *MockServer) parseRequest(r *http.Request) (llmRequest, []byte) {
	body, _ := io.ReadAll(r.Body)
	var req llmRequest
	json.Unmarshal(body, &req)
	s.record(r, body, req.Model)
	return req, body
}

func (s *MockServer) record(r *http.Request, body []byte, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	headers := make(map[string]string)
	for k, v := range r.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}

	s.log = append(s.log, LogEntry{
		Timestamp: time.Now(),
		Method:    r.Method,
		Path:      r.URL.Path,
		Headers:   headers,
		Body:      json.RawMessage(body),
		Model:     model,
	})
}

// --- Handlers: models, test endpoints, Grok ---

func (s *MockServer) handleModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, ModelList{
		Data: []ModelEntry{
			{ID: "mock-model", Object: "model", OwnedBy: "mock"},
			{ID: "gpt-4o-mini", Object: "model", OwnedBy: "mock"},
		},
	})
}

func (s *MockServer) handleGetLog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.Log())
}

func (s *MockServer) handleLogCount(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]int{"count": s.LogCount()})
}

func (s *MockServer) handleClearLog(w http.ResponseWriter, _ *http.Request) {
	s.ClearLog()
	w.WriteHeader(204)
}

func (s *MockServer) handleSetResponse(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(body, &req) == nil && req.Text != "" {
		s.SetResponse(req.Text)
	}
	w.WriteHeader(204)
}

func (s *MockServer) handleGrokJSON(data any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.record(r, nil, "")
		writeJSON(w, data)
	}
}

func (s *MockServer) handleGrokRecord(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.record(r, body, "")
	writeJSON(w, map[string]any{"ok": true})
}

// --- SSE helpers ---

type sseEvent struct {
	Type string
	Data any
}

func typed(eventType string, data map[string]any) sseEvent {
	data["type"] = eventType
	return sseEvent{Type: eventType, Data: data}
}

func beginSSE(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	return f
}

func streamSSEEvents(w http.ResponseWriter, events []sseEvent) {
	f := beginSSE(w)
	for _, evt := range events {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, mustJSON(evt.Data))
		if f != nil {
			f.Flush()
		}
	}
}

func streamData(w http.ResponseWriter, f http.Flusher, v any) {
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(v))
	if f != nil {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
