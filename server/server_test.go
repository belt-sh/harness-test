package server

import (
	"encoding/json"
	"io"
	"net/http"
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
