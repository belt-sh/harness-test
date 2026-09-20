package harness

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// clearRuntimeEnv unsets every variable any runtime rule reads, so one test's
// host environment cannot decide another test's answer. Running these inside
// a coding agent is the normal case, so without this the suite's result
// depends on who is running it.
func clearRuntimeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AI_AGENT", "")
	os.Unsetenv("AI_AGENT")
	for _, rt := range runtimeAgents {
		for _, v := range append(slices.Clone(rt.Env), rt.VariantEnv) {
			if v == "" {
				continue
			}
			t.Setenv(v, "")
			os.Unsetenv(v)
		}
	}
}

func TestDetectRunningFindsNothingInAPlainShell(t *testing.T) {
	clearRuntimeEnv(t)
	if r, ok := DetectRunning(); ok {
		t.Errorf("detected %q via %s with no agent variables set", r.Name, r.Signal)
	}
}

func TestDetectRunningIdentifiesAgentsByTheirOwnVariables(t *testing.T) {
	cases := []struct {
		env, value, want string
	}{
		{"CLAUDECODE", "1", "claude-code"},
		{"CLAUDE_CODE_SESSION_ID", "abc", "claude-code"},
		{"CURSOR_INVOKED_AS", "agent", "cursor-cli"},
		{"CURSOR_TRACE_ID", "t", "cursor"},
		{"CODEX_MANAGED_BY_NPM", "1", "codex"},
		{"COPILOT_CLI", "1", "github-copilot"},
		{"GEMINI_SESSION_ID", "s", "gemini"},
		{"QWEN_CODE_CLI", "1", "qwen"},
		{"DROID_PROJECT_DIR", "/w", "droid"},
		{"GROK_SESSION_ID", "s", "grok"},
		{"KIRO_SESSION_ID", "s", "kiro"},
		{"HERMES_SESSION_ID", "s", "hermes"},
		{"PI_CODING_AGENT", "1", "pi"},
		{"REPL_ID", "r", "replit"},
	}
	for _, c := range cases {
		t.Run(c.env, func(t *testing.T) {
			clearRuntimeEnv(t)
			t.Setenv(c.env, c.value)
			r, ok := DetectRunning()
			if !ok || r.Name != c.want {
				t.Errorf("%s=%s gave %q (%v), want %q", c.env, c.value, r.Name, ok, c.want)
			}
			if r.Signal != c.env {
				t.Errorf("signal = %q, want %q — a wrong answer must name the rule that made it", r.Signal, c.env)
			}
		})
	}
}

// Claude Code inside Cowork is the same binary reporting a different product,
// and survey rows group on the name, so the variant must win by both routes.
func TestCoworkVariantWinsByEnvAndByOverride(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_IS_COWORK", "1")
	if r, _ := DetectRunning(); r.Name != "cowork" {
		t.Errorf("env route gave %q, want cowork", r.Name)
	}
	clearRuntimeEnv(t)
	t.Setenv("AI_AGENT", "claude-code_2-1-220_agent")
	t.Setenv("CLAUDE_CODE_IS_COWORK", "1")
	r, _ := DetectRunning()
	if r.Name != "cowork" {
		t.Errorf("AI_AGENT route gave %q, want cowork", r.Name)
	}
	if r.Version != "2.1.220" {
		t.Errorf("version = %q, want 2.1.220", r.Version)
	}
}

// This suite sets COPILOT_MODEL, GOOSE_MODEL, FACTORY_API_KEY and friends as
// configuration. A runtime rule that reads one cannot tell the agent from its
// own setup, and would fire for any user who exported it by hand.
func TestNoRuntimeRuleReadsAVariableThisSuiteSets(t *testing.T) {
	configured := map[string][]string{}
	for name, h := range All {
		for k := range h.EnvVars {
			configured[k] = append(configured[k], name)
		}
		if h.APIKeyEnvVar != "" {
			configured[h.APIKeyEnvVar] = append(configured[h.APIKeyEnvVar], name)
		}
	}
	for _, rt := range runtimeAgents {
		for _, v := range rt.Env {
			if owners, ok := configured[v]; ok {
				t.Errorf("%s detects on %s, which this suite sets as configuration for %v", rt.Name, v, owners)
			}
		}
	}
}

