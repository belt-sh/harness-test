package driver

import (
	"bytes"
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
	"github.com/inference-sh/agentprotocol/acp"
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
	stripped         string
	strippedFor      int
	beltProbe        chan beltProbeResult
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

// promptText is the one question every mode asks; the answer is the codename
// the mock returns, so a check can tell a real turn from an empty one.
const promptText = "What is the project codename? Reply ONLY the codename."

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
			r.prepareToolCall("headless")
			r.requestHooksFor("headless")
			r.runHeadless()
			r.runChecks("headless")
		} else {
			r.skip(r.harness.Name + " has no headless mode")
		}
	}
	if r.mode == ModeBoth || r.mode == ModeInteractive {
		r.resetPhase("interactive")
		r.requestHooksFor("interactive")
		r.runInteractive()
		r.runChecks("interactive")
	}
	if r.mode == ModeACP {
		r.resetPhase("acp")
		r.requestHooksFor("acp")
		r.runACP()
		r.runChecks("acp")
		// After the checks, because the probes run turns of their own and the
		// checks read the same mock log and hook log.
		if os.Getenv("HARNESS_ACP_LOAD") != "" {
			r.resetPhase("acp")
			r.probeSessionLoad()
		}
		if os.Getenv("HARNESS_ACP_INFLIGHT") != "" {
			r.resetPhase("acp")
			r.probeToolCallInFlight()
		}
		if os.Getenv("HARNESS_ACP_COMPACT") != "" {
			r.resetPhase("acp")
			r.probeCompactedResume()
		}
	}
	if r.mode == ModeSDK {
		r.resetPhase("sdk")
		r.requestHooksFor("sdk")
		r.runSDK()
		r.runChecks("sdk")
	}

	return r.finish()
}

func (r *TestRunner) resetPhase(mode string) {
	os.Remove(hookLogPath)
	os.Remove(hookLogPath + ".stdin")
	r.server.ClearLog()
	r.prepareToolCall(mode)
}

func (r *TestRunner) prepareToolCall(mode string) {
	if r.harness.Events.PreToolUse == "" && r.harness.Events.PostToolUse == "" {
		return
	}
	r.armToolCall(mode)
}

// armToolCall points the mock at the tool this agent calls in this mode and
// reports whether the agent has one at all. Tool hooks are the usual reason to
// serve a tool call, but not the only one: the in-flight probe needs a tool
// call to have something to ask permission for.
func (r *TestRunner) armToolCall(mode string) bool {
	name, args := r.harness.ToolCallName, r.harness.ToolCallArgs
	if o, ok := r.harness.ToolCallByMode[mode]; ok {
		name, args = o.Name, o.Args
	}
	return r.armTool(name, args)
}

// armGatedToolCall arms the tool this agent is expected to ask permission for,
// falling back to its ordinary one. It reports the tool armed, so a result can
// say what the agent was asked to do rather than implying it was asked to do
// anything.
func (r *TestRunner) armGatedToolCall() (tool string, gated bool) {
	if tc := r.harness.ToolCallGated; tc.Name != "" && r.armTool(tc.Name, tc.Args) {
		return tc.Name, true
	}
	if !r.armToolCall("acp") {
		return "", false
	}
	return r.toolMatcher(), false
}

