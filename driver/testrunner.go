package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/harness"
	"github.com/belt-sh/harness-test/server"
)

type Result struct {
	Harness    string
	Version    string
	Passed     int
	Failed     int
	Skipped    int
	Duration   time.Duration
	SkipReason harness.SkipReason // set when the whole harness was skipped
	SkipDetail string
}

// SkippedResult records a harness that was not run at all.
func SkippedResult(h harness.Harness, reason harness.SkipReason, detail string) Result {
	return Result{Harness: h.Name, SkipReason: reason, SkipDetail: detail}
}

type Mode int

const (
	ModeBoth Mode = iota
	ModeHeadless
	ModeInteractive
	ModeACP
	ModeSDK
)

type HookSource int

const (
	HooksMock HookSource = iota // test-generated hooks that log to file
	HooksBelt                   // real belt hooks via Install(), with logging shim
)

type TestRunner struct {
	harness          harness.Harness
	server           *server.MockServer
	baseURL          string
	home             string
	repoDir          string
	injectCode       string
	instructionCodes map[string]string // instruction file → codename written into it
	cleanups         []func()          // undo steps for files written into a preserved HOME
	tokenHash16      string
	sessionID        string
	startTime        time.Time
	savedEnv         []string
	mode             Mode
	hookSource       HookSource
	intercept        bool
	failed           bool
	result           Result
	lastOutput       string
	proxyURL         string            // HTTPS_PROXY value, set only during agent execution
	testEntries      []server.LogEntry // checks read these when set (unit tests)
}

func (r *TestRunner) entries() []server.LogEntry {
	if r.testEntries != nil || r.server == nil {
		return r.testEntries
	}
	return r.server.Log()
}

const hookLogPath = "/tmp/belt-hook-events.log"

// promptPayload is what the mock prompt hook prints, in the shape the
// agent's context channel expects (harness.HookStdout). Empty when the
// agent has no stdout channel for that event.
func (r *TestRunner) promptPayload() string {
	payload, ok := harness.HookStdout(r.harness.Name, "user-prompt-submit", "The project codename is "+r.injectCode+".")
	if !ok {
		return ""
	}
	return payload
}

// shellPrint returns a shell fragment that prints payload verbatim, safe
// to append after "&&". Single quotes in the payload are escaped.
func shellPrint(payload string) string {
	return "printf '%s\\n' '" + strings.ReplaceAll(payload, "'", `'\''`) + "'"
}

// promptEcho is the "&& print" suffix for the prompt hook command, or "".
func (r *TestRunner) promptEcho() string {
	p := r.promptPayload()
	if p == "" {
		return ""
	}
	return " && " + shellPrint(p)
}

// jsonStr escapes a shell command for embedding inside a JSON/TOML string.
func jsonStr(cmd string) string {
	b, _ := json.Marshal(cmd)
	return string(b[1 : len(b)-1])
}

const (
	TagSessionStart = "SESSION_START"
	TagPrompt       = "PROMPT"
	TagPreTool      = "PRE_TOOL"
	TagPostTool     = "POST_TOOL"
	TagStop         = "STOP"
	TagPreCompact   = "PRE_COMPACT"
)

var originalHome = os.Getenv("HOME")

func New(h harness.Harness, srv *server.MockServer, baseURL string) *TestRunner {
	return &TestRunner{
		harness:    h,
		server:     srv,
		baseURL:    baseURL,
		mode:       ModeBoth,
		hookSource: HooksMock,
		result:     Result{Harness: h.Name},
	}
}

func (r *TestRunner) SetHookSource(s HookSource) {
	r.hookSource = s
}

func (r *TestRunner) SetIntercept(on bool) {
	r.intercept = on
}

func (r *TestRunner) SetMode(m string) {
	switch m {
	case "headless":
		r.mode = ModeHeadless
	case "interactive":
		r.mode = ModeInteractive
	case "acp":
		r.mode = ModeACP
	case "sdk":
		r.mode = ModeSDK
	default:
		r.mode = ModeBoth
	}
}

func (r *TestRunner) pass(msg string) {
	r.result.Passed++
	fmt.Printf("  ✓ %s\n", msg)
}

func (r *TestRunner) fail(msg string) {
	r.result.Failed++
	r.failed = true
	fmt.Fprintf(os.Stderr, "  ✗ %s\n", msg)
}

func (r *TestRunner) skip(msg string) {
	r.result.Skipped++
	fmt.Printf("  ○ %s\n", msg)
}

func (r *TestRunner) Run() Result {
	r.startTime = time.Now()
	r.savedEnv = os.Environ()
	fmt.Printf("=== %s ===\n", r.harness.Name)

	if r.intercept || r.harness.NeedsIntercept {
		r.intercept = true
		r.setupIntercept()
	}
	r.setupHome()
	r.checkBinary()
	if r.failed {
		return r.finish()
	}
	r.setupEndpoint()
	r.writeConfigFiles()
	if r.hookSource == HooksBelt {
		r.writeBeltHooks()
	} else {
		r.writeHooks()
	}
	r.setupSkills()
	r.writeInstructions()

	if r.mode == ModeBoth || r.mode == ModeHeadless {
		if len(r.harness.HeadlessCmd) > 0 {
			r.prepareToolCall(true)
			r.requestHooksFor("headless")
			r.runHeadless()
			r.runChecks("headless")
		} else {
			r.skip(r.harness.Name + " has no headless mode")
		}
	}
	if r.mode == ModeBoth || r.mode == ModeInteractive {
		r.resetPhase()
		r.requestHooksFor("interactive")
		r.runInteractive()
		r.runChecks("interactive")
	}
	if r.mode == ModeACP {
		r.resetPhase()
		r.requestHooksFor("acp")
		r.runACP()
		r.runChecks("acp")
	}
	if r.mode == ModeSDK {
		r.resetPhase()
		r.requestHooksFor("sdk")
		r.runSDK()
		r.runChecks("sdk")
	}

	return r.finish()
}

func (r *TestRunner) resetPhase() {
	os.Remove(hookLogPath)
	os.Remove(hookLogPath + ".stdin")
	r.server.ClearLog()
	r.prepareToolCall(false)
}

