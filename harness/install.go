package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// GoosePluginDir is belt's plugin directory under goose's shared plugin root.
//
// It was ".agents/plugins/belt-test/hooks" until 2026-09: this suite's name
// had been copied into the shipped registry, so every belt user's goose hooks
// went into a directory named after the test harness. Renaming it moves the
// install, so Install removes the old one.
const GoosePluginDir = ".agents/plugins/belt"

// gooseLegacyPluginDir is where belt used to write goose's hooks. Removable
// once no install predating 2026-09 is in use.
const gooseLegacyPluginDir = ".agents/plugins/belt-test"

// InstallScope determines where hooks are written.
type InstallScope int

// Deprecated: use InstallScope.
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

	// Inactive names the agent the user selected instead of belt's, when an
	// install writes hooks the agent will not read. Discovered here, so
	// doctor and the test suite report it rather than each re-deriving it.
	Inactive string
}

// These names are what go/cli imported before harness/ was extracted out of
// it. They forward to the canonical names and go away once go/cli is off them.

// Deprecated: use KnownNames.
func KnownAgentNames() []string { return KnownNames() }

// Deprecated: use Install.
func InstallHooks(name string, scope InstallScope) InstallResult { return Install(name, scope) }

// Deprecated: use Uninstall.
func UninstallHooks(name string, scope InstallScope) InstallResult { return Uninstall(name, scope) }

// Deprecated: use HookConfig, which reports why generation failed.
func HookTemplate(name string) string {
	s, _ := HookConfig(name)
	return s
}

// Install writes belt hook configs for a harness at the given scope.
func Install(name string, scope InstallScope) InstallResult {
	return InstallWithCommand(name, scope, beltCmd)
}

