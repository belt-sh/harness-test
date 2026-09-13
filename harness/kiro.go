package harness

import (
	"os"
	"path/filepath"
)

// KiroDefaultAgentName is kiro-cli's built-in agent. Earlier belt versions
// installed their hooks into a kiro_default.json override; kiro's V2 engine
// honours that override but the V1 engine ignores it and keeps the built-in
// (kiro-cli 2.21.3, measured in Docker), so belt now uses its own agent.
const KiroDefaultAgentName = "kiro_default"

// KiroBeltAgentName is the agent belt installs its kiro hooks into. Install
// selects it with chat.defaultAgent, which both engines honour.
const KiroBeltAgentName = "belt"

// kiroSettingsPath is where kiro-cli keeps settings under root: the home dir
// for global settings, the project dir for workspace settings (the file
// `kiro-cli settings --workspace` writes, which overrides the global one).
func kiroSettingsPath(root string) string {
	return filepath.Join(root, ".kiro", "settings", "cli.json")
}

// KiroActiveAgent returns the agent kiro-cli starts with: chat.defaultAgent
// from the workspace settings, else from the global settings; empty means the
// built-in default. belt's hooks run only when this is KiroBeltAgentName.
func KiroActiveAgent(home, cwd string) string {
	for _, root := range []string{cwd, home} {
		obj, _ := readKiroSettings(root)
		if name, _ := obj["chat.defaultAgent"].(string); name != "" {
			return name
		}
	}
	return ""
}

func readKiroSettings(root string) (map[string]any, error) {
	return readJSONObject(kiroSettingsPath(root))
}

// writeKiroSettings keeps kiro-cli's own 0600 on the settings file.
func writeKiroSettings(root string, obj map[string]any) error {
	return writeJSONObject(kiroSettingsPath(root), obj, 0600)
}

// KiroSelectBeltAgent makes belt's agent the default in root's kiro settings
// when no other agent is: chat.defaultAgent unset, kiro_default, or already
// belt. It returns the other agent's name, untouched, when the user chose one.
func KiroSelectBeltAgent(root string) (other string, err error) {
	obj, readErr := readKiroSettings(root)
	if readErr != nil && !os.IsNotExist(readErr) {
		// Unparseable settings: leave the user's file alone.
		return "", readErr
	}
	switch name, _ := obj["chat.defaultAgent"].(string); name {
	case KiroBeltAgentName:
		return "", nil
	case "", KiroDefaultAgentName:
		obj["chat.defaultAgent"] = KiroBeltAgentName
		return "", writeKiroSettings(root, obj)
	default:
		return name, nil
	}
}

// kiroDeselectBeltAgent drops chat.defaultAgent from root's kiro settings when
// it points at belt's agent, so kiro falls back to its built-in default.
func kiroDeselectBeltAgent(root string) error {
	obj, err := readKiroSettings(root)
	if err != nil || obj["chat.defaultAgent"] != KiroBeltAgentName {
		return nil
	}
	delete(obj, "chat.defaultAgent")
	return writeKiroSettings(root, obj)
}