func (r *TestRunner) prepareToolCall(headless bool) {
	hasToolHooks := r.harness.Events.PreToolUse != "" || r.harness.Events.PostToolUse != ""
	if r.server != nil && hasToolHooks {
		name, args := r.harness.ToolCallName, r.harness.ToolCallArgs
		if headless && r.harness.HeadlessToolCallName != "" {
			name, args = r.harness.HeadlessToolCallName, r.harness.HeadlessToolCallArgs
		}
		r.server.PrepareToolCall(name, r.expand(args), r.harness.ToolCallPath)
		// The mocked tool call reads README.md relative to the agent's cwd.
		readme := filepath.Join(r.workDir(), "README.md")
		if _, err := os.Stat(readme); err != nil {
			os.WriteFile(readme, []byte("test"), 0644)
		}
	}
}

func (r *TestRunner) finish() Result {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
	r.cleanups = nil
	r.result.Duration = time.Since(r.startTime)
	fmt.Printf("\n=== %s: %d passed, %d failed, %d skipped (%s) ===\n\n",
		r.harness.Name, r.result.Passed, r.result.Failed, r.result.Skipped, r.result.Duration.Round(time.Second))
	os.Clearenv()
	for _, e := range r.savedEnv {
		k, v, _ := strings.Cut(e, "=")
		os.Setenv(k, v)
	}
	return r.result
}

func (r *TestRunner) setupIntercept() {
	// DNS interception: map LLM domains to 127.0.0.1
	entries := server.HostsEntries()
	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("  ⚠ intercept: can't write /etc/hosts (%v)\n", err)
	} else {
		f.WriteString("\n# harness-test intercept\n" + entries + "\n")
		f.Close()
		fmt.Printf("  → intercept: %d LLM domains → 127.0.0.1\n", len(server.LLMHosts))
	}

	// HTTPS proxy: stored on runner, applied only when launching the agent
	// (not globally — npm/pip install would break if routed through the proxy)
	if proxyAddr := r.server.ProxyAddr(); proxyAddr != "" {
		r.proxyURL = proxyAddr
		fmt.Printf("  → intercept: proxy=%s (applied at agent launch)\n", proxyAddr)
	} else {
		proxyAddr, proxyErr := r.server.StartProxy()
		if proxyErr == nil {
			r.proxyURL = proxyAddr
			fmt.Printf("  → intercept: proxy=%s (applied at agent launch)\n", proxyAddr)
		}
	}

	// Install CA cert so runtimes that check system CAs trust our MITM
	caPEM := r.server.CAPem()
	if len(caPEM) == 0 {
		os.Setenv("NODE_TLS_REJECT_UNAUTHORIZED", "0")
		fmt.Println("  ⚠ intercept: no CA cert, falling back to TLS bypass")
		return
	}

	caFile := filepath.Join(os.TempDir(), "harness-test-ca.crt")
	os.WriteFile(caFile, caPEM, 0644)

	// Node / Bun
	os.Setenv("NODE_EXTRA_CA_CERTS", caFile)
	// Python
	os.Setenv("REQUESTS_CA_BUNDLE", caFile)
	// Rust (native-tls / rustls-native-certs) + OpenSSL
	combinedFile := filepath.Join(os.TempDir(), "harness-test-combined-ca.crt")
	systemBundle, _ := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
	combined := append(systemBundle, '\n')
	combined = append(combined, caPEM...)
	os.WriteFile(combinedFile, combined, 0644)
	os.Setenv("SSL_CERT_FILE", combinedFile)

	// Fallback: disable TLS verification for Node/Python
	os.Setenv("NODE_TLS_REJECT_UNAUTHORIZED", "0")
	os.Setenv("PYTHONHTTPSVERIFY", "0")

	fmt.Printf("  → intercept: CA installed (%s)\n", caFile)
}

func (r *TestRunner) setupHome() {
	if r.harness.PreserveHome {
		r.home = originalHome
		os.Setenv("HOME", originalHome)
		return
	}
	dir, err := os.MkdirTemp("", "harness-test-"+r.harness.Name+"-")
	if err != nil {
		r.fail("create temp home: " + err.Error())
		return
	}
	r.home = dir
	os.Setenv("HOME", dir)
}

func (r *TestRunner) checkBinary() {
	fmt.Println("[phase 1] prerequisites")
	if _, err := exec.LookPath(r.harness.Binary); err != nil {
		if len(r.harness.InstallCmd) == 0 {
			r.fail(r.harness.Binary + " not found (no install command)")
			return
		}
		fmt.Printf("  … installing %s\n", r.harness.Binary)
		cmd := exec.Command(r.harness.InstallCmd[0], r.harness.InstallCmd[1:]...)
		cmd.Env = os.Environ()
		out, installErr := cmd.CombinedOutput()
		if installErr != nil {
			r.fail(fmt.Sprintf("install %s: %v\n%s", r.harness.Binary, installErr, string(out)))
			return
		}
		for _, d := range r.harness.InstallBinDirs {
			p := filepath.Join(r.home, d)
			if !strings.Contains(os.Getenv("PATH"), p) {
				os.Setenv("PATH", p+":"+os.Getenv("PATH"))
			}
		}
		if _, err := exec.LookPath(r.harness.Binary); err != nil {
			r.fail(r.harness.Binary + " not found after install")
			return
		}
		r.pass(r.harness.Binary + " installed")
		r.detectVersion()
		for _, postCmd := range r.harness.PostInstall {
			cmd := exec.Command(postCmd[0], postCmd[1:]...)
			cmd.Env = os.Environ()
			cmd.Run()
		}
		return
	}
	r.pass(r.harness.Binary + " found")
	r.detectVersion()
}

func (r *TestRunner) detectVersion() {
	for _, flag := range []string{"--version", "-v", "version"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, r.harness.Binary, flag)
		cmd.Env = os.Environ()
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		ver := strings.TrimSpace(string(out))
		ver, _, _ = strings.Cut(ver, "\n")
		r.result.Version = ver
		fmt.Printf("  → version: %s\n", ver)
		return
	}
}

func (r *TestRunner) setupEndpoint() {
	fmt.Println("[phase 2] endpoint")
	keys := make([]string, 0, len(r.harness.EnvVars))
	for k := range r.harness.EnvVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, envVar := range keys {
		val := r.expand(r.harness.EnvVars[envVar])
		os.Setenv(envVar, val)
		r.pass(envVar + "=" + val)
	}
	if r.harness.APIKeyEnvVar != "" {
		os.Setenv(r.harness.APIKeyEnvVar, "mock-key")
		r.pass(r.harness.APIKeyEnvVar + " set")
	}
	r.server.ClearLog()
}

