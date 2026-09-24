package server

import (
	"strings"
	"testing"
)

func TestLastUserText(t *testing.T) {
	cases := map[string]string{
		"chat":      `{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ok"},{"role":"user","content":"/compress"}]}`,
		"anthropic": `{"messages":[{"role":"user","content":[{"type":"text","text":"first"}]},{"role":"user","content":[{"type":"text","text":"/compress"}]}]}`,
		"responses": `{"input":[{"role":"user","content":[{"type":"input_text","text":"first"}]},{"role":"user","content":[{"type":"input_text","text":"/compress"}]}]}`,
		"gemini":    `{"contents":[{"role":"user","parts":[{"text":"first"}]},{"role":"model","parts":[{"text":"ok"}]},{"role":"user","parts":[{"text":"/compress"},{"text":"<hook_context>x</hook_context>"}]}]}`,
	}
	for name, body := range cases {
		got := LastUserText([]byte(body))
		if !strings.Contains(got, "/compress") || strings.Contains(got, "first") {
			t.Errorf("%s: LastUserText = %q", name, got)
		}
	}
	if got := LastUserText([]byte(`not json`)); got != "" {
		t.Errorf("non-JSON: %q", got)
	}
}
