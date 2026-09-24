package server

import (
	"encoding/json"
	"strings"
)

// LastUserText is the text of the last user message in a model request, in
// any format this mock speaks: chat completions and anthropic ("messages"),
// responses ("input") and gemini ("contents"). It is "" when the body has no
// user message or is not JSON.
func LastUserText(body []byte) string {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	for _, key := range []string{"messages", "input", "contents"} {
		items, _ := req[key].([]any)
		for i := len(items) - 1; i >= 0; i-- {
			m, _ := items[i].(map[string]any)
			if m["role"] == "user" {
				var b strings.Builder
				collectText(m, &b)
				return b.String()
			}
		}
	}
	return ""
}

// collectText appends every string under a "text" or "content" key, which is
// where all four formats put message text.
func collectText(v any, b *strings.Builder) {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && (k == "text" || k == "content") {
				b.WriteString(s)
				b.WriteByte('\n')
				continue
			}
			collectText(x, b)
		}
	case []any:
		for _, x := range v {
			collectText(x, b)
		}
	}
}
