package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/server"
	ap "github.com/inference-sh/agentprotocol"
	"github.com/inference-sh/agentprotocol/driver"
	"github.com/inference-sh/agentprotocol/harness"
)

type Result struct {
	Harness    string
	Version    string
	Passed     int
	Failed     int
	Skipped    int
	Findings   int
	Checks     []Check
	Duration   time.Duration
	SkipReason harness.SkipReason // set when the whole harness was skipped
	SkipDetail string
}

// SkippedResult records a harness that was not run at all.
func SkippedResult(h harness.Harness, reason harness.SkipReason, detail string) Result {
	return Result{Harness: h.Name, SkipReason: reason, SkipDetail: detail}
}

// Mode and its constants live in harness, where the registry's per-mode
// tables and KnownIssues keys are written in the same words.
type Mode = harness.Mode

const (
	ModeBoth        = harness.ModeBoth
	ModeHeadless    = harness.ModeHeadless
	ModeInteractive = harness.ModeInteractive
	ModeACP         = harness.ModeACP
	ModeSDK         = harness.ModeSDK
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
	probes           Probes
	section          string         // the phase checks are filed under in the report
	seen             map[string]int // check ids recorded so far, for repeats
	agentVersion     string         // --agent-version: install this version, not the latest
}

func (r *TestRunner) entries() []server.LogEntry {
	if r.testEntries != nil || r.server == nil {
		return r.testEntries
	}
	return r.server.Log()
}

// entryCount is len(entries()) without copying every recorded request body,
// which a poll loop would otherwise do on every tick.
func (r *TestRunner) entryCount() int {
	if r.testEntries != nil || r.server == nil {
		return len(r.testEntries)
	}
	return r.server.LogCount()
}

const hookLogPath = "/tmp/belt-hook-events.log"

// envDumpPath collects what each agent exports to its hooks under --probe env.
const envDumpPath = "/tmp/agent-env-dump.txt"

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