// InstallWithCommand is Install with a caller's own hook command. The test
// suite uses it to install hooks that run its own scripts: the file shape,
// the merge behaviour and every per-agent quirk then come from this code
// rather than from a second implementation that can drift from it.
func InstallWithCommand(name string, scope InstallScope, cmdFor HookCommand) InstallResult {
	h, ok := All[name]
	if !ok {
		return InstallResult{Harness: name, Error: fmt.Errorf("unknown harness: %s", name)}
	}

	home, _ := os.UserHomeDir()
	var hooksPath string
	root := home

	switch scope {
	case ScopeUser:
		target := HooksTarget(name)
		if target == "" {
			return InstallResult{Harness: name, Error: fmt.Errorf("no user hook path for %s", name)}
		}
		hooksPath = filepath.Join(home, target)
	case ScopeProject:
		root, _ = os.Getwd()
		hooksPath = filepath.Join(root, h.HookConfigDir, hookFileName(h))
	}

	content, err := generateHookConfig(name, h, cmdFor)
	if err != nil {
		return InstallResult{Harness: name, Error: err}
	}

	result := InstallResult{Harness: name, Scope: scope, HooksPath: hooksPath}

	// For formats that merge into existing files (settings.json, config.yaml, config.toml),
	// we need to read-modify-write. For standalone files, just write.
	switch h.HookFormat {
	case JSONNested:
		if name == "goose" {
			// Migration, added 2026-09: belt wrote goose's hooks into a
			// directory named after this test suite. Remove the old file so
			// goose does not run both.
			removeMergedHooks(filepath.Join(root, gooseLegacyPluginDir, "hooks", hookFileName(h)), JSONNested)
			os.Remove(filepath.Join(root, gooseLegacyPluginDir, "hooks"))
			os.Remove(filepath.Join(root, gooseLegacyPluginDir))
		}
		if needsMerge(h) {
			result.Merged = true
			err = mergeJSONHooks(hooksPath, content)
		} else {
			err = writeFile(hooksPath, content)
			result.Created = true
		}
	case JSONFlat, JSONCopilot:
		err = writeFile(hooksPath, content)
		result.Created = true
	case JSONKiro:
		result.Merged = true
		err = mergeKiroAgentHooks(hooksPath, content)
		if err == nil {
			// Migration, added 2026-09 in belt 1.18.32: earlier versions
			// merged into a kiro_default.json override. Removable once no
			// install predating that version is in use.
			removeMergedHooks(filepath.Join(root, h.HookConfigDir, KiroDefaultAgentName+".json"), JSONKiro)
			// A user-chosen default agent is left alone and reported.
			result.Inactive, err = KiroSelectBeltAgent(root)
		}
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
	return generateHookConfig(name, h, beltCmd)
}

// Uninstall removes belt hooks for a harness at the given scope.
func Uninstall(name string, scope InstallScope) InstallResult {
	h, ok := All[name]
	if !ok {
		return InstallResult{Harness: name, Error: fmt.Errorf("unknown harness: %s", name)}
	}

	home, _ := os.UserHomeDir()
	var hooksPath string
	root := home
	switch scope {
	case ScopeUser:
		target := HooksTarget(name)
		if target == "" {
			return InstallResult{Harness: name, Error: fmt.Errorf("no user hook path for %s", name)}
		}
		hooksPath = filepath.Join(home, target)
	case ScopeProject:
		root, _ = os.Getwd()
		hooksPath = filepath.Join(root, h.HookConfigDir, hookFileName(h))
	}

	result := InstallResult{Harness: name, Scope: scope, HooksPath: hooksPath}
	if h.HookFormat == JSONKiro {
		result.Merged = true
		result.Error = removeMergedHooks(hooksPath, JSONKiro)
		removeMergedHooks(filepath.Join(root, h.HookConfigDir, KiroDefaultAgentName+".json"), JSONKiro)
		if err := kiroDeselectBeltAgent(root); result.Error == nil {
			result.Error = err
		}
	} else if needsMerge(h) {
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
	if format != JSONNested && format != JSONKiro {
		return os.Remove(path)
	}
	// An unreadable or unparseable file is left exactly as it is: it is the
	// user's, and belt did not write it.
	obj, err := readJSONObject(path)
	if err != nil {
		return nil
	}
	switch format {
	case JSONNested:
		delete(obj, "hooks")
		if len(obj) == 0 {
			return os.Remove(path)
		}
		return writeJSONObject(path, obj, 0644)
	case JSONKiro:
		hooks, _ := obj["hooks"].(map[string]any)
		for k, v := range hooks {
			kept := withoutBeltHooks(v)
			if len(kept) == 0 {
				delete(hooks, k)
			} else {
				hooks[k] = kept
			}
		}
		if len(hooks) == 0 {
			delete(obj, "hooks")
		}
		// Only belt's own scaffold left: remove the file.
		if obj["description"] == kiroAgentDescription && isKiroScaffold(obj) {
			return os.Remove(path)
		}
		return writeJSONObject(path, obj, 0644)
	}
	return nil
}

// kiroAgentDescription marks a kiro agent file belt created; the legacy
// kiro_default.json override carried the same description.
const kiroAgentDescription = "Default agent with belt hooks"

// isKiroScaffold reports whether obj holds nothing beyond what generateJSONKiro writes.
func isKiroScaffold(obj map[string]any) bool {
	for k := range obj {
		switch k {
		case "name", "description", "tools", "includeMcpJson":
		default:
			return false
		}
	}
	return true
}

// withoutBeltHooks drops entries whose command is a belt hook from a kiro hook list.
func withoutBeltHooks(list any) []any {
	arr, _ := list.([]any)
	var kept []any
	for _, e := range arr {
		m, _ := e.(map[string]any)
		cmd, _ := m["command"].(string)
		if strings.Contains(cmd, "belt plugin hook") {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// mergeKiroAgentHooks adds belt's hooks to a kiro agent config, keeping the
// user's prompt, tools, and their own hooks; belt entries are replaced, not duplicated.
func mergeKiroAgentHooks(path, newContent string) error {
	obj, err := readJSONObject(path)
	if err != nil {
		return writeFile(path, newContent)
	}
	var newObj map[string]any
	json.Unmarshal([]byte(newContent), &newObj)
	newHooks, _ := newObj["hooks"].(map[string]any)

	hooks, _ := obj["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for k, v := range newHooks {
		hooks[k] = append(withoutBeltHooks(hooks[k]), v.([]any)...)
	}
	obj["hooks"] = hooks
	return writeJSONObject(path, obj, 0644)
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

func generateHookConfig(name string, h Harness, cmdFor HookCommand) (string, error) {
	switch h.HookFormat {
	case JSONNested:
		return generateJSONNested(name, h, cmdFor), nil
	case JSONFlat:
		return generateJSONFlat(h, cmdFor), nil
	case JSONKiro:
		return generateJSONKiro(h, cmdFor), nil
	case JSONCopilot:
		return generateJSONCopilot(h, cmdFor), nil
	case TOML:
		return generateTOML(h, cmdFor), nil
	case YAML:
		return generateYAML(h, cmdFor), nil
	case TSExtension:
		return generateTSExtension(h, cmdFor), nil
	case TSPlugin:
		return generateTSPlugin(name, h, cmdFor), nil
	default:
		return "", fmt.Errorf("unsupported format")
	}
}

// HookCommand builds the command a hook runs for a belt event. belt's own
// hooks are the default; the test suite passes its own script so the config
// it tests is generated by this same code rather than a second copy of it.
type HookCommand func(beltEvent HookEvent) string

func beltCmd(event HookEvent) string {
	return fmt.Sprintf("belt plugin hook %s", event)
}

func hookTimeout(h Harness) int {
	if h.HookTimeoutMs {
		return 10000
	}
	return 10
}

func generateJSONFlat(h Harness, cmdFor HookCommand) string {
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent) {
		if event == "" {
			return
		}
		if h.HookFlatBare {
			parts = append(parts, fmt.Sprintf(`"%s":[{"command":"%s"}]`, event, jsonEscape(cmdFor(beltEvent))))
			return
		}
		parts = append(parts, fmt.Sprintf(`"%s":[{"type":"command","command":"%s","timeout":%d}]`, event, jsonEscape(cmdFor(beltEvent)), timeout))
	}

	for _, e := range h.Defined() {
		add(e.AgentName(h), e)
	}

	hooks := "{" + strings.Join(parts, ",") + "}"
	if h.HookWrapper != "" {
		return fmt.Sprintf(h.HookWrapper, hooks)
	}
	return `{"hooks":` + hooks + `}`
}

func generateJSONKiro(h Harness, cmdFor HookCommand) string {
	var hooks []string
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent) {
		if event == "" {
			return
		}
		hooks = append(hooks, fmt.Sprintf(`"%s":[{"command":"%s","timeout_ms":%d}]`, event, jsonEscape(cmdFor(beltEvent)), timeout))
	}

	for _, e := range h.Defined() {
		add(e.AgentName(h), e)
	}

	// Selected as the default, belt's agent stands in for the built-in one: a
	// config without "tools" has no tools at all (verified: the request
	// carries no toolSpecification). tools ["*"] + includeMcpJson keep the
	// built-in behaviour; steering, README and skill descriptions reach the
	// model as they do with the built-in (compared in Docker on V1 and V2).
	return fmt.Sprintf(`{"name":"%s","description":"%s","tools":["*"],"includeMcpJson":true,"hooks":{%s}}`, KiroBeltAgentName, kiroAgentDescription, strings.Join(hooks, ","))
}

func generateJSONNested(name string, h Harness, cmdFor HookCommand) string {
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent, matcher string) {
		if event == "" {
			return
		}
		cmd := jsonEscape(cmdFor(beltEvent))
		hook := fmt.Sprintf(`{"type":"command","command":"%s","timeout":%d}`, cmd, timeout)
		if matcher != "" {
			parts = append(parts, fmt.Sprintf(`"%s":[{"matcher":"%s","hooks":[%s]}]`, event, matcher, hook))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s":[{"hooks":[%s]}]`, event, hook))
		}
	}

	for _, e := range h.Defined() {
		matcher := ""
		if e == PreToolUse {
			matcher = "*"
		}
		add(e.AgentName(h), e, matcher)
	}

	hooks := "{" + strings.Join(parts, ",") + "}"

	if h.HookNoEnvelope {
		return hooks
	}
	return fmt.Sprintf(`{"hooks":%s}`, hooks)
}

func generateJSONCopilot(h Harness, cmdFor HookCommand) string {
	parts := []string{}
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent) {
		if event == "" {
			return
		}
		parts = append(parts, fmt.Sprintf(`"%s":[{"type":"command","bash":"%s","timeoutSec":%d}]`,
			event, jsonEscape(cmdFor(beltEvent)), timeout))
	}

	for _, e := range h.Defined() {
		add(e.AgentName(h), e)
	}

	return fmt.Sprintf(`{"version":1,"hooks":{%s}}`, strings.Join(parts, ","))
}

func generateTOML(h Harness, cmdFor HookCommand) string {
	var lines []string
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent, matcher string) {
		if event == "" {
			return
		}
		lines = append(lines, fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"", event))
		if matcher != "" {
			lines = append(lines, fmt.Sprintf("matcher = \"%s\"", matcher))
		}
		lines = append(lines, fmt.Sprintf("command = \"%s\"\ntimeout = %d", jsonEscape(declare(h, cmdFor(beltEvent))), timeout))
	}

	for _, e := range h.Defined() {
		matcher := ""
		if e == PreToolUse {
			matcher = "*"
		}
		add(e.AgentName(h), e, matcher)
	}

	return strings.Join(lines, "\n") + "\n"
}