func (r *TestRunner) writeToHome(relPath string, content string) {
	path := filepath.Join(r.home, relPath)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(content), 0644)
	if r.harness.NeedsGitRepo {
		projPath := filepath.Join(r.ensureGitRepo(), relPath)
		os.MkdirAll(filepath.Dir(projPath), 0755)
		os.WriteFile(projPath, []byte(content), 0644)
	}
}

func (r *TestRunner) writeConfigFiles() {
	if len(r.harness.ConfigFiles) == 0 {
		return
	}
	for _, cf := range r.harness.ConfigFiles {
		r.writeToHome(r.expand(cf.Path), r.expand(cf.Content))
	}
}

func (r *TestRunner) writeHooks() {
	fmt.Println("[phase 3] hooks")

	hookDir := filepath.Join(r.home, r.harness.HookConfigDir)
	os.MkdirAll(hookDir, 0755)

	logPath := hookLogPath
	os.Remove(logPath)
	// Distinct prefix: instruction-file codes are INSTR-<scope>-<NAME>-<ts> and
	// share the millisecond, so a bare <NAME>-<ts> matched them and the
	// injection check passed whenever the instruction file loaded.
	r.injectCode = fmt.Sprintf("HOOK-%s-%d", strings.ToUpper(r.harness.Name), time.Now().UnixMilli())

	var content string
	var filename string

	switch r.harness.HookFormat {
	case harness.JSONNested:
		filename = "belt.json"
		hooksJSON := r.buildNestedHooksJSON(logPath)
		if r.harness.HookWrapper != "" {
			content = r.expand(fmt.Sprintf(r.harness.HookWrapper, hooksJSON))
			filename = r.harness.HookFileName
		} else {
			content = fmt.Sprintf(`{"hooks":%s}`, hooksJSON)
		}

	case harness.JSONFlat:
		filename = "belt.json"
		parts := []string{}
		for _, e := range r.eventEntries() {
			cmd := fmt.Sprintf("echo %s >> %s", e.Tag, logPath)
			if e.Tag == TagPrompt {
				cmd += r.promptEcho()
			}
			timeout := 5
			if r.harness.HookTimeoutMs {
				timeout = 5000
			}
			parts = append(parts, fmt.Sprintf(`"%s":[{"type":"command","command":"%s","timeout":%d}]`, e.Event, jsonStr(cmd), timeout))
		}
		hooks := "{" + strings.Join(parts, ",") + "}"
		if r.harness.HookWrapper != "" {
			content = fmt.Sprintf(r.harness.HookWrapper, hooks)
		} else {
			content = `{"hooks":` + hooks + `}`
		}

	case harness.JSONKiro:
		// kiro-cli agent config, selected below with chat.defaultAgent as belt's
		// install does.
		filename = r.harness.HookFileName
		var hooks []string
		for _, e := range r.eventEntries() {
			cmd := fmt.Sprintf("echo %s >> %s", e.Tag, logPath)
			if e.Tag == TagPrompt {
				cmd += r.promptEcho()
			}
			hooks = append(hooks, fmt.Sprintf(`"%s":[{"command":"%s","timeout_ms":5000}]`, e.Event, jsonStr(cmd)))
		}
		content = fmt.Sprintf(`{"name":"%s","description":"harness test agent","tools":["*"],"hooks":{%s}}`, strings.TrimSuffix(filename, ".json"), strings.Join(hooks, ","))
		if other, err := harness.KiroSelectBeltAgent(r.home); err != nil || other != "" {
			r.fail(fmt.Sprintf("kiro default agent not selected (other=%q, err=%v)", other, err))
		}

	case harness.JSONCopilot:
		filename = "belt.json"
		scriptDir := filepath.Join(r.home, ".copilot", "test-hooks")
		os.MkdirAll(scriptDir, 0755)
		promptScript := filepath.Join(scriptDir, "prompt.sh")
		os.WriteFile(promptScript, []byte(fmt.Sprintf("#!/bin/sh\necho PROMPT >> %s\n%s\n", logPath, shellPrint(r.promptPayload()))), 0755)
		stopScript := filepath.Join(scriptDir, "stop.sh")
		os.WriteFile(stopScript, []byte(fmt.Sprintf("#!/bin/sh\necho STOP >> %s\n", logPath)), 0755)
		content = fmt.Sprintf(`{"version":1,"hooks":{"%s":[{"type":"command","bash":"%s","timeoutSec":5}],"%s":[{"type":"command","bash":"%s","timeoutSec":5}]}}`,
			r.harness.Events.PromptSubmit, promptScript, r.harness.Events.Stop, stopScript)

	case harness.YAML:
		scriptDir := filepath.Join(r.home, ".hermes", "test-hooks")
		os.MkdirAll(scriptDir, 0755)

		yamlHooks := "hooks:\n"
		for _, e := range r.eventEntries() {
			script := filepath.Join(scriptDir, e.Tag+".sh")
			body := fmt.Sprintf("#!/bin/sh\ncat - >/dev/null\necho %s >> %s\n", e.Tag, logPath)
			if e.Tag == TagPrompt {
				if p := r.promptPayload(); p != "" {
					body += shellPrint(p) + "\n"
				}
			}
			os.WriteFile(script, []byte(body), 0755)
			yamlHooks += fmt.Sprintf("  %s:\n    - command: %s\n      timeout: 5\n", e.Event, script)
		}
		cfgPath := filepath.Join(r.home, r.harness.HookConfigDir, "config.yaml")
		existing, _ := os.ReadFile(cfgPath)
		existingStr := strings.Replace(string(existing), "hooks: {}", "", 1)
		content = existingStr + yamlHooks
		filename = "config.yaml"

	case harness.TSExtension:
		filename = "belt-test.ts"
		tsHooks := ""
		for _, e := range r.eventEntries() {
			if e.Tag == TagPrompt {
				tsHooks += fmt.Sprintf(`  pi.on("%s", async (event: any) => {
    require("fs").appendFileSync("%s", "PROMPT\n");
    return { systemPrompt: (event.systemPrompt || '') + '\nThe project codename is %s.' };
  });
`, e.Event, logPath, r.injectCode)
			} else {
				tsHooks += fmt.Sprintf(`  pi.on("%s", async () => {
    require("fs").appendFileSync("%s", "%s\n");
  });
`, e.Event, logPath, e.Tag)
			}
		}
		content = fmt.Sprintf("export default function (pi: any) {\n%s}\n", tsHooks)

	case harness.TOML:
		filename = "config.toml"
		cfgPath := filepath.Join(r.home, r.harness.HookConfigDir, "config.toml")
		existing, _ := os.ReadFile(cfgPath)
		tomlHooks := ""
		for _, e := range r.eventEntries() {
			cmd := fmt.Sprintf("echo %s >> %s", e.Tag, logPath)
			if e.Tag == TagPrompt {
				cmd += r.promptEcho()
			}
			if e.Tag == TagPreTool || e.Tag == TagPostTool {
				tomlHooks += fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"\nmatcher = \"%s\"\ncommand = \"%s\"\ntimeout = 10\n", e.Event, r.toolMatcher(), jsonStr(cmd))
			} else {
				tomlHooks += fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"\ncommand = \"%s\"\ntimeout = 10\n", e.Event, jsonStr(cmd))
			}
		}
		content = string(existing) + tomlHooks

	case harness.TSPlugin:
		filename = "belt-test.ts"
		var hookParts []string
		startLine := ""
		for _, e := range r.eventEntries() {
			switch e.Tag {
			case TagSessionStart:
				startLine = fmt.Sprintf("  require(\"fs\").appendFileSync(\"%s\", \"%s\\n\");\n", logPath, TagSessionStart)
			case TagPrompt:
				hookParts = append(hookParts, fmt.Sprintf(`    "%s": async (_input: any, output: any) => {
      require("fs").appendFileSync("%s", "PROMPT\n");
      output.system.push("The project codename is %s.");
    }`, e.Event, logPath, r.injectCode))
			case TagStop:
				hookParts = append(hookParts, fmt.Sprintf(`    "event": async ({ event }: any) => {
      if (event.type === "%s") {
        require("fs").appendFileSync("%s", "STOP\n");
      }
    }`, e.Event, logPath))
			default:
				hookParts = append(hookParts, fmt.Sprintf(`    "%s": async () => {
      require("fs").appendFileSync("%s", "%s\n");
    }`, e.Event, logPath, e.Tag))
			}
		}
		content = fmt.Sprintf("export const TestPlugin = async (_ctx: any) => {\n%s  return {\n%s,\n  };\n};\n",
			startLine, strings.Join(hookParts, ",\n"))

	default:
		r.skip("hook format not yet implemented")
		return
	}

	if r.harness.HookFileName != "" && filename == "belt.json" {
		filename = r.harness.HookFileName
	}
	hookFile := filepath.Join(hookDir, filename)
	os.WriteFile(hookFile, []byte(content), 0644)
	if r.harness.NeedsGitRepo {
		projHookDir := filepath.Join(r.ensureGitRepo(), r.harness.HookConfigDir)
		os.MkdirAll(projHookDir, 0755)
		os.WriteFile(filepath.Join(projHookDir, filename), []byte(content), 0644)
	}
	r.pass(fmt.Sprintf("hooks configured (code: %s)", r.injectCode))
}

