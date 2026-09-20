package harness

import (
	"os"
	"regexp"
	"strings"
)

// Which agent belt is running *inside* is a different question from which
// agents are installed on this machine. DetectOne answers the second from
// config dirs, binaries and package registries; this file answers the first,
// from what the surrounding process exported.
//
// The two must not share a table. A registry entry's DetectEnvVars says "this
// agent has been used here"; a runtime entry says "this process is a child of
// that agent right now". Cursor is the case that proves it: the registry's
// "cursor" is the cursor-agent CLI, which belt reports as cursor-cli, while
// "cursor" in belt's own naming is the IDE.

// RunningAgent identifies the coding agent belt is executing inside.
type RunningAgent struct {
	// Name is the stable name belt reports. Survey rows group on it, so it
	// changes only when the agent does.
	Name string

	// Version is what the runtime declared, when it declared one.
	Version string

	// Signal names the evidence, so a wrong answer can be traced to the rule
	// that produced it rather than guessed at.
	Signal string
}

// runtimeAgent is one detectable runtime.
type runtimeAgent struct {
	// Name is the stable reporting name, which is not always the registry key.
	Name string

	// Harness is the registry key for the same program, or "" when belt can
	// only ever observe this runtime and never install into it.
	Harness string

	// Env identifies the runtime when any one of these is set and non-empty.
	Env []string

	// Files identifies the runtime by a marker path.
	Files []string

	// VariantEnv promotes the match to VariantName when that variable is set:
	// Claude Code inside Cowork is the same binary reporting a different
	// product.
	VariantEnv  string
	VariantName string

	// Measured records how the Env list was established. "" means it came
	// from another source and no run has confirmed it here.
	Measured string
}

