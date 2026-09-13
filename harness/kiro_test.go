package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return obj
}

// A fresh install writes belt's own agent and selects it with
// chat.defaultAgent, keeping the user's other kiro settings; uninstall
// removes both and hands the default back to the built-in agent.
func TestKiroInstallSelectsBeltAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".kiro", "settings", "cli.json")
	os.MkdirAll(filepath.Dir(settings), 0755)
	os.WriteFile(settings, []byte(`{"chat.agentEngine":"v2"}`), 0600)

	res := Install("kiro", ScopeUser)
	agent := filepath.Join(home, ".kiro", "agents", "belt.json")
	if res.Error != nil || res.HooksPath != agent {
		t.Fatalf("install: %+v", res)
	}
	obj := readJSON(t, agent)
	if obj["name"] != KiroBeltAgentName {
		t.Errorf("agent name = %v, want %s (kiro selects agents by this name)", obj["name"], KiroBeltAgentName)
	}
	data, _ := os.ReadFile(agent)
	if !strings.Contains(string(data), `"tools":["*"]`) || !strings.Contains(string(data), `"includeMcpJson":true`) {
		t.Errorf("agent must keep the built-in tools and mcp.json:\n%s", data)
	}
	if got := KiroActiveAgent(home, t.TempDir()); got != KiroBeltAgentName {
		t.Errorf("active agent after install = %q", got)
	}
	if s := readJSON(t, settings); s["chat.agentEngine"] != "v2" {
		t.Errorf("install dropped the user's settings: %v", s)
	}
	if fi, _ := os.Stat(settings); fi.Mode().Perm() != 0600 {
		t.Errorf("settings mode = %v, kiro writes 0600", fi.Mode().Perm())
	}

	if res := Uninstall("kiro", ScopeUser); res.Error != nil {
		t.Fatalf("uninstall: %v", res.Error)
	}
	if _, err := os.Stat(agent); !os.IsNotExist(err) {
		t.Error("belt-created belt.json should be removed on uninstall")
	}
	s := readJSON(t, settings)
	if _, ok := s["chat.defaultAgent"]; ok || s["chat.agentEngine"] != "v2" {
		t.Errorf("uninstall should drop only chat.defaultAgent: %v", s)
	}
}

// Installing must keep the user's edits to belt's agent and their own hooks,
// never duplicate belt's, and uninstall must remove only belt's entries.
func TestKiroAgentConfigMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kiro", "agents", "belt.json")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(`{"name":"belt","prompt":"be terse","tools":["*"],"hooks":{"userPromptSubmit":[{"command":"echo mine"}]}}`), 0644)

	if res := Install("kiro", ScopeUser); res.Error != nil || !res.Merged {
		t.Fatalf("install: %+v", res)
	}
	obj := readJSON(t, path)
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

	Install("kiro", ScopeUser)
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "belt plugin hook user-prompt-submit") != 1 {
		t.Errorf("duplicate belt hooks after reinstall:\n%s", data)
	}

	if res := Uninstall("kiro", ScopeUser); res.Error != nil {
		t.Fatalf("uninstall: %v", res.Error)
	}
	data, _ = os.ReadFile(path)
	obj = readJSON(t, path)
	if strings.Contains(string(data), "belt plugin hook") || obj["prompt"] != "be terse" {
		t.Errorf("uninstall left belt hooks or dropped user config:\n%s", data)
	}
	if got := obj["hooks"].(map[string]any)["userPromptSubmit"].([]any); len(got) != 1 {
		t.Errorf("user hook lost: %v", got)
	}
}

// Earlier belt versions merged into a kiro_default.json override, which the
// V1 engine ignores. Install moves belt out of it: a pure belt scaffold is
// deleted, a file the user extended keeps everything but belt's entries.
func TestKiroInstallMigratesKiroDefaultOverride(t *testing.T) {
	for _, tc := range []struct {
		name, legacy string
		wantGone     bool
	}{
		{"scaffold", `{"name":"kiro_default","description":"Default agent with belt hooks","tools":["*"],"includeMcpJson":true,"hooks":{"stop":[{"command":"belt plugin hook stop","timeout_ms":10000}]}}`, true},
		{"user file", `{"name":"kiro_default","prompt":"mine","hooks":{"stop":[{"command":"echo mine"},{"command":"belt plugin hook stop"}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			legacy := filepath.Join(home, ".kiro", "agents", "kiro_default.json")
			os.MkdirAll(filepath.Dir(legacy), 0755)
			os.WriteFile(legacy, []byte(tc.legacy), 0644)

			if res := Install("kiro", ScopeUser); res.Error != nil {
				t.Fatal(res.Error)
			}
			_, err := os.Stat(legacy)
			if tc.wantGone {
				if !os.IsNotExist(err) {
					t.Error("belt's kiro_default.json scaffold should be deleted")
				}
				return
			}
			data, _ := os.ReadFile(legacy)
			if strings.Contains(string(data), "belt plugin hook") || !strings.Contains(string(data), "echo mine") || !strings.Contains(string(data), `"prompt": "mine"`) {
				t.Errorf("legacy file should keep only the user's content:\n%s", data)
			}
		})
	}
}

// A default agent the user chose stays the default; belt's agent is written
// but not selected, and doctor reports that its hooks will not run.
func TestKiroInstallKeepsUserDefaultAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".kiro", "settings", "cli.json")
	os.MkdirAll(filepath.Dir(settings), 0755)
	os.WriteFile(settings, []byte(`{"chat.defaultAgent":"mine"}`), 0600)

	if res := Install("kiro", ScopeUser); res.Error != nil {
		t.Fatal(res.Error)
	}
	if got := KiroActiveAgent(home, t.TempDir()); got != "mine" {
		t.Errorf("install replaced the user's default agent: %q", got)
	}
	Uninstall("kiro", ScopeUser)
	if got := KiroActiveAgent(home, t.TempDir()); got != "mine" {
		t.Errorf("uninstall touched the user's default agent: %q", got)
	}
}

func TestKiroProjectInstall(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	wd, _ := os.Getwd()
	os.Chdir(cwd)
	defer os.Chdir(wd)

	if res := Install("kiro", ScopeProject); res.Error != nil {
		t.Fatal(res.Error)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".kiro", "agents", "belt.json")); err != nil {
		t.Errorf("project agent: %v", err)
	}
	if got := KiroActiveAgent(home, cwd); got != KiroBeltAgentName {
		t.Errorf("workspace chat.defaultAgent = %q", got)
	}
	if obj, _ := readKiroSettings(home); obj["chat.defaultAgent"] != nil {
		t.Errorf("project install wrote the global setting: %v", obj["chat.defaultAgent"])
	}
}

// The paths are the ones kiro-cli 2.21.3 writes: `kiro-cli settings` puts
// global settings in ~/.kiro/settings/cli.json and `--workspace` puts them in
// .kiro/settings/cli.json, which wins (checked in Docker with
// `kiro-cli settings list`).
func TestKiroActiveAgent(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if got := KiroActiveAgent(home, cwd); got != "" {
		t.Errorf("no settings: %q", got)
	}
	os.MkdirAll(filepath.Join(home, ".kiro", "settings"), 0755)
	os.WriteFile(filepath.Join(home, ".kiro", "settings", "cli.json"), []byte(`{"chat.defaultAgent":"mine"}`), 0600)
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
