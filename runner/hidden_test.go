package runner

import (
	"reflect"
	"sort"
	"testing"

	"github.com/inference-sh/agentprotocol/harness"
)

// One registration per CLI framework the agents are built on, as it appears
// in a minified bundle or a Python source file.
func TestRegisteredCommands(t *testing.T) {
	src := []byte(`x.command("acp").description("ACP");y=new Command("worker-server");` +
		`{command:"serve [port]",describe:"s"};{command: ["mcp <cmd>","m"]};` +
		`sub.add_parser('chat', help='c');@app.command(name="export")` + "\n" +
		`&cobra.Command{Use: "proto [flags]"};a.command(variable);commandLine("x")`)
	got := registeredCommands(src)
	sort.Strings(got)
	want := []string{"acp", "chat", "export", "mcp", "proto", "serve", "worker-server"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestClassifyHelp(t *testing.T) {
	root := "Usage: agent [options] [command] [prompt...]\n\nOptions:\n  --print  print\n\nCommands:\n  login  log in\n"
	for _, c := range []struct {
		name, out string
		args      []string
		want      string
	}{
		{"commander unknown", "error: unknown command 'rpc'", []string{"rpc"}, hiddenRejected},
		{"clap", "error: unrecognized subcommand 'stdio'\n\nUsage: goose [COMMAND]", []string{"stdio"}, hiddenRejected},
		{"choices", "error: option '--mode <mode>' argument 'rpc' is invalid. Allowed choices are plan, ask.", []string{"--mode", "rpc"}, hiddenRejected},
		{"the word taken as a prompt", root, []string{"serve"}, hiddenRootHelp},
		{"its own page", "Usage: agent get-channel [options]\n\nPrint the channel\n", []string{"get-channel"}, hiddenHelp},
		{"another command's page", "Usage: agent login [options]\n\nLog in\n", []string{"daemon"}, hiddenNothing},
		{"a flag", "usage: hermes acp [-h] [--check]\n", []string{"--output-format", "acp"}, hiddenNothing},
	} {
		if got, detail := classifyHelp(c.out, root, c.args); got != c.want {
			t.Errorf("%s: got %s (%s), want %s", c.name, got, detail, c.want)
		}
	}
}

func TestClassifyInitializeLine(t *testing.T) {
	for line, want := range map[string]string{
		`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}}`: hiddenACP,
		`{"id":1,"result":{"userAgent":"codex/0.1"}}`:                                                      hiddenJSONRPC,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no"}}`:                                  hiddenJSONRPC,
		`{"jsonrpc":"2.0","method":"session/update","params":{}}`:                                          "",
		`{"type":"response","success":false}`:                                                              "",
		`Welcome to agent`:                                                                                 "",
	} {
		if got, _ := classifyInitializeLine(line); got != want {
			t.Errorf("%s: got %q, want %q", line, got, want)
		}
	}
}

func TestListedInHelp(t *testing.T) {
	help := "Options:\n  --mode <mode>  text, json or rpc\n  --acp  ACP\n"
	cmds, flags := []string{"acp", "mcp"}, []string{"--acp", "--mode"}
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"acp"}, true},
		{[]string{"serve"}, false},
		{[]string{"--acp"}, true},
		{[]string{"--mode", "rpc"}, true},
		{[]string{"--mode", "acp"}, false},
		{[]string{"--stdio"}, false},
	} {
		if got := listedInHelp(c.args, cmds, flags, help); got != c.want {
			t.Errorf("%v: got %v, want %v", c.args, got, c.want)
		}
	}
}

// The registry cross-check: a candidate is the driver's when its words
// appear in order in the command the driver runs.
func TestDriverEntries(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want bool
	}{
		{"cursor", []string{"acp"}, true},
		{"qwen", []string{"--acp"}, true},
		{"qwen", []string{"--experimental-acp"}, false},
		{"codex", []string{"app-server"}, true},
		{"pi", []string{"--mode", "rpc"}, true},
		{"grok", []string{"agent"}, true},
		{"droid", []string{"exec"}, true},
		{"claude", []string{"acp"}, false},
	} {
		h, ok := harness.All[c.name]
		if !ok {
			t.Fatalf("no harness %s", c.name)
		}
		if got := usesEntry(driverEntries(h), c.args); got != c.want {
			t.Errorf("%s %v: got %v, want %v (entries %v)", c.name, c.args, got, c.want, driverEntries(h))
		}
	}
}

func TestPromptHasWords(t *testing.T) {
	if !promptHasWords("serve", []string{"serve"}) || promptHasWords("mcp-server", []string{"mcp"}) || promptHasWords("server", []string{"serve"}) {
		t.Error("whole words only")
	}
}
