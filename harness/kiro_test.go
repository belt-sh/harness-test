package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kiro hooks live in the agent config; installing must keep the user's agent
// settings and their own hooks, and uninstalling must remove only belt's entries.
func TestKiroAgentConfigMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kiro", "agents", "kiro_default.json")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(`{"name":"kiro_default","prompt":"be terse","tools":["*"],"hooks":{"userPromptSubmit":[{"command":"echo mine"}]}}`), 0644)

	res := Install("kiro", ScopeUser)
	if res.Error != nil || !res.Merged || res.HooksPath != path {
		t.Fatalf("install: %+v", res)
	}
	var obj map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &obj)
	if obj["prompt"] != "be terse" {
		t.Errorf("prompt lost: %v", obj["prompt"])
	}
	hooks := obj["hooks"].(map[string]any)
	ups := hooks["userPromptSubmit"].([]any)
	if len(ups) != 2 || ups[0].(map[string]any)["command"] != "echo mine" || !strings.Contains(ups[1].(map[string]any)["command"].(string), "belt plugin hook user-prompt-submit") {
		t.Errorf("userPromptSubmit = %v", ups)
	}
	if _, ok := hooks["agentSpawn"]; !ok {
		t.Errorf("agentSpawn missing: %v", hooks)
	}
	if !HooksInstalled("kiro", ScopeUser) {
		t.Error("HooksInstalled false after install")
	}

	// Reinstall must not duplicate.
	Install("kiro", ScopeUser)
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "belt plugin hook user-prompt-submit") != 1 {
		t.Errorf("duplicate belt hooks after reinstall:\n%s", data)
	}

	if res := Uninstall("kiro", ScopeUser); res.Error != nil {
		t.Fatalf("uninstall: %v", res.Error)
	}
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &obj)
	if strings.Contains(string(data), "belt plugin hook") || obj["prompt"] != "be terse" {
		t.Errorf("uninstall left belt hooks or dropped user config:\n%s", data)
	}
	if got := obj["hooks"].(map[string]any)["userPromptSubmit"].([]any); len(got) != 1 {
		t.Errorf("user hook lost: %v", got)
	}
}

func TestKiroAgentConfigFreshInstallRemovedOnUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if res := Install("kiro", ScopeUser); res.Error != nil {
		t.Fatal(res.Error)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".kiro", "agents", "kiro_default.json"))
	if !strings.Contains(string(data), `"tools":["*"]`) || !strings.Contains(string(data), `"includeMcpJson":true`) {
		t.Errorf("scaffold must keep built-in tools and mcp.json:\n%s", data)
	}
	Uninstall("kiro", ScopeUser)
	if _, err := os.Stat(filepath.Join(home, ".kiro", "agents", "kiro_default.json")); !os.IsNotExist(err) {
		t.Error("belt-created kiro_default.json should be removed on uninstall")
	}
}

func TestKiroActiveAgent(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if got := KiroActiveAgent(home, cwd); got != "" {
		t.Errorf("no settings: %q", got)
	}
	os.MkdirAll(filepath.Join(home, ".kiro"), 0755)
	os.WriteFile(filepath.Join(home, ".kiro", "settings.json"), []byte(`{"chat.defaultAgent":"mine"}`), 0644)
	if got := KiroActiveAgent(home, cwd); got != "mine" {
		t.Errorf("global: %q", got)
	}
	os.MkdirAll(filepath.Join(cwd, ".kiro", "settings"), 0755)
	os.WriteFile(filepath.Join(cwd, ".kiro", "settings", "cli.json"), []byte(`{"chat.defaultAgent":"ws"}`), 0644)
	if got := KiroActiveAgent(home, cwd); got != "ws" {
		t.Errorf("workspace override: %q", got)
	}
}

// Every KnownIssues key must be one the runner actually looks up, or a typo
// silently turns a skip back into a failure (or hides a real regression).
func TestKnownIssueKeysAreWellFormed(t *testing.T) {
	modes := map[string]bool{"headless": true, "interactive": true, "acp": true, "sdk": true}
	tags := map[string]bool{"SESSION_START": true, "PROMPT": true, "PRE_TOOL": true, "POST_TOOL": true, "STOP": true, "PRE_COMPACT": true}
	checks := map[string]bool{"prompt-context": true, "streaming": true, "model": true}
	for name, h := range All {
		for key, reason := range h.KnownIssues {
			if reason == "" {
				t.Errorf("%s: %q has no reason", name, key)
			}
			parts := strings.Split(key, ":")
			if len(parts) < 2 || !modes[parts[0]] {
				t.Errorf("%s: %q does not start with a mode", name, key)
				continue
			}
			switch {
			case len(parts) == 3 && parts[1] == "event":
				if !tags[parts[2]] {
					t.Errorf("%s: %q names an unknown event tag", name, key)
				}
				// The event must exist for this harness, or the entry is dead.
				if !hasEvent(h, parts[2]) {
					t.Errorf("%s: %q but the harness declares no such event", name, key)
				}
			case len(parts) == 2 && checks[parts[1]]:
			default:
				t.Errorf("%s: %q is not a known key shape", name, key)
			}
		}
	}
}

func hasEvent(h Harness, tag string) bool {
	switch tag {
	case "SESSION_START":
		return h.Events.SessionStart != ""
	case "PROMPT":
		return h.Events.PromptSubmit != ""
	case "PRE_TOOL":
		return h.Events.PreToolUse != ""
	case "POST_TOOL":
		return h.Events.PostToolUse != ""
	case "STOP":
		return h.Events.Stop != ""
	case "PRE_COMPACT":
		return h.Events.PreCompact != ""
	}
	return false
}
