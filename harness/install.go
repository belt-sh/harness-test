package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstallScope determines where hooks are written.
type InstallScope int

// Scope is an alias for InstallScope (CLI compatibility).
type Scope = InstallScope

const (
	ScopeUser    InstallScope = iota // ~/.config/<harness>/ or ~/.<harness>/
	ScopeProject                     // ./<harness-config-dir>/
)

// InstallResult describes what happened during install.
type InstallResult struct {
	Harness   string
	Scope     InstallScope
	HooksPath string
	Created   bool
	Merged    bool
	Error     error
}

// KnownAgentNames is an alias for KnownNames (CLI compatibility).
func KnownAgentNames() []string { return KnownNames() }

// InstallHooks is an alias for Install (CLI compatibility).
func InstallHooks(name string, scope InstallScope) InstallResult { return Install(name, scope) }

// UninstallHooks removes belt hooks for a harness at the given scope.
func UninstallHooks(name string, scope InstallScope) InstallResult { return Uninstall(name, scope) }

// HookTemplate returns the generated hook configuration for an agent (CLI compatibility).
func HookTemplate(name string) string {
	s, _ := HookConfig(name)
	return s
}

// Install writes belt hook configs for a harness at the given scope.
func Install(name string, scope InstallScope) InstallResult {
	h, ok := All[name]
	if !ok {
		return InstallResult{Harness: name, Error: fmt.Errorf("unknown harness: %s", name)}
	}

	home, _ := os.UserHomeDir()
	var hooksPath string

	switch scope {
	case ScopeUser:
		target := hooksTarget(name)
		if target == "" {
			return InstallResult{Harness: name, Error: fmt.Errorf("no user hook path for %s", name)}
		}
		hooksPath = filepath.Join(home, target)
	case ScopeProject:
		cwd, _ := os.Getwd()
		hooksPath = filepath.Join(cwd, h.HookConfigDir, hookFileName(h))
	}

	content, err := generateHookConfig(name, h)
	if err != nil {
		return InstallResult{Harness: name, Error: err}
	}

	result := InstallResult{Harness: name, Scope: scope, HooksPath: hooksPath}

	// For formats that merge into existing files (settings.json, config.yaml, config.toml),
	// we need to read-modify-write. For standalone files, just write.
	switch h.HookFormat {
	case JSONNested:
		if needsMerge(h) {
			result.Merged = true
			err = mergeJSONHooks(hooksPath, content)
		} else {
			err = writeFile(hooksPath, content)
			result.Created = true
		}
	case JSONFlat, JSONCopilot, JSONKiro:
		err = writeFile(hooksPath, content)
		result.Created = true
	case TOML:
		result.Merged = true
		err = appendTOMLHooks(hooksPath, content)
	case YAML:
		result.Merged = true
		err = mergeYAMLHooks(hooksPath, content)
	case TSExtension, TSPlugin:
		err = writeFile(hooksPath, content)
		result.Created = true
	default:
		err = fmt.Errorf("hook format not supported for install: %d", h.HookFormat)
	}

	result.Error = err
	return result
}

func hookFileName(h Harness) string {
	if h.HookFileName != "" {
		return h.HookFileName
	}
	switch h.HookFormat {
	case TSExtension, TSPlugin:
		return "belt.ts"
	case TOML:
		return "config.toml"
	case YAML:
		return "config.yaml"
	default:
		return "belt.json"
	}
}

func needsMerge(h Harness) bool {
	return h.HookWrapper != ""
}

// HookConfig returns the generated hook configuration for an agent.
func HookConfig(name string) (string, error) {
	h, ok := All[name]
	if !ok {
		return "", fmt.Errorf("unknown harness: %s", name)
	}
	return generateHookConfig(name, h)
}

// Uninstall removes belt hooks for a harness at the given scope.
func Uninstall(name string, scope InstallScope) InstallResult {
	h, ok := All[name]
	if !ok {
		return InstallResult{Harness: name, Error: fmt.Errorf("unknown harness: %s", name)}
	}

	home, _ := os.UserHomeDir()
	var hooksPath string
	switch scope {
	case ScopeUser:
		target := hooksTarget(name)
		if target == "" {
			return InstallResult{Harness: name, Error: fmt.Errorf("no user hook path for %s", name)}
		}
		hooksPath = filepath.Join(home, target)
	case ScopeProject:
		cwd, _ := os.Getwd()
		hooksPath = filepath.Join(cwd, h.HookConfigDir, hookFileName(h))
	}

	result := InstallResult{Harness: name, Scope: scope, HooksPath: hooksPath}
	if needsMerge(h) {
		result.Merged = true
		result.Error = removeMergedHooks(hooksPath, h.HookFormat)
	} else {
		result.Error = os.Remove(hooksPath)
		if os.IsNotExist(result.Error) {
			result.Error = nil
		}
	}
	if h.PluginManifest != "" {
		os.Remove(filepath.Join(home, h.PluginManifest))
	}
	return result
}

