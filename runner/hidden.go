package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/belt-sh/harness-test/server"
	"github.com/inference-sh/agentprotocol/harness"
)

// The surface snapshot reads --help, and --help is what the agent chooses to
// show. cursor-agent acp answers ACP and is not in `cursor-agent help`, so the
// snapshot never saw the one command the registry drives cursor with. This
// probe asks the CLI instead of its help page: it runs a fixed list of entry
// points agents use for protocols, plus every command name registered in the
// installed package, and records the ones the CLI accepts and --help leaves
// out. Runs with --probe hidden, against the mock, so a candidate the agent
// takes as a prompt costs a mock turn and nothing else.

// hiddenCandidates are the protocol and service entry points seen across the
// agents: acp (cursor, kiro, goose, hermes, kimi, omp), --acp (copilot,
// gemini, qwen), --experimental-acp (gemini), app-server and mcp-server
// (codex), --mode rpc (pi), agent stdio (grok), exec (codex, droid), serve
// (goose, opencode), worker (cursor), and the words around them.
var hiddenCandidates = [][]string{
	{"acp"}, {"mcp"}, {"mcp-server"}, {"app-server"}, {"serve"}, {"server"}, {"rpc"},
	{"stdio"}, {"proto"}, {"exec"}, {"worker"}, {"daemon"}, {"agent"}, {"sdk"},
	{"--acp"}, {"--experimental-acp"}, {"--stdio"}, {"--rpc"}, {"--server"},
	{"--mode", "rpc"}, {"--mode", "acp"}, {"--output-format", "acp"},
}