func (r *TestRunner) writeBeltHooks() {
	fmt.Println("[phase 3] hooks (belt)")

	os.Setenv("BELT_HOOK_DEBUG", "1")
	os.Setenv("BELT_HOOK_DEBUG_LOG", hookLogPath)
	os.Setenv("BELT_NO_HOOKS", "0")
	os.Remove(hookLogPath)
	os.Remove(hookLogPath + ".stdin")
	os.MkdirAll(filepath.Join(r.home, ".belt"), 0755)
	// Logged-out belt opens device auth from the session-start hook and polls
	// for 45s, which starves slow TUIs. A fresh cooldown marker makes the hook
	// print the login hint and return at once.
	os.MkdirAll(filepath.Join(r.home, ".inferencesh"), 0755)
	os.WriteFile(filepath.Join(r.home, ".inferencesh", "hook-auth-cooldown"), []byte(time.Now().Format(time.RFC3339)), 0644)

	// Harnesses with HookWrapper need the non-hook config (permissions, base URL, auth)
	// pre-seeded so Install()'s merge adds hooks alongside them.
	if r.harness.HookWrapper != "" && r.harness.HookFileName != "" {
		r.seedWrapperConfig()
	}

	result := harness.Install(r.harness.Name, harness.ScopeUser)
	if result.Error != nil {
		r.fail("belt hook install: " + result.Error.Error())
		return
	}

	if result.Merged {
		r.pass(fmt.Sprintf("belt hooks merged into %s", result.HooksPath))
	} else {
		r.pass(fmt.Sprintf("belt hooks created at %s", result.HooksPath))
	}

	if r.harness.NeedsGitRepo {
		repoDir := r.ensureGitRepo()
		projHookDir := filepath.Join(repoDir, r.harness.HookConfigDir)
		os.MkdirAll(projHookDir, 0755)
		fname := r.harness.HookFileName
		if fname == "" {
			fname = "belt.json"
		}
		src := filepath.Join(r.home, r.harness.HookConfigDir, fname)
		if data, err := os.ReadFile(src); err == nil {
			dst := filepath.Join(projHookDir, fname)
			os.WriteFile(dst, data, 0644)
			r.pass(fmt.Sprintf("belt hooks copied to project (%s)", dst))
		}
	}
}

// seedWrapperConfig writes the non-hook fields from HookWrapper so that
// Install()'s merge adds hooks into a file that already has the correct
// endpoint/permissions config.
func (r *TestRunner) seedWrapperConfig() {
	wrapper := r.expand(r.harness.HookWrapper)
	// The wrapper is a format string like `{"permissions":...,"hooks":%s}`.
	// Replace the %s with an empty hooks object to get the base config.
	base := fmt.Sprintf(wrapper, "{}")
	path := filepath.Join(r.home, r.harness.HookConfigDir, r.harness.HookFileName)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(base), 0644)
}

func (r *TestRunner) setupSkills() {
	if r.harness.SkillsDir == "" {
		return
	}
	fmt.Println("[phase 4] skills")
	os.MkdirAll(filepath.Join(r.home, r.harness.SkillsDir), 0755)
	r.pass("skills directory created")
}

const (
	instructionStart = "<!-- harness-test:start -->"
	instructionEnd   = "<!-- harness-test:end -->"
)

