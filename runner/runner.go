package runner

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
	Harness  string
	Passed   int
	Failed   int
	Skipped  int
	Duration time.Duration
}

type Mode int

const (
	ModeBoth Mode = iota
	ModeHeadless
	ModeInteractive
	ModeACP
)

type HookSource int

const (
	HooksMock HookSource = iota // test-generated hooks that log to file
	HooksBelt                   // real belt hooks via Install(), with logging shim
)

type Runner struct {
	harness      harness.Harness
	server       *server.MockServer
	baseURL      string
	home         string
	repoDir      string
	injectCode   string
	tokenHash16  string
	sessionID    string
	startTime    time.Time
	savedEnv     []string
	mode         Mode
	hookSource   HookSource
	failed       bool
	result       Result
	lastOutput   string
}

var originalHome = os.Getenv("HOME")

func New(h harness.Harness, srv *server.MockServer, baseURL string) *Runner {
	return &Runner{
		harness:    h,
		server:     srv,
		baseURL:    baseURL,
		mode:       ModeBoth,
		hookSource: HooksMock,
		result:     Result{Harness: h.Name},
	}
}

func (r *Runner) SetHookSource(s HookSource) {
	r.hookSource = s
}

func (r *Runner) SetMode(m string) {
	switch m {
	case "headless":
		r.mode = ModeHeadless
	case "interactive":
		r.mode = ModeInteractive
	case "acp":
		r.mode = ModeACP
	default:
		r.mode = ModeBoth
	}
}

func (r *Runner) pass(msg string) {
	r.result.Passed++
	fmt.Printf("  ✓ %s\n", msg)
}

func (r *Runner) fail(msg string) {
	r.result.Failed++
	r.failed = true
	fmt.Fprintf(os.Stderr, "  ✗ %s\n", msg)
}

func (r *Runner) skip(msg string) {
	r.result.Skipped++
	fmt.Printf("  ○ %s\n", msg)
}

func (r *Runner) Run() Result {
	r.startTime = time.Now()
	r.savedEnv = os.Environ()
	fmt.Printf("=== %s ===\n", r.harness.Name)

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

	if r.mode == ModeBoth || r.mode == ModeHeadless {
		if r.harness.HooksInHeadless {
			r.prepareToolCall()
			r.runHeadless()
			r.runChecks("headless")
		} else {
			r.skip(r.harness.Name + " does not fire hooks in headless mode")
		}
	}
	if r.mode == ModeBoth || r.mode == ModeInteractive {
		os.Remove("/tmp/belt-hook-events.log")
		r.server.ClearLog()
		r.prepareToolCall()
		r.runInteractive()
		r.runChecks("interactive")
	}
	if r.mode == ModeACP {
		os.Remove("/tmp/belt-hook-events.log")
		r.server.ClearLog()
		r.prepareToolCall()
		r.runACP()
		r.runChecks("acp")
	}

	return r.finish()
}

func (r *Runner) prepareToolCall() {
	hasToolHooks := r.harness.Events.PreToolUse != "" || r.harness.Events.PostToolUse != ""
	if r.server != nil && hasToolHooks {
		r.server.PrepareToolCall(r.harness.ToolCallName, r.expand(r.harness.ToolCallArgs), r.harness.ToolCallPath)
	}
}

