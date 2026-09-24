package runner

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/inference-sh/agentprotocol/harness"
)

// Surface is what an agent's CLI says it can do: its version, and the
// commands and long flags its top-level --help lists.
//
// The nightly compares the commands and flags with a committed snapshot, so a
// capability an agent gains between runs (cursor-agent growing an acp
// subcommand) shows up the night it ships instead of whenever someone next
// reads the changelog. The version is reported beside it and never compared:
// a release that changes nothing on the surface is not news.
type Surface struct {
	Version  string
	Help     string // the raw --help output, for when the parse misses something
	Commands []string
	Flags    []string
}

// Text is the snapshot form: one name per line under a heading, sorted, and
// nothing that changes with a release that did not change the surface.
func (s Surface) Text(name string) string {
	var b strings.Builder
	b.WriteString("# " + name + ": top-level commands and long flags from --help (regenerate: see README, CI)\n")
	b.WriteString("[commands]\n")
	for _, c := range s.Commands {
		b.WriteString(c + "\n")
	}
	b.WriteString("[flags]\n")
	for _, f := range s.Flags {
		b.WriteString(f + "\n")
	}
	return b.String()
}

// ReadSurface installs the agent the way a test run does and reads its
// --help. ok is false when the agent could not be installed or run; the
// reason has been printed.
func ReadSurface(h harness.Harness) (s Surface, ok bool) {
	r := New(h, nil, "")
	r.startTime = time.Now()
	r.savedEnv = os.Environ()
	defer r.finish()
	r.setupHome()
	r.checkBinary()
	if r.failed {
		return s, false
	}
	s.Version = r.result.Version
	s.Help = helpText(h.Binary)
	if strings.TrimSpace(s.Help) == "" {
		r.fail("surface", h.Binary+" --help printed nothing")
		return s, false
	}
	s.Commands, s.Flags = parseHelp(s.Help, h.Binary)
	return s, true
}

func helpText(bin string) string {
	help := runHelp(bin, "--help")
	// kiro-cli's --help is a short "popular subcommands" box that ends by
	// pointing at --help-all for the rest.
	if strings.Contains(help, "--help-all") {
		if all := runHelp(bin, "--help-all"); strings.TrimSpace(all) != "" {
			return all
		}
	}
	return help
}

func runHelp(bin, flag string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, flag)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "COLUMNS=400")
	out, _ := cmd.CombinedOutput()
	return stripANSI(string(out))
}

var (
	// A section heading: "Commands:", "Available Commands:", "COMMANDS",
	// "positional arguments:", "Options:", "Flags:", ...
	helpHeading = regexp.MustCompile(`^([A-Za-z][A-Za-z /-]*?):?\s*$`)
	// A command entry: the name first, then an alias, a placeholder, a
	// description or nothing.
	helpCommand = regexp.MustCompile(`^([a-z][a-z0-9_:-]*)(?:[ ,|]|$)`)
	helpFlag    = regexp.MustCompile(`(?:^|[\s,\[|(])(--[a-zA-Z0-9][a-zA-Z0-9_-]*)`)
	// argparse lists its subcommands as "{chat,model,...}".
	helpChoices = regexp.MustCompile(`^\{([a-z0-9_,-]+)\}`)
	helpProgram = regexp.MustCompile(`(?i)usage:\s+(\S+)`)
)

// parseHelp reads the commands and long flags out of a --help page.
//
// The agents' CLI frameworks (commander, yargs, clap, cobra, argparse, oclif,
// kiro's boxed help) all list commands one per line under a heading with
// "command" in it, at one indentation, with wrapped descriptions indented
// further. So a command is the first word of a line at the section's first
// indentation. yargs repeats the program name before each command ("gemini
// mcp"), and argparse lists them as a brace set. Long flags are taken from
// anywhere on the page: they are unambiguous, and a flag can be the only sign
// of a mode (qwen's --acp).
func parseHelp(help, bin string) (commands, flags []string) {
	cmdSet, flagSet := map[string]bool{}, map[string]bool{}
	programs := map[string]bool{bin: true}
	if m := helpProgram.FindStringSubmatch(help); m != nil {
		programs[strings.TrimSuffix(m[1], ":")] = true
	}
	inCommands, indent := false, -1
	for _, raw := range strings.Split(help, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		// Boxed help draws a border around each section; the text inside is
		// an ordinary help line.
		boxed := strings.HasPrefix(strings.TrimSpace(line), "│")
		if boxed {
			line = strings.TrimRight(strings.TrimPrefix(strings.TrimSpace(line), "│"), " │")
			line = " " + line
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "╭") || strings.HasPrefix(t, "╰") {
			heading := strings.ToLower(strings.Trim(t, "╭╮╰╯─ "))
			if heading != "" {
				inCommands, indent = strings.Contains(heading, "command"), -1
			}
			continue
		}
		for _, m := range helpFlag.FindAllStringSubmatch(line, -1) {
			flagSet[m[1]] = true
		}
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		if lead == 0 && !boxed {
			if m := helpHeading.FindStringSubmatch(line); m != nil {
				h := strings.ToLower(m[1])
				inCommands = strings.Contains(h, "command") || strings.Contains(h, "positional arguments")
				indent = -1
			} else {
				inCommands = false
			}
			continue
		}
		if !inCommands {
			continue
		}
		body := strings.TrimSpace(line)
		if m := helpChoices.FindStringSubmatch(body); m != nil {
			for _, c := range strings.Split(m[1], ",") {
				cmdSet[c] = true
			}
			// The set is the whole list; the lines under it repeat it
			// with descriptions, at an indentation of their own.
			inCommands = false
			continue
		}
		if indent < 0 {
			indent = lead
		}
		if lead != indent {
			continue
		}
		fields := strings.Fields(body)
		if len(fields) > 1 && programs[fields[0]] {
			body = strings.Join(fields[1:], " ")
		}
		if m := helpCommand.FindStringSubmatch(body); m != nil {
			cmdSet[m[1]] = true
		}
	}
	for c := range cmdSet {
		commands = append(commands, c)
	}
	for f := range flagSet {
		flags = append(flags, f)
	}
	sort.Strings(commands)
	sort.Strings(flags)
	return commands, flags
}