func (r *TestRunner) armTool(name, args string) bool {
	if r.server == nil || name == "" {
		return false
	}
	r.server.PrepareToolCall(name, r.expand(args), r.harness.ToolCallPath)
	// The mocked tool call reads README.md relative to the agent's cwd.
	readme := filepath.Join(r.workDir(), "README.md")
	if _, err := os.Stat(readme); err != nil {
		os.WriteFile(readme, []byte("test"), 0644)
	}
	return true
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
		// Every command-based format is generated by harness/install.go, from
		// the same code that writes belt's own hooks, with this suite's script
		// as the command. Re-implementing the eight formats here meant the
		// mock tested a file shape no user ever gets, and every install side
		// effect had to be duplicated too.
		// Agents whose hooks live beside other config (base URL, auth,
		// permissions) need that config present for the merge to add to.
		if r.harness.HookWrapper != "" && r.harness.HookFileName != "" {
			r.seedWrapperConfig()
		}
		res := harness.InstallWithCommand(r.harness.Name, harness.ScopeUser, r.mockHookCommand)
		if res.Error != nil {
			r.fail("hook install: " + res.Error.Error())
			return
		}
		if res.Inactive != "" {
			r.fail(fmt.Sprintf("hooks installed but %q is the active agent", res.Inactive))
			return
		}
		if r.harness.NeedsGitRepo {
			r.copyHooksToProject(res.HooksPath)
		}
		r.pass(fmt.Sprintf("hooks configured (code: %s)", r.injectCode))
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

	// The released binary re-execs into a newer one mid-run if it finds an
	// update, so --hooks belt would be testing a version this suite never
	// fetched, and the re-exec sometimes swallowed the hook's own output.
	os.Setenv("INFSH_NO_AUTOUPDATE", "1")
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

	r.probeBeltPromptOutput()

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
	prompt := promptText

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

// runOneShotPhase runs a mode that is one command and one prompt, then drives
// the post-run steps a harness declares — compaction, which needs the session
// the turn created. Headless and SDK differ only in which command they run.
func (r *TestRunner) runOneShotPhase(banner, label string, cmd, args []string) {
	fmt.Println(banner)
	out := r.runOneShot(label, cmd, args)

	// The compaction step addresses a session by id. SDK mode used to leave it
	// unset, and the step then expanded to nothing and failed as a
	// configuration error rather than testing compaction.
	r.resolveSessionID(out)
	for _, step := range r.harness.PostHeadlessCmd {
		r.runPostHeadless(r.workDir(), step)
	}
}

// Run already skips the phase when there is no headless command.
func (r *TestRunner) runHeadless() {
	r.runOneShotPhase("[phase 5] headless prompt", "headless", r.harness.HeadlessCmd, r.harness.HeadlessModelArgs)
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
		r.sendLine(session, promptText)
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
		os.WriteFile(dump, []byte(r.strippedOutput()), 0644)
	}
	if os.Getenv("HARNESS_DEBUG") != "" {
		stripped := r.strippedOutput()
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

	bin, args, dir := r.acpInvocation()

	// Write ACP-specific config files (some agents need config in the project dir)
	r.writeACPConfig()

	driver := NewACPDriver(bin, args, dir, r.envIn(dir))
	if err := driver.Start(); err != nil {
		r.fail("ACP start: " + err.Error())
		return
	}
	defer driver.Close()
	r.pass("ACP session started")

	// Agents finish loading hooks after session/new returns; a prompt sent
	// at once can run the hook without its output being attached (grok).
	time.Sleep(2 * time.Second)

	prompt := promptText
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

	if os.Getenv("HARNESS_DUMP_TOOLS") != "" {
		// What this agent offered the model, for picking the tool the
		// in-flight probe should ask permission for.
		fmt.Printf("  [tools] %s declares: %s\n", r.harness.Name, strings.Join(r.server.DeclaredTools(), " "))
	}

}

// acpInvocation is the command that starts this agent in ACP mode, expanded.
func (r *TestRunner) acpInvocation() (bin string, args []string, dir string) {
	for _, a := range r.harness.ACPCmd[1:] {
		args = append(args, r.expand(a))
	}
	for _, a := range r.harness.ACPArgs {
		args = append(args, r.expand(a))
	}
	return r.harness.ACPCmd[0], args, r.workDir()
}

// resumeAttemptDelays are how long after the first process ended each attempt
// at session/load is made.
//
// A probe that attempts the load once, at whatever moment the phase happens to
// reach it, cannot tell "this agent does not resume" from "this agent had not
// finished writing yet". That is not hypothetical: gemini was recorded as
// refusing session/load for weeks, and the refusal disappeared when the probe
// moved later in the phase for an unrelated reason. So the wait is a measured
// quantity now, reported with the result, instead of whatever the preceding
// checks happened to cost.
var resumeAttemptDelays = []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}

// probeSessionLoad answers one question per agent: can a second process pick
// up the conversation the first one was having?
//
// Accepting session/load is not that question, and for a while this probe
// conflated them. An agent that takes the call, ignores the id, opens an empty
// session and sends its usual openers produces exactly what a short replay
// produces: a handful of notifications and no error. Counting notifications
// cannot tell those apart, so the probe no longer tries. It asks the agent a
// follow-up question and reads what the model was sent: if the earlier turn is
// in that request, the session came back; if the request holds only the new
// prompt, the agent attached to nothing.
//
// The probe runs its own session from start to finish. Sharing the phase's
// session made the answer depend on where the probe sat, which is how the
// gemini result went wrong.
//
// Opt-in via HARNESS_ACP_LOAD; =kill ends the first process outright, the way
// a closed laptop does, instead of closing its session.
func (r *TestRunner) probeSessionLoad() {
	if len(r.harness.ACPCmd) == 0 {
		return
	}
	kill := os.Getenv("HARNESS_ACP_LOAD") == "kill"
	how := "closed"
	if kill {
		how = "killed"
	}
	fmt.Printf("[probe] session/load (resume after the first process was %s)\n", how)

	bin, args, dir := r.acpInvocation()
	r.armToolCall("acp")

	first := NewACPDriver(bin, args, dir, r.envIn(dir))
	if err := first.Start(); err != nil {
		r.skip("session/load: ACP start: " + err.Error())
		return
	}
	time.Sleep(2 * time.Second)

	// What this agent sends on a session that has no history at all, recorded
	// from the same process that is about to have some: the yardstick a replay
	// has to beat.
	var openers []string
	for _, n := range first.Updates() {
		openers = append(openers, n.Kind)
	}

	if err := first.SendPrompt(promptText); err != nil {
		first.Kill()
		r.skip("session/load: ACP prompt: " + err.Error())
		return
	}
	first.WaitForResponse([]string{"mock", "hello", "Hello", "codename", "server"}, 60*time.Second)
	first.WaitIdle(2 * time.Second)

	sessionID := first.SessionID()
	if kill {
		first.Kill()
	} else {
		first.Close()
	}
	endedAt := time.Now()
	if sessionID == "" {
		r.skip("session/load: the agent reported no session id to resume")
		return
	}

	r.attemptResume(sessionID, bin, args, dir, how, endedAt, openers)
}