func removeMergedHooks(path string, format HookFormat) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	switch format {
	case JSONNested:
		var obj map[string]any
		if json.Unmarshal(data, &obj) != nil {
			return nil
		}
		delete(obj, "hooks")
		if len(obj) == 0 {
			return os.Remove(path)
		}
		out, _ := json.MarshalIndent(obj, "", "  ")
		return os.WriteFile(path, out, 0644)
	default:
		return os.Remove(path)
	}
}

// HooksInstalled checks if belt hooks exist for an agent at the given scope.
func HooksInstalled(name string, scope InstallScope) bool {
	h, ok := All[name]
	if !ok {
		return false
	}
	var root string
	switch scope {
	case ScopeProject:
		root, _ = os.Getwd()
	default:
		root, _ = os.UserHomeDir()
	}
	if root == "" {
		return false
	}
	path := filepath.Join(root, h.HookConfigDir, hookFileName(h))
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "belt plugin hook")
}

func generateHookConfig(name string, h Harness) (string, error) {
	switch h.HookFormat {
	case JSONNested:
		return generateJSONNested(name, h), nil
	case JSONFlat:
		return generateJSONFlat(h), nil
	case JSONKiro:
		return generateJSONKiro(h), nil
	case JSONCopilot:
		return generateJSONCopilot(h), nil
	case TOML:
		return generateTOML(h), nil
	case YAML:
		return generateYAML(h), nil
	case TSExtension:
		return generateTSExtension(h), nil
	case TSPlugin:
		return generateTSPlugin(name, h), nil
	default:
		return "", fmt.Errorf("unsupported format")
	}
}

func beltCmd(event string) string {
	return fmt.Sprintf("belt plugin hook %s", event)
}

func hookTimeout(h Harness) int {
	if h.HookTimeoutMs {
		return 10000
	}
	return 10
}

func generateJSONFlat(h Harness) string {
	evts := h.Events
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		parts = append(parts, fmt.Sprintf(`"%s":[{"type":"command","command":"%s","timeout":%d}]`, event, beltCmd(beltEvent), timeout))
	}

	add(evts.SessionStart, "session-start")
	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.PreToolUse, "pre-tool-use")
	add(evts.PostToolUse, "post-tool-use")
	add(evts.Stop, "stop")
	add(evts.PreCompact, "pre-compact")

	return `{"hooks":{` + strings.Join(parts, ",") + `}}`
}

func generateJSONKiro(h Harness) string {
	evts := h.Events
	var hooks []string
	timeout := hookTimeout(h)

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		hooks = append(hooks, fmt.Sprintf(`{"name":"belt-%s","trigger":"%s","action":{"type":"command","command":"%s"},"timeout":%d}`,
			beltEvent, event, beltCmd(beltEvent), timeout))
	}

	add(evts.SessionStart, "session-start")
	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.PreToolUse, "pre-tool-use")
	add(evts.PostToolUse, "post-tool-use")
	add(evts.Stop, "stop")
	add(evts.PreCompact, "pre-compact")

	return fmt.Sprintf(`{"version":"v1","hooks":[%s]}`, strings.Join(hooks, ","))
}

func generateJSONNested(name string, h Harness) string {
	evts := h.Events
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event, beltEvent string, matcher string) {
		if event == "" {
			return
		}
		cmd := beltCmd(beltEvent)
		hook := fmt.Sprintf(`{"type":"command","command":"%s","timeout":%d}`, cmd, timeout)
		if matcher != "" {
			parts = append(parts, fmt.Sprintf(`"%s":[{"matcher":"%s","hooks":[%s]}]`, event, matcher, hook))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s":[{"hooks":[%s]}]`, event, hook))
		}
	}

	add(evts.SessionStart, "session-start", "")
	add(evts.PromptSubmit, "user-prompt-submit", "")
	add(evts.PreToolUse, "pre-tool-use", "*")
	add(evts.PostToolUse, "post-tool-use", "")
	add(evts.Stop, "stop", "")
	add(evts.PreCompact, "pre-compact", "")

	hooks := "{" + strings.Join(parts, ",") + "}"

	if h.HookNoEnvelope {
		return hooks
	}
	return fmt.Sprintf(`{"hooks":%s}`, hooks)
}

func generateJSONCopilot(h Harness) string {
	evts := h.Events
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		parts = append(parts, fmt.Sprintf(`"%s":[{"type":"command","bash":"%s","timeoutSec":%d}]`,
			event, beltCmd(beltEvent), timeout))
	}

	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.Stop, "stop")

	return fmt.Sprintf(`{"version":1,"hooks":{%s}}`, strings.Join(parts, ","))
}

