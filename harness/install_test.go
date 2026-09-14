package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

// A hook command containing quotes must not break the config it goes into.
// belt's own command has none, so the generators embedded commands raw and
// the first caller to pass a quoted one got malformed JSON — and hooks that
// silently never fired.
func TestGeneratedConfigSurvivesAQuotedCommand(t *testing.T) {
	cmd := func(event HookEvent) string {
		return `echo ` + string(event) + ` && printf '%s' '{"key":"value"}'`
	}
	for name, h := range All {
		switch h.HookFormat {
		case TSExtension, TSPlugin, YAML, TOML:
			continue // not JSON; covered by their own shapes
		}
		content, err := HookConfigFor(name, cmd)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.Replace(content, "{{.BaseURL}}", "http://x", -1)), &obj); err != nil {
			t.Errorf("%s: generated config is not valid JSON: %v\n%s", name, err, content)
		}
	}
}