// acpInitialize is the first line an ACP client sends. Any JSON-RPC server
// answers it, with a result or an error, so one line tells an ACP server,
// another JSON-RPC server, and a CLI that read it as a prompt apart.
const acpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false},"clientInfo":{"name":"harness-test","version":"0"}}}` + "\n"

// How a candidate came out.
const (
	hiddenListed   = "listed"    // --help lists it; the snapshot has it
	hiddenRejected = "rejected"  // unknown command, usage error
	hiddenRootHelp = "root-help" // --help printed the top-level page: the word was ignored or taken as an argument
	hiddenHelp     = "help"      // its own help page
	hiddenACP      = "acp"       // answered initialize with an ACP result
	hiddenJSONRPC  = "jsonrpc"   // answered initialize with some other JSON-RPC response
	hiddenJSON     = "json"      // printed JSON on stdout, not a response to the line
	hiddenPrompt   = "prompt"    // the mock received a model request: it started a turn
	hiddenNothing  = "nothing"   // no help, no answer, no turn
)

// hiddenRejectedRe matches the unknown-command errors of commander, yargs,
// cobra, clap, argparse, click/typer and oclif.
var hiddenRejectedRe = regexp.MustCompile(`(?i)unknown (command|argument|option|subcommand|flag)|unrecognized (subcommand|argument|option)|invalid choice|no such (command|option)|unexpected argument|command not found|not a valid command|is not a .*command|did you mean|is invalid|invalid value|allowed choices|possible values`)

type hiddenResult struct {
	args   []string
	mined  bool   // found registered in the package, not in hiddenCandidates
	marker string // in the initialize line, to find it in a prompt
	how    string // one of the hidden* outcomes, from both runs
	detail string
	steps  []string // what each run came to, for the log
}

// probeHidden runs the candidates and records the accepted ones --help does
// not list.
func (r *TestRunner) probeHidden() {
	if r.server == nil {
		return
	}
	saved := r.section
	r.section = "surface"
	defer func() { r.section = saved }()
	fmt.Println("[probe] hidden commands")
	start := time.Now()
	defer func() { fmt.Printf("  [hidden] %s\n", time.Since(start).Round(time.Second)) }()

	bin := r.harness.Binary
	dir := r.workDir()
	help := helpText(bin)
	commands, flags := parseHelp(help, bin)
	fmt.Printf("  [hidden] %s --help lists %d commands: %s\n", bin, len(commands), strings.Join(commands, " "))

	cands := make([]hiddenResult, 0, len(hiddenCandidates))
	fixed := map[string]bool{}
	for _, c := range hiddenCandidates {
		cands = append(cands, hiddenResult{args: c})
		fixed[strings.Join(c, " ")] = true
	}
	mined, roots := minedCommands(bin)
	fmt.Printf("  [hidden] package: %s; registered commands: %s\n", orNone(strings.Join(roots, " ")), orNone(strings.Join(mined, " ")))
	for _, m := range mined {
		if !fixed[m] {
			cands = append(cands, hiddenResult{args: []string{m}, mined: true})
		}
	}

	// --help first, for every candidate at once: a help page starts no turn,
	// and at one agent start each the list takes as long as its slowest.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range cands {
		c := &cands[i]
		if listedInHelp(c.args, commands, flags, help) {
			c.how = hiddenListed
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := r.runHiddenHelp(dir, c.args)
			c.how, c.detail = classifyHelp(out, help, c.args)
			c.steps = append(c.steps, "--help: "+c.how+" "+c.detail)
		}()
	}
	wg.Wait()

	// Then the fixed candidates the help did not reject, run bare with an
	// initialize line on stdin. A name read out of the package is only asked
	// for its help: it could be uninstall. They run at once, so a request at
	// the mock is matched to its candidate afterwards: by the marker in the
	// line, for an agent that read stdin as the prompt, or by the candidate's
	// words, for one that took them as the prompt. stdin stays open, so an
	// agent waiting for the end of its prompt never starts that turn.
	before := r.server.LogCount()
	var bare []*hiddenResult
	for i := range cands {
		c := &cands[i]
		if c.mined || c.how == hiddenListed || c.how == hiddenRejected {
			continue
		}
		bare = append(bare, c)
		wg.Add(1)
		c.marker = fmt.Sprintf("hidden-probe-%d", i)
		go func() {
			defer wg.Done()
			how, detail := r.runHiddenInitialize(dir, c.args, c.marker)
			c.steps = append(c.steps, "bare: "+how+" "+detail)
			switch {
			case how == hiddenACP || how == hiddenJSONRPC:
			case how == hiddenNothing, c.how == hiddenHelp:
				// Its own help page stands: a subcommand run without
				// its arguments can print anything.
				return
			}
			c.how, c.detail = how, detail
		}()
	}
	wg.Wait()
	unmatched := 0
	for _, e := range r.entries()[before:] {
		text := server.LastUserText(e.Body)
		matched := false
		for _, c := range bare {
			if c.how == hiddenACP || c.how == hiddenJSONRPC || c.how == hiddenJSON {
				continue
			}
			// The words as the whole of a JSON string covers a body whose
			// user text the mock does not parse (cursor's protobuf, logged
			// as JSON).
			if strings.Contains(string(e.Body), c.marker) || promptHasWords(text, c.args) ||
				strings.Contains(string(e.Body), `"`+strings.Join(c.args, " ")+`"`) {
				if c.how != hiddenPrompt {
					c.how, c.detail = hiddenPrompt, "the mock received a model request with it as the prompt"
					c.steps = append(c.steps, "mock: "+c.detail)
				}
				matched = true
			}
		}
		if !matched && text != "" {
			unmatched++
		}
	}
	if unmatched > 0 {
		fmt.Printf("  [hidden] %d model request(s) at the mock match no candidate\n", unmatched)
	}

	// A candidate that started a turn left requests and hook events that the
	// phase after this one would read as its own.
	r.server.ClearLog()
	os.Remove(hookLogPath)
	os.Remove(hookLogPath + ".stdin")

	used := driverEntries(r.harness)
	other := map[string][]string{}
	defer func() {
		for _, how := range []string{hiddenRejected, hiddenRootHelp, hiddenNothing} {
			if len(other[how]) > 0 {
				fmt.Printf("  [hidden] registered, --help %s: %s\n", how, strings.Join(other[how], " "))
			}
		}
	}()
	for _, c := range cands {
		label := strings.Join(c.args, " ")
		accepted := false
		switch c.how {
		case hiddenHelp, hiddenACP, hiddenJSONRPC, hiddenJSON:
			accepted = true
		}
		if c.mined && !accepted {
			if c.how != hiddenListed {
				other[c.how] = append(other[c.how], label)
			}
			continue
		}
		fmt.Printf("  [hidden] %-22s %s\n", label, c.how)
		for _, st := range c.steps {
			fmt.Printf("  [hidden]   %s\n", st)
		}
		if !accepted {
			continue
		}
		entry := hiddenEntryID(c.args)
		source := "a fixed candidate"
		if c.mined {
			source = "registered in the package"
		}
		r.pass("hidden."+entry+":"+c.how, fmt.Sprintf("hidden: %s %s works (%s: %s) and --help does not list it (%s)",
			bin, label, c.how, c.detail, source))
		if c.how != hiddenACP && c.how != hiddenJSONRPC {
			continue
		}
		if usesEntry(used, c.args) {
			fmt.Printf("  [hidden] %s %s is the registry's %s entry\n", bin, label, r.harness.DriverKind())
			continue
		}
		r.finding("hidden."+entry+".registry:unused", fmt.Sprintf("hidden: %s %s answers %s, and the registry's %s driver runs %s instead",
			bin, label, c.how, orNone(r.harness.DriverKind()), orNone(describeEntries(used))))
	}
}

// hiddenEntryID is the check id part for a candidate: "acp", "flag.acp",
// "flag.mode-rpc".
func hiddenEntryID(args []string) string {
	id := slug(strings.Join(args, " "))
	if strings.HasPrefix(args[0], "-") {
		id = "flag." + id
	}
	return id
}

// listedInHelp: a command in the command list, a flag in the flag list, and
// a flag with a value when the page names the value as a word of its own
// (pi's --mode lists "rpc" among its values; "--acp" does not name acp).
func listedInHelp(args, commands, flags []string, help string) bool {
	if strings.HasPrefix(args[0], "-") {
		if !containsString(flags, args[0]) {
			return false
		}
		for _, v := range args[1:] {
			if !promptHasWords(help, []string{v}) {
				return false
			}
		}
		return true
	}
	return containsString(commands, args[0])
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (r *TestRunner) runHiddenHelp(dir string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.harness.Binary, append(append([]string{}, args...), "--help")...)
	cmd.Dir = dir
	cmd.Env = append(r.envIn(dir), "NO_COLOR=1", "TERM=dumb", "COLUMNS=400")
	killGroupOnCancel(cmd)
	out, _ := cmd.CombinedOutput()
	return stripANSI(string(out))
}

// classifyHelp reads what `<bin> <candidate> --help` printed. A flag has no
// help page of its own, so for a flag only a rejection counts: hermes prints
// its acp subcommand's help for `--output-format acp --help`.
func classifyHelp(out, rootHelp string, args []string) (how, detail string) {
	if m := hiddenRejectedRe.FindString(out); m != "" {
		return hiddenRejected, fmt.Sprintf("%q", m)
	}
	lines := helpLines(out)
	if len(lines) == 0 {
		return hiddenNothing, "no output"
	}
	root := map[string]bool{}
	for _, l := range helpLines(rootHelp) {
		root[l] = true
	}
	same := 0
	for _, l := range lines {
		if root[l] {
			same++
		}
	}
	if same*5 >= len(lines)*4 {
		return hiddenRootHelp, fmt.Sprintf("%d of %d lines are the top-level help", same, len(lines))
	}
	if strings.HasPrefix(args[0], "-") {
		return hiddenNothing, "a flag's --help is the page of whatever else is on the line"
	}
	if !regexp.MustCompile(`(?i)usage|options:|commands:|flags:|arguments:`).MatchString(out) || !promptHasWords(out, args) {
		return hiddenNothing, fmt.Sprintf("%d lines, not a help page: %q", len(lines), firstLine(out))
	}
	return hiddenHelp, fmt.Sprintf("its own help, %d lines: %q", len(lines), firstLine(out))
}

func helpLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func firstLine(s string) string {
	for _, l := range helpLines(s) {
		if len(l) > 80 {
			l = l[:80]
		}
		return l
	}
	return ""
}

// runHiddenInitialize starts `<bin> <candidate>` with an ACP initialize line
// on stdin and waits up to 15 seconds for an answer or for the process to
// exit.
func (r *TestRunner) runHiddenInitialize(dir string, args []string, marker string) (how, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.harness.Binary, args...)
	cmd.Dir = dir
	cmd.Env = append(r.envIn(dir), "NO_COLOR=1", "TERM=dumb")
	killGroupOnCancel(cmd)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return hiddenNothing, err.Error()
	}
	defer func() {
		stdin.Close()
		cancel()
		cmd.Wait()
	}()
	stdin.Write([]byte(strings.Replace(acpInitialize, `"harness-test"`, `"harness-test `+marker+`"`, 1)))

	type answer struct{ how, detail string }
	answers := make(chan answer, 2)
	var mu sync.Mutex
	var firstJSON, firstText string
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := strings.TrimSpace(stripANSI(sc.Text()))
			if how, ok := classifyInitializeLine(line); ok {
				answers <- answer{how, fmt.Sprintf("%.120s", line)}
				return
			}
			mu.Lock()
			if firstJSON == "" && strings.HasPrefix(line, "{") && json.Valid([]byte(line)) {
				firstJSON = line
			} else if firstText == "" && line != "" {
				firstText = line
			}
			mu.Unlock()
		}
		answers <- answer{}
	}()
	var a answer
	select {
	case a = <-answers:
	case <-ctx.Done():
	}
	if a.how != "" {
		return a.how, a.detail
	}
	mu.Lock()
	defer mu.Unlock()
	if firstJSON != "" {
		return hiddenJSON, fmt.Sprintf("%.120s", firstJSON)
	}
	out := firstText + "\n" + stderr.String()
	if m := hiddenRejectedRe.FindString(out); m != "" {
		return hiddenRejected, fmt.Sprintf("%q", m)
	}
	if ctx.Err() != nil {
		return hiddenNothing, "no answer in 15s"
	}
	return hiddenNothing, fmt.Sprintf("exited: %q", firstLine(out))
}

// lockedBuffer is a strings.Builder the process writes while the probe reads.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return stripANSI(l.b.String())
}

// promptHasWords: the words, as a whole, in a prompt or a help page.
// "serve" is not in "server", "mcp" not in "mcp-server", "acp" not in "--acp".
func promptHasWords(text string, args []string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(strings.Join(args, " ")) + `($|[^A-Za-z0-9_-])`)
	return re.MatchString(text)
}

// classifyInitializeLine reads one stdout line for a response to id 1.
func classifyInitializeLine(line string) (string, bool) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &msg) != nil {
		return "", false
	}
	if strings.TrimSpace(string(msg.ID)) != "1" || (msg.Result == nil && msg.Error == nil) {
		return "", false
	}
	var res struct {
		ProtocolVersion   json.RawMessage `json:"protocolVersion"`
		AgentCapabilities json.RawMessage `json:"agentCapabilities"`
	}
	if msg.Result != nil && json.Unmarshal(msg.Result, &res) == nil && res.ProtocolVersion != nil && res.AgentCapabilities != nil {
		return hiddenACP, true
	}
	return hiddenJSONRPC, true
}

// killGroupOnCancel puts the command in its own process group and kills the
// group on timeout: a node launcher's child would otherwise outlive it and
// hold the output pipe open.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}

// driverEntries is what the registry runs this agent's session with: the ACP
// command's arguments, or the fixed arguments of a native driver
// (agentprotocol's codexapp and pirpc start "app-server" and "--mode rpc").
func driverEntries(h harness.Harness) [][]string {
	var out [][]string
	if len(h.ACPCmd) > 1 {
		out = append(out, h.ACPCmd[1:])
	}
	switch h.DriverKind() {
	case harness.DriverCodex:
		out = append(out, []string{"app-server"})
	case harness.DriverPi:
		out = append(out, []string{"--mode", "rpc"})
	case harness.DriverClaudeCode:
		out = append(out, []string{"--input-format", "stream-json"})
	}
	if len(h.SDKCmd) > 1 {
		out = append(out, h.SDKCmd[1:])
	}
	return out
}

// usesEntry: the candidate's words appear in order in one of the entries
// (grok's "agent stdio" uses agent, droid's "exec --output-format acp" uses
// exec).
func usesEntry(entries [][]string, args []string) bool {
	for _, e := range entries {
		for i := 0; i+len(args) <= len(e); i++ {
			match := true
			for j, a := range args {
				if e[i+j] != a {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

func describeEntries(entries [][]string) string {
	var out []string
	for _, e := range entries {
		out = append(out, strings.Join(e, " "))
	}
	return strings.Join(out, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// commandRegistration matches a subcommand registered in source or a bundle:
// commander and yargs (.command("x"), new Command("x"), command: "x"),
// argparse (add_parser("x")), click and typer (.command("x"),
// .command(name="x")), cobra (Use: "x"). The name ends where a placeholder,
// an alias list or the quote starts.
var commandRegistration = regexp.MustCompile("(?:\\.command\\(\\s*(?:name\\s*=\\s*)?|new Command\\(\\s*|\\bcommand\\s*:\\s*\\[?\\s*|add_parser\\(\\s*|\\bUse:\\s*)[\"'`]([a-z][a-z0-9-]{1,30})[\"'` \\[<|]")

