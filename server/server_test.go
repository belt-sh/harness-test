package server

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func startTestServer(t *testing.T, response string) string {
	t.Helper()
	s := New()
	s.SetResponse(response)
	url, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return url
}

func TestChatCompletions(t *testing.T) {
	url := startTestServer(t, "test response")

	resp, err := http.Post(url+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	var result map[string]any
	json.Unmarshal(data, &result)

	choices, ok := result["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("no choices")
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "test response" {
		t.Fatalf("unexpected content: %v", msg["content"])
	}
}

func TestStreamingChatCompletions(t *testing.T) {
	url := startTestServer(t, "streamed")

	resp, err := http.Post(url+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("wrong content type: %s", resp.Header.Get("Content-Type"))
	}

	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "streamed") {
		t.Fatalf("response doesn't contain 'streamed': %s", data)
	}
	if !strings.Contains(string(data), "[DONE]") {
		t.Fatal("missing [DONE]")
	}
}

func TestAnthropicMessages(t *testing.T) {
	url := startTestServer(t, "anthropic response")

	resp, err := http.Post(url+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result map[string]any
	json.Unmarshal(data, &result)

	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("no content")
	}
	if content[0].(map[string]any)["text"] != "anthropic response" {
		t.Fatalf("unexpected text: %v", content[0].(map[string]any)["text"])
	}
}

func TestResponsesAPI(t *testing.T) {
	url := startTestServer(t, "responses api text")

	resp, err := http.Post(url+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"test","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "responses api text") {
		t.Fatalf("response doesn't contain expected text: %s", data)
	}
}

func TestMITMProxy(t *testing.T) {
	s := New()
	s.SetResponse("mitm response")

	_, err := s.StartIntercept()
	if err != nil {
		t.Skip("can't start intercept (port 443 in use?): " + err.Error())
	}
	t.Cleanup(s.Close)

	proxyAddr, err := s.StartProxy()
	if err != nil {
		t.Fatal(err)
	}

	proxyURL, _ := url.Parse(proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Post("https://api.openai.com/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal("proxy request failed:", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "mitm response") {
		t.Fatalf("expected 'mitm response', got: %s", data)
	}

	if s.LogCount() != 1 {
		t.Fatalf("expected 1 logged request, got %d", s.LogCount())
	}
}

func TestProxyCACert(t *testing.T) {
	s := New()

	_, err := s.StartIntercept()
	if err != nil {
		t.Skip("can't start intercept: " + err.Error())
	}
	t.Cleanup(s.Close)

	ca := s.CAPem()
	if len(ca) == 0 {
		t.Fatal("no CA PEM after StartIntercept")
	}
	if !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatal("CA PEM doesn't look like a certificate")
	}

	addr := s.ProxyAddr()
	if addr == "" {
		_, err := s.StartProxy()
		if err != nil {
			t.Fatal(err)
		}
		addr = s.ProxyAddr()
	}
	if !strings.HasPrefix(addr, "http://127.0.0.1:") {
		t.Fatalf("unexpected proxy addr: %s", addr)
	}
}

func TestOpenRouterPath(t *testing.T) {
	url := startTestServer(t, "openrouter response")

	resp, err := http.Post(url+"/api/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "openrouter response") {
		t.Fatalf("unexpected: %s", data)
	}
}

func TestLogEndpoints(t *testing.T) {
	s := New()
	url, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	http.Post(url+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))

	resp, _ := http.Get(url + "/log/count")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"count":1`) {
		t.Fatalf("unexpected count: %s", data)
	}

	req, _ := http.NewRequest("DELETE", url+"/log", nil)
	http.DefaultClient.Do(req)

	resp, _ = http.Get(url + "/log/count")
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"count":0`) {
		t.Fatalf("log not cleared: %s", data)
	}
}

// The registry needs each agent's own tool names to provoke a permission
// request, and guessing twelve of them is how a probe ends up measuring
// nothing. These are read off the wire instead, from the shapes agents
// actually send: OpenAI's function envelope, Anthropic's flat tools, and
// Gemini's functionDeclarations.
func TestDeclaredToolsReadsEachWireShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai function envelope", `{"tools":[{"type":"function","function":{"name":"shell","parameters":{}}}]}`, "shell"},
		{"anthropic flat tools", `{"tools":[{"name":"Write","input_schema":{}}]}`, "Write"},
		{"gemini function declarations", `{"functionDeclarations":[{"name":"write_file"}]}`, "write_file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			s.log = []LogEntry{{Body: []byte(c.body)}}
			got := s.DeclaredTools()
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("DeclaredTools() = %v, want [%s]", got, c.want)
			}
		})
	}

	// A tool named in conversation history is not a declaration. The mock once
	// served a prepared call to a request that never offered the tool, and the
	// agent answered "Tool not found".
	t.Run("history is not a declaration", func(t *testing.T) {
		s := New()
		s.log = []LogEntry{{Body: []byte(`{"messages":[{"role":"user","content":"use the shell tool"},{"name":"shell"}]}`)}}
		if got := s.DeclaredTools(); len(got) != 0 {
			t.Errorf("DeclaredTools() = %v, want none", got)
		}
	})
}
