package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// KiroDefaultAgentName is the built-in agent that a kiro_default.json in the
// agents dir overrides; belt's hooks are installed there.
const KiroDefaultAgentName = "kiro_default"

// KiroActiveAgent returns the agent kiro-cli starts with: the workspace
// setting .kiro/settings/cli.json overrides the global ~/.kiro/settings.json,
// key "chat.defaultAgent"; empty means the built-in default (kiro_default).
// Hooks installed into kiro_default.json do not run when a different agent is
// the default.
func KiroActiveAgent(home, cwd string) string {
	for _, p := range []string{
		filepath.Join(cwd, ".kiro", "settings", "cli.json"),
		filepath.Join(home, ".kiro", "settings.json"),
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(data, &obj) != nil {
			continue
		}
		if name, _ := obj["chat.defaultAgent"].(string); name != "" {
			return name
		}
	}
	return ""
}