func generateYAML(h Harness, cmdFor HookCommand) string {
	var lines []string
	lines = append(lines, "hooks:")
	timeout := hookTimeout(h)

	add := func(event string, beltEvent HookEvent) {
		if event == "" {
			return
		}
		lines = append(lines, fmt.Sprintf("  %s:\n    - command: %s\n      timeout: %d", event, cmdFor(beltEvent), timeout))
	}

	for _, e := range h.Defined() {
		add(e.AgentName(h), e)
	}

	return strings.Join(lines, "\n") + "\n"
}

// tsExecOpts keeps the hook command off the agent's own stdin. execSync
// inherits it by default, and over ACP that is the JSON-RPC stream: a hook
// that reads stdin eats the agent's input.
func tsExecOpts(h Harness) string {
	return "{ timeout: 5000, stdio: [\"ignore\", \"pipe\", \"ignore\"]" + tsEnvOpt(h) + " }"
}

// tsEnvOpt renders the env option that declares which agent belt is running
// inside, for the agents measured to export nothing that identifies them.
// process.env is spread first: replacing the environment rather than adding to
// it would take PATH with it.
func tsEnvOpt(h Harness) string {
	if !DeclaresAgent(h.Name) {
		return ""
	}
	return fmt.Sprintf(`, env: { ...process.env, AI_AGENT: "%s" }`, h.Name)
}