// writeInstructions puts a second codename into the agent's instruction file
// (CLAUDE.md, AGENTS.md, ...) so the checks can tell that the file, not just
// the hooks, reached the model. The block is marker-wrapped and removed in
// finish() so a preserved HOME is left as it was.
func (r *TestRunner) writeInstructions() {
	h := r.harness
	if h.InstructionFile == "" && h.ProjectInstructionFile == "" {
		return
	}
	fmt.Println("[phase 4] instruction files")
	stamp := time.Now().UnixMilli()
	block := func(code string) string {
		return instructionStart + "\nThe project codename is " + code + ".\n" + instructionEnd + "\n"
	}

	r.instructionCodes = map[string]string{}
	userPath := ""
	if h.InstructionFile != "" {
		userPath = filepath.Join(r.home, h.InstructionFile)
		code := fmt.Sprintf("INSTR-USER-%s-%d", strings.ToUpper(h.Name), stamp)
		r.appendInstructionBlock(userPath, block(code))
		r.instructionCodes["~/"+h.InstructionFile] = code
	}
	// Project file goes where the agent runs: the test repo when the agent
	// needs one, otherwise HOME itself. Each file carries its own code so the
	// check can tell which one the agent loaded.
	if h.ProjectInstructionFile != "" {
		path := filepath.Join(r.workDir(), h.ProjectInstructionFile)
		if path != userPath {
			code := fmt.Sprintf("INSTR-PROJ-%s-%d", strings.ToUpper(h.Name), stamp)
			r.appendInstructionBlock(path, block(code))
			r.instructionCodes["./"+h.ProjectInstructionFile] = code
		}
	}
	for name := range r.instructionCodes {
		r.pass("instruction file written: " + name)
	}
}

func (r *TestRunner) appendInstructionBlock(path, block string) {
	existing, err := os.ReadFile(path)
	existed := err == nil
	os.MkdirAll(filepath.Dir(path), 0755)
	content := string(existing)
	if !existed && r.harness.InstructionFrontmatter != "" {
		content = r.harness.InstructionFrontmatter
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	os.WriteFile(path, []byte(content+block), 0644)
	r.cleanups = append(r.cleanups, func() {
		if !existed {
			os.Remove(path)
			return
		}
		os.WriteFile(path, existing, 0644)
	})
}

func (r *TestRunner) ensureGitRepo() string {
	if r.repoDir != "" {
		return r.repoDir
	}
	r.repoDir = filepath.Join(r.home, "test-repo")
	os.MkdirAll(r.repoDir, 0755)
	run(r.repoDir, "git", "init", "-q")
	run(r.repoDir, "git", "config", "user.email", "t@t")
	run(r.repoDir, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(r.repoDir, "README.md"), []byte("test"), 0644)
	run(r.repoDir, "git", "add", ".")
	run(r.repoDir, "git", "commit", "-qm", "init")
	return r.repoDir
}

func (r *TestRunner) workDir() string {
	if r.harness.NeedsGitRepo {
		return r.ensureGitRepo()
	}
	return r.home
}

func (r *TestRunner) runOneShot(label string, cmdSlice, extraArgs []string) []byte {
	dir := r.workDir()
	prompt := "What is the project codename? Reply ONLY the codename."

	var args []string
	for _, a := range cmdSlice[1:] {
		args = append(args, r.expand(a))
	}
	if !r.harness.PromptViaStdin {
		args = append(args, prompt)
	}
	for _, a := range extraArgs {
		args = append(args, r.expand(a))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdSlice[0], args...)
	cmd.Env = r.envIn(dir)
	cmd.Dir = dir
	if r.harness.PromptViaStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

	out, err := cmd.CombinedOutput()
	r.lastOutput = string(out)
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] %s output (%d bytes):\n%s\n", label, len(out), r.lastOutput)
	}
	if err != nil && len(out) > 0 {
		r.pass(fmt.Sprintf("%s produced output (%d bytes, exit: %v)", label, len(out), err))
	} else if err != nil {
		r.fail(label + ": " + err.Error())
	} else if len(out) > 0 {
		r.pass(fmt.Sprintf("%s produced output (%d bytes)", label, len(out)))
	} else {
		r.fail(label + " produced no output")
	}

	if r.harness.Events.Stop != "" {
		time.Sleep(3 * time.Second)
	}

	return out
}

func (r *TestRunner) runHeadless() {
	if len(r.harness.HeadlessCmd) == 0 {
		r.skip("no headless command configured")
		return
	}

	fmt.Println("[phase 5] headless prompt")
	out := r.runOneShot("headless", r.harness.HeadlessCmd, r.harness.HeadlessModelArgs)

	if len(r.harness.PostHeadlessCmd) > 0 {
		var parsed struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(out, &parsed) == nil && parsed.SessionID != "" {
			r.sessionID = parsed.SessionID
		}
		if r.sessionID == "" {
			r.sessionID = r.findLatestSessionID(r.workDir())
		}
	}

	for _, step := range r.harness.PostHeadlessCmd {
		r.runPostHeadless(r.workDir(), step)
	}
}

func (r *TestRunner) runPostHeadless(dir string, rawArgs []string) {
	var args []string
	for _, a := range rawArgs {
		expanded := r.expand(a)
		if expanded == "" {
			// A registry template that expands to nothing is a configuration
			// error in this repo, not something the agent did.
			r.fail("post-headless step not run: template variable expanded to nothing in " + strings.Join(rawArgs, " "))
			return
		}
		args = append(args, expanded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.harness.HeadlessCmd[0], args...)
	cmd.Env = r.envIn(dir)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] post-headless output: %s\n", string(out))
	}
	time.Sleep(2 * time.Second)
}

func (r *TestRunner) sendLine(session *PTYSession, text string) {
	delay := time.Duration(0)
	if r.harness.SlowInput {
		delay = 5 * time.Millisecond
	}
	session.SendText(text, delay)
	// pi-tui (kimi, pi, omp) reads a fast run of keystrokes as a paste and
	// turns an Enter within 120ms of it into a newline. Sent 50ms after the
	// text, kimi's prompt sat in the composer until the next line ("/exit")
	// submitted both. A person pauses before Enter; so does the runner.
	time.Sleep(250 * time.Millisecond)
	session.SendRaw("\r")
}