func (r *Runner) finish() Result {
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

func (r *Runner) setupHome() {
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

func (r *Runner) checkBinary() {
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
		for _, postCmd := range r.harness.PostInstall {
			cmd := exec.Command(postCmd[0], postCmd[1:]...)
			cmd.Env = os.Environ()
			cmd.Run()
		}
		return
	}
	r.pass(r.harness.Binary + " found")
}

func (r *Runner) setupEndpoint() {
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

func (r *Runner) writeToHome(relPath string, content string) {
	path := filepath.Join(r.home, relPath)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(content), 0644)
	if r.harness.NeedsGitRepo {
		projPath := filepath.Join(r.ensureGitRepo(), relPath)
		os.MkdirAll(filepath.Dir(projPath), 0755)
		os.WriteFile(projPath, []byte(content), 0644)
	}
}

func (r *Runner) writeConfigFiles() {
	if len(r.harness.ConfigFiles) == 0 {
		return
	}
	for _, cf := range r.harness.ConfigFiles {
		r.writeToHome(r.expand(cf.Path), r.expand(cf.Content))
	}
}

func (r *Runner) writeHooks() {
	fmt.Println("[phase 3] hooks")

	hookDir := filepath.Join(r.home, r.harness.HookConfigDir)
	os.MkdirAll(hookDir, 0755)

	logPath := "/tmp/belt-hook-events.log"
	os.Remove(logPath)
	r.injectCode = fmt.Sprintf("%s-%d", strings.ToUpper(r.harness.Name), time.Now().UnixMilli())

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

	case harness.JSONCopilot:
		filename = "belt.json"
		scriptDir := filepath.Join(r.home, ".copilot", "test-hooks")
		os.MkdirAll(scriptDir, 0755)
		promptScript := filepath.Join(scriptDir, "prompt.sh")
		os.WriteFile(promptScript, []byte(fmt.Sprintf("#!/bin/sh\necho PROMPT >> %s\nprintf '{\"additionalContext\": \"The project codename is %s.\"}\\n'\n", logPath, r.injectCode)), 0755)
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
			if e.Tag == "PROMPT" {
				body += fmt.Sprintf("printf '{\"context\": \"The project codename is %s.\"}\\n'\n", r.injectCode)
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
			if e.Tag == "PROMPT" {
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
			if e.Tag == "PROMPT" {
				cmd += fmt.Sprintf(" && echo 'The project codename is %s.'", r.injectCode)
			}
			if e.Tag == "PRE_TOOL" || e.Tag == "POST_TOOL" {
				tomlHooks += fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"\nmatcher = \"%s\"\ncommand = \"%s\"\ntimeout = 10\n", e.Event, r.toolMatcher(), cmd)
			} else {
				tomlHooks += fmt.Sprintf("\n[[hooks]]\nevent = \"%s\"\ncommand = \"%s\"\ntimeout = 10\n", e.Event, cmd)
			}
		}
		content = string(existing) + tomlHooks

	case harness.TSPlugin:
		filename = "belt-test.ts"
		var hookParts []string
		startLine := ""
		for _, e := range r.eventEntries() {
			switch e.Tag {
			case "SESSION_START":
				startLine = fmt.Sprintf("  require(\"fs\").appendFileSync(\"%s\", \"SESSION_START\\n\");\n", logPath)
			case "PROMPT":
				hookParts = append(hookParts, fmt.Sprintf(`    "%s": async (_input: any, output: any) => {
      require("fs").appendFileSync("%s", "PROMPT\n");
      output.system.push("The project codename is %s.");
    }`, e.Event, logPath, r.injectCode))
			case "STOP":
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

func (r *Runner) writeBeltHooks() {
	fmt.Println("[phase 3] hooks (belt)")

	os.Setenv("BELT_HOOK_DEBUG", "1")
	os.Setenv("BELT_HOOK_DEBUG_LOG", "/tmp/belt-hook-events.log")
	os.Setenv("BELT_NO_HOOKS", "0")
	os.Remove("/tmp/belt-hook-events.log")
	os.MkdirAll(filepath.Join(r.home, ".belt"), 0755)

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
func (r *Runner) seedWrapperConfig() {
	wrapper := r.expand(r.harness.HookWrapper)
	// The wrapper is a format string like `{"permissions":...,"hooks":%s}`.
	// Replace the %s with an empty hooks object to get the base config.
	base := fmt.Sprintf(wrapper, "{}")
	path := filepath.Join(r.home, r.harness.HookConfigDir, r.harness.HookFileName)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(base), 0644)
}

func (r *Runner) setupSkills() {
	if r.harness.SkillsDir == "" {
		return
	}
	fmt.Println("[phase 4] skills")
	os.MkdirAll(filepath.Join(r.home, r.harness.SkillsDir), 0755)
	r.pass("skills directory created")
}

func (r *Runner) ensureGitRepo() string {
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

func (r *Runner) workDir() string {
	if r.harness.NeedsGitRepo {
		return r.ensureGitRepo()
	}
	return r.home
}

func (r *Runner) runHeadless() {
	if len(r.harness.HeadlessCmd) == 0 {
		r.skip("no headless command configured")
		return
	}

	fmt.Println("[phase 5] headless prompt")

	dir := r.workDir()
	prompt := "What is the project codename? Reply ONLY the codename."

	var args []string
	args = append(args, r.harness.HeadlessCmd[1:]...)
	if !r.harness.PromptViaStdin {
		args = append(args, prompt)
	}
	for _, a := range r.harness.HeadlessModelArgs {
		args = append(args, r.expand(a))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.harness.HeadlessCmd[0], args...)
	cmd.Env = os.Environ()
	cmd.Dir = dir
	if r.harness.PromptViaStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

	out, err := cmd.CombinedOutput()
	r.lastOutput = string(out)
	if os.Getenv("HARNESS_DEBUG") != "" {
		if len(out) > 0 {
			fmt.Printf("    [debug] output:\n%s\n", r.lastOutput)
		} else {
			fmt.Printf("    [debug] no output, err=%v\n", err)
		}
	}
	if err != nil && len(out) > 0 {
		r.pass(fmt.Sprintf("headless produced output (%d bytes, exit: %v)", len(out), err))
	} else if err != nil {
		r.fail("headless: " + err.Error())
	} else if len(out) > 0 {
		r.pass(fmt.Sprintf("headless produced output (%d bytes)", len(out)))
	} else {
		r.fail("headless produced no output")
	}

	if len(r.harness.PostHeadlessCmd) > 0 {
		var parsed struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(out, &parsed) == nil && parsed.SessionID != "" {
			r.sessionID = parsed.SessionID
		}
		if r.sessionID == "" {
			r.sessionID = r.findLatestSessionID(dir)
		}
	}

	if r.harness.Events.Stop != "" {
		time.Sleep(3 * time.Second)
	}

	for _, step := range r.harness.PostHeadlessCmd {
		r.runPostHeadless(dir, step)
	}
}

func (r *Runner) runPostHeadless(dir string, rawArgs []string) {
	var args []string
	for _, a := range rawArgs {
		expanded := r.expand(a)
		if expanded == "" {
			r.skip("post-headless skipped (missing template variable)")
			return
		}
		args = append(args, expanded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.harness.HeadlessCmd[0], args...)
	cmd.Env = os.Environ()
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] post-headless output: %s\n", string(out))
	}
	time.Sleep(2 * time.Second)
}

func (r *Runner) sendLine(session *PTYSession, text string) {
	if r.harness.SlowInput {
		session.SendLineDelayed(text, 5*time.Millisecond)
	} else {
		session.SendLine(text)
	}
}

func (r *Runner) runInteractive() {
	if len(r.harness.InteractiveCmd) == 0 || !r.harness.HooksInInteractive {
		return
	}

	fmt.Println("[phase 6] interactive (PTY) mode")

	dir := r.workDir()

	var iargs []string
	iargs = append(iargs, r.harness.InteractiveCmd[1:]...)
	for _, a := range r.harness.InteractiveArgs {
		iargs = append(iargs, r.expand(a))
	}

	session, err := StartPTY(r.harness.InteractiveCmd[0], iargs, dir, os.Environ())
	if err != nil {
		r.fail("PTY start: " + err.Error())
		return
	}
	defer session.Close()

	time.Sleep(3 * time.Second)
	if len(r.harness.OnboardingDismiss) > 0 {
		for i := 0; i < 15; i++ {
			out := session.Output()
			dismissed := false
			for _, action := range r.harness.OnboardingDismiss {
				if !strings.Contains(out, action.Pattern) {
					continue
				}
				if action.SendUp {
					session.SendUp()
					time.Sleep(200 * time.Millisecond)
				}
				session.SendLine("")
				time.Sleep(2 * time.Second)
				dismissed = true
				break
			}
			if dismissed {
				continue
			}
			if len(out) > 200 {
				break
			}
			time.Sleep(1 * time.Second)
		}
	} else {
		_, _ = session.WaitForAny([]string{">", "❯", "$", "?", "Type your message"}, 15*time.Second)
	}
	r.pass("TUI started")

	if r.harness.InteractivePromptInArgs {
		session.WaitForAny([]string{"mock", "hello", "Hello", "codename", "server", "build", "Changes", "Duration", "Resume"}, 60*time.Second)
		time.Sleep(3 * time.Second)
	} else {
		r.sendLine(session, "What is the project codename? Reply ONLY the codename.")
		session.WaitForAny([]string{"mock", "hello", "Hello", "codename", "server"}, 30*time.Second)
		time.Sleep(3 * time.Second)
	}
	if r.harness.CompactCommand != "" {
		r.sendLine(session, "Tell me more about the project.")
		session.WaitForAny([]string{"mock", "hello", "Hello", "server"}, 30*time.Second)
		time.Sleep(2 * time.Second)
		r.sendLine(session, r.harness.CompactCommand)
		session.WaitForAny([]string{"compact", "Compact", "compress", "Compress", "summar"}, 15*time.Second)
		time.Sleep(3 * time.Second)
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
	if os.Getenv("HARNESS_DEBUG") != "" {
		stripped := stripANSI(r.lastOutput)
		if len(stripped) > 500 {
			stripped = stripped[len(stripped)-500:]
		}
		fmt.Printf("    [debug] PTY output (%d bytes):\n%s\n", len(r.lastOutput), stripped)
	}

	r.pass("interactive session completed")
}

func (r *Runner) writeACPConfig(dir string) {
	switch r.harness.Name {
	case "opencode":
		// opencode ACP defaults to its built-in provider. Override HOME and write
		// config that points the openai provider at our mock server.
		if r.harness.PreserveHome {
			tmpHome, _ := os.MkdirTemp("", "acp-opencode-")
			os.Setenv("HOME", tmpHome)
			r.home = tmpHome
		}
		cfgDir := filepath.Join(r.home, ".config", "opencode")
		os.MkdirAll(cfgDir, 0755)
		model := strings.TrimPrefix(r.harness.DefaultModel, "openai/")
		cfg := fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "openai": {
      "apiKey": "mock-key",
      "baseURL": "%s/v1",
      "models": {
        "%s": {}
      }
    }
  }
}`, r.baseURL, model)
		os.WriteFile(filepath.Join(cfgDir, "opencode.json"), []byte(cfg), 0644)

		// Also write auth.json so opencode sees the openai credential
		authDir := filepath.Join(r.home, ".local", "share", "opencode")
		os.MkdirAll(authDir, 0755)
		auth := `{"openai":{"apiKey":"mock-key"}}`
		os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(auth), 0644)
	}
}

func (r *Runner) runACP() {
	if len(r.harness.ACPCmd) == 0 || !r.harness.HooksInACP {
		r.skip(r.harness.Name + " does not support ACP mode")
		return
	}

	fmt.Println("[phase 7] ACP (JSON-RPC over stdio)")

	dir := r.workDir()

	var args []string
	args = append(args, r.harness.ACPCmd[1:]...)
	for _, a := range r.harness.ACPArgs {
		args = append(args, r.expand(a))
	}

	// Write ACP-specific config files (some agents need config in the project dir)
	r.writeACPConfig(dir)

	driver := NewACPDriver(r.harness.ACPCmd[0], args, dir, os.Environ())
	if err := driver.Start(); err != nil {
		r.fail("ACP start: " + err.Error())
		return
	}
	defer driver.Close()
	r.pass("ACP session started")

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

	if r.harness.CompactCommand != "" {
		driver.SendCommand(r.harness.CompactCommand)
		time.Sleep(3 * time.Second)
	}

	r.lastOutput = driver.Output()
	if os.Getenv("HARNESS_DEBUG") != "" {
		fmt.Printf("    [debug] ACP output (%d bytes):\n%s\n", len(r.lastOutput), r.lastOutput)
	}

	r.pass("ACP session completed")
}

func (r *Runner) checkHookEvents(phase string) {
	fmt.Printf("[phase] hook events (%s)\n", phase)

	if r.hookSource == HooksBelt {
		r.checkBeltHookEvents(phase)
		return
	}

	logContent := ""
	if data, err := os.ReadFile("/tmp/belt-hook-events.log"); err == nil {
		logContent = string(data)
	}

	ptyContent := stripANSI(r.lastOutput)

	for _, e := range r.eventEntries() {
		label := strings.ToLower(strings.ReplaceAll(e.Tag, "_", "-"))
		found := strings.Contains(logContent, e.Tag) ||
			strings.Contains(ptyContent, "hook: "+e.Event)
		if found {
			r.pass(fmt.Sprintf("%s: %s hook fired", phase, label))
		} else {
			r.skip(fmt.Sprintf("%s: %s hook not fired", phase, label))
		}
	}

	if r.server.LogCount() > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, r.server.LogCount()))
	}
}

// beltEventNames maps our internal tag names to belt plugin hook event names.
var beltEventNames = map[string]string{
	"SESSION_START": "session-start",
	"PROMPT":        "user-prompt-submit",
	"PRE_TOOL":      "pre-tool-use",
	"POST_TOOL":     "post-tool-use",
	"STOP":          "stop",
	"PRE_COMPACT":   "pre-compact",
}

func (r *Runner) checkBeltHookEvents(phase string) {
	beltLog := ""
	if data, err := os.ReadFile(filepath.Join(r.home, ".belt", "hooks.log")); err == nil {
		beltLog = string(data)
	}
	if data, err := os.ReadFile("/tmp/belt-hook-events.log"); err == nil {
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

		if found {
			r.pass(fmt.Sprintf("%s: belt %s hook fired", phase, label))
		} else {
			r.skip(fmt.Sprintf("%s: belt %s hook not fired", phase, label))
		}
	}

	if r.server.LogCount() > 0 {
		r.pass(fmt.Sprintf("%s: mock server received %d request(s)", phase, r.server.LogCount()))
	}
}

type eventEntry struct {
	Event string
	Tag   string
}

func (r *Runner) eventEntries() []eventEntry {
	evts := r.harness.Events
	all := []eventEntry{
		{evts.SessionStart, "SESSION_START"},
		{evts.PromptSubmit, "PROMPT"},
		{evts.PreToolUse, "PRE_TOOL"},
		{evts.PostToolUse, "POST_TOOL"},
		{evts.Stop, "STOP"},
		{evts.PreCompact, "PRE_COMPACT"},
	}
	var result []eventEntry
	for _, e := range all {
		if e.Event != "" {
			result = append(result, e)
		}
	}
	return result
}

func (r *Runner) toolMatcher() string {
	if r.harness.HookToolMatcher != "" {
		return r.harness.HookToolMatcher
	}
	if r.harness.ToolCallName != "" {
		return r.harness.ToolCallName
	}
	return server.DefaultToolName
}

func (r *Runner) buildNestedHooksJSON(logPath string) string {
	entries := r.eventEntries()
	parts := []string{}
	for _, e := range entries {
		cmd := fmt.Sprintf("echo %s >> %s", e.Tag, logPath)
		if e.Tag == "PROMPT" {
			cmd += fmt.Sprintf(" && echo 'The project codename is %s.'", r.injectCode)
		}
		hook := fmt.Sprintf(`{"type":"command","command":"%s","timeout":5}`, cmd)
		if e.Tag == "PRE_TOOL" || e.Tag == "POST_TOOL" {
			parts = append(parts, fmt.Sprintf(`"%s":[{"matcher":"%s","hooks":[%s]}]`, e.Event, r.toolMatcher(), hook))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s":[{"hooks":[%s]}]`, e.Event, hook))
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (r *Runner) expand(tmpl string) string {
	s := strings.ReplaceAll(tmpl, "{{.BaseURL}}", r.baseURL)
	s = strings.ReplaceAll(s, "{{.Model}}", r.harness.DefaultModel)
	s = strings.ReplaceAll(s, "{{.APIKey}}", "mock-key")
	s = strings.ReplaceAll(s, "{{.HomeDir}}", r.home)
	if r.repoDir != "" {
		s = strings.ReplaceAll(s, "{{.RepoDir}}", r.repoDir)
	} else {
		s = strings.ReplaceAll(s, "{{.RepoDir}}", filepath.Join(r.home, "test-repo"))
	}
	if strings.Contains(s, "{{.TokenHash16}}") {
		if r.tokenHash16 == "" {
			input := fmt.Sprintf(`{"oauthHost":"https://auth.kimi.com","baseUrl":"%s/coding/v1"}`, r.baseURL)
			hash := sha256.Sum256([]byte(input))
			r.tokenHash16 = hex.EncodeToString(hash[:])[:16]
		}
		s = strings.ReplaceAll(s, "{{.TokenHash16}}", r.tokenHash16)
	}
	s = strings.ReplaceAll(s, "{{.SessionID}}", r.sessionID)
	return s
}

func (r *Runner) findLatestSessionID(cwd string) string {
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

func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run()
}
