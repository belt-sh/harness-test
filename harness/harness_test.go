package harness

import (
	"reflect"
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
	// IDE-only agents (windsurf) have detection + install but no CLI test modes
	ideOnly := map[string]bool{"windsurf": true}
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
	for _, h := range All {
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

func TestInstructionFiles(t *testing.T) {
	noUserFile := map[string]bool{"cursor": true, "hermes": true}
	for name, h := range All {
		if h.ProjectInstructionFile == "" {
			t.Errorf("%s: ProjectInstructionFile is empty", name)
		}
		if h.InstructionFile == "" && !noUserFile[name] {
			t.Errorf("%s: InstructionFile is empty", name)
		}
		if h.InstructionFile != "" && noUserFile[name] {
			t.Errorf("%s: unexpected user-scope InstructionFile %q", name, h.InstructionFile)
		}
		if _, ok := instructionFiles[name]; !ok {
			t.Errorf("%s: missing from instructionFiles", name)
		}
		if h.SkillsDir == "" {
			t.Errorf("%s: SkillsDir is empty", name)
		}
	}
}

func TestHookContextTableCoversAllHarnesses(t *testing.T) {
	for name := range All {
		if _, ok := hookContexts[name]; !ok {
			t.Errorf("%s: missing from hookContexts", name)
		}
	}
	if out, ok := HookStdout("gemini", "user-prompt-submit", "hi"); !ok || out != `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"BeforeAgent"}}` {
		t.Errorf("gemini payload: %q %v", out, ok)
	}
	if _, ok := HookStdout("goose", "user-prompt-submit", "hi"); ok {
		t.Error("goose has no prompt context channel")
	}
	if reason, _ := All["windsurf"].SkipFor("headless"); reason != SkipIDEOnly {
		t.Errorf("windsurf skip = %q", reason)
	}
	if reason, _ := All["kiro"].SkipFor("acp"); reason != SkipNone {
		t.Errorf("kiro acp skip = %q", reason)
	}
	if reason, _ := All["claude"].SkipFor("headless"); reason != SkipNone {
		t.Errorf("claude skip = %q", reason)
	}
}

// A mode used to be switchable off per harness with HooksIn<mode>, which
// skipped the whole phase — every check in it, not just the hooks — with no
// reason anyone had to write down. That is how kiro's headless and ACP went
// untested. Coverage now follows the commands: a mode with a command runs,
// and an agent that fires no hook there says why in KnownIssues.
func TestModesAreNotSwitchableOff(t *testing.T) {
	fields := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeOf(Harness{})) {
		fields[f.Name] = true
	}
	for _, name := range []string{"HooksInHeadless", "HooksInInteractive", "HooksInACP", "HooksInSDK"} {
		if fields[name] {
			t.Errorf("%s is back: a mode must not be skippable without a stated cause", name)
		}
	}
}

// Every ACP agent needs a tool it actually gates, or the in-flight probe asks
// it to read a file and records "never asked the client" as an agent trait.
// Names come from --probe tools, which reads what the agent declared to
// the model; a tool an agent does not declare is answered "tool not found" and
// never runs.
func TestEveryACPAgentHasAGatedTool(t *testing.T) {
	for name, h := range All {
		if len(h.ACPCmd) == 0 {
			continue
		}
		if h.ToolCallGated.Name == "" {
			t.Errorf("%s speaks ACP but names no gated tool", name)
			continue
		}
		if h.ToolCallGated.Args == "" {
			t.Errorf("%s names gated tool %q with no arguments", name, h.ToolCallGated.Name)
		}
	}
}
