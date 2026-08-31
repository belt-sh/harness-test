package harness

import (
	"testing"
)

func TestAllHarnessesHaveRequiredFields(t *testing.T) {
	for name, h := range All {
		if h.Name == "" {
			t.Errorf("%s: Name is empty", name)
		}
		if h.Binary == "" {
			t.Errorf("%s: Binary is empty", name)
		}
		if h.Name != name {
			t.Errorf("%s: Name=%q does not match map key", name, h.Name)
		}
		if h.Events.PromptSubmit == "" && h.Events.Stop == "" {
			t.Errorf("%s: no events configured", name)
		}
		if h.HookFormat == 0 && h.HookConfigDir == "" {
			// JSONNested is 0, so check HookConfigDir too
		}
		if h.HookConfigDir == "" {
			t.Errorf("%s: HookConfigDir is empty", name)
		}
	}
}

func TestAllHarnessesHaveAtLeastOneMode(t *testing.T) {
	// IDE-only agents (cursor, windsurf) have detection + install but no CLI test modes
	ideOnly := map[string]bool{"cursor": true, "windsurf": true}
	for name, h := range All {
		if ideOnly[name] {
			continue
		}
		hasHeadless := len(h.HeadlessCmd) > 0
		hasInteractive := len(h.InteractiveCmd) > 0
		hasACP := len(h.ACPCmd) > 0
		if !hasHeadless && !hasInteractive && !hasACP {
			t.Errorf("%s: no headless, interactive, or ACP command", name)
		}
	}
}

func TestACPHarnessesHaveACPCmd(t *testing.T) {
	acpAgents := 0
	for name, h := range All {
		if h.HooksInACP && len(h.ACPCmd) == 0 {
			t.Errorf("%s: HooksInACP=true but no ACPCmd", name)
		}
		if len(h.ACPCmd) > 0 {
			acpAgents++
		}
	}
	if acpAgents == 0 {
		t.Error("no agents have ACP support configured")
	}
}

func TestHarnessCount(t *testing.T) {
	if len(All) < 13 {
		t.Errorf("expected at least 13 harnesses, got %d", len(All))
	}
}

func TestEventNames(t *testing.T) {
	for name, h := range All {
		evts := h.Events
		// Every harness must have at least PromptSubmit
		if evts.PromptSubmit == "" {
			t.Errorf("%s: PromptSubmit event is empty", name)
		}
	}
}