// The suite's log tags, taken from the one event vocabulary in harness rather
// than spelled out again here. They stay as names because the registry's
// KnownIssues keys and ServerRequestedHooks are written in terms of them.
var (
	TagSessionStart = harness.SessionStart.Tag()
	TagPrompt       = harness.PromptSubmit.Tag()
	TagPreTool      = harness.PreToolUse.Tag()
	TagPostTool     = harness.PostToolUse.Tag()
	TagStop         = harness.Stop.Tag()
	TagPreCompact   = harness.PreCompact.Tag()
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

func (r *TestRunner) SetMode(m Mode) {
	r.mode = m
}

func (r *TestRunner) SetProbes(p Probes) {
	r.probes = p
}

func (r *TestRunner) pass(id, msg string) {
	r.result.Passed++
	r.record(OutcomePass, id, msg)
	fmt.Printf("  ✓ %s\n", msg)
}

func (r *TestRunner) fail(id, msg string) {
	r.result.Failed++
	r.failed = true
	r.record(OutcomeFail, id, msg)
	fmt.Fprintf(os.Stderr, "  ✗ %s\n", msg)
}

func (r *TestRunner) skip(id, msg string) {
	r.result.Skipped++
	r.record(OutcomeSkip, id, msg)
	fmt.Printf("  ○ %s\n", msg)
}

// finding records a probe that ran to completion and whose answer is "no".
//
// This used to be a skip, and it made the suite misreport itself: a skip means
// the question could not be asked, and six agents were being counted as
// untested when in fact they had been asked and had answered. A reader
// totalling the skips saw holes where there were results. An answer is not a
// gap, so it gets its own mark and its own column.
func (r *TestRunner) finding(id, msg string) {
	r.result.Findings++
	r.record(OutcomeFinding, id, msg)
	fmt.Printf("  ● %s\n", msg)
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
	if r.probes.Hidden {
		r.probeHidden()
	}

	if r.mode == ModeBoth || r.mode == ModeHeadless {
		r.section = "headless"
		if len(r.harness.HeadlessCmd) > 0 {
			r.prepareToolCall(ModeHeadless)
			r.requestHooksFor(ModeHeadless)
			r.runHeadless()
			r.runChecks("headless")
			// After the checks: the probe runs a turn of its own and the
			// checks read the same mock log.
			if r.probes.Deferred {
				r.probeDeferredTools("headless")
			}
			if r.probes.Transcript {
				r.probeTranscriptRoundTrip("headless")
			}
		} else {
			r.skip("phase:unsupported", r.harness.Name+" has no headless mode")
		}
	}
	if r.mode == ModeBoth || r.mode == ModeInteractive {
		r.section = "interactive"
		r.resetPhase(ModeInteractive)
		r.requestHooksFor(ModeInteractive)
		r.runInteractive()
		r.runChecks("interactive")
	}
	if r.mode == ModeACP {
		r.section = "acp"
		r.resetPhase(ModeACP)
		r.requestHooksFor(ModeACP)
		r.runACP()
		r.runChecks("acp")
		if r.probes.Transcript {
			r.probeTranscriptRoundTrip("acp")
		}
		// After the checks, because the probes run turns of their own and the
		// checks read the same mock log and hook log.
		if r.probes.Resume {
			r.resetPhase(ModeACP)
			r.probeSessionLoad()
		}
		if r.probes.InFlight {
			r.resetPhase(ModeACP)
			r.probeToolCallInFlight()
		}
		if r.probes.Compact {
			r.resetPhase(ModeACP)
			r.probeCompactedResume()
		}
		if r.probes.Deferred {
			r.probeDeferredTools("acp")
		}
		if r.probes.Seed {
			r.resetPhase(ModeACP)
			r.probeTranscriptSeed()
		}
		if r.probes.SeedKinds {
			r.resetPhase(ModeACP)
			r.probeSeedKinds()
			if r.probes.SeedShapes {
				r.resetPhase(ModeACP)
				r.probeSeedShapes()
			}
		}
	}
	if r.mode == ModeSDK {
		r.section = "sdk"
		r.resetPhase(ModeSDK)
		r.requestHooksFor(ModeSDK)
		r.runSDK()
		r.runChecks("sdk")
	}

	return r.finish()
}

func (r *TestRunner) resetPhase(mode Mode) {
	os.Remove(hookLogPath)
	os.Remove(hookLogPath + ".stdin")
	r.server.ClearLog()
	r.prepareToolCall(mode)
}

func (r *TestRunner) prepareToolCall(mode Mode) {
	if r.harness.Events.PreToolUse == "" && r.harness.Events.PostToolUse == "" {
		return
	}
	r.armToolCall(mode)
}

// armToolCall points the mock at the tool this agent calls in this mode and
// reports whether the agent has one at all. Tool hooks are the usual reason to
// serve a tool call, but not the only one: the in-flight probe needs a tool
// call to have something to ask permission for.
func (r *TestRunner) armToolCall(mode Mode) bool {
	name, args := r.harness.ToolCallName, r.harness.ToolCallArgs
	if o, ok := r.harness.ToolCallByMode[mode]; ok {
		name, args = o.Name, o.Args
	}
	if name == "" {
		// toolMatcher falls back to the default tool name, so arming has to
		// as well. They disagreed: claude has tool hooks and no ToolCallName,
		// so its hook config was written with a Read matcher while the mock
		// was never given a tool call to serve, and its pre/post-tool checks
		// skipped in every mode with "the agent was never offered the tool
		// call" — which reads like an agent trait and was a missing default.
		name, args = server.DefaultToolName, server.DefaultToolArgs
	}
	return r.armTool(name, args)
}

// armGatedToolCall arms the tool this agent is expected to ask permission for,
// falling back to its ordinary one. It reports the tool armed, so a result can
// say what the agent was asked to do rather than implying it was asked to do
// anything.
func (r *TestRunner) armGatedToolCall() (tool string, gated bool) {
	tc := r.harness.ToolCallGated
	if tc.Name != "" && r.armTool(tc.Name, tc.Args) {
		return tc.Name, true
	}
	if !r.armToolCall(ModeACP) {
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
	found := ""
	if r.result.Findings > 0 {
		found = fmt.Sprintf(", %d found", r.result.Findings)
	}
	fmt.Printf("\n=== %s: %d passed, %d failed, %d skipped%s (%s) ===\n\n",
		r.harness.Name, r.result.Passed, r.result.Failed, r.result.Skipped, found, r.result.Duration.Round(time.Second))
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
		r.fail("home", "create temp home: "+err.Error())
		return
	}
	r.home = dir
	os.Setenv("HOME", dir)
}

func (r *TestRunner) checkBinary() {
	fmt.Println("[phase 1] prerequisites")
	if r.agentVersion != "" {
		r.installPinned()
		return
	}
	if _, err := exec.LookPath(r.harness.Binary); err != nil {
		if len(r.harness.InstallCmd) == 0 {
			r.fail("binary", r.harness.Binary+" not found (no install command)")
			return
		}
		fmt.Printf("  … installing %s\n", r.harness.Binary)
		cmd := exec.Command(r.harness.InstallCmd[0], r.harness.InstallCmd[1:]...)
		cmd.Env = os.Environ()
		out, installErr := cmd.CombinedOutput()
		if installErr != nil {
			r.fail("binary", fmt.Sprintf("install %s: %v\n%s", r.harness.Binary, installErr, string(out)))
			return
		}
		for _, d := range r.harness.InstallBinDirs {
			p := filepath.Join(r.home, d)
			if !strings.Contains(os.Getenv("PATH"), p) {
				os.Setenv("PATH", p+":"+os.Getenv("PATH"))
			}
		}
		if _, err := exec.LookPath(r.harness.Binary); err != nil {
			r.fail("binary", r.harness.Binary+" not found after install")
			return
		}
		r.pass("binary", r.harness.Binary+" installed")
		r.detectVersion()
		r.checkDetection()
		for _, postCmd := range r.harness.PostInstall {
			cmd := exec.Command(postCmd[0], postCmd[1:]...)
			cmd.Env = os.Environ()
			cmd.Run()
		}
		return
	}
	r.pass("binary", r.harness.Binary+" found")
	r.detectVersion()
	r.checkDetection()
}

// checkDetection compares what harness.DetectInstalled() claims against the
// agent this run has just installed. belt consumes that list to decide where
// to install hooks, and until now nothing checked it: a container that
// installs exactly one agent is the only place "installed" is ground truth.
func (r *TestRunner) checkDetection() {
	if !r.probes.Detect {
		return
	}
	self := harness.DetectOne(r.harness.Name)
	// "Inside this agent" and "this agent is installed" are separate answers.
	// An env-var match alone must never reach the installed list, or belt
	// offers to write hooks for an agent that is not on the machine.
	if envOnly := (harness.DetectResult{Name: self.Name, Probes: []harness.Probe{harness.ProbeEnvVar}}); envOnly.Installed() || envOnly.Configured() {
		r.fail("detect.env-only", "detect: an env-var match alone reports as installed")
	}
	if self.IsEnvironment() && !self.Installed() && !self.Configured() {
		r.fail("detect.env-installed", fmt.Sprintf("detect: %s is only an environment match yet reached the installed list", self.Name))
	}
	if self.Installed() {
		r.pass("detect.self", fmt.Sprintf("detect: %s found installed (%s)", r.harness.Name, probeNames(self)))
	} else {
		r.fail("detect.self", fmt.Sprintf("detect: %s is installed but DetectInstalled does not report it (probes: %s)",
			r.harness.Name, probeNames(self)))
	}
	// Another agent reported installed is only a bug when this run's own
	// install is what it found. Resolving the binary says so exactly: grok's
	// installer drops ~/.grok/bin/agent, and "agent" was cursor's binary
	// name, so every machine with grok reported cursor as installed too.
	// A multi-harness run genuinely has several installed, so the test is
	// provenance, not count.
	var others []string
	for _, d := range harness.DetectInstalled() {
		if d.Name == r.harness.Name {
			continue
		}
		others = append(others, fmt.Sprintf("%s(%s)", d.Name, probeNames(d)))
		if d.Binary == "" {
			continue
		}
		if target, err := filepath.EvalSymlinks(d.Binary); err == nil && r.ownsPath(target) {
			r.fail("detect.other."+d.Name, fmt.Sprintf("detect: %s is reported installed, but its binary %s resolves to %s, which belongs to %s — the binary name is not specific enough to identify it",
				d.Name, d.Binary, target, r.harness.Name))
		}
	}
	if len(others) > 0 {
		fmt.Printf("  [detect] also reported installed: %s\n", strings.Join(others, " "))
	}
}

// ownsPath reports whether a resolved binary path lives inside what this
// harness installed.
func (r *TestRunner) ownsPath(target string) bool {
	for _, d := range r.harness.InstallBinDirs {
		if strings.Contains(target, filepath.Join(r.home, filepath.Dir(d))) {
			return true
		}
	}
	return strings.Contains(target, "/."+r.harness.Name+"/") ||
		strings.Contains(target, "/"+r.harness.Binary+"/")
}

func probeNames(d harness.DetectResult) string {
	var out []string
	for _, p := range d.Probes {
		out = append(out, string(p))
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, "+")
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
		if ver == "" {
			continue
		}
		r.result.Version = ver
		fmt.Printf("  → version: %s\n", ver)
		return
	}
	// Every agent in the registry answers one of those flags, so reaching here
	// means the binary is on PATH and will not run. Say that, because the
	// alternative is what happened to omp in CI on 2026-09-21: the install
	// passed, the version column was blank, the turn exited 127, and the run
	// reported "prompt hook did not fire" and "mock server received no
	// requests" — three symptoms of a cause nothing named.
	r.fail("version", fmt.Sprintf("%s is installed but will not run: no output from --version, -v or version", r.harness.Binary))
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
		r.pass("env."+envVar, envVar+"="+val)
	}
	if r.harness.APIKeyEnvVar != "" {
		os.Setenv(r.harness.APIKeyEnvVar, "mock-key")
		r.pass("env."+r.harness.APIKeyEnvVar, r.harness.APIKeyEnvVar+" set")
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

	os.MkdirAll(filepath.Join(r.home, r.harness.HookConfigDir), 0755)
	os.Remove(hookLogPath)
	// Distinct prefix: instruction-file codes are INSTR-<scope>-<NAME>-<ts> and
	// share the millisecond, so a bare <NAME>-<ts> matched them and the
	// injection check passed whenever the instruction file loaded.
	r.injectCode = fmt.Sprintf("HOOK-%s-%d", strings.ToUpper(r.harness.Name), time.Now().UnixMilli())

	// Every format is generated by harness/install.go, from the same code that
	// writes belt's own hooks, with this suite's script as the command.
	// Re-implementing a format here meant the mock tested a file shape no user
	// ever gets: the hand-written TS plugin injected context that belt's
	// generated one discarded, so the suite reported a channel working that
	// belt did not ship.
	// Agents whose hooks live beside other config (base URL, auth,
	// permissions) need that config present for the merge to add to.
	if r.harness.HookWrapper != "" && r.harness.HookFileName != "" {
		r.seedWrapperConfig()
	}
	res := harness.InstallWithCommand(r.harness.Name, harness.ScopeUser, r.mockHookCommand)
	if res.Error != nil {
		r.fail("hooks.install", "hook install: "+res.Error.Error())
		return
	}
	if res.Inactive != "" {
		r.fail("hooks.install:inactive", fmt.Sprintf("hooks installed but %q is the active agent", res.Inactive))
		return
	}
	if r.harness.NeedsGitRepo {
		r.copyHooksToProject(res.HooksPath)
	}
	r.pass("hooks.install", fmt.Sprintf("hooks configured (code: %s)", r.injectCode))
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
		r.fail("hooks.install", "belt hook install: "+result.Error.Error())
		return
	}

	if result.Merged {
		r.pass("hooks.install", fmt.Sprintf("belt hooks merged into %s", result.HooksPath))
	} else {
		r.pass("hooks.install", fmt.Sprintf("belt hooks created at %s", result.HooksPath))
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
			r.pass("hooks.project-copy", fmt.Sprintf("belt hooks copied to project (%s)", dst))
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
	r.pass("skills", "skills directory created")
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
		r.pass("instructions.written."+name, "instruction file written: "+name)
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
		// The transcript probe reads the session the agent saved, so a flag
		// this suite passes to stop it saving defeats the probe. pi runs
		// headless with --no-session, and its store had never been sampled
		// because of it.
		if a == "--no-session" && r.probes.Transcript {
			continue
		}
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
		r.pass("turn:nonzero-exit", fmt.Sprintf("%s produced output (%d bytes, exit: %v)", label, len(out), err))
	} else if err != nil {
		r.fail("turn", label+": "+err.Error())
	} else if len(out) > 0 {
		r.pass("turn", fmt.Sprintf("%s produced output (%d bytes)", label, len(out)))
	} else {
		r.fail("turn:no-output", label+" produced no output")
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
	r.answeringWithSummary(func() {
		for _, step := range r.harness.PostHeadlessCmd {
			r.runPostHeadless(r.workDir(), step)
		}
	})
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
			r.fail("post-step", "post-headless step not run: template variable expanded to nothing in "+strings.Join(rawArgs, " "))
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
		r.skip("phase:unsupported", r.harness.Name+" has no interactive mode")
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
	launchMark := r.markTurn(true)
	session, err := StartPTY(r.harness.InteractiveCmd[0], iargs, dir, r.envIn(dir))
	if err != nil {
		r.fail("tui.start", "PTY start: "+err.Error())
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
				if action.SendDown {
					session.SendDown()
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
	r.pass("tui.start", "TUI started")

	mark := launchMark
	if !r.harness.InteractivePromptInArgs {
		waitScreenQuiet(session, 1500*time.Millisecond, 15*time.Second)
		mark = r.markTurn(true)
		r.sendLine(session, promptText)
	}
	r.step("waiting for answer > %d (served %d)", mark.answers, r.server.AnswersServed())
	r.waitTurnSettled(mark, 90*time.Second)
	r.step("turn settled (served %d, requests %d)", r.server.AnswersServed(), r.server.LogCount())
	if os.Getenv("HARNESS_DEBUG") != "" && r.server.LogCount() == 0 {
		scr := stripANSI(session.Output())
		if len(scr) > 1200 {
			scr = scr[len(scr)-1200:]
		}
		fmt.Printf("    [step] no request yet; screen tail:\n%s\n", scr)
	}
	if r.harness.CompactCommand != "" {
		mark = r.markTurn(true)
		r.step("typing second prompt")
		r.sendLine(session, "Tell me more about the project.")
		r.waitTurnSettled(mark, 60*time.Second)
		r.step("second turn settled (served %d, requests %d)", r.server.AnswersServed(), r.server.LogCount())
		// The stop hook ends the wait above, and codex runs it before its
		// task is over: a /compact typed then is refused with "'/compact' is
		// disabled while a task is in progress" and nothing compacts (1 run
		// in 8 locally, 1 in 3 in CI). A TUI still working redraws its
		// spinner, so a quiet screen is the task being over.
		waitScreenQuiet(session, 1500*time.Millisecond, 20*time.Second)
		compactMark := r.markTurn(false)
		r.answeringWithSummary(func() {
			r.sendLine(session, r.harness.CompactCommand)
			session.WaitForAny([]string{"compact", "Compact", "compress", "Compress", "summar"}, 15*time.Second)
			if r.harness.CompactConfirm {
				if _, ok := session.WaitForAny([]string{"Enter to confirm", "confirm"}, 10*time.Second); ok {
					session.SendLine("")
				}
			}
			r.waitTurnSettled(compactMark, 30*time.Second)
		})
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

	r.pass("tui.completed", "interactive session completed")
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
	kind := r.harness.DriverKind()
	if kind == "" {
		r.skip("phase:unsupported", r.harness.Name+" has no session driver (no ACP command and no native backend)")
		return
	}
	label := r.sessionLabel()

	if kind == harness.DriverACP {
		fmt.Println("[phase 7] ACP (JSON-RPC over stdio)")
	} else {
		fmt.Printf("[phase 7] session (native %s backend)\n", kind)
	}

	// Write ACP-specific config files (some agents need config in the project dir)
	r.writeACPConfig()

	sess := r.newSession(false, false)
	if err := sess.Start(); err != nil {
		r.fail("session.start", label+" start: "+err.Error())
		return
	}
	defer sess.Close()
	r.pass("session.start", label+" session started")

	// Agents finish loading hooks after session/new returns; a prompt sent
	// at once can run the hook without its output being attached (grok).
	time.Sleep(2 * time.Second)

	answered := r.server.AnswersServed()
	prompt := promptText
	if err := sess.SendPrompt(prompt); err != nil {
		r.fail("session.prompt", label+" prompt: "+err.Error())
		return
	}

	err := sess.WaitAnswered(r.server.AnswersServed, answered, 60*time.Second)
	if err != nil {
		r.skip("session.answer:no-answer", label+" response: "+err.Error())
	} else {
		r.pass("session.answer", label+" prompt answered")
	}

	// Let tool execution and post-tool hooks settle
	sess.WaitIdle(2 * time.Second)

	if r.harness.CompactCommand != "" {
		r.answeringWithSummary(func() {
			sess.SendCommand(r.harness.CompactCommand)
			sess.WaitIdle(3 * time.Second)
		})
	}

	r.lastOutput = sess.Output()
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] %s output (%d bytes):\n%s\n", label, len(r.lastOutput), r.lastOutput)
	}

	r.pass("session.completed", label+" session completed")

}

// acpInvocation is the command that starts this agent in ACP mode, expanded,
// including whatever makes it run unattended.
func (r *TestRunner) acpInvocation() (bin string, args []string, dir string) {
	bin, args, dir = r.acpInvocationGated()
	for _, a := range r.harness.ACPAutoApproveArgs {
		args = append(args, r.expand(a))
	}
	return bin, args, dir
}

// acpInvocationGated leaves out the arguments that stop the agent asking its
// client for permission, which the registry names rather than the runner
// recognising them by pattern afterwards.
func (r *TestRunner) acpInvocationGated() (bin string, args []string, dir string) {
	for _, a := range r.harness.ACPCmd[1:] {
		args = append(args, r.expand(a))
	}
	for _, a := range r.harness.ACPArgs {
		args = append(args, r.expand(a))
	}
	return r.harness.ACPCmd[0], args, r.workDir()
}

// sessionBackend builds the backend this agent's sessions run on, chosen by
// its DriverKind. It is built for this suite's environment, not with
// driver.ForHarness: that one deliberately leaves out ACPArgs and the mock
// endpoints, which are exactly what a run against the mock needs.
//
//   - ACP: the registry's ACP command and ACPArgs, plus ACPAutoApproveArgs
//     unless gated. emitReplay publishes a resumed session's replay as
//     events, which the replay checks count.
//   - claude-code: ClaudeBackend. The mock endpoint comes from the
//     environment setupEndpoint exported (ANTHROPIC_BASE_URL and the
//     tokens), the same variables headless mode uses; the model is set per
//     session.
//   - pi: PiBackend, with the registry's --provider from its SDK arguments;
//     the model is set per session and the intercept carries the requests to
//     the mock, as in headless mode. --no-session is left out: sessions are
//     what the probes resume.
//   - codex: CodexBackend, with the registry's -c provider overrides that
//     point codex at the mock, taken from its SDK arguments, and
//     bypass_hook_trust in the thread config so this suite's hooks run.
//
// gated only matters to ACP: the native backends never skip approvals, and
// this suite answers them through Resolve.
func (r *TestRunner) sessionBackend(gated, emitReplay bool) BackendFactory {
	dir := r.workDir()
	env := r.envIn(dir)
	switch r.harness.DriverKind() {
	case harness.DriverClaudeCode:
		bin := r.harness.Binary
		return func(diagnose func(string)) driver.Backend {
			return &driver.ClaudeBackend{Command: bin, Env: env, Stderr: os.Stderr, OnDiagnostic: diagnose}
		}
	case harness.DriverPi:
		bin, args := r.harness.Binary, r.piProviderArgs()
		return func(diagnose func(string)) driver.Backend {
			return &driver.PiBackend{Command: bin, Args: args, Env: env, Stderr: os.Stderr, OnDiagnostic: diagnose}
		}
	case harness.DriverCodex:
		bin, args := r.harness.Binary, r.codexProviderArgs()
		return func(diagnose func(string)) driver.Backend {
			return &driver.CodexBackend{Command: bin, Args: args, Env: env, Stderr: os.Stderr,
				Config:     codexThreadConfig,
				ClientName: "harness-test", ClientVersion: "1.0.0", OnDiagnostic: diagnose}
		}
	default:
		bin, args, _ := r.acpInvocation()
		if gated {
			bin, args, _ = r.acpInvocationGated()
		}
		return func(diagnose func(string)) driver.Backend {
			return &driver.ACPBackend{Command: bin, Args: args, Env: env,
				ClientName: "harness-test", ClientVersion: "1.0.0",
				EmitReplay: emitReplay, OnDiagnostic: diagnose}
		}
	}
}

// codexProviderArgs are the -c overrides in the registry's codex arguments
// (model, model_provider and the mock provider's endpoint). The registry keeps
// them in an unexported list, so they are read back out of SDKArgs, which
// carries them after flags `codex app-server` does not take.
//
// One of those flags has no -c equivalent: exec and the TUI get
// --dangerously-bypass-hook-trust, which app-server rejects as an argument
// (codex 0.156.1), and -c bypass_hook_trust=true is "ignored" as a session
// flag. app-server honours it only as thread config, which is
// codexThreadConfig. --dangerously-bypass-approvals-and-sandbox is left out on
// purpose: it would take the approvals away from the session.
// codexThreadConfig is the thread config this suite's codex sessions run
// with. bypass_hook_trust lets the hooks it writes run without the trust
// codex otherwise asks a person to persist; the test's own hooks are the only
// ones there. It is set here, for this suite, and never by agentprotocol.
var codexThreadConfig = map[string]any{"bypass_hook_trust": true}

func (r *TestRunner) codexProviderArgs() []string {
	var out []string
	src := r.harness.SDKArgs
	for i := 0; i+1 < len(src); i++ {
		if src[i] == "-c" {
			out = append(out, "-c", r.expand(src[i+1]))
			i++
		}
	}
	return out
}

// piProviderArgs is the --provider pair in the registry's pi arguments.
func (r *TestRunner) piProviderArgs() []string {
	src := r.harness.SDKArgs
	for i := 0; i+1 < len(src); i++ {
		if src[i] == "--provider" {
			return []string{"--provider", r.expand(src[i+1])}
		}
	}
	return nil
}

// newSession is a session driver on this agent's backend, at the working
// directory. gated withholds ACPAutoApproveArgs; emitReplay publishes an ACP
// replay as events.
func (r *TestRunner) newSession(gated, emitReplay bool) *SessionDriver {
	d := NewSessionDriver(r.sessionBackend(gated, emitReplay), r.workDir())
	if r.harness.DriverKind() != harness.DriverACP {
		d.Model = r.harness.DefaultModel
	}
	return d
}

// resumeSession is newSession attached to an existing session.
func (r *TestRunner) resumeSession(id string, gated, emitReplay bool) *SessionDriver {
	d := r.newSession(gated, emitReplay)
	d.ResumeSessionID = id
	return d
}

// sessionLabel names the session transport in result lines. "ACP" keeps the
// lines ACP agents have always produced.
func (r *TestRunner) sessionLabel() string {
	if k := r.harness.DriverKind(); k != harness.DriverACP {
		return k
	}
	return "ACP"
}

// resumeLabel names the resume operation in result lines: session/load for
// ACP, and resume for the native backends, which reopen a session by id.
func (r *TestRunner) resumeLabel() string {
	if r.harness.DriverKind() == harness.DriverACP {
		return "session/load"
	}
	return "resume"
}

// startProbeTurn opens a session of the probe's own and runs one turn in it.
//
// Every ACP probe needs the same prologue, and it used to be pasted into each
// of them: three copies had drifted by the time they were counted. The session
// belongs to the probe, which is the rule that keeps a probe's answer from
// depending on what the phase did to a shared session.
//
// ok is false when the probe should give up; the skip has been recorded.
//
// The caller arms whatever tool call it wants served. This used to arm the
// default one here, which silently overwrote a probe that had armed its own —
// the deferred-tools probe arms a tool-search call and would have had it
// replaced by Read before the turn ran.
func (r *TestRunner) startProbeTurn(label string) (d *SessionDriver, ok bool) {
	d = r.newSession(false, false)
	if err := d.Start(); err != nil {
		r.skip(probeID(label)+".start", label+": "+r.sessionLabel()+" start: "+err.Error())
		return nil, false
	}
	// Agents finish loading hooks after session/new returns; a prompt sent at
	// once can run the hook without its output being attached (grok).
	time.Sleep(2 * time.Second)

	answered := r.server.AnswersServed()
	if err := d.SendPrompt(promptText); err != nil {
		d.Kill()
		r.skip(probeID(label)+".prompt", label+": "+r.sessionLabel()+" prompt: "+err.Error())
		return nil, false
	}
	d.WaitAnswered(r.server.AnswersServed, answered, 60*time.Second)
	d.WaitIdle(2 * time.Second)
	return d, true
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
// Opt-in via --probe resume; resume=kill ends the first process outright, the
// way a closed laptop does, instead of closing its session.
func (r *TestRunner) probeSessionLoad() {
	if r.harness.DriverKind() == "" {
		return
	}
	op := r.resumeLabel()
	kill := r.probes.ResumeKill
	how := r.probes.How()
	fmt.Printf("[probe] %s (resume after the first process was %s)\n", op, how)

	r.armToolCall(ModeACP)
	first, ok := r.startProbeTurn(op)
	if !ok {
		return
	}

	sessionID := first.SessionID()
	if kill {
		if err := first.Kill(); errors.Is(err, errNoKill) {
			// Closed instead; reporting the resume as one after a kill would
			// be reporting the easy case as the hard one.
			r.skip("resume.kill:unkillable", fmt.Sprintf("%s after killed: the %s session could not be killed (%v)", op, r.sessionLabel(), err))
			return
		}
	} else {
		first.Close()
	}
	endedAt := time.Now()
	if sessionID == "" {
		r.skip("resume.session-id", op+": the agent reported no session id to resume")
		return
	}

	// What an ACP agent claims at initialize, recorded next to what it does.
	// The claim has carried no information in any run so far: every agent
	// measured declares loadSession, including ones that refuse the call.
	// The native backends have no such claim; they resume by id.
	claim := "resumes by id"
	if r.harness.DriverKind() == harness.DriverACP {
		bin, args, dir := r.acpInvocation()
		claim = acpLoadClaim(bin, args, dir, r.envIn(dir))
	}
	r.attemptResume(sessionID, func() *SessionDriver { return r.resumeSession(sessionID, false, true) },
		how, claim, endedAt)
}

// attemptResume tries the load at increasing delays and reports the first one
// that works, so an agent that needs time to persist its session is told apart
// from one that will not resume at all.
func (r *TestRunner) attemptResume(sessionID string, open func() *SessionDriver, how, claim string, endedAt time.Time) {
	op := r.resumeLabel()
	var lastErr error
	for _, delay := range resumeAttemptDelays {
		delay += r.probes.ResumeAfter
		if wait := time.Until(endedAt.Add(delay)); wait > 0 {
			time.Sleep(wait)
		}
		waited := time.Since(endedAt).Round(100 * time.Millisecond)

		resumed := open()
		if err := resumed.Start(); err != nil {
			lastErr = err
			resumed.Close()
			continue
		}

		load := resumed.LoadResult()
		if load.Reported {
			r.pass("resume.load", fmt.Sprintf("%s after %s: %s accepted the call %s after the process ended (%s, %d update(s) replayed, answered=%v, %s)",
				op, how, r.harness.Name, waited, claim, load.Replayed, load.Answered,
				load.Elapsed.Round(time.Millisecond)))
		} else {
			r.pass("resume.load:by-id", fmt.Sprintf("%s after %s: %s reopened the session %s after the process ended (%s)",
				op, how, r.harness.Name, waited, claim))
		}
		if lastErr != nil {
			// The earlier refusals were the agent still writing, not the agent
			// declining. A probe that asked once would have reported the
			// refusal as the answer.
			r.pass("resume.retried", fmt.Sprintf("%s: %s refused until %s had passed, so the session is written after the process ends, not before",
				op, r.harness.Name, waited))
		}
		if load.Reported {
			r.reportReplayShape(load, resumed.ReplayedKinds())
		} else {
			r.skip("resume.replay:by-id", fmt.Sprintf("%s: %s replays nothing to its client (%s resumes by id), so the replay check is ACP's; the next check reads the model request",
				op, r.harness.Name, resumed.Kind()))
		}
		r.reportResumedContext(resumed)
		resumed.Close()
		return
	}

	// Not a failure of this suite or of belt: it is the answer.
	r.skip("resume.load:not-resumed", fmt.Sprintf("%s after %s: %s does not resume within %s (%s, %v)",
		op, how, r.harness.Name, resumeAttemptDelays[len(resumeAttemptDelays)-1], claim, lastErr))
}

// reportReplayShape is the cheap half of the question, and the library answers
// it: a fresh session cannot replay the user's own turn, because on a fresh
// session the user has not spoken, so RestoredConversation separates a replay
// from an agent that opened a blank session and sent its usual notifications.
//
// The counts come from ACPBackend's report of the load, the kinds from the
// events the replay produced with EmitReplay on. Those are lifecycle events,
// not ACP update kinds: a user turn, a mode or a command list maps to no event,
// so the kinds can be empty while the count is not. That is also why the old
// comparison against the agent's openers is gone: a replay that restored the
// user's turn could never equal a fresh session's openers, so once
// RestoredConversation is true it could not fire.
func (r *TestRunner) reportReplayShape(load LoadResult, replayed []string) {
	if load.Replayed == 0 {
		r.skip("resume.replay:nothing", "session/load: "+r.harness.Name+" replayed nothing at all")
		return
	}
	kinds := fmt.Sprintf("%d update(s)", load.Replayed)
	if len(replayed) > 0 {
		kinds += ": " + strings.Join(replayed, ", ")
	}
	if !load.RestoredConversation {
		r.skip("resume.replay:no-user-turn", fmt.Sprintf("session/load: %s replayed %s — %d of them conversation and none of them the user's turn, which a fresh session cannot replay",
			r.harness.Name, kinds, load.Conversation))
		return
	}
	r.pass("resume.replay", fmt.Sprintf("session/load: %s replayed the user's own turn (%s), which a fresh session has none of",
		r.harness.Name, kinds))
}

// askResumed puts a question to a resumed session and returns what reached the
// model because of it. Both resume probes need exactly this, and the wait is
// the part most likely to need changing, so it lives in one place.
//
// The wait ends on a request arriving or the turn ending — not on words in the
// answer, which is both the wrong signal and a slow one.
func (r *TestRunner) askResumed(d *SessionDriver, label string) ([]server.LogEntry, bool) {
	before := r.entryCount()
	if err := d.SendPrompt(resumeFollowUp); err != nil {
		r.skip(probeID(label)+".followup", label+": prompting the resumed session failed: "+err.Error())
		return nil, false
	}
	deadline := time.Now().Add(60 * time.Second)
	for r.entryCount() <= before && d.Alive() && !d.TurnDone() && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	d.WaitIdle(2 * time.Second)

	after := r.entries()
	if len(after) <= before {
		r.skip(probeID(label)+".followup:nothing-sent", label+": the resumed session sent nothing to the model, so there is nothing to read")
		return nil, false
	}
	return after[before:], true
}

// entriesContain reports whether any of these requests carried the text.
func entriesContain(entries []server.LogEntry, want string) bool {
	for _, e := range entries {
		if bytes.Contains(e.Body, []byte(want)) {
			return true
		}
	}
	return false
}

// resumeFollowUp is asked after a resume. It only has an answer if the earlier
// turn came back.
const resumeFollowUp = "What was my previous question?"

// reportResumedContext is the check that settles it: ask the resumed session a
// question and look at what reached the model. The test is not whether the
// agent answers — the mock answers everything — but whether the request it
// sends carries the turn from before the resume.
func (r *TestRunner) reportResumedContext(resumed *SessionDriver) {
	op := r.resumeLabel()
	sent, ok := r.askResumed(resumed, op)
	if !ok {
		return
	}
	if entriesContain(sent, promptText) {
		r.pass("resume.context", fmt.Sprintf("%s: %s carried the earlier turn to the model, so the resume was real", op, r.harness.Name))
		return
	}
	// The load returned without an error, the notifications arrived, and the
	// conversation is still gone. A negative about an agent has to carry its
	// evidence, so the requests it did send are described: one message is the
	// new prompt alone, and several would mean it carries something this
	// check does not recognise.
	var detail []string
	for _, e := range sent {
		body := string(e.Body)
		note := ""
		if strings.Contains(body, resumeFollowUp) {
			note = ", holds the new prompt"
		}
		detail = append(detail, fmt.Sprintf("%s with %d message(s)%s", e.Path, strings.Count(body, `"role"`), note))
	}
	r.skip("resume.context:lost", fmt.Sprintf("%s: %s sent %d request(s) after the resume and none carried the earlier turn — %s",
		op, r.harness.Name, len(sent), strings.Join(detail, "; ")))
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
// Opt in with --probe compact. Only agents with a CompactCommand can be asked.
func (r *TestRunner) probeCompactedResume() {
	if r.harness.DriverKind() == "" {
		return
	}
	if r.harness.CompactCommand == "" {
		r.skip("compact:no-command", "compaction: "+r.harness.Name+" has no compaction command, so there is nothing to compact")
		return
	}
	fmt.Println("[probe] resuming a session the agent compacted")

	r.armToolCall(ModeACP)
	first, ok := r.startProbeTurn("compaction")
	if !ok {
		return
	}

	// Enough turns that there is something worth compacting. One exchange is
	// below every agent's threshold, and compacting it is a no-op that reports
	// a cheerful "nothing was lost".
	for _, follow := range compactionFillers {
		if first.SendPrompt(follow) != nil {
			break
		}
		first.WaitAnswered(r.server.AnswersServed, r.server.AnswersServed()-1, 60*time.Second)
		first.WaitIdle(time.Second)
	}

	// The compaction itself. Closed rather than killed: this probe is about
	// what compaction costs, and a kill would confound it with durability.
	before := r.entryCount()
	widestBefore := widestRequest(r.entries())
	eventsBefore := len(first.Updates())
	r.answeringWithSummary(func() {
		first.SendCommand(r.harness.CompactCommand)
		first.WaitIdle(4 * time.Second)
	})
	sent := r.entries()
	if len(sent) <= before {
		// Nothing reached the model, so the agent did not compact. Reporting
		// the resume anyway would be reporting an uncompacted session.
		first.Close()
		r.skip("compact.run:nothing-sent", "compaction: "+r.harness.Name+" sent nothing to the model for "+r.harness.CompactCommand+", so the session was never compacted")
		return
	}
	// A request is not a compaction. Over ACP a slash command can arrive as an
	// ordinary user message (codex, gemini and droid all do this with theirs),
	// and the model request it produces looks like any other turn. Without
	// these checks the probe passes on a session that was never compacted,
	// and "nothing was lost" means only that nothing happened.
	//
	// Both are structural rather than a search for summarising words, which
	// would be a denylist that had to stay disjoint from the probe's filler
	// prompts. A compaction request asks the model to condense the thread, so
	// its last user message is the agent's own instruction; a command that
	// arrived as a prompt is the last user message itself. Width alone does
	// not tell them apart: such a prompt carries the whole history plus one
	// message, wider than the turn before.
	//
	// A backend that reports the compaction settles it without either:
	// ClaudeBackend emits context.compacted from claude's compact_boundary.
	// claude's compaction request is narrower than the turn before it, so the
	// width check below would call a real compaction a no-op. No ACP update
	// maps to the event, so ACP agents are still judged by the requests.
	compacted := false
	for _, n := range first.Updates()[eventsBefore:] {
		compacted = compacted || n.Kind == string(ap.AgentEventContextCompacted)
	}
	for _, e := range sent[before:] {
		if compacted {
			break // what reached the model does not matter once it is reported
		}
		// Starts with, not contains: a real compaction request may quote the
		// conversation, and the command is the last thing in it.
		if strings.HasPrefix(strings.TrimSpace(server.LastUserText(e.Body)), r.harness.CompactCommand) {
			first.Close()
			r.finding("compact.run:as-prompt", fmt.Sprintf("compaction: %s sent %s to the model as a user message, so over %s it is a prompt, not a command, and nothing was compacted",
				r.harness.Name, r.harness.CompactCommand, r.sessionLabel()))
			return
		}
	}
	if compacted {
		r.pass("compact.run:reported", fmt.Sprintf("compaction: %s ran %s and reported the compaction (%d-message request, %d before)",
			r.harness.Name, r.harness.CompactCommand, widestRequest(sent[before:]), widestBefore))
	} else if widestRequest(sent[before:]) < widestBefore {
		first.Close()
		r.finding("compact.run:not-compacted", fmt.Sprintf("compaction: %s answered %s with a %d-message request where the turn before carried %d, so it did not compact",
			r.harness.Name, r.harness.CompactCommand, widestRequest(sent[before:]), widestBefore))
		return
	} else {
		r.pass("compact.run", fmt.Sprintf("compaction: %s ran %s over %d message(s)", r.harness.Name, r.harness.CompactCommand, widestRequest(sent[before:])))
	}

	sessionID := first.SessionID()
	first.Close()
	closedAt := time.Now()
	if sessionID == "" {
		r.skip("compact.session-id", "compaction: the agent reported no session id to resume")
		return
	}

	r.probes.holdBack(closedAt)
	resumed := r.resumeSession(sessionID, false, false)
	if err := resumed.Start(); err != nil {
		r.skip("compact.resume:refused", fmt.Sprintf("compaction: %s does not resume a compacted session (%v)", r.harness.Name, err))
		resumed.Close()
		return
	}
	defer resumed.Close()
	r.reportCompactedContext(resumed)
}

// answeringWithSummary runs a compaction step with the mock answering
// compactionSummary. The mock's usual answer is one short line, and an agent
// that checks its summary rejects that as degenerate and keeps the history:
// grok logs "Compaction produced only degenerate summaries" and compacts
// nothing.
func (r *TestRunner) answeringWithSummary(step func()) {
	answer := r.server.Response()
	r.server.SetResponse(compactionSummary)
	defer r.server.SetResponse(answer)
	step()
}

// compactionSummary is what the mock answers a compaction with: long enough
// that no agent's degenerate-summary check rejects it (grok wants more than
// 500 characters), and free of the probe's prompt wording, so a resume that
// carries it is told apart from one that carries the original turn.
var compactionSummary = "Summary of the conversation so far. The user opened a session in a small " +
	"git repository and asked a series of questions about it. The assistant listed the files in " +
	"the repository, described the work done up to that point, and recalled the first request " +
	"when asked. A file named test-output.txt was written with the content test. No errors " +
	"occurred, no changes are pending review, and no follow-up work was requested. The session " +
	"was then compacted at the user's request so that further turns start from this summary " +
	"instead of the full history. Nothing in the conversation depends on exact wording, and " +
	"the next turn may ask about anything covered above."

// compactionFillers give the session enough history to be worth compacting.
// The content does not matter; the number of turns does.
var compactionFillers = []string{
	"What files are in this repository?",
	"Summarise what you have done so far.",
	"What was the first thing I asked you?",
}

// messageCount is how many messages a request carried to the model. Every
// format this mock speaks marks them the same way, and the count is what
// compaction changes: a compacted thread goes to the model as a summary plus
// the new turn, not as the whole history.
func messageCount(e server.LogEntry) int {
	return bytes.Count(e.Body, []byte(`"role"`))
}

// widestRequest is the largest message count among these requests.
func widestRequest(entries []server.LogEntry) int {
	widest := 0
	for _, e := range entries {
		if n := messageCount(e); n > widest {
			widest = n
		}
	}
	return widest
}

// reportCompactedContext classifies what the model was sent after a compacted
// session was resumed: the original wording, something else, or nothing but
// the new prompt.
func (r *TestRunner) reportCompactedContext(resumed *SessionDriver) {
	name := r.harness.Name
	sent, ok := r.askResumed(resumed, "compaction")
	if !ok {
		return
	}

	carried := entriesContain(sent, promptText)
	widest := widestRequest(sent)
	switch {
	case carried:
		// Either the agent kept the original wording through compaction, or
		// the compaction did not touch this turn.
		r.pass("compact.context:original", fmt.Sprintf("compaction: %s still sent the original wording after compacting, so nothing was lost", name))
	case widest > 1:
		// Lossy by design, which is what compaction is for.
		r.pass("compact.context:summary", fmt.Sprintf("compaction: %s sent the model %d message(s) but not the original wording, so the resume carries a summary", name, widest))
	default:
		// The dangerous one: the resume worked and the conversation is gone.
		r.fail("compact.context:lost", fmt.Sprintf("compaction: %s sent the model only the new prompt, so resuming a compacted session loses the conversation", name))
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
// client that has to answer. Opt in with --probe inflight; inflight=cancel
// answers the re-raised request with a cancellation instead of an approval.
func (r *TestRunner) probeToolCallInFlight() {
	if r.harness.DriverKind() == "" {
		return
	}
	fmt.Println("[probe] a tool call in flight across a kill")

	// The main ACP phase consumed the prepared tool call, and this turn needs
	// its own: without a tool there is nothing to ask permission for.
	// Named in every line below, because whether an agent asks depends on what
	// it was asked to do: agents run reads without consulting anyone, so a
	// probe armed with a read measures nothing about gating.
	tool, gated := r.armGatedToolCall()
	if tool == "" {
		r.skip("inflight:no-tool", "in-flight: no tool call is defined for "+r.harness.Name+", so the mock cannot provoke an approval")
		return
	}
	if !gated {
		r.skip("inflight.gated-tool:unknown", "in-flight: no gated tool is known for "+r.harness.Name+", so this asks it to run "+tool+", which agents do not gate")
	}

	// Without this the probe would measure the harness, not the agent: the
	// registry tells several agents to approve everything so unattended runs
	// finish, and an agent told that never asks its client for anything. The
	// native backends are never told that.
	var ungated []string
	if r.harness.DriverKind() == harness.DriverACP {
		ungated = r.harness.ACPAutoApproveArgs
	}
	if len(ungated) > 0 {
		r.step("in-flight: withheld %s so the agent gates its own tool calls", strings.Join(ungated, " "))
	}
	label := r.sessionLabel()

	first := r.newSession(true, false)
	first.ParkPermission = true
	if err := first.Start(); err != nil {
		r.skip("inflight.start", "in-flight: "+label+" start: "+err.Error())
		return
	}
	if !first.CanKill() {
		// The probe is a kill with an approval held. Closing instead answers
		// the held request, which is a different question.
		first.Close()
		r.skip("inflight.kill:unkillable", fmt.Sprintf("in-flight: the %s session cannot be killed (%v)", label, errNoKill))
		return
	}
	sessionID := first.SessionID()
	time.Sleep(2 * time.Second)
	if err := first.SendPrompt(promptText); err != nil {
		first.Kill()
		r.skip("inflight.prompt", "in-flight: "+label+" prompt: "+err.Error())
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
			r.skip("inflight.park:tool-not-served", "in-flight: the mock never served "+r.harness.Name+" its "+tool+" call, so no approval was ever due")
			return
		}
		gated := "with nothing auto-approving for it"
		if len(ungated) > 0 {
			gated = "even without " + strings.Join(ungated, " ")
		}
		r.skip("inflight.park:not-gated", fmt.Sprintf("in-flight: %s ran %s without asking the client %s, so there is nothing to park",
			r.harness.Name, tool, gated))
		return
	}
	parked := lastPermission(first.Permissions())
	r.pass("inflight.park", fmt.Sprintf("in-flight: %s asked before running %s — parked its approval for %s (%s)",
		r.harness.Name, tool, orUnnamed(parked.Title), orUnnamed(parked.ToolCallID)))

	// Killed rather than closed: a client that is still holding an approval
	// does not get to send session/close first.
	first.Kill()
	killedAt := time.Now()

	if sessionID == "" {
		r.skip("inflight.session-id", "in-flight: the agent reported no session id to resume")
		return
	}

	r.probes.holdBack(killedAt)
	before := r.server.LogCount()
	// EmitReplay off: a replay is not what this probe reads, and ACPBackend
	// drops events its buffer cannot hold while the load runs, which must not
	// be the approval.
	resumed := r.resumeSession(sessionID, true, false)
	resumed.CancelDuringLoad = r.probes.Answer == AnswerCancelled
	// =hold answers nothing on the resumed session either, which asks the last
	// question in the set: does an agent wait on an approval forever, or give
	// up? Measurable only since agentprotocol v0.4.0 — before that the held
	// answer stopped this client's own read loop, and an agent that waited
	// looked exactly like a client that had deadlocked.
	resumed.ParkPermission = r.probes.Answer == AnswerParked
	if err := resumed.Start(); err != nil {
		r.skip("inflight.resume:refused", fmt.Sprintf("in-flight: %s does not resume a session with a tool call in flight (%v)", r.harness.Name, err))
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
	// The first load wrote a recording of its own, so the second is a load of
	// a session the agent just wrote, held back like the first (gemini names
	// that recording for the minute of the load).
	r.probes.holdBack(time.Now())
	again := r.resumeSession(sessionID, true, false)
	if err := again.Start(); err != nil {
		r.skip("inflight.resume-twice:refused", fmt.Sprintf("in-flight: %s resumes once but not twice (%v)", r.harness.Name, err))
		return
	}
	if l := again.LoadResult(); l.Reported {
		r.pass("inflight.resume-twice", fmt.Sprintf("in-flight: %s resumed the same session twice (%d replayed the second time)",
			r.harness.Name, l.Replayed))
	} else {
		r.pass("inflight.resume-twice:by-id", fmt.Sprintf("in-flight: %s resumed the same session twice", r.harness.Name))
	}
	again.Close()
}

// reportInFlightResume says what the resumed process was asked, which is the
// whole point of the probe: whether the parked approval comes back, when, and
// whether it is recognisably the same tool call the api already has a row for.
func (r *TestRunner) reportInFlightResume(seen []PermissionObservation, parked PermissionObservation, load LoadResult, requests int) {
	name := r.harness.Name
	if load.Reported {
		r.pass("inflight.resume", fmt.Sprintf("in-flight: %s resumed with a tool call parked (%d replayed, answered=%v, %s)",
			name, load.Replayed, load.Answered, load.Elapsed.Round(time.Millisecond)))
	} else {
		r.pass("inflight.resume:by-id", fmt.Sprintf("in-flight: %s resumed with a tool call parked (by id, nothing replayed to the client)", name))
	}

	if len(seen) == 0 {
		// The agent rebuilt the session and never mentioned the tool call
		// again. Nothing errors, and the work is simply gone.
		r.finding("inflight.reraise:none", fmt.Sprintf("in-flight: %s does not re-raise the parked approval on resume", name))
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
		r.pass("inflight.reraise:"+slug(when)+"/"+slug(correlates), fmt.Sprintf("in-flight: %s re-raised an approval %s for %s, %s, answered %s",
			name, when, orUnnamed(p.Title), correlates, p.Answer))
	}

	// Whether the answer went anywhere is the difference between a live
	// question and history: a tool the agent actually runs produces a request
	// to the model with its result, and a replayed one produces nothing.
	if requests > 0 {
		r.pass("inflight.answer:ran-tool", fmt.Sprintf("in-flight: answering it made %s run the tool (%d model request(s) after the resume)", name, requests))
	} else {
		r.pass("inflight.answer:nothing-sent", fmt.Sprintf("in-flight: %s sent nothing to the model after the answer, so nothing was waiting on it", name))
	}
}

// observeUnansweredApproval watches a resumed session whose re-raised approval
// this client is holding and never answering. An agent that waits goes quiet;
// an agent that gives up either says so in an update or exits.
func (r *TestRunner) observeUnansweredApproval(d *SessionDriver) {
	const watch = 60 * time.Second
	name := r.harness.Name
	before := len(d.Updates())
	deadline := time.Now().Add(watch)
	for time.Now().Before(deadline) {
		if !d.Alive() {
			r.pass("inflight.unanswered:exited", fmt.Sprintf("in-flight: %s exited rather than wait on an approval nobody answered", name))
			return
		}
		if notes := d.Updates(); len(notes) > before {
			var kinds []string
			for _, n := range notes[before:] {
				kinds = append(kinds, n.Kind)
			}
			r.pass("inflight.unanswered:moved-on", fmt.Sprintf("in-flight: %s moved on without its answer, sending %s", name, strings.Join(kinds, ", ")))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.pass("inflight.unanswered:waited", fmt.Sprintf("in-flight: %s waited out %s on an approval nobody answered, saying nothing", name, watch))
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
		r.skip("phase:unsupported", r.harness.Name+" does not support SDK mode")
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
	beltLog := r.hookLogText()
	ptyContent := r.strippedOutput()

	for _, e := range r.eventEntries() {
		label := strings.ToLower(strings.ReplaceAll(e.Tag, "_", "-"))
		found := strings.Contains(beltLog, "["+string(e.Belt)+"]")
		if !found {
			found = strings.Contains(ptyContent, "[belt:hook] "+string(e.Belt)+" done")
		}

		r.reportEvent(phase, label, e.Tag, found, "belt ")
	}

}

// eventEntry pairs the agent's own name for an event with the suite's tag.
type eventEntry struct {
	Belt  harness.HookEvent
	Event string // what this agent calls it
	Tag   string
}

func (r *TestRunner) eventEntries() []eventEntry {
	var result []eventEntry
	for _, e := range r.harness.Defined() {
		result = append(result, eventEntry{Belt: e, Event: e.AgentName(r.harness), Tag: e.Tag()})
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

// findLatestSessionID reads the newest session id out of the directory this
// agent writes transcripts to, or "" when the registry does not say where that
// is — which is most agents, and is the honest answer for them.
func (r *TestRunner) findLatestSessionID(cwd string) string {
	if r.harness.SessionDir == "" {
		return ""
	}
	sessDir := r.expand(strings.ReplaceAll(r.harness.SessionDir,
		"{{.MangledRepoDir}}", strings.ReplaceAll(cwd, "/", "-")))
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), r.harness.SessionExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = strings.TrimSuffix(e.Name(), r.harness.SessionExt)
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

// run runs a command in dir without the caller's GIT_* variables. Inside a
// git hook (the pre-push hook runs go test) GIT_DIR names the repository
// being pushed, and in a linked worktree it is absolute, so the test repo's
// git init, config and commit went to that repository instead: its HEAD
// moved to an "init" commit that deleted every file, its config gained
// user t@t and core.bare = true.
func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = withoutGitEnv(os.Environ())
	return cmd.Run()
}

func withoutGitEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
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
		r.pass(hookID(prefix, label), fmt.Sprintf("%s: %s%s hook fired", phase, prefix, label))
		return
	}
	if (tag == TagPreTool || tag == TagPostTool) && r.server != nil && !r.server.ToolCallServed() {
		r.skip(hookID(prefix, label)+":tool-not-offered", fmt.Sprintf("%s: %s%s hook not checked — the agent was never offered the tool call", phase, prefix, label))
		return
	}
	if reason, ok := r.harness.EventKnownMissing(phase, tag); ok {
		r.skip(hookID(prefix, label)+":known-missing", fmt.Sprintf("%s: %s%s hook not fired — %s", phase, prefix, label, reason))
		return
	}
	r.fail(hookID(prefix, label), fmt.Sprintf("%s: %s%s hook did not fire", phase, prefix, label))
}

// requestHooksFor tells the mock which hooks this agent needs its backend to
// request in this mode (Harness.ServerRequestedHooks), and nothing else.
func (r *TestRunner) requestHooksFor(mode Mode) {
	if r.server != nil {
		r.server.SetRequestedHooks(r.harness.ServerRequestedHooks[mode])
	}
}

// hookLogText is everything the hooks have written so far. Which files those
// are, and what a fired event looks like in them, is the hook source's
// business: the mock hooks append this suite's tag, belt writes its own event
// name in brackets to a log of its own.
func (r *TestRunner) hookLogText() string {
	var b strings.Builder
	if r.hookSource == HooksBelt {
		if data, err := os.ReadFile(filepath.Join(r.home, ".belt", "hooks.log")); err == nil {
			b.Write(data)
		}
	}
	if data, err := os.ReadFile(hookLogPath); err == nil {
		b.Write(data)
	}
	return b.String()
}

// stopsLogged counts the stop hooks that have fired, or -1 when this agent
// has no stop hook and there is therefore no signal to wait for.
func (r *TestRunner) stopsLogged() int {
	if harness.Stop.AgentName(r.harness) == "" {
		return -1
	}
	marker := TagStop
	if r.hookSource == HooksBelt {
		marker = "[" + string(harness.Stop) + "]"
	}
	return strings.Count(r.hookLogText(), marker)
}

// turnMark is the counters taken before a prompt goes in, so waitTurnSettled
// can tell this turn's answer and stop hook from the previous turn's.
type turnMark struct {
	answers int // -1: no answer expected, as for a slash command
	stops   int // -1: this agent has no stop hook
}

// markTurn records where a turn starts. Take it before sending the prompt: a
// fast agent can fire its stop hook while the caller is still setting up.
func (r *TestRunner) markTurn(expectAnswer bool) turnMark {
	m := turnMark{answers: -1, stops: r.stopsLogged()}
	if expectAnswer {
		m.answers = r.server.AnswersServed()
	}
	return m
}

// waitTurnSettled waits for the turn to end: first the model's answer, then
// the agent's own stop hook. A stop hook that fired since the mark is positive
// evidence the turn is over and ends the wait immediately. Without one — the
// agent has no stop hook, or it never fires — it falls back to waiting out a
// quiet window, which costs `quiet` on every turn of every such agent.
func (r *TestRunner) waitTurnSettled(m turnMark, timeout time.Duration) {
	const quiet = 4 * time.Second
	deadline := time.Now().Add(timeout)
	for m.answers >= 0 && r.server.AnswersServed() <= m.answers && time.Now().Before(deadline) {
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
		if m.stops >= 0 && r.stopsLogged() > m.stops {
			return
		}
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
	last, since := session.Len(), time.Now()
	for time.Now().Before(deadline) && time.Since(since) < quiet {
		time.Sleep(100 * time.Millisecond)
		if n := session.Len(); n != last {
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
func (r *TestRunner) mockHookCommand(beltEvent harness.HookEvent) string {
	tag := beltEvent.Tag()
	cmd := fmt.Sprintf("[ -t 0 ] || cat >> %s.stdin; echo %s >> %s", hookLogPath, tag, hookLogPath)
	if tag == TagPrompt {
		cmd += r.promptEcho()
	}
	// belt prints nothing when stdin carries no prompt, so a mock that prints
	// its payload regardless tests a contract belt does not honour. The
	// plugin agents get their prompt from the plugin file rather than from
	// the agent, which is the case that was silently broken: the hook fired,
	// printed the codename, and belt in the same position would have printed
	// nothing. Gating the payload on stdin makes the injection check fail for
	// a plugin that hands the command no prompt.
	if tag == TagPrompt && harness.ContextChannelFor(r.harness.Name, string(beltEvent)) == harness.ContextPlugin {
		cmd = fmt.Sprintf("payload=$(cat); echo %s >> %s; printf '%%s' \"$payload\" >> %s.stdin; case \"$payload\" in *'\"prompt\"'*) %s;; esac",
			tag, hookLogPath, hookLogPath, shellPrint(r.promptPayload()))
	}
	// --probe env: the hook is a child of the agent, so its environment is
	// exactly what the agent exports to a subprocess. Written per agent and
	// phase, appended, because an agent can export different variables in
	// headless and in its TUI.
	if r.probes.DumpEnv {
		cmd = fmt.Sprintf("{ echo '### %s %s'; env; } >> %s 2>/dev/null; ", r.harness.Name, tag, envDumpPath) + cmd
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
