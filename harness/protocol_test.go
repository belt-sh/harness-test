package harness

import "testing"

// The shapes belt prints are written once, in HookStdout; reading them back
// must cover the same set. A second copy in the test runner knew four of the
// six cases and silently ignored the rest.
func TestHookContextTextRoundTripsEveryChannel(t *testing.T) {
	for name := range hookContexts {
		payload, ok := HookStdout(name, "user-prompt-submit", "suggestions")
		if !ok {
			continue // no stdout channel for this agent
		}
		got, ok := HookContextText(name, "user-prompt-submit", payload)
		if !ok || got != "suggestions" {
			t.Errorf("%s: HookContextText(%q) = %q, %v", name, payload, got, ok)
		}
		if _, wrapped := AnyHookContext(got); wrapped {
			t.Errorf("%s: plain text must not read back as an envelope", name)
		}
		if ContextChannelFor(name, "user-prompt-submit") != ContextPlainText {
			if _, wrapped := AnyHookContext(payload); !wrapped {
				t.Errorf("%s: an envelope must read back as one: %s", name, payload)
			}
		}
	}
}

func TestHookContextTextRejectsTheWrongShape(t *testing.T) {
	// copilot takes a top-level additionalContext; claude's nested envelope is
	// not that shape, and saying so is the whole point of ok.
	claudeShape, _ := HookStdout("claude", "user-prompt-submit", "suggestions")
	if _, ok := HookContextText("copilot", "user-prompt-submit", claudeShape); ok {
		t.Error("claude's envelope must not pass as copilot's shape")
	}
	// kimi reads stdout as text, so JSON is the wrong thing to print at it.
	if _, ok := HookContextText("kimi", "user-prompt-submit", claudeShape); ok {
		t.Error("JSON must not pass as plain stdout")
	}
}
