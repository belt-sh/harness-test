package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/belt-sh/harness-test/tests/expected"
)

// Cursor's tool row cannot be read off the wire: the CLI never sends its
// built-in tools, and the backend picks them from the mode, the model and the
// client's capability fields. So the row is three things, of which only the
// first is not measured on every run:
//
//   - the tools the real backend gave the model, per mode, from one snapshot
//     (tests/expected/cursor-tools.json, the model's own report);
//   - the exec types this CLI version can run, read out of its bundle;
//   - the capability fields it sends the mock (server.CursorClientFlags).
//
// The last two are checks, so a release that adds an exec type or flips a
// flag is a difference under --compare.

// CursorToolSnapshot is tests/expected/cursor-tools.json.
type CursorToolSnapshot struct {
	CLIVersion string                    `json:"cli_version"`
	Date       string                    `json:"date"`
	Source     string                    `json:"source"`
	Modes      map[string][]string       `json:"modes"`
	Params     map[string][]string       `json:"params"`
	Exec       map[string]CursorToolExec `json:"exec"`
}

// CursorToolExec is how one tool runs: the ExecServerMessage types the
// backend sends the client for it, none for a tool the backend runs itself.
type CursorToolExec struct {
	Exec     []string `json:"exec"`
	GatedBy  string   `json:"gated_by,omitempty"`
	Evidence string   `json:"evidence"`
}

func loadCursorToolSnapshot() (CursorToolSnapshot, error) {
	var s CursorToolSnapshot
	err := json.Unmarshal(expected.CursorTools, &s)
	return s, err
}

// execServerOneof is the ExecServerMessage schema string in the bundle:
// "ExecServerMessage|1 id 13|2 shell_args #0 message|...". Oneof members are
// the entries whose last word is the oneof's name.
var execServerOneof = regexp.MustCompile(`"ExecServerMessage\|([^"]*)"`)

// cursorExecTypes reads the ExecServerMessage oneof out of the installed
// CLI's bundle: every request the backend can make the client run.
// ~/.local/bin/cursor-agent links to versions/<build>/cursor-agent, and the
// bundle is index.js beside it.
func cursorExecTypes(binary string) ([]string, error) {
	bin, err := exec.LookPath(binary)
	if err != nil {
		return nil, err
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "index.js"))
	if err != nil {
		return nil, err
	}
	return parseExecTypes(data)
}

func parseExecTypes(bundle []byte) ([]string, error) {
	m := execServerOneof.FindSubmatch(bundle)
	if m == nil {
		return nil, fmt.Errorf("no ExecServerMessage schema in the bundle")
	}
	var out []string
	for _, f := range strings.Split(string(m[1]), "|") {
		w := strings.Fields(f)
		if len(w) == 4 && w[3] == "message" {
			out = append(out, w[1])
		}
	}
	sort.Strings(out)
	return out, nil
}

// probeCursorTools records the client's side of cursor's tool negotiation
// and prints the snapshot row. Runs with --probe tools.
func (r *TestRunner) probeCursorTools(phase string) {
	if r.harness.Name != "cursor" || r.server == nil {
		return
	}
	snap, err := loadCursorToolSnapshot()
	if err != nil {
		r.fail("cursor.snapshot", "cursor tool snapshot: "+err.Error())
		return
	}
	fmt.Printf("  [tools-snapshot] cursor/agent %s (%s, %s, %s)\n",
		strings.Join(snap.Modes["agent"], " "), snap.Source, snap.CLIVersion, snap.Date)

	types, err := cursorExecTypes(r.harness.Binary)
	if err != nil {
		r.fail("cursor.exec", fmt.Sprintf("%s: exec types not read from the bundle: %v", phase, err))
	}
	have := map[string]bool{}
	for _, execType := range types {
		have[execType] = true
		r.pass("cursor.exec."+execType, fmt.Sprintf("%s: the bundle's ExecServerMessage has %s", phase, execType))
	}
	if len(types) > 0 {
		// The snapshot's mapping names exec types; one this version no longer
		// has means the snapshot is out of date for it.
		var missing []string
		names := make([]string, 0, len(snap.Exec))
		for n := range snap.Exec {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, t := range snap.Exec[n].Exec {
				if !have[t] {
					missing = append(missing, n+"→"+t)
				}
			}
		}
		if len(missing) > 0 {
			r.finding("cursor.snapshot:stale", fmt.Sprintf("%s: snapshot maps tools to exec types this CLI does not have: %s", phase, strings.Join(missing, " ")))
		} else {
			r.pass("cursor.snapshot", fmt.Sprintf("%s: every exec type the %s snapshot maps a tool to is in this CLI", phase, snap.CLIVersion))
		}
	}

	flags := r.server.CursorClientFlags()
	if len(flags) == 0 {
		r.fail("cursor.flags", phase+": the client sent no run_request or request context")
		return
	}
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// The value is the answer, so a flag the client flips is a difference.
		flag := k + ":" + flags[k]
		r.pass("cursor.flag."+flag, fmt.Sprintf("%s: client sends %s = %s", phase, k, flags[k]))
	}
}