// attemptResume tries the load at increasing delays and reports the first one
// that works, so an agent that needs time to persist its session is told apart
// from one that will not resume at all.
func (r *TestRunner) attemptResume(sessionID, bin string, args []string, dir, how string, endedAt time.Time, openers []string) {
	var lastErr error
	claim := ""
	for _, delay := range resumeAttemptDelays {
		if wait := time.Until(endedAt.Add(delay)); wait > 0 {
			time.Sleep(wait)
		}
		waited := time.Since(endedAt).Round(100 * time.Millisecond)

		resumed := NewACPDriver(bin, args, dir, r.envIn(dir))
		resumed.ResumeSessionID = sessionID
		err := resumed.Start()

		// What the agent claims at initialize, recorded next to what it does.
		// The claim has carried no information in any run so far: every agent
		// measured declares loadSession, including ones that refuse the call.
		claim = "declares loadSession"
		if !resumed.CanLoadSession() {
			claim = "declares nothing"
		}
		if err != nil {
			lastErr = err
			resumed.Close()
			continue
		}

		load := resumed.LoadResult()
		r.pass(fmt.Sprintf("session/load after %s: %s accepted the call %s after the process ended (%s, %d update(s) replayed, answered=%v, %s)",
			how, r.harness.Name, waited, claim, load.Replayed, load.Answered,
			load.Elapsed.Round(time.Millisecond)))
		if lastErr != nil {
			// The earlier refusals were the agent still writing, not the agent
			// declining. A probe that asked once would have reported the
			// refusal as the answer.
			r.pass(fmt.Sprintf("session/load: %s refused until %s had passed, so the session is written after the process ends, not before",
				r.harness.Name, waited))
		}
		r.reportReplayShape(load, resumed.ReplayedKinds(), openers)
		r.reportResumedContext(resumed)
		resumed.Close()
		return
	}

	// Not a failure of this suite or of belt: it is the answer.
	r.skip(fmt.Sprintf("session/load after %s: %s does not resume within %s (%s, %v)",
		how, r.harness.Name, resumeAttemptDelays[len(resumeAttemptDelays)-1], claim, lastErr))
}

// reportReplayShape is the cheap half of the question, and the library answers
// most of it: a fresh session cannot replay the user's own turn, because on a
// fresh session the user has not spoken, so RestoredConversation separates a
// replay from an agent that opened a blank session and sent its usual
// notifications. The comparison against this agent's own openers stays as
// corroboration — it catches a replay that is neither.
func (r *TestRunner) reportReplayShape(load acp.LoadResult, replayed, openers []string) {
	if len(replayed) == 0 {
		r.skip("session/load: " + r.harness.Name + " replayed nothing at all")
		return
	}
	kinds := strings.Join(replayed, ", ")
	if !load.RestoredConversation {
		r.skip(fmt.Sprintf("session/load: %s replayed %s — %d of them conversation and none of them the user's turn, which a fresh session cannot replay",
			r.harness.Name, kinds, load.Conversation))
		return
	}
	if sameKinds(replayed, openers) {
		r.skip(fmt.Sprintf("session/load: %s replayed %s, which is what it sends on a session with no history",
			r.harness.Name, kinds))
		return
	}
	r.pass(fmt.Sprintf("session/load: %s replayed the user's own turn (%s), which a fresh session has none of",
		r.harness.Name, kinds))
}

// resumeFollowUp is asked after a resume. It only has an answer if the earlier
// turn came back.
const resumeFollowUp = "What was my previous question?"