// An agent measured to export nothing identifying must not acquire an env
// rule without a new measurement: the last one to be "fixed" by guessing was
// opencode, whose OPENCODE_CLIENT is never set.
func TestUndetectableAgentsHaveNoMeasuredEnvRule(t *testing.T) {
	for _, name := range UndetectableByEnv {
		for _, rt := range runtimeAgents {
			if rt.Harness != name {
				continue
			}
			if rt.Measured != "" {
				t.Errorf("%s is listed as undetectable by env but its rule claims a measurement: %s", name, rt.Measured)
			}
		}
	}
}

// Every runtime naming a registry harness must name one that exists.
func TestRuntimeHarnessKeysResolve(t *testing.T) {
	for _, rt := range runtimeAgents {
		if rt.Harness == "" {
			continue
		}
		if _, ok := All[rt.Harness]; !ok {
			t.Errorf("runtime %s points at unknown harness %q", rt.Name, rt.Harness)
		}
	}
}

// The trailing word in AI_AGENT is not stable across releases, and the name
// is what survey rows group on. These are values observed in the wild, not
// invented: 2.1.263 said _agent, 2.1.278 said _harness.
func TestAIAgentVersionSplitSurvivesTheSurfaceWord(t *testing.T) {
	cases := []struct{ raw, name, version string }{
		{"claude-code_2-1-263_agent", "claude-code", "2.1.263"},
		{"claude-code_2-1-278_harness", "claude-code", "2.1.278"},
		{"pi", "pi", ""},
		{"opencode", "opencode", ""},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			clearRuntimeEnv(t)
			t.Setenv("AI_AGENT", c.raw)
			r, ok := DetectRunning()
			if !ok || r.Name != c.name || r.Version != c.version {
				t.Errorf("AI_AGENT=%s gave name=%q version=%q, want %q/%q", c.raw, r.Name, r.Version, c.name, c.version)
			}
		})
	}
}

// An agent that exports nothing identifying can only be known if belt's own
// hook config says so. Losing that declaration would silently blank the
// survey rows for every one of them, with nothing else failing.
func TestUndetectableAgentsDeclareThemselvesInTheirHookConfig(t *testing.T) {
	for _, name := range UndetectableByEnv {
		if !DeclaresAgent(name) {
			t.Errorf("%s exports nothing identifying and does not declare itself either", name)
			continue
		}
		cfg, err := HookConfig(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(cfg, "AI_AGENT") {
			t.Errorf("%s: generated hook config carries no AI_AGENT declaration:\n%s", name, cfg)
		}
		if !strings.Contains(cfg, name) {
			t.Errorf("%s: declaration does not name the agent", name)
		}
	}
}

// The declaration is only for agents that need it. claude and pi set AI_AGENT
// themselves and carry their version in it; overwriting that with a bare name
// would throw the version away.
func TestDetectableAgentsDoNotDeclare(t *testing.T) {
	for name := range All {
		if DeclaresAgent(name) {
			continue
		}
		cfg, err := HookConfig(name)
		if err != nil {
			continue
		}
		if strings.Contains(cfg, "AI_AGENT") {
			t.Errorf("%s is detectable but its hook config overrides AI_AGENT", name)
		}
	}
}

// Every name DetectRunning can return should be reachable. A declared agent
// with no runtime entry still works, but it is missing from RunningNames,
// which is what a consumer checks its survey grouping against.
func TestEveryDeclaredAgentHasARuntimeEntry(t *testing.T) {
	for _, name := range UndetectableByEnv {
		found := false
		for _, rt := range runtimeAgents {
			if rt.Harness == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s declares itself but has no runtime entry, so RunningNames omits it", name)
		}
	}
}