func (r *TestRunner) runInteractive() {
	if len(r.harness.InteractiveCmd) == 0 {
		r.skip(r.harness.Name + " has no interactive mode")
		return
	}

	fmt.Println("[phase 6] interactive (PTY) mode")

	dir := r.workDir()

	var iargs []string
	for _, a := range r.harness.InteractiveCmd[1:] {
		iargs = append(iargs, r.expand(a)) // kiro received a literal "{{.Model}}" before this
	}
	for _, a := range r.harness.InteractiveArgs {
		iargs = append(iargs, r.expand(a))
	}

	// An agent given its prompt as a launch argument can be answered while
	// the runner is still dismissing onboarding screens, so count answers from
	// before launch; otherwise the wait below looks for a second answer that
	// never comes and sits out its whole timeout (kiro: 55s became 2m23s).
	answeredAtLaunch := r.server.AnswersServed()
	session, err := StartPTY(r.harness.InteractiveCmd[0], iargs, dir, r.envIn(dir))
	if err != nil {
		r.fail("PTY start: " + err.Error())
		return
	}
	defer session.Close()

	time.Sleep(3 * time.Second)
	if len(r.harness.OnboardingDismiss) > 0 {
		// Match only output drawn since the last dismissal: the whole buffer
		// keeps a dismissed dialog's text forever, which pressed Enter on every
		// pass (up to 15 times, 30 seconds) once any pattern had appeared.
		seen := 0
		pending := map[string]bool{}
		for _, a := range r.harness.OnboardingDismiss {
			if a.Required {
				pending[a.Pattern] = true
			}
		}
		for i := 0; i < 15 || (len(pending) > 0 && i < 30); i++ {
			out := session.Output()
			fresh := ""
			if seen <= len(out) {
				fresh = out[seen:]
			}
			// Handle the earliest dialog in the fresh output, and move the
			// marker only past its text: agents draw several screens in one
			// burst (claude: the API-key question and "Press Enter to
			// continue"), and skipping to the end of the buffer lost the second.
			dismissed := false
			pick, at := -1, len(fresh)
			for k, action := range r.harness.OnboardingDismiss {
				if idx := strings.Index(fresh, action.Pattern); idx >= 0 && idx < at {
					pick, at = k, idx
				}
			}
			if pick >= 0 {
				action := r.harness.OnboardingDismiss[pick]
				seen += at + len(action.Pattern)
				delete(pending, action.Pattern)
				if action.SendUp {
					session.SendUp()
					time.Sleep(200 * time.Millisecond)
				}
				session.SendLine("")
				time.Sleep(2 * time.Second)
				dismissed = true
			}
			if dismissed {
				continue
			}
			if len(out) > 200 && len(pending) == 0 {
				break
			}
			time.Sleep(1 * time.Second)
		}
	} else {
		_, _ = session.WaitForAny([]string{">", "❯", "$", "?", "Type your message"}, 15*time.Second)
	}
	r.pass("TUI started")

	answered := answeredAtLaunch
	if !r.harness.InteractivePromptInArgs {
		waitScreenQuiet(session, 1500*time.Millisecond, 15*time.Second)
		answered = r.server.AnswersServed()
		r.sendLine(session, "What is the project codename? Reply ONLY the codename.")
	}
	r.step("waiting for answer > %d (served %d)", answered, r.server.AnswersServed())
	r.waitTurnSettled(answered, 90*time.Second)
	r.step("turn settled (served %d, requests %d)", r.server.AnswersServed(), r.server.LogCount())
	if os.Getenv("HARNESS_DEBUG") != "" && r.server.LogCount() == 0 {
		scr := stripANSI(session.Output())
		if len(scr) > 1200 {
			scr = scr[len(scr)-1200:]
		}
		fmt.Printf("    [step] no request yet; screen tail:\n%s\n", scr)
	}
	if r.harness.CompactCommand != "" {
		answered = r.server.AnswersServed()
		r.step("typing second prompt")
		r.sendLine(session, "Tell me more about the project.")
		r.waitTurnSettled(answered, 60*time.Second)
		r.step("second turn settled (served %d, requests %d)", r.server.AnswersServed(), r.server.LogCount())
		r.sendLine(session, r.harness.CompactCommand)
		session.WaitForAny([]string{"compact", "Compact", "compress", "Compress", "summar"}, 15*time.Second)
		if r.harness.CompactConfirm {
			if _, ok := session.WaitForAny([]string{"Enter to confirm", "confirm"}, 10*time.Second); ok {
				session.SendLine("")
			}
		}
		r.waitTurnSettled(-1, 30*time.Second)
		r.step("compaction settled (requests %d)", r.server.LogCount())
	}
	if !r.harness.InteractivePromptInArgs && r.harness.ExitCommand != "" {
		r.sendLine(session, r.harness.ExitCommand)
		time.Sleep(3 * time.Second)
	}
	session.SendCtrlC()
	session.Wait(5 * time.Second)

	if r.harness.Events.Stop != "" {
		time.Sleep(2 * time.Second)
	}

	r.lastOutput = session.Output()
	if dump := os.Getenv("HARNESS_PTY_DUMP"); dump != "" {
		os.WriteFile(dump, []byte(stripANSI(r.lastOutput)), 0644)
	}
	if os.Getenv("HARNESS_DEBUG") != "" {
		stripped := stripANSI(r.lastOutput)
		head := stripped
		if len(head) > 1500 {
			head = head[:1500]
		}
		tail := stripped
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		fmt.Printf("    [debug] PTY output (%d bytes) head:\n%s\n    [debug] tail:\n%s\n", len(r.lastOutput), head, tail)
	}

	r.pass("interactive session completed")
}

func (r *TestRunner) writeACPConfig() {
	if r.harness.ACPNeedsTempHome {
		tmpHome, _ := os.MkdirTemp("", "acp-"+r.harness.Name+"-")
		// The user-scope artifacts written in earlier phases (hooks or belt
		// plugin, skills, instruction file) must follow HOME, or the ACP run
		// tests an empty home.
		for _, rel := range []string{r.harness.HookConfigDir, r.harness.SkillsDir, r.harness.InstructionFile} {
			if rel != "" {
				copyTree(filepath.Join(r.home, rel), filepath.Join(tmpHome, rel))
			}
		}
		os.Setenv("HOME", tmpHome)
		r.home = tmpHome
	}
	for _, cf := range r.harness.ACPConfigFiles {
		path := filepath.Join(r.home, r.expand(cf.Path))
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(r.expand(cf.Content)), 0644)
	}
}

