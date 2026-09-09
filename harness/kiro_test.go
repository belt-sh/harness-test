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
	Uninstall("kiro", ScopeUser)
	if _, err := os.Stat(filepath.Join(home, ".kiro", "agents", "kiro_default.json")); !os.IsNotExist(err) {
		t.Error("belt-created kiro_default.json should be removed on uninstall")
	}
}