// tsExecOptsInput is tsExecOpts for a hook that must be given the prompt.
// `input` supplies its own pipe, so the command is fed the payload without
// ever touching the stdin the agent is using.
//
// belt reads the prompt from stdin and returns without printing when there is
// none (suggest.ParseHookInput). Handing the command /dev/null therefore
// produced a hook that fired, printed nothing, and injected nothing — the
// channel correct end to end with nothing flowing through it.
func tsExecOptsInput(h Harness) string {
	return "{ input: beltInput, timeout: 5000, stdio: [\"pipe\", \"pipe\", \"ignore\"]" + tsEnvOpt(h) + " }"
}

// DeclaresAgent reports whether belt's generated hook config for this agent
// must state which agent it is. The agents in UndetectableByEnv export nothing
// that identifies them, so without the declaration belt cannot tell where it
// is running and every survey row from them is blank.
//
// It is deliberately not every agent: claude and pi set AI_AGENT themselves
// and carry their version in it, and overwriting that would throw the version
// away.
func DeclaresAgent(name string) bool {
	return slices.Contains(UndetectableByEnv, name)
}

// declare prefixes a shell hook command with the agent declaration. The
// command is evaluated by a shell — kimi's own hook commands already use ;
// and && — so an assignment followed by ; applies to the whole command,
// which a bare "VAR=x cmd" prefix would not for a compound one.
func declare(h Harness, cmd string) string {
	if !DeclaresAgent(h.Name) {
		return cmd
	}
	return fmt.Sprintf("export AI_AGENT=%s; %s", h.Name, cmd)
}

// tsRun renders the execSync call for a hook that only needs to fire, with
// its continuation lines indented to pad.
func tsRun(h Harness, cmd, pad string) string {
	return fmt.Sprintf("const { execSync } = require(\"child_process\");\n%stry { execSync(\"%s\", %s); } catch {}",
		pad, jsonEscape(cmd), tsExecOpts(h))
}

// tsRunCapture renders the execSync call for a hook whose stdout is context:
// it feeds the command the prompt payload bound to `beltInput`, binds the
// trimmed output to `out`, and runs sink only when it is non-empty.
// ContextPlugin agents have no stdout channel of their own, so the plugin file
// is the only place the command's output can reach the model.
func tsRunCapture(h Harness, cmd, sink, pad string) string {
	return fmt.Sprintf("const { execSync } = require(\"child_process\");\n"+
		"%[4]stry {\n"+
		"%[4]s  const out = execSync(\"%[1]s\", %[2]s).toString().trim();\n"+
		"%[4]s  if (out) %[3]s\n"+
		"%[4]s} catch {}",
		jsonEscape(cmd), tsExecOptsInput(h), sink, pad)
}

// injectsContext reports whether the agent takes this event's hook output as
// model context through the plugin file rather than through stdout.
func injectsContext(h Harness, e HookEvent) bool {
	return ContextChannelFor(h.Name, string(e)) == ContextPlugin
}