func (r *TestRunner) runACP() {
	if len(r.harness.ACPCmd) == 0 {
		r.skip(r.harness.Name + " does not support ACP mode")
		return
	}

	fmt.Println("[phase 7] ACP (JSON-RPC over stdio)")

	dir := r.workDir()

	var args []string
	for _, a := range r.harness.ACPCmd[1:] {
		args = append(args, r.expand(a))
	}
	for _, a := range r.harness.ACPArgs {
		args = append(args, r.expand(a))
	}

	// Write ACP-specific config files (some agents need config in the project dir)
	r.writeACPConfig()

	driver := NewACPDriver(r.harness.ACPCmd[0], args, dir, r.envIn(dir))
	if err := driver.Start(); err != nil {
		r.fail("ACP start: " + err.Error())
		return
	}
	defer driver.Close()
	r.pass("ACP session started")

	// Agents finish loading hooks after session/new returns; a prompt sent
	// at once can run the hook without its output being attached (grok).
	time.Sleep(2 * time.Second)

	prompt := "What is the project codename? Reply ONLY the codename."
	if err := driver.SendPrompt(prompt); err != nil {
		r.fail("ACP prompt: " + err.Error())
		return
	}

	_, err := driver.WaitForResponse(
		[]string{"mock", "hello", "Hello", "codename", "server"},
		60*time.Second,
	)
	if err != nil {
		r.skip("ACP response: " + err.Error())
	} else {
		r.pass("ACP prompt answered")
	}

	// Let tool execution and post-tool hooks settle
	driver.WaitIdle(2 * time.Second)

	if r.harness.CompactCommand != "" {
		driver.SendCommand(r.harness.CompactCommand)
		driver.WaitIdle(3 * time.Second)
	}

	r.lastOutput = driver.Output()
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] ACP output (%d bytes):\n%s\n", len(r.lastOutput), r.lastOutput)
	}

	r.pass("ACP session completed")
}

func (r *TestRunner) runSDK() {
	if len(r.harness.SDKCmd) == 0 {
		r.skip(r.harness.Name + " does not support SDK mode")
		return
	}

	fmt.Println("[phase 8] SDK (stream-json over stdio)")
	r.runOneShot("SDK", r.harness.SDKCmd, r.harness.SDKArgs)
	// Drive compaction the same way headless does (a --continue turn that
	// sends the compact command); without this the runner never asked, and
	// "the SDK session never compacts" was a statement about the runner.
	for _, step := range r.harness.PostHeadlessCmd {
		r.runPostHeadless(r.workDir(), step)
	}
}

func (r *TestRunner) checkHookEvents(phase string) {
	fmt.Printf("[phase] hook events (%s)\n", phase)

	r.dumpHookLogs(phase)
	if r.hookSource == HooksBelt {
		r.checkBeltHookEvents(phase)
		return
	}

	logContent := ""
	if data, err := os.ReadFile(hookLogPath); err == nil {
		logContent = string(data)
	}

	ptyContent := stripANSI(r.lastOutput)

	for _, e := range r.eventEntries() {
		label := strings.ToLower(strings.ReplaceAll(e.Tag, "_", "-"))
		found := strings.Contains(logContent, e.Tag) ||
			strings.Contains(ptyContent, "hook: "+e.Event)
		r.reportEvent(phase, label, e.Tag, found, "")
	}

	if r.server.LogCount() > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, r.server.LogCount()))
	}
}

// beltEventNames maps our internal tag names to belt plugin hook event names.
var beltEventNames = map[string]string{
	TagSessionStart: "session-start",
	TagPrompt:       "user-prompt-submit",
	TagPreTool:      "pre-tool-use",
	TagPostTool:     "post-tool-use",
	TagStop:         "stop",
	TagPreCompact:   "pre-compact",
}

func (r *TestRunner) dumpHookLogs(phase string) {
	if os.Getenv("HARNESS_DEBUG") == "" {
		return
	}
	for _, p := range []string{hookLogPath, hookLogPath + ".stdin"} {
		if data, err := os.ReadFile(p); err == nil {
			fmt.Printf("    [debug] %s %s (%d bytes):\n%s\n", phase, p, len(data), truncate(string(data), 3000))
		}
	}
}

func (r *TestRunner) checkBeltHookEvents(phase string) {
	beltLog := ""
	if data, err := os.ReadFile(filepath.Join(r.home, ".belt", "hooks.log")); err == nil {
		beltLog = string(data)
	}
	if data, err := os.ReadFile(hookLogPath); err == nil {
		beltLog += string(data)
	}

	ptyContent := stripANSI(r.lastOutput)

	for _, e := range r.eventEntries() {
		label := strings.ToLower(strings.ReplaceAll(e.Tag, "_", "-"))
		beltName := beltEventNames[e.Tag]

		found := false
		if beltName != "" {
			found = strings.Contains(beltLog, "["+beltName+"]")
		}
		if !found {
			found = strings.Contains(ptyContent, "[belt:hook] "+beltName+" done")
		}

		r.reportEvent(phase, label, e.Tag, found, "belt ")
	}

	if r.server.LogCount() > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, r.server.LogCount()))
	}
}

type eventEntry struct {
	Event string
	Tag   string
}

func (r *TestRunner) eventEntries() []eventEntry {
	evts := r.harness.Events
	all := []eventEntry{
		{evts.SessionStart, TagSessionStart},
		{evts.PromptSubmit, TagPrompt},
		{evts.PreToolUse, TagPreTool},
		{evts.PostToolUse, TagPostTool},
		{evts.Stop, TagStop},
		{evts.PreCompact, TagPreCompact},
	}
	var result []eventEntry
	for _, e := range all {
		if e.Event != "" {
			result = append(result, e)
		}
	}
	return result
}

func (r *TestRunner) toolMatcher() string {
	if r.harness.HookToolMatcher != "" {
		return r.harness.HookToolMatcher
	}
	if r.harness.ToolCallName != "" {
		return r.harness.ToolCallName
	}
	return server.DefaultToolName
}