// reportResumedContext is the check that settles it: ask the resumed session a
// question and look at what reached the model. The test is not whether the
// agent answers — the mock answers everything — but whether the request it
// sends carries the turn from before the resume.
func (r *TestRunner) reportResumedContext(resumed *ACPDriver) {
	before := len(r.entries())
	if err := resumed.SendPrompt(resumeFollowUp); err != nil {
		r.skip("session/load: prompting the resumed session failed: " + err.Error())
		return
	}
	// Waiting on the answer's text would be the wrong signal and a slow one:
	// the question here is what the agent sent to the model, so the wait ends
	// as soon as a request arrives, or as soon as the turn ends without one.
	deadline := time.Now().Add(60 * time.Second)
	for len(r.entries()) <= before && resumed.Alive() && !resumed.TurnDone() && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	resumed.WaitIdle(2 * time.Second)

	after := r.entries()
	if len(after) <= before {
		r.skip("session/load: the resumed session sent nothing to the model, so there is nothing to read")
		return
	}
	for _, e := range after[before:] {
		if bytes.Contains(e.Body, []byte(promptText)) {
			r.pass(fmt.Sprintf("session/load: %s carried the earlier turn to the model, so the resume was real", r.harness.Name))
			return
		}
	}
	// The load returned without an error, the notifications arrived, and the
	// conversation is still gone. A negative about an agent has to carry its
	// evidence, so the requests it did send are described: one message is the
	// new prompt alone, and several would mean it carries something this
	// check does not recognise.
	var detail []string
	for _, e := range after[before:] {
		body := string(e.Body)
		note := ""
		if strings.Contains(body, resumeFollowUp) {
			note = ", holds the new prompt"
		}
		detail = append(detail, fmt.Sprintf("%s with %d message(s)%s", e.Path, strings.Count(body, `"role"`), note))
	}
	r.skip(fmt.Sprintf("session/load: %s sent %d request(s) after the resume and none carried the earlier turn — %s",
		r.harness.Name, len(after)-before, strings.Join(detail, "; ")))
}

