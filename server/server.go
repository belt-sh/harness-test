package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

// LLMHosts are API domains that agents call. Used by --intercept mode to
// map these to 127.0.0.1 via /etc/hosts so all LLM traffic hits the mock.
var LLMHosts = []string{
	"api.openai.com",
	"api.anthropic.com",
	"generativelanguage.googleapis.com",
	"api.x.ai",
	"openrouter.ai",
	"api.moonshot.cn",
	"dashscope.aliyuncs.com",
	"api.factory.ai",
	"kilo.codes",
	"opencode.ai",
	// Kiro (Amazon Q backend + kiro runtime)
	"q.us-east-1.amazonaws.com",
	"q.eu-central-1.amazonaws.com",
	"runtime.us-east-1.kiro.dev",
	"runtime.eu-central-1.kiro.dev",
	"app.kiro.dev",
	"prod.us-east-1.auth.desktop.kiro.dev",
	"management.us-east-1.kiro.dev",
	"cognito-identity.us-east-1.amazonaws.com",
	"client-telemetry.us-east-1.amazonaws.com",
	"prod.us-east-1.telemetry-v2.kiro.dev",
}

type MockServer struct {
	srv         *http.Server
	listener    net.Listener
	tlsListener net.Listener
	proxyListener net.Listener
	caPEM       []byte            // PEM-encoded CA cert (set after StartIntercept)
	tlsCerts    []tls.Certificate // server certs signed by our CA

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

	// LLM APIs — registered with /v1, /api/v1 (OpenRouter), and bare prefix
	for _, prefix := range []string{"/v1", "/api/v1", ""} {
		mux.HandleFunc("POST "+prefix+"/chat/completions", s.handleChatCompletions)
		mux.HandleFunc("POST "+prefix+"/responses", s.handleResponses)
		mux.HandleFunc("POST "+prefix+"/messages", s.handleMessages)
		mux.HandleFunc("GET "+prefix+"/models", s.handleModels)
	}

	// Gemini API
	mux.HandleFunc("POST /v1beta/models/", s.handleGemini)
	mux.HandleFunc("POST /v1alpha/models/", s.handleGemini)

	// AWS Bedrock / Amazon Q (kiro uses q.*.amazonaws.com)
	mux.HandleFunc("POST /model/{modelId}/converse", s.handleBedrockConverse)
	mux.HandleFunc("POST /model/{modelId}/converse-stream", s.handleBedrockConverseStream)
	mux.HandleFunc("POST /model/{modelId}/invoke", s.handleBedrockConverse)
	mux.HandleFunc("POST /model/{modelId}/invoke-with-response-stream", s.handleBedrockConverseStream)

	// Kiro runtime API — catch-all for /runtime/ paths
	mux.HandleFunc("/runtime/", s.handleKiroRuntime)

	// Factory proxy paths (Droid TUI routes LLM calls through /api/llm/{provider}/...)
	mux.HandleFunc("POST /api/llm/a/v1/messages", s.handleMessages)
	mux.HandleFunc("POST /api/llm/o/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /api/llm/o/v1/responses", s.handleResponses)

	// Harness management endpoints (Grok auth, settings, sessions)
	stub := s.stubJSON(map[string]any{})
	settings := s.stubJSON(map[string]any{"models": map[string]any{"default": "mock-model"}})
	user := s.stubJSON(map[string]any{"userId": "mock-user", "email": "mock@test.invalid"})
	privacy := s.stubJSON(map[string]any{"opted_out": false})
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
	mux.HandleFunc("GET /api/cli/whoami", s.stubJSON(map[string]any{
		"userId": "mock-user-001", "orgId": "mock-org-001", "region": "global",
	}))
	mux.HandleFunc("GET /api/cli/org", s.stubJSON(map[string]any{
		"id": "mock-org-001", "name": "Mock Org",
	}))

	// Kimi managed API stubs
	mux.HandleFunc("GET /coding/v1/me", s.stubJSON(map[string]any{
		"id": "mock-user", "email": "mock@test.invalid",
	}))
	mux.HandleFunc("GET /coding/v1/models", s.stubJSON(map[string]any{
		"models": []map[string]any{
			{"id": "mock-model", "name": "Mock Model", "max_context_size": 128000},
		},
	}))
	mux.HandleFunc("GET /coding/v1/usages", s.stubJSON(map[string]any{
		"used": 0, "limit": 1000000,
	}))

	// Kiro auth + Cognito stubs
	mux.HandleFunc("POST /auth/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r, nil, "")
		writeJSON(w, map[string]any{"accessToken": "mock-token", "expiresIn": 86400})
	})

	// Test utilities
	mux.HandleFunc("GET /log", s.handleGetLog)
	mux.HandleFunc("GET /log/count", s.handleLogCount)
	mux.HandleFunc("DELETE /log", s.handleClearLog)
	mux.HandleFunc("POST /response", s.handleSetResponse)

	// Catch-all: handle AWS service APIs (Cognito, CodeWhisperer, telemetry)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		switch target {
		// Cognito Identity
		case "AWSCognitoIdentityService.GetId":
			writeJSON(w, map[string]any{"IdentityId": "us-east-1:mock-identity-id"})
		case "AWSCognitoIdentityService.GetCredentialsForIdentity":
			writeJSON(w, map[string]any{
				"IdentityId": "us-east-1:mock-identity-id",
				"Credentials": map[string]any{
					"AccessKeyId": "ASIAMOCKKEY", "SecretKey": "mocksecretkey",
					"SessionToken": "mocksessiontoken", "Expiration": 4102444800,
				},
			})
		case "AWSCognitoIdentityService.GetOpenIdToken":
			writeJSON(w, map[string]any{"IdentityId": "us-east-1:mock-identity-id", "Token": "mock-openid-token"})
		// CodeWhisperer management
		case "AmazonCodeWhispererService.GetProfile":
			writeJSON(w, map[string]any{"profileArn": "arn:aws:codewhisperer:us-east-1:mock:profile/mock", "profileName": "mock"})
		case "AmazonCodeWhispererService.GetUsageLimits":
			writeJSON(w, map[string]any{"usageLimits": []any{}})
		case "AmazonCodeWhispererService.ListAvailableModels":
			writeJSON(w, map[string]any{"models": []map[string]any{
				{"modelId": "kiro-default", "modelName": "Kiro Default", "provider": "kiro"},
			}})
		case "AmazonCodeWhispererService.SendTelemetryEvent":
			writeJSON(w, map[string]any{})
		// CodeWhisperer streaming (LLM call)
		case "AmazonCodeWhispererStreamingService.GenerateAssistantResponse":
			body, _ := io.ReadAll(r.Body)
			s.record(r, body, "kiro-default")
			text := s.getResponse()
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			w.WriteHeader(200)
			writeBedrockEvent(w, "assistantResponseEvent", map[string]any{"content": text})
			writeBedrockEvent(w, "messageMetadataEvent", map[string]any{
				"conversationId": "mock-conv-1",
				"usage":          map[string]any{"inputTokens": 10, "outputTokens": 5},
			})
		default:
			w.WriteHeader(200)
		}
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