func generateTOML(h Harness) string {
	evts := h.Events
	var lines []string
	timeout := hookTimeout(h)

	add := func(event, beltEvent, matcher string) {
		if event == "" {
			return
		}
		lines = append(lines, fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"", event))
		if matcher != "" {
			lines = append(lines, fmt.Sprintf("matcher = \"%s\"", matcher))
		}
		lines = append(lines, fmt.Sprintf("command = \"%s\"\ntimeout = %d", beltCmd(beltEvent), timeout))
	}

	add(evts.SessionStart, "session-start", "")
	add(evts.PromptSubmit, "user-prompt-submit", "")
	add(evts.PreToolUse, "pre-tool-use", "*")
	add(evts.PostToolUse, "post-tool-use", "")
	add(evts.Stop, "stop", "")

	return strings.Join(lines, "\n") + "\n"
}

func generateYAML(h Harness) string {
	evts := h.Events
	var lines []string
	lines = append(lines, "hooks:")
	timeout := hookTimeout(h)

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		lines = append(lines, fmt.Sprintf("  %s:\n    - command: %s\n      timeout: %d", event, beltCmd(beltEvent), timeout))
	}

	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.PreToolUse, "pre-tool-use")
	add(evts.PostToolUse, "post-tool-use")
	add(evts.Stop, "stop")

	return strings.Join(lines, "\n") + "\n"
}

func generateTSExtension(h Harness) string {
	evts := h.Events
	var handlers []string

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		handlers = append(handlers, fmt.Sprintf(`  pi.on("%s", async () => {
    const { execSync } = require("child_process");
    try { execSync("%s", { timeout: 5000 }); } catch {}
  });`, event, beltCmd(beltEvent)))
	}

	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.Stop, "stop")

	return fmt.Sprintf("export default function (pi: any) {\n%s\n}\n", strings.Join(handlers, "\n"))
}

func generateTSPlugin(name string, h Harness) string {
	evts := h.Events
	var hooks []string

	add := func(event, beltEvent string) {
		if event == "" {
			return
		}
		hooks = append(hooks, fmt.Sprintf(`    "%s": async () => {
      const { execSync } = require("child_process");
      try { execSync("%s", { timeout: 5000 }); } catch {}
    }`, event, beltCmd(beltEvent)))
	}

	add(evts.PromptSubmit, "user-prompt-submit")
	add(evts.PreToolUse, "pre-tool-use")
	add(evts.PostToolUse, "post-tool-use")

	if evts.Stop != "" {
		hooks = append(hooks, fmt.Sprintf(`    "event": async ({ event }: any) => {
      if (event && event.type === "%s") {
        const { execSync } = require("child_process");
        try { execSync("%s", { timeout: 5000 }); } catch {}
      }
    }`, evts.Stop, beltCmd("stop")))
	}

	body := fmt.Sprintf("export const BeltPlugin = async (_ctx: any) => {\n  return {\n%s,\n  };\n};\n",
		strings.Join(hooks, ",\n"))

	if h.TSPluginExport != "" {
		return body + "\n" + h.TSPluginExport + "\n"
	}
	return body
}

// File operations

func writeFile(path, content string) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	return os.WriteFile(path, []byte(content), 0644)
}

func mergeJSONHooks(path, newContent string) error {
	os.MkdirAll(filepath.Dir(path), 0755)

	var newObj map[string]any
	json.Unmarshal([]byte(newContent), &newObj)

	newHooks, _ := newObj["hooks"].(map[string]any)
	if newHooks == nil {
		return writeFile(path, newContent)
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		return writeFile(path, newContent)
	}

	var existingObj map[string]any
	if json.Unmarshal(existing, &existingObj) != nil {
		return writeFile(path, newContent)
	}

	existingHooks, _ := existingObj["hooks"].(map[string]any)
	if existingHooks == nil {
		existingObj["hooks"] = newHooks
	} else {
		for k, v := range newHooks {
			existingHooks[k] = v
		}
	}

	out, _ := json.MarshalIndent(existingObj, "", "  ")
	return os.WriteFile(path, out, 0644)
}

func appendTOMLHooks(path, tomlContent string) error {
	os.MkdirAll(filepath.Dir(path), 0755)

	existing, _ := os.ReadFile(path)
	combined := string(existing) + "\n# belt hooks\n" + tomlContent
	return os.WriteFile(path, []byte(combined), 0644)
}

func mergeYAMLHooks(path, yamlContent string) error {
	os.MkdirAll(filepath.Dir(path), 0755)

	existing, err := os.ReadFile(path)
	if err != nil {
		return writeFile(path, yamlContent)
	}

	content := string(existing)
	if strings.Contains(content, "hooks: {}") {
		content = strings.Replace(content, "hooks: {}", "", 1)
	}
	return os.WriteFile(path, []byte(content+"\n"+yamlContent), 0644)
}