func generateTSExtension(h Harness, cmdFor HookCommand) string {
	var handlers []string

	for _, e := range h.Defined() {
		event := e.AgentName(h)
		if event == "" {
			continue
		}
		if injectsContext(h, e) {
			// before_agent_start carries the prompt (measured: the event has
			// type, prompt and systemPrompt), so the payload is built here.
			body := `const beltInput = JSON.stringify({ prompt: (event && event.prompt) || "" });` + "\n    " +
				tsRunCapture(h, cmdFor(e), `return { systemPrompt: (event.systemPrompt || "") + "\n" + out };`, "    ")
			handlers = append(handlers, fmt.Sprintf("  pi.on(\"%s\", async (event: any) => {\n    %s\n  });", event, body))
			continue
		}
		handlers = append(handlers, fmt.Sprintf("  pi.on(\"%s\", async () => {\n    %s\n  });", event, tsRun(h, cmdFor(e), "    ")))
	}

	return fmt.Sprintf("export default function (pi: any) {\n%s\n}\n", strings.Join(handlers, "\n"))
}

func generateTSPlugin(name string, h Harness, cmdFor HookCommand) string {
	var hooks []string

	for _, e := range h.Defined() {
		event := e.AgentName(h)
		if event == "" || e == Stop {
			// Stop is not a named hook in this format; it arrives through the
			// generic event channel below.
			continue
		}
		if injectsContext(h, e) {
			// This format's context hook is a system-prompt transform, and it
			// is handed only sessionID and model — no prompt (measured on
			// opencode 1.18 and kilo 7.7). chat.message is where the user's
			// text arrives, so it is captured there and used here. Measured
			// order: chat.message once at the start of the turn, then the
			// transform once per model request in it.
			body := tsRunCapture(h, cmdFor(e), "output.system.push(out);", "      ")
			hooks = append(hooks, fmt.Sprintf(`    "chat.message": async (input: any, output: any) => {
      beltSession = (input && input.sessionID) || "";
      beltPrompt = ((output && output.parts) || [])
        .filter((p: any) => p && p.type === "text")
        .map((p: any) => p.text)
        .join("\n");
      beltPending = true;
    }`))
			hooks = append(hooks, fmt.Sprintf("    \"%s\": async (_input: any, output: any) => {\n"+
				"      if (!beltPending) return;\n"+
				"      beltPending = false;\n"+
				"      const beltInput = JSON.stringify({ prompt: beltPrompt, session_id: beltSession });\n"+
				"      %s\n    }", event, body))
			continue
		}
		hooks = append(hooks, fmt.Sprintf("    \"%s\": async () => {\n      %s\n    }", event, tsRun(h, cmdFor(e), "      ")))
	}

	if stop := Stop.AgentName(h); stop != "" {
		hooks = append(hooks, fmt.Sprintf(`    "event": async ({ event }: any) => {
      if (event && event.type === "%s") {
        %s
      }
    }`, stop, tsRun(h, cmdFor(Stop), "        ")))
	}

	// The transform hook runs once per model request in a turn, so without a
	// pending flag belt was invoked three times for one prompt and pushed
	// three copies of the same suggestions into the system prompt.
	state := ""
	if injectsContext(h, PromptSubmit) {
		state = "  let beltPrompt = \"\";\n  let beltSession = \"\";\n  let beltPending = false;\n"
	}
	body := fmt.Sprintf("export const BeltPlugin = async (_ctx: any) => {\n%s  return {\n%s,\n  };\n};\n",
		state, strings.Join(hooks, ",\n"))

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

	existingObj, err := readJSONObject(path)
	if err != nil {
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

	return writeJSONObject(path, existingObj, 0644)
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

// readJSONObject reads a JSON object from path. A missing or unparseable file
// yields an empty object and the error, so a caller can tell "nothing there"
// from "do not touch this".
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}, err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return map[string]any{}, err
	}
	return obj, nil
}

// writeJSONObject writes obj to path, creating the directory, with the file
// mode the owning tool uses.
func writeJSONObject(path string, obj map[string]any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	out, _ := json.MarshalIndent(obj, "", "  ")
	return os.WriteFile(path, append(out, '\n'), perm)
}

// HookConfigFor generates an agent's hook configuration with a caller's own
// command, so a test can install hooks that are shaped exactly like belt's but
// run something it can observe.
func HookConfigFor(name string, cmdFor HookCommand) (string, error) {
	h, ok := All[name]
	if !ok {
		return "", fmt.Errorf("unknown harness: %s", name)
	}
	return generateHookConfig(name, h, cmdFor)
}

// HookFileName is the file an agent's hooks are written to.
func HookFileName(name string) string {
	h, ok := All[name]
	if !ok {
		return ""
	}
	return hookFileName(h)
}

// jsonEscape escapes a command for embedding inside a JSON or TOML string.
// belt's own hook command has no characters that need it, so nothing noticed
// until a caller passed one that did and the config came out malformed.
func jsonEscape(cmd string) string {
	b, _ := json.Marshal(cmd)
	return string(b[1 : len(b)-1])
}