// registrationAnchors are literal prefixes of commandRegistration. The regexp
// runs only where one occurs: over a 200MB compiled binary, a bytes.Index
// scan takes a fraction of a second and the regexp alone took forty.
var registrationAnchors = [][]byte{[]byte("Command("), []byte("command"), []byte("add_parser("), []byte("Use:")}

func registeredCommands(data []byte) []string {
	var out []string
	for _, a := range registrationAnchors {
		for off := 0; ; {
			i := bytes.Index(data[off:], a)
			if i < 0 {
				break
			}
			i += off
			start := max(i-5, 0)
			end := min(i+len(a)+60, len(data))
			// The window starts before the anchor for ".command(" and
			// "new Command(", and the match must cover the anchor itself.
			for _, m := range commandRegistration.FindAllSubmatchIndex(data[start:end], -1) {
				if start+m[0] <= i && i < start+m[1] {
					out = append(out, string(data[start+m[2]:start+m[3]]))
				}
			}
			off = i + len(a)
		}
	}
	return out
}

// minedCommands lists the command names registered in the agent's installed
// code, and the roots it read. Compiled agents (goose, kiro-cli, grok's
// binary) have no registrations left to read; their list is empty and the
// fixed candidates are all that is asked.
func minedCommands(binary string) (names, roots []string) {
	roots = packageRoots(binary)
	seen := map[string]bool{}
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// A package's own dependencies register their own commands.
				if p != root && (d.Name() == "node_modules" || d.Name() == "__pycache__") {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > 300<<20 {
				return nil
			}
			// Sources, and executables big enough to be a runtime with the
			// code compiled in: bun keeps claude's, kilo's and opencode's JS
			// in the binary as text.
			switch filepath.Ext(p) {
			case ".js", ".mjs", ".cjs", ".py":
			default:
				if p != root && (info.Mode()&0111 == 0 || info.Size() < 1<<20) {
					return nil
				}
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for _, n := range registeredCommands(data) {
				seen[n] = true
			}
			return nil
		})
	}
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, roots
}

