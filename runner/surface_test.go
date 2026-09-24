package runner

import (
	"reflect"
	"testing"
)

// One excerpt per help format the agents print. The parse has to find the
// commands in each and nothing else: a wrapped description line is not a
// command, and neither is the program name yargs repeats.
func TestParseHelpFormats(t *testing.T) {
	cases := []struct {
		name, bin, help string
		commands        []string
	}{
		{"commander, wrapped descriptions", "droid", `Usage: droid [options] [command] [prompt...]

Options:
  -v, --version                      output the version number

Commands:
  resume [options] [sessionId]        Resume a session (shows a session picker
                                      without an id)
  search|find [options] <query>       Search across local sessions (messages,
                                      documents, tool results)
  help [command]                      display help for command

Examples:
  droid exec "fix the tests"
`, []string{"help", "resume", "search"}},
		{"yargs, program name first", "kilo", `Commands:
  kilo completion          generate shell completion script
  kilo acp                 start ACP (Agent Client Protocol) server
  kilo [project]           start kilo tui                     [default]

Options:
  -h, --help  show help
`, []string{"acp", "completion"}},
		{"argparse choices", "hermes", `usage: hermes [-h] [--version] {chat,model,acp} ...

positional arguments:
  {chat,model,acp}
                        Command to run
    chat                Interactive chat with the agent

options:
  --resume SESSION      resume
`, []string{"acp", "chat", "model"}},
		{"uppercase heading", "omp", `COMMANDS
  acp            Run omp as an ACP server over stdio
  agents         Manage bundled task agents

Useful Commands:
  omp agents unpack           - Export bundled subagents
`, []string{"acp", "agents"}},
		{"clap", "kiro-cli", `USAGE:
    kiro-cli [OPTIONS] [SUBCOMMAND]

Commands:
  chat          AI assistant in your terminal
  acp           Agent Client Protocol (ACP)

Options:
      --help-all
          Print help for all subcommands
`, []string{"acp", "chat"}},
	}
	for _, c := range cases {
		got, _ := parseHelp(c.help, c.bin)
		if !reflect.DeepEqual(got, c.commands) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.commands)
		}
	}
}

func TestParseHelpFlags(t *testing.T) {
	_, flags := parseHelp(`Options:
  -p, --print               print and exit
      --acp                 start in ACP mode
  --model <model>           model
  [--no-session]
`, "x")
	want := []string{"--acp", "--model", "--no-session", "--print"}
	if !reflect.DeepEqual(flags, want) {
		t.Errorf("got %v, want %v", flags, want)
	}
}