// sameKinds reports whether two update-kind sequences are identical.
func sameKinds(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// probeCompactedResume asks what a resumed session gives the model after the
// agent has compacted it.
//
// The question exists because a resume can be genuine at the protocol level
// and still not return the conversation: qwen replayed the user's own turn
// after a /compress and sent the model a summary, so both the replay check and
// the capability agreed while the original wording was gone. Two outcomes
// matter and they are very different for a runner. If the model receives the
// summary, compaction-then-resume is lossy by design and safe to build on. If
// it receives nothing, resuming a compacted session silently discards the
// conversation, and a runner has to refuse rather than pretend.
//
// Opt in with HARNESS_ACP_COMPACT. Only agents with a CompactCommand can be
// asked.
func (r *TestRunner) probeCompactedResume() {
	if len(r.harness.ACPCmd) == 0 {
		return
	}
	if r.harness.CompactCommand == "" {
		r.skip("compaction: " + r.harness.Name + " has no compaction command, so there is nothing to compact")
		return
	}
	fmt.Println("[probe] resuming a session the agent compacted")

	bin, args, dir := r.acpInvocation()
	r.armToolCall("acp")

	first := NewACPDriver(bin, args, dir, r.envIn(dir))
	if err := first.Start(); err != nil {
		r.skip("compaction: ACP start: " + err.Error())
		return
	}
	time.Sleep(2 * time.Second)
	if err := first.SendPrompt(promptText); err != nil {
		first.Kill()
		r.skip("compaction: ACP prompt: " + err.Error())
		return
	}
	first.WaitForResponse([]string{"mock", "hello", "Hello", "codename", "server"}, 60*time.Second)
	first.WaitIdle(2 * time.Second)

	// Enough turns that there is something worth compacting. One exchange is
	// below every agent's threshold, and compacting it is a no-op that reports
	// a cheerful "nothing was lost".
	for _, follow := range compactionFillers {
		if first.SendPrompt(follow) != nil {
			break
		}
		first.WaitForResponse([]string{"mock", "hello", "Hello", "server"}, 60*time.Second)
		first.WaitIdle(time.Second)
	}

	// The compaction itself. Closed rather than killed: this probe is about
	// what compaction costs, and a kill would confound it with durability.
	before := len(r.entries())
	first.SendCommand(r.harness.CompactCommand)
	first.WaitIdle(4 * time.Second)
	sent := r.entries()
	if len(sent) <= before {
		// Nothing reached the model, so the agent did not compact. Reporting
		// the resume anyway would be reporting an uncompacted session.
		first.Close()
		r.skip("compaction: " + r.harness.Name + " sent nothing to the model for " + r.harness.CompactCommand + ", so the session was never compacted")
		return
	}
	// A request is not a compaction. Over ACP a slash command can arrive as an
	// ordinary user message — the registry already records that codex treats
	// /compact that way — and the model request it produces looks like any
	// other turn. Without this the probe passes on a session that was never
	// compacted, and "nothing was lost" means only that nothing happened.
	if !looksLikeSummarisation(sent[before:]) {
		first.Close()
		r.skip(fmt.Sprintf("compaction: %s sent %s to the model as an ordinary message rather than compacting, so there is nothing to measure",
			r.harness.Name, r.harness.CompactCommand))
		return
	}
	r.pass("compaction: " + r.harness.Name + " ran " + r.harness.CompactCommand + " and asked the model to summarise")

	sessionID := first.SessionID()
	first.Close()
	if sessionID == "" {
		r.skip("compaction: the agent reported no session id to resume")
		return
	}

	resumed := NewACPDriver(bin, args, dir, r.envIn(dir))
	resumed.ResumeSessionID = sessionID
	if err := resumed.Start(); err != nil {
		r.skip(fmt.Sprintf("compaction: %s does not resume a compacted session (%v)", r.harness.Name, err))
		resumed.Close()
		return
	}
	defer resumed.Close()
	r.reportCompactedContext(resumed)
}

// compactionFillers give the session enough history to be worth compacting.
// The content does not matter; the number of turns does.
var compactionFillers = []string{
	"What files are in this repository?",
	"Summarise what you have done so far.",
	"What was the first thing I asked you?",
}

// looksLikeSummarisation reports whether any of these requests asked the model
// to condense the conversation, which is what an agent does when it compacts.
// Checked against the instructions rather than the whole body, since the word
// can appear in a user turn — one of the fillers above contains it.
func looksLikeSummarisation(entries []server.LogEntry) bool {
	for _, e := range entries {
		body := strings.ToLower(string(e.Body))
		// Phrases an agent uses when instructing a model to condense a
		// thread. Deliberately not the bare word "compact": the slash command
		// arriving as an ordinary user message contains it, and that is the
		// case this exists to rule out.
		for _, phrase := range []string{"summarize the conversation", "summarise the conversation",
			"conversation summary", "summary of the conversation", "condense the conversation",
			"previous conversation", "chat history"} {
			if strings.Contains(body, phrase) {
				return true
			}
		}
	}
	return false
}

// reportCompactedContext classifies what the model was sent after a compacted
// session was resumed: the original wording, something else, or nothing but
// the new prompt.
func (r *TestRunner) reportCompactedContext(resumed *ACPDriver) {
	name := r.harness.Name
	before := len(r.entries())
	if err := resumed.SendPrompt(resumeFollowUp); err != nil {
		r.skip("compaction: prompting the resumed session failed: " + err.Error())
		return
	}
	deadline := time.Now().Add(60 * time.Second)
	for len(r.entries()) <= before && resumed.Alive() && !resumed.TurnDone() && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	resumed.WaitIdle(2 * time.Second)

	after := r.entries()
	if len(after) <= before {
		r.skip("compaction: the resumed session sent nothing to the model, so there is nothing to read")
		return
	}

	carried, widest := false, 0
	for _, e := range after[before:] {
		body := string(e.Body)
		if strings.Contains(body, promptText) {
			carried = true
		}
		if n := strings.Count(body, `"role"`); n > widest {
			widest = n
		}
	}
	switch {
	case carried:
		// Either the agent kept the original wording through compaction, or
		// the compaction did not touch this turn.
		r.pass(fmt.Sprintf("compaction: %s still sent the original wording after compacting, so nothing was lost", name))
	case widest > 1:
		// Lossy by design, which is what compaction is for.
		r.pass(fmt.Sprintf("compaction: %s sent the model %d message(s) but not the original wording, so the resume carries a summary", name, widest))
	default:
		// The dangerous one: the resume worked and the conversation is gone.
		r.fail(fmt.Sprintf("compaction: %s sent the model only the new prompt, so resuming a compacted session loses the conversation", name))
	}
}

// probeToolCallInFlight asks what an agent does about an approval nobody ever
// gave: park a permission request, kill the client while the tool call is
// still waiting on it, then attach to the same session from a new process.
//
// A resumed session that quietly drops the parked call loses work with no
// error anywhere. A resumed session that raises it again is asking a question
// the dead process never got an answer to, and the client has to decide
// whether that is a live question or replayed history. This probe is what
// decides it: agentprotocol marks such a request DuringLoad and refuses to
// interpret it, which is the right call for a library and no help at all to a
// client that has to answer. Opt in with HARNESS_ACP_INFLIGHT; =cancel
// answers the re-raised request with a cancellation instead of an approval.
func (r *TestRunner) probeToolCallInFlight() {
	if len(r.harness.ACPCmd) == 0 {
		return
	}
	fmt.Println("[probe] a tool call in flight across a kill")
	bin, args, dir := r.acpInvocation()

	// The main ACP phase consumed the prepared tool call, and this turn needs
	// its own: without a tool there is nothing to ask permission for.
	// Named in every line below, because whether an agent asks depends on what
	// it was asked to do: agents run reads without consulting anyone, so a
	// probe armed with a read measures nothing about gating.
	tool, gated := r.armGatedToolCall()
	if tool == "" {
		r.skip("in-flight: no tool call is defined for " + r.harness.Name + ", so the mock cannot provoke an approval")
		return
	}
	if !gated {
		r.skip("in-flight: no gated tool is known for " + r.harness.Name + ", so this asks it to run " + tool + ", which agents do not gate")
	}

	// Without this the probe would measure the harness, not the agent: the
	// registry tells several agents to approve everything so unattended runs
	// finish, and an agent told that never asks its client for anything.
	args, ungated := withoutAutoApproval(args)
	if len(ungated) > 0 {
		r.step("in-flight: dropped %s so the agent gates its own tool calls", strings.Join(ungated, " "))
	}

	first := NewACPDriver(bin, args, dir, r.envIn(dir))
	first.ParkPermission = true
	if err := first.Start(); err != nil {
		r.skip("in-flight: ACP start: " + err.Error())
		return
	}
	sessionID := first.SessionID()
	time.Sleep(2 * time.Second)
	if err := first.SendPrompt(promptText); err != nil {
		first.Kill()
		r.skip("in-flight: ACP prompt: " + err.Error())
		return
	}

	if !first.WaitParked(90 * time.Second) {
		first.Kill()
		// Not a failure: an agent that runs tools without consulting its
		// client has no approval to lose, and that is worth knowing too.
		if !r.server.ToolCallServed() {
			// The turn never reached a tool call, so the agent was never in a
			// position to ask. A limit of this harness, not a trait of the
			// agent.
			r.skip("in-flight: the mock never served " + r.harness.Name + " its " + tool + " call, so no approval was ever due")
			return
		}
		gated := "with nothing auto-approving for it"
		if len(ungated) > 0 {
			gated = "even without " + strings.Join(ungated, " ")
		}
		r.skip(fmt.Sprintf("in-flight: %s ran %s without asking the client %s, so there is nothing to park",
			r.harness.Name, tool, gated))
		return
	}
	parked := lastPermission(first.Permissions())
	r.pass(fmt.Sprintf("in-flight: %s asked before running %s — parked its approval for %s (%s)",
		r.harness.Name, tool, orUnnamed(parked.Title), orUnnamed(parked.ToolCallID)))

	// Killed rather than closed: a client that is still holding an approval
	// does not get to send session/close first.
	first.Kill()

	if sessionID == "" {
		r.skip("in-flight: the agent reported no session id to resume")
		return
	}

	before := r.server.LogCount()
	mode := os.Getenv("HARNESS_ACP_INFLIGHT")
	resumed := NewACPDriver(bin, args, dir, r.envIn(dir))
	resumed.ResumeSessionID = sessionID
	resumed.CancelDuringLoad = mode == "cancel"
	// =hold answers nothing on the resumed session either, which asks the last
	// question in the set: does an agent wait on an approval forever, or give
	// up? Measurable only since agentprotocol v0.4.0 — before that the held
	// answer stopped this client's own read loop, and an agent that waited
	// looked exactly like a client that had deadlocked.
	resumed.ParkPermission = mode == "hold"
	if err := resumed.Start(); err != nil {
		r.skip(fmt.Sprintf("in-flight: %s does not resume a session with a tool call in flight (%v)", r.harness.Name, err))
		resumed.Close()
		return
	}
	load := resumed.LoadResult()

	// Anything the agent decides to do about the parked call — re-raise it,
	// abandon it, or run it — happens after the load returns as well as
	// during it, so settle before reading the tally.
	resumed.WaitIdle(3 * time.Second)
	r.reportInFlightResume(resumed.Permissions(), parked, load, r.server.LogCount()-before)

	if resumed.ParkPermission && len(resumed.Permissions()) > 0 {
		r.observeUnansweredApproval(resumed)
	}

	// Twice, because a client reconnects more than once over a long session
	// and an agent may treat the first load as consuming the session.
	resumed.Close()
	again := NewACPDriver(bin, args, dir, r.envIn(dir))
	again.ResumeSessionID = sessionID
	if err := again.Start(); err != nil {
		r.skip(fmt.Sprintf("in-flight: %s resumes once but not twice (%v)", r.harness.Name, err))
		return
	}
	r.pass(fmt.Sprintf("in-flight: %s resumed the same session twice (%d replayed the second time)",
		r.harness.Name, again.LoadResult().Replayed))
	again.Close()
}

// reportInFlightResume says what the resumed process was asked, which is the
// whole point of the probe: whether the parked approval comes back, when, and
// whether it is recognisably the same tool call the api already has a row for.
func (r *TestRunner) reportInFlightResume(seen []PermissionObservation, parked PermissionObservation, load acp.LoadResult, requests int) {
	name := r.harness.Name
	r.pass(fmt.Sprintf("in-flight: %s resumed with a tool call parked (%d replayed, answered=%v, %s)",
		name, load.Replayed, load.Answered, load.Elapsed.Round(time.Millisecond)))

	if len(seen) == 0 {
		// The agent rebuilt the session and never mentioned the tool call
		// again. Nothing errors, and the work is simply gone.
		r.skip(fmt.Sprintf("in-flight: %s does not re-raise the parked approval on resume", name))
		return
	}

	for _, p := range seen {
		when := "after the load returned"
		if p.DuringLoad {
			when = "during the load"
		}
		correlates := "a different toolCallId than the parked one"
		switch {
		case p.ToolCallID == "" || parked.ToolCallID == "":
			correlates = "no toolCallId to correlate by"
		case p.ToolCallID == parked.ToolCallID:
			correlates = "the same toolCallId as the parked call"
		}
		r.pass(fmt.Sprintf("in-flight: %s re-raised an approval %s for %s, %s, answered %s",
			name, when, orUnnamed(p.Title), correlates, p.Answer))
	}

	// Whether the answer went anywhere is the difference between a live
	// question and history: a tool the agent actually runs produces a request
	// to the model with its result, and a replayed one produces nothing.
	if requests > 0 {
		r.pass(fmt.Sprintf("in-flight: answering it made %s run the tool (%d model request(s) after the resume)", name, requests))
	} else {
		r.pass(fmt.Sprintf("in-flight: %s sent nothing to the model after the answer, so nothing was waiting on it", name))
	}
}

// observeUnansweredApproval watches a resumed session whose re-raised approval
// this client is holding and never answering. An agent that waits goes quiet;
// an agent that gives up either says so in an update or exits.
func (r *TestRunner) observeUnansweredApproval(d *ACPDriver) {
	const watch = 60 * time.Second
	name := r.harness.Name
	before := len(d.Updates())
	deadline := time.Now().Add(watch)
	for time.Now().Before(deadline) {
		if !d.Alive() {
			r.pass(fmt.Sprintf("in-flight: %s exited rather than wait on an approval nobody answered", name))
			return
		}
		if notes := d.Updates(); len(notes) > before {
			var kinds []string
			for _, n := range notes[before:] {
				kinds = append(kinds, n.Kind)
			}
			r.pass(fmt.Sprintf("in-flight: %s moved on without its answer, sending %s", name, strings.Join(kinds, ", ")))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.pass(fmt.Sprintf("in-flight: %s waited out %s on an approval nobody answered, saying nothing", name, watch))
}

// autoApprovalFlags are the flags the registry passes so an unattended run
// finishes without a human, mapped to whether they take a value. They are
// exactly the flags the in-flight probe must leave out.
var autoApprovalFlags = map[string]bool{
	"--trust-all-tools": false,
	"--yolo":            false,
	"--auto":            true,
	"--approval-mode":   true,
}

// withoutAutoApproval returns args with the auto-approval flags removed, and
// the flags it removed. Config files can grant the same blanket approval —
// kimi's permissions block does — and this cannot see those, so an agent that
// still never asks is an agent whose configuration was never the reason.
func withoutAutoApproval(args []string) (kept, removed []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		takesValue, known := autoApprovalFlags[a]
		if !known && !strings.HasPrefix(a, "--dangerously-") {
			kept = append(kept, a)
			continue
		}
		removed = append(removed, a)
		if takesValue && i+1 < len(args) {
			i++
			removed = append(removed, args[i])
		}
	}
	return kept, removed
}

// lastPermission is the most recent observation, which for a parking driver is
// the request it is still holding.
func lastPermission(all []PermissionObservation) PermissionObservation {
	if len(all) == 0 {
		return PermissionObservation{}
	}
	return all[len(all)-1]
}

func orUnnamed(s string) string {
	if s == "" {
		return "(unnamed)"
	}
	return s
}

func (r *TestRunner) runSDK() {
	if len(r.harness.SDKCmd) == 0 {
		r.skip(r.harness.Name + " does not support SDK mode")
		return
	}

	r.runOneShotPhase("[phase 8] SDK (stream-json over stdio)", "SDK", r.harness.SDKCmd, r.harness.SDKArgs)
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

	ptyContent := r.strippedOutput()

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

	ptyContent := r.strippedOutput()

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

// resolveSessionID finds the session a follow-up command should address:
// the id the agent reported, else the newest session on disk. Only needed
// when a harness has a post-run step.
func (r *TestRunner) resolveSessionID(out []byte) {
	if len(r.harness.PostHeadlessCmd) == 0 {
		return
	}
	var parsed struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(out, &parsed) == nil && parsed.SessionID != "" {
		r.sessionID = parsed.SessionID
		return
	}
	if r.sessionID == "" {
		r.sessionID = r.findLatestSessionID(r.workDir())
	}
}

// probeBeltPromptOutput asks belt's own prompt hook what it would print, so a
// check can compare that against the shape this agent's channel takes.
//
// It runs in the background: the answer depends on belt and the agent name,
// not on the run, and a suggestion lookup is a network round trip that would
// otherwise sit in front of every belt-mode run.
func (r *TestRunner) probeBeltPromptOutput() {
	r.beltProbe = make(chan beltProbeResult, 1)
	// belt shapes its output for the agent it detects from the environment,
	// and a probe the harness runs itself looks like no agent at all — belt
	// then prints plain text and the shape check has nothing to judge.
	// AI_AGENT is belt's own explicit declaration for exactly this case.
	env := append(r.envIn(r.workDir()),
		"BELT_HOOK_DEBUG_LOG="+filepath.Join(r.home, "belt-probe.log"),
		"AI_AGENT="+r.harness.Name)
	dir, event := r.workDir(), r.harness.Events.PromptSubmit

	go func() {
		// A prompt belt reliably matches. The codename question the agents are
		// asked matches nothing, so probing with it proves only that belt is
		// quiet — which is not what this check is about.
		input, _ := json.Marshal(map[string]any{
			"prompt":          "debug a go test",
			"session_id":      "probe",
			"cwd":             dir,
			"hook_event_name": event,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "belt", "plugin", "hook", "user-prompt-submit")
		// The probe must not write to the log the event checks read: its own
		// entry made a silent prompt hook look like it had fired.
		cmd.Env = env
		cmd.Dir = dir
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output() // stdout is the hook channel; stderr carries notices
		r.beltProbe <- beltProbeResult{
			stdout: stripANSI(string(out)),
			stderr: truncate(strings.TrimSpace(stderr.String()), 120),
			err:    err,
		}
	}()
}

// beltProbeResult is what belt's prompt hook printed when asked directly.
type beltProbeResult struct {
	stdout string
	stderr string
	err    error
}

// strippedOutput is lastOutput with escape sequences removed, computed once
// per captured output. An interactive phase leaves megabytes of TUI dump here
// and several checks read it, each paying a full regexp pass.
func (r *TestRunner) strippedOutput() string {
	if r.stripped == "" || r.strippedFor != len(r.lastOutput) {
		r.stripped = stripANSI(r.lastOutput)
		r.strippedFor = len(r.lastOutput)
	}
	return r.stripped
}

// mockHookCommand is what this suite's hooks run: append the event tag to a
// log the checks read, and for the prompt hook print the payload the agent's
// channel carries, so injection can be traced end to end.
//
// Agents that hand the hook a JSON payload on stdin need it drained: a hook
// that exits with stdin unread gives the agent a broken pipe, and gemini's
// TUI then discards the hook's output.
func (r *TestRunner) mockHookCommand(beltEvent string) string {
	tag := beltTagFor(beltEvent)
	cmd := fmt.Sprintf("[ -t 0 ] || cat >> %s.stdin; echo %s >> %s", hookLogPath, tag, hookLogPath)
	if tag == TagPrompt {
		cmd += r.promptEcho()
	}
	// Formats whose config names a script rather than a command line get one
	// written for them.
	switch r.harness.HookFormat {
	case harness.JSONCopilot, harness.YAML:
		dir := filepath.Join(r.home, "test-hooks")
		os.MkdirAll(dir, 0755)
		script := filepath.Join(dir, tag+".sh")
		os.WriteFile(script, []byte("#!/bin/sh\n"+cmd+"\n"), 0755)
		return script
	}
	return cmd
}

// beltTagFor is the inverse of beltEventNames: the suite's tag for a belt
// hook event.
func beltTagFor(beltEvent string) string {
	for tag, name := range beltEventNames {
		if name == beltEvent {
			return tag
		}
	}
	return strings.ToUpper(beltEvent)
}

// copyHooksToProject mirrors the installed hook file into the repo the agent
// runs in, for agents that read project-scoped config.
func (r *TestRunner) copyHooksToProject(hooksPath string) {
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return
	}
	projDir := filepath.Join(r.ensureGitRepo(), r.harness.HookConfigDir)
	os.MkdirAll(projDir, 0755)
	os.WriteFile(filepath.Join(projDir, filepath.Base(hooksPath)), data, 0644)
}