func (r *TestRunner) buildNestedHooksJSON(logPath string) string {
	entries := r.eventEntries()
	parts := []string{}
	for _, e := range entries {
		// Drain the JSON payload the agent pipes to the hook, as a real hook
		// (belt reads stdin) would: a hook that exits with stdin unread gives
		// the agent a broken pipe, and gemini's TUI then discards its output.
		cmd := fmt.Sprintf("[ -t 0 ] || cat >> %s.stdin; echo %s >> %s", logPath, e.Tag, logPath)
		if e.Tag == TagPrompt {
			cmd += r.promptEcho()
		}
		// Same unit belt's generated config uses: seconds, or milliseconds
		// where the registry says so (gemini, qwen read "timeout" as ms and
		// killed a 5-"second" hook after 5ms).
		timeout := 5
		if r.harness.HookTimeoutMs {
			timeout = 5000
		}
		hook := fmt.Sprintf(`{"type":"command","command":"%s","timeout":%d}`, jsonStr(cmd), timeout)
		if e.Tag == TagPreTool || e.Tag == TagPostTool {
			parts = append(parts, fmt.Sprintf(`"%s":[{"matcher":"%s","hooks":[%s]}]`, e.Event, r.toolMatcher(), hook))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s":[{"hooks":[%s]}]`, e.Event, hook))
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (r *TestRunner) expand(tmpl string) string {
	s := strings.ReplaceAll(tmpl, "{{.BaseURL}}", r.baseURL)
	s = strings.ReplaceAll(s, "{{.Model}}", r.harness.DefaultModel)
	s = strings.ReplaceAll(s, "{{.APIKey}}", "mock-key")
	s = strings.ReplaceAll(s, "{{.HomeDir}}", r.home)
	if r.repoDir != "" {
		s = strings.ReplaceAll(s, "{{.RepoDir}}", r.repoDir)
	} else {
		s = strings.ReplaceAll(s, "{{.RepoDir}}", filepath.Join(r.home, "test-repo"))
	}
	if strings.Contains(s, "{{.TokenHash16}}") && r.harness.TokenHashInput != "" {
		if r.tokenHash16 == "" {
			input := r.expand(r.harness.TokenHashInput)
			hash := sha256.Sum256([]byte(input))
			r.tokenHash16 = hex.EncodeToString(hash[:])[:16]
		}
		s = strings.ReplaceAll(s, "{{.TokenHash16}}", r.tokenHash16)
	}
	s = strings.ReplaceAll(s, "{{.SessionID}}", r.sessionID)
	return s
}

func (r *TestRunner) findLatestSessionID(cwd string) string {
	mangled := strings.ReplaceAll(cwd, "/", "-")
	sessDir := filepath.Join(r.home, ".factory", "sessions", mangled)
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	return newest
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// agentEnv returns os.Environ() with proxy vars injected for intercepted agents.
// envIn returns the agent env with PWD pointing at dir. exec.Cmd.Dir does
// not touch PWD, and Bun-based agents (opencode, kilo, omp) take their
// working directory from it, so without this they resolve the project root
// to wherever harness-test itself was started.
func (r *TestRunner) envIn(dir string) []string {
	env := r.agentEnv()
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PWD=") {
			out = append(out, kv)
		}
	}
	return append(out, "PWD="+dir)
}

func (r *TestRunner) agentEnv() []string {
	env := os.Environ()
	if r.proxyURL == "" {
		return env
	}
	return append(env,
		"HTTPS_PROXY="+r.proxyURL,
		"HTTP_PROXY="+r.proxyURL,
		"https_proxy="+r.proxyURL,
		"http_proxy="+r.proxyURL,
	)
}

func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run()
}

// copyTree copies a file or directory tree; missing sources are ignored.
func copyTree(src, dst string) {
	info, err := os.Stat(src)
	if err != nil {
		return
	}
	if !info.IsDir() {
		os.MkdirAll(filepath.Dir(dst), 0755)
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(dst, data, info.Mode())
		}
		return
	}
	entries, _ := os.ReadDir(src)
	os.MkdirAll(dst, 0755)
	for _, e := range entries {
		copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
	}
}

// reportEvent records one hook event. An event that fires passes. One that
// does not fire is a skip only when the registry says this agent cannot fire
// it in this mode; otherwise it fails, so a hook that silently stops firing
// cannot pass as a skip.
func (r *TestRunner) reportEvent(phase, label, tag string, fired bool, prefix string) {
	if fired {
		r.pass(fmt.Sprintf("%s: %s%s hook fired", phase, prefix, label))
		return
	}
	if (tag == TagPreTool || tag == TagPostTool) && r.server != nil && !r.server.ToolCallServed() {
		r.skip(fmt.Sprintf("%s: %s%s hook not checked — the agent was never offered the tool call", phase, prefix, label))
		return
	}
	if reason, ok := r.harness.EventKnownMissing(phase, tag); ok {
		r.skip(fmt.Sprintf("%s: %s%s hook not fired — %s", phase, prefix, label, reason))
		return
	}
	r.fail(fmt.Sprintf("%s: %s%s hook did not fire", phase, prefix, label))
}

// requestHooksFor tells the mock which hooks this agent needs its backend to
// request in this mode (Harness.ServerRequestedHooks), and nothing else.
func (r *TestRunner) requestHooksFor(mode string) {
	if r.server != nil {
		r.server.SetRequestedHooks(r.harness.ServerRequestedHooks[mode])
	}
}

// waitTurnSettled waits for a turn to finish: the mock has served an answer
// beyond `after` (pass -1 to skip that), and then neither the mock's request
// log nor the hook event log has changed for a few seconds, so trailing hooks
// (stop, compaction; belt's take seconds) are done before the session is cut.
func (r *TestRunner) waitTurnSettled(after int, timeout time.Duration) {
	const quiet = 4 * time.Second
	deadline := time.Now().Add(timeout)
	for after >= 0 && r.server.AnswersServed() <= after && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	fingerprint := func() string {
		size := int64(0)
		if fi, err := os.Stat(hookLogPath); err == nil {
			size = fi.Size()
		}
		return fmt.Sprintf("%d/%d", r.server.LogCount(), size)
	}
	last, since := fingerprint(), time.Now()
	for time.Now().Before(deadline) && time.Since(since) < quiet {
		time.Sleep(250 * time.Millisecond)
		if fp := fingerprint(); fp != last {
			last, since = fp, time.Now()
		}
	}
}

// step prints a timestamped runner step when HARNESS_DEBUG is set.
func (r *TestRunner) step(format string, a ...any) {
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [step %s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
	}
}

// waitScreenQuiet waits until the TUI has drawn nothing new for `quiet`, up to
// `max`: typing into a screen that is still settling (kimi right after its
// trust dialog) lost the first Enter, so the prompt sat in the composer.
func waitScreenQuiet(session *PTYSession, quiet, max time.Duration) {
	deadline := time.Now().Add(max)
	last, since := len(session.Output()), time.Now()
	for time.Now().Before(deadline) && time.Since(since) < quiet {
		time.Sleep(100 * time.Millisecond)
		if n := len(session.Output()); n != last {
			last, since = n, time.Now()
		}
	}
}