// runtimeAgents is ordered: the first match wins, so a more specific runtime
// must precede the one it is a variant of.
//
// Entries marked measured were taken from `--probe env`, which dumps the
// environment each agent hands its hooks — a hook is a child process, so that
// dump is exactly what the agent exports. Unmeasured entries carry vars that
// belt's CLI has relied on; this repo cannot install those agents, so absence
// from a run is not evidence against them.
var runtimeAgents = []runtimeAgent{
	{
		Name: "claude-code", Harness: "claude",
		Env:         []string{"CLAUDECODE", "CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID"},
		VariantEnv:  "CLAUDE_CODE_IS_COWORK",
		VariantName: "cowork",
		Measured:    "2026-09, claude 2.1.273: CLAUDECODE, CLAUDE_CODE_ENTRYPOINT, CLAUDE_CODE_SESSION_ID, CLAUDE_PID",
	},
	{
		// cursor-agent, the CLI. Measured vars belong here and not to the IDE.
		Name: "cursor-cli", Harness: "cursor",
		Env:      []string{"CURSOR_AGENT", "CURSOR_INVOKED_AS", "CURSOR_VERSION", "CURSOR_PROJECT_DIR"},
		Measured: "2026-09, cursor-agent 2026.09.10: CURSOR_INVOKED_AS=agent, CURSOR_VERSION, CURSOR_PROJECT_DIR, CURSOR_RIPGREP_PATH, CURSOR_USER_EMAIL",
	},
	{
		// The IDE's integrated terminal, which this repo cannot run.
		Name: "cursor",
		Env:  []string{"CURSOR_TRACE_ID"},
	},
	{
		Name: "codex", Harness: "codex",
		Env:      []string{"CODEX_SANDBOX", "CODEX_CI", "CODEX_THREAD_ID", "CODEX_MANAGED_BY_NPM"},
		Measured: "2026-09, codex-cli 0.154.0: CODEX_MANAGED_BY_NPM, CODEX_MANAGED_PACKAGE_ROOT",
	},
	{
		Name: "github-copilot", Harness: "copilot",
		// COPILOT_MODEL and COPILOT_ALLOW_ALL are omitted on purpose: this
		// suite sets both as configuration, so a run cannot tell an agent
		// that exports them from its own setup, and a user who sets
		// COPILOT_MODEL by hand is not inside Copilot.
		Env:      []string{"COPILOT_CLI", "COPILOT_LOADER_PID", "COPILOT_PROJECT_DIR", "COPILOT_GITHUB_TOKEN"},
		Measured: "2026-09, Copilot CLI 1.0.85: COPILOT_CLI, COPILOT_CLI_BINARY_VERSION, COPILOT_LOADER_PID, COPILOT_PROJECT_DIR",
	},
	{
		Name: "gemini", Harness: "gemini",
		Env:      []string{"GEMINI_CLI", "GEMINI_SESSION_ID", "GEMINI_CLI_NO_RELAUNCH", "GEMINI_CWD"},
		Measured: "2026-09, gemini-cli 0.60.0: GEMINI_SESSION_ID, GEMINI_CLI_NO_RELAUNCH, GEMINI_CWD, GEMINI_PLANS_DIR",
	},
	{
		Name: "qwen", Harness: "qwen",
		Env:      []string{"QWEN_CODE_CLI", "QWEN_CODE_SESSION_ID", "QWEN_CODE_AGENT_ID"},
		Measured: "2026-09, qwen 0.23.4: QWEN_CODE_CLI, QWEN_CODE_SESSION_ID, QWEN_CODE_AGENT_ID, QWEN_CODE_PROJECT_DIR",
	},
	{
		Name: "droid", Harness: "droid",
		// FACTORY_API_* are this suite's own configuration, so they are not
		// identity. DROID_* and the non-config FACTORY_* are droid's.
		Env:      []string{"DROID_PROJECT_DIR", "DROID_PLUGIN_ROOT", "FACTORY_UPSTREAM_CLIENT_TYPE", "FACTORY_ENV"},
		Measured: "2026-09, droid 0.220.0: DROID_PROJECT_DIR, DROID_PLUGIN_ROOT, FACTORY_UPSTREAM_CLIENT_TYPE, FACTORY_ENV, FACTORY_DEPLOYMENT_ENV",
	},
	{
		Name: "grok", Harness: "grok",
		Env:      []string{"GROK_SESSION_ID", "GROK_WORKSPACE_ROOT"},
		Measured: "2026-09, grok 1.0.30: GROK_SESSION_ID, GROK_WORKSPACE_ROOT; GROK_HOOK_EVENT and GROK_HOOK_NAME are set for hooks only",
	},
	{
		Name: "kiro", Harness: "kiro",
		Env:      []string{"KIRO_SESSION_ID", "KIRO_VERSION", "KIRO_CHAT_CLI_BIN"},
		Measured: "2026-09, kiro-cli 2.21.4: KIRO_SESSION_ID, KIRO_VERSION, KIRO_CHAT_CLI_BIN, KIRO_TELEMETRY_CLIENT_ID",
	},
	{
		Name: "hermes", Harness: "hermes",
		Env:      []string{"HERMES_SESSION_ID", "HERMES_INTERACTIVE"},
		Measured: "2026-09, Hermes 0.19.0: HERMES_SESSION_ID, HERMES_INTERACTIVE, HERMES_QUIET, HERMES_KANBAN_BOARD",
	},
	{
		Name: "goose", Harness: "goose",
		// GOOSE_MODE/MODEL/PROVIDER are this suite's configuration.
		Env:      []string{"GOOSE_DISABLE_KEYRING"},
		Measured: "2026-09, goose 1.50.1: GOOSE_DISABLE_KEYRING is the only non-config GOOSE_* var exported",
	},
	{
		Name: "kilo", Harness: "kilo",
		Env:      []string{"KILO_TREE_SITTER_WASM_DIR"},
		Measured: "2026-09, kilo 7.7.2: KILO_TREE_SITTER_WASM_DIR is the only identifying var exported",
	},
	{Name: "pi", Harness: "pi",
		Env:      []string{"PI_CODING_AGENT"},
		Measured: "2026-09, pi 0.85.1: PI_CODING_AGENT",
	},
	{Name: "windsurf", Harness: "windsurf", Env: []string{"WINDSURF_EXTENSION_HOST_ROLE"}},
	// These three export nothing that identifies them, so they carry no Env
	// rule and are reached only through AI_AGENT, which belt's own generated
	// hook config sets for them (DeclaresAgent). They are listed so the names
	// belt can report are all in one place — a consumer grouping survey rows
	// on the name can check them against RunningNames.
	//
	// opencode keeps OPENCODE_CLIENT because belt has always checked it; a
	// run shows opencode 1.18.31 never setting it, and absence in one version
	// is not proof for every surface.
	{Name: "opencode", Harness: "opencode", Env: []string{"OPENCODE_CLIENT"}},
	{Name: "kimi", Harness: "kimi"},
	{Name: "omp", Harness: "omp"},
	{Name: "antigravity", Env: []string{"ANTIGRAVITY_AGENT"}},
	{Name: "augment", Env: []string{"AUGMENT_AGENT"}},
	{Name: "replit", Env: []string{"REPL_ID"}},
	{Name: "devin", Files: []string{"/opt/.devin"}},
}