// packageRoots is where the agent's code lives, the three shapes
// tools/package-sweep.py resolves: an npm package (the binary links into
// node_modules), a launcher beside its bundle (cursor's versions/<build>/),
// and a Python console script (its first import names the package in
// site-packages).
func packageRoots(binary string) []string {
	p, err := exec.LookPath(binary)
	if err != nil {
		return nil
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil
	}
	if i := strings.LastIndex(real, "/node_modules/"); i >= 0 {
		rest := strings.Split(real[i+len("/node_modules/"):], "/")
		n := 1
		if strings.HasPrefix(rest[0], "@") && len(rest) > 1 {
			n = 2
		}
		name := strings.Join(rest[:n], "/")
		pkg := real[:i] + "/node_modules/" + name
		roots := []string{pkg, real}
		// The code can be in a platform package (@github/copilot-linux-x64),
		// nested in the package or hoisted beside it.
		for _, pat := range []string{pkg + "/node_modules/" + name + "-*", pkg + "-*"} {
			m, _ := filepath.Glob(pat)
			roots = append(roots, m...)
		}
		return roots
	}
	roots := []string{real}
	parent := filepath.Dir(real)
	// ~/.local/bin holds every agent's shim; reading it would report one
	// agent's registrations under another's name.
	shared := false
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if rd, err := filepath.EvalSymlinks(d); err == nil && rd == parent {
			shared = true
		}
	}
	if !shared {
		roots = append(roots, parent)
	}
	if pkg := pythonPackage(real); pkg != "" {
		roots = append(roots, pkg)
	}
	return roots
}

var (
	shebangPython = regexp.MustCompile(`^#!\s*(\S*python[0-9.]*)`)
	pythonImport  = regexp.MustCompile(`(?m)^(?:from|import)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

// pythonPackage reads a console script: its interpreter's site-packages and
// the package its entry point imports.
func pythonPackage(script string) string {
	data, err := os.ReadFile(script)
	if err != nil || len(data) > 64<<10 {
		return ""
	}
	m := shebangPython.FindSubmatch(data)
	if m == nil {
		return ""
	}
	env := filepath.Dir(filepath.Dir(string(m[1])))
	for _, imp := range pythonImport.FindAllSubmatch(data, -1) {
		mod := string(imp[1])
		if mod == "sys" || mod == "re" || mod == "os" {
			continue
		}
		dirs, _ := filepath.Glob(filepath.Join(env, "lib", "python*", "site-packages", mod))
		if len(dirs) > 0 {
			return dirs[0]
		}
	}
	return ""
}