// StartIntercept starts both HTTP (random port) and HTTPS (port 443) listeners.
// Uses a CA cert to sign server certs for all LLM API domains. The CA PEM is
// available via CAPem() for injection into runtimes (NODE_EXTRA_CA_CERTS etc.).
func (s *MockServer) StartIntercept() (string, error) {
	baseURL, err := s.Start()
	if err != nil {
		return "", err
	}

	caKey, caDER, err := generateCA()
	if err != nil {
		return baseURL, fmt.Errorf("generate CA: %w", err)
	}

	bundle, err := generateMITMCert(caKey, caDER, LLMHosts)
	if err != nil {
		return baseURL, fmt.Errorf("generate server cert: %w", err)
	}
	s.caPEM = bundle.caPEM
	s.tlsCerts = []tls.Certificate{bundle.cert}

	tlsLn, err := tls.Listen("tcp", "127.0.0.1:443", &tls.Config{
		Certificates: s.tlsCerts,
	})
	if err != nil {
		// Port 443 requires root; fall back to random port (proxy still works)
		tlsLn, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
			Certificates: s.tlsCerts,
		})
		if err != nil {
			return baseURL, fmt.Errorf("tls listen: %w", err)
		}
	}
	s.tlsListener = tlsLn

	tlsSrv := &http.Server{Handler: s.srv.Handler}
	go tlsSrv.Serve(tlsLn)

	return baseURL, nil
}

// CAPem returns the PEM-encoded CA certificate used by the MITM listener.
// Returns nil if StartIntercept hasn't been called.
func (s *MockServer) CAPem() []byte {
	return s.caPEM
}

// ProxyAddr returns the MITM proxy address (e.g. "http://127.0.0.1:34567")
// for use as HTTPS_PROXY. Returns empty if StartProxy hasn't been called.
func (s *MockServer) ProxyAddr() string {
	if s.proxyListener == nil {
		return ""
	}
	return fmt.Sprintf("http://%s", s.proxyListener.Addr())
}

// HostsEntries returns /etc/hosts lines mapping LLM API domains to 127.0.0.1.
func HostsEntries() string {
	var lines []string
	for _, host := range LLMHosts {
		lines = append(lines, fmt.Sprintf("127.0.0.1 %s", host))
	}
	return strings.Join(lines, "\n")
}

type caBundle struct {
	cert    tls.Certificate
	caCert  []byte // DER-encoded CA certificate
	caPEM   []byte // PEM-encoded CA certificate (for NODE_EXTRA_CA_CERTS etc.)
}

func generateCA() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"harness-test CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return key, caDER, nil
}

func generateMITMCert(caKey *ecdsa.PrivateKey, caDER []byte, hosts []string) (caBundle, error) {
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return caBundle{}, err
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return caBundle{}, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"harness-test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	for _, h := range hosts {
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return caBundle{}, err
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	return caBundle{
		cert: tls.Certificate{
			Certificate: [][]byte{serverDER, caDER},
			PrivateKey:  serverKey,
		},
		caCert: caDER,
		caPEM:  caPEM,
	}, nil
}

func (s *MockServer) Close() {
	if s.srv != nil {
		s.srv.Close()
	}
	if s.tlsListener != nil {
		s.tlsListener.Close()
	}
	if s.proxyListener != nil {
		s.proxyListener.Close()
	}
}

func (s *MockServer) handleKiroRuntime(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.record(r, body, "")

	text := s.getResponse()
	writeJSON(w, map[string]any{
		"output": map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"text": text}},
			},
		},
		"stopReason": "end_turn",
	})
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

func (s *MockServer) getToolCallParsed() (string, any) {
	name, argsJSON := s.getToolCall()
	var parsed any
	json.Unmarshal([]byte(argsJSON), &parsed)
	return name, parsed
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

func (s *MockServer) stubJSON(data any) http.HandlerFunc {
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