// UndetectableByEnv names the agents a run has confirmed export nothing that
// identifies them. They are listed so the gap is a recorded measurement
// rather than an entry someone later "fixes" with a guess.
//
// 2026-09: opencode 1.18.31 exports 13 variables and none name it — the
// OPENCODE_CLIENT belt has checked for is never set. kimi 0.43.1 exports 12,
// the only KIMI_* one being the base URL this suite configured. omp 18.2.1
// exports __PI_NATIVE_VARIANT_CACHE, a pi-family internal whose value does not
// name omp.
//
// All three run belt from a TS plugin rather than a command hook, so the
// generated plugin is the natural place to declare the agent.
var UndetectableByEnv = []string{"kimi", "omp", "opencode"}

// versionedAgent matches the "<name>_<version>_<surface>" form some runtimes
// put in AI_AGENT, e.g. "claude-code_2-1-220_agent".
//
// The trailing word is not always "agent". Measured 2026-09: Claude Code
// 2.1.263 emits _agent and 2.1.278 emits _harness. A pattern anchored on
// _agent stops matching at that upgrade and the whole string becomes the
// name, so every version opens its own survey row.
var versionedAgent = regexp.MustCompile(`^(.+?)_(\d[\d-]*)_[a-z][a-z0-9-]*$`)

// splitAgentVersion separates a stable name from its version so the name can
// be grouped on. A plain name passes through unchanged.
func splitAgentVersion(raw string) (name, version string) {
	m := versionedAgent.FindStringSubmatch(raw)
	if m == nil {
		return raw, ""
	}
	return m[1], strings.ReplaceAll(m[2], "-", ".")
}

// DetectRunning reports which coding agent belt is executing inside.
//
// AI_AGENT wins: it is belt's own convention and the only signal that works
// for an agent exporting nothing of its own. Environment evidence comes next,
// then marker files.
func DetectRunning() (RunningAgent, bool) {
	if raw := os.Getenv("AI_AGENT"); raw != "" {
		name, ver := splitAgentVersion(raw)
		return RunningAgent{
			Name:    applyVariant(name),
			Version: ver,
			Signal:  "AI_AGENT",
		}, true
	}
	for _, rt := range runtimeAgents {
		for _, v := range rt.Env {
			if os.Getenv(v) == "" {
				continue
			}
			return RunningAgent{Name: rt.resolve(), Signal: v}, true
		}
		for _, f := range rt.Files {
			if _, err := os.Stat(f); err == nil {
				return RunningAgent{Name: rt.resolve(), Signal: f}, true
			}
		}
	}
	return RunningAgent{}, false
}

// resolve returns the runtime's name, promoted to its variant when that
// variant's variable is set.
func (rt runtimeAgent) resolve() string {
	if rt.VariantEnv != "" && os.Getenv(rt.VariantEnv) != "" {
		return rt.VariantName
	}
	return rt.Name
}

// applyVariant refines a name from AI_AGENT the same way an env match is
// refined, so "claude-code" inside Cowork reports as cowork either way.
func applyVariant(name string) string {
	for _, rt := range runtimeAgents {
		if rt.Name == name && rt.VariantEnv != "" && os.Getenv(rt.VariantEnv) != "" {
			return rt.VariantName
		}
	}
	return name
}

// RunningName is DetectRunning's name alone, empty when belt is not inside a
// known agent.
func RunningName() string {
	r, ok := DetectRunning()
	if !ok {
		return ""
	}
	return r.Name
}

// RunningNames lists every name DetectRunning can return, so a consumer that
// groups on the name can be checked against the set that produces it.
func RunningNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, rt := range runtimeAgents {
		for _, n := range []string{rt.Name, rt.VariantName} {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
