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

// An agent whose context channel is ContextPlugin has no stdout channel: the
// plugin file itself must take the hook command's output and hand it to the
// agent. The generators used to run the command with execSync and throw the
// result away, so belt injected nothing on the four plugin agents while the
// test suite — which wrote its own plugin file with the injection in it —
// reported the channel working.
func TestPluginContextChannelIsWiredIntoTheGeneratedFile(t *testing.T) {
	cmd := func(HookEvent) string { return "belt-hook-marker" }
	for name, h := range All {
		for _, e := range h.Defined() {
			if ContextChannelFor(name, string(e)) != ContextPlugin {
				continue
			}
			content, err := HookConfigFor(name, cmd)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			sink := map[HookFormat]string{
				TSExtension: "systemPrompt",
				TSPlugin:    "output.system.push",
			}[h.HookFormat]
			if sink == "" {
				t.Errorf("%s: %s carries a plugin context channel but format %v has no known sink", name, e, h.HookFormat)
				continue
			}
			if !strings.Contains(content, sink) {
				t.Errorf("%s: %s is a plugin context channel but the generated file never reaches %s:\n%s", name, e, sink, content)
			}
		}
	}
}
