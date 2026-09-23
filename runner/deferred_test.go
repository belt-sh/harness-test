package runner

import (
	"strings"
	"testing"

	"github.com/belt-sh/harness-test/server"
)

// The probe finds the search tool by name, so the names have to be the ones
// agents actually declare. These four were read off the wire in 2026-09.
func TestDeferredToolNamesMatchWhatAgentsDeclare(t *testing.T) {
	for _, declared := range []string{"ToolSearch", "tool_search", "use_tool", "search_tool"} {
		if _, ok := findDeferredTool([]string{"Read", "Bash", declared}); !ok {
			t.Errorf("%s is a deferred-tool mechanism the probe would walk past", declared)
		}
	}
	if name, ok := findDeferredTool([]string{"Read", "Bash", "Write"}); ok {
		t.Errorf("an agent with no search tool must reveal nothing, got %q", name)
	}
}

// An agent ships both halves of the mechanism: one tool finds a tool, another
// invokes one by name. Only the finding half expands the declaration, so it
// has to win regardless of which sorts first. qwen declares both.
func TestTheSearchHalfWinsOverTheInvokeHalf(t *testing.T) {
	qwen := []string{"agent", "edit", "read_file", "tool_call", "tool_search"}
	if got, _ := findDeferredTool(qwen); got != "tool_search" {
		t.Errorf("want tool_search, got %q — tool_call asks for a tool by name and reveals nothing", got)
	}
	grok := []string{"grep", "search_tool", "use_tool", "web_search"}
	if got, _ := findDeferredTool(grok); got != "search_tool" {
		t.Errorf("want search_tool, got %q", got)
	}
}

// A search tool's query argument is not always called "query", and filling
// the wrong one sends a call the agent rejects. Prefer a conventional name,
// fall back to the first declared string, and never invent a non-string.
func TestQueryArgumentPrefersTheConventionalNameThenAString(t *testing.T) {
	decl := func(props map[string]any, required ...string) server.ToolDeclaration {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		return server.ToolDeclaration{Schema: map[string]any{"properties": props, "required": req}}
	}
	str := map[string]any{"type": "string"}
	num := map[string]any{"type": "number"}

	if got, _ := queryArgumentFor(decl(map[string]any{"limit": num, "query": str})); got != "query" {
		t.Errorf("want query, got %q", got)
	}
	// No conventional name: the first declared string, required ordered first.
	if got, _ := queryArgumentFor(decl(map[string]any{"zeta": str, "alpha": str}, "zeta")); got != "zeta" {
		t.Errorf("want the required string zeta, got %q", got)
	}
	// Nothing fillable. Skipping beats sending a number where a query goes.
	if got, ok := queryArgumentFor(decl(map[string]any{"limit": num})); ok {
		t.Errorf("want no argument when none is a string, got %q", got)
	}
	if got, ok := queryArgumentFor(server.ToolDeclaration{}); ok {
		t.Errorf("want no argument when no schema was declared, got %q", got)
	}
}

// The probe reports the delta, so a tool present before the expansion must not
// be counted as revealed by it.
func TestRevealedToolsAreOnlyTheNewOnes(t *testing.T) {
	got := added([]string{"Bash", "Read"}, []string{"Bash", "Edit", "Read", "Write"})
	want := []string{"Edit", "Write"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
	if n := len(added([]string{"Bash"}, []string{"Bash"})); n != 0 {
		t.Errorf("an unchanged declaration reveals nothing, got %d", n)
	}
}

// "deferred" has to reach Probes, or the flag parses and the probe never runs.
func TestDeferredProbeParsesFromTheFlag(t *testing.T) {
	p, err := ParseProbes("tools,deferred")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Deferred || !p.DumpTools {
		t.Fatalf("want both set, got %+v", p)
	}
	if _, err := ParseProbes("defered"); err == nil {
		t.Error("a typo must be an error, not silence")
	}
}

// The probe was headless-only, which is a silent gap rather than a loud one:
// all three agents with a search tool have headless today, so nothing would
// fail until an ACP-only agent shipped one. Guard both phases are wired.
func TestTheDeferredProbeRunsInBothPhases(t *testing.T) {
	src := readSource(t, "testrunner.go")
	for _, want := range []string{`r.probeDeferredTools("headless")`, `r.probeDeferredTools("acp")`} {
		if !strings.Contains(src, want) {
			t.Errorf("%s is not wired into the run loop", want)
		}
	}
}

// startProbeTurn used to arm the default tool call itself, which silently
// overwrote a probe that had armed its own — the deferred probe arms a
// tool-search call and would have had it replaced by Read before the turn ran.
// Arming belongs to the caller.
func TestTheSharedTurnDoesNotArmATheCallersToolCall(t *testing.T) {
	src := readSource(t, "testrunner.go")
	i := strings.Index(src, "func (r *TestRunner) startProbeTurn(")
	if i < 0 {
		t.Fatal("startProbeTurn is gone")
	}
	body := src[i:strings.Index(src[i:], "\n}\n")+i]
	if strings.Contains(body, "r.armToolCall(") {
		t.Error("startProbeTurn arms a tool call, overwriting whatever the caller armed")
	}
}

