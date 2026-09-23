package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/belt-sh/harness-test/server"
)

// deferredToolNames are the tools an agent ships to keep its other tools out
// of the declaration until something asks for them. An agent that declares one
// is telling the model its list is partial, so the list this suite reads off
// the wire is partial too.
//
// Matched by name because the schema does not say what a tool does. The names
// are checked case-insensitively: the same mechanism is ToolSearch in droid and
// tool_search in qwen.
//
// Order is preference, not a set. An agent ships two halves of this mechanism
// — one to find a tool, one to invoke it — and only the finding half expands
// the declaration. qwen declares tool_search and tool_call; picking whichever
// sorted first got tool_call, which asks for a tool by name and reveals
// nothing, and the probe reported that as qwen revealing nothing.
var deferredToolNames = []string{"toolsearch", "tool_search", "search_tool", "use_tool", "tool_call"}

// deferredQueryArgs are the argument names a search tool takes its query in,
// most specific first. Anything else is filled from the schema.
var deferredQueryArgs = []string{"query", "q", "search", "keywords", "pattern", "task", "description"}

// deferredQuery is deliberately broad: the probe is asking how much surface a
// single expansion reveals, not looking for one tool.
const deferredQuery = "read write edit file search run shell command list directory"

// probeDeferredTools serves the agent its own tool-search tool and reports
// which tools that reveals.
//
// The tool matrix is read off the wire, which makes it exact for agents that
// declare everything up front and an undercount for agents that do not. This
// probe is the difference: it calls the search tool the way the model would,
// then re-reads the declarations. Whatever appears was reachable all along and
// invisible to a single turn.
//
// What it cannot do is enumerate. A search tool answers a query, so the result
// is the surface reachable under deferredQuery, not the agent's catalogue. The
// output says so rather than implying a total.
func (r *TestRunner) probeDeferredTools(phase string) {
	if r.server == nil {
		return
	}
	fmt.Printf("[probe] deferred tools (%s)\n", phase)

	before := r.server.DeclaredTools()
	decls := r.server.ToolDeclarations()

	name, ok := findDeferredTool(before)
	if !ok {
		r.pass(fmt.Sprintf("deferred tools: %s declares no tool-search tool, so its %d declared tools are the whole surface it offers on a turn",
			r.harness.Name, len(before)))
		return
	}

	arg, argOK := queryArgumentFor(decls[name])
	if !argOK {
		r.skip(fmt.Sprintf("deferred tools: %s declares %s but no string argument to query it with (args: %s)",
			r.harness.Name, name, strings.Join(decls[name].ArgumentNames(), " ")))
		return
	}

	args := fmt.Sprintf(`{%q: %q}`, arg, deferredQuery)
	if !r.armTool(name, args) {
		r.skip(fmt.Sprintf("deferred tools: %s could not be armed with %s", r.harness.Name, name))
		return
	}
	r.pass(fmt.Sprintf("deferred tools: %s declares %s(%s) — asking it to expand", r.harness.Name, name, arg))

	if !r.runTurnFor(phase) {
		return
	}

	revealed := added(before, r.server.DeclaredTools())
	if len(revealed) == 0 {
		// "Revealed nothing" has two causes with opposite meanings: the agent
		// was never offered the call, which says nothing about its surface, or
		// it ran the search and its declaration did not grow, which is a
		// measurement. Reporting them as one line makes an untested agent look
		// like a tested one.
		if !r.server.ToolCallServed() {
			r.skip(fmt.Sprintf("deferred tools: %s was never offered %s, so its surface is still unmeasured",
				r.harness.Name, name))
			return
		}
		r.pass(fmt.Sprintf("deferred tools: %s ran %s and declared nothing new — its search returns results to the model rather than expanding the tool list",
			r.harness.Name, name))
		return
	}
	r.pass(fmt.Sprintf("deferred tools: %s revealed %d tool(s) not visible on a plain turn: %s",
		r.harness.Name, len(revealed), strings.Join(revealed, " ")))
	fmt.Printf("  [tools] %s/%s after expansion (%d total): %s\n",
		r.harness.Name, phase, len(before)+len(revealed), strings.Join(r.server.DeclaredTools(), " "))
}

// runTurnFor runs one more turn so the agent gets a chance to call the armed
// tool.
//
// The expansion happens inside the agent, so the mode does not change what a
// search reveals — but a probe that only runs in one mode is silently
// inapplicable to an agent that has the other. All three agents with a search
// tool today have headless, which is exactly why the gap would not have shown
// up until an ACP-only agent shipped one.
func (r *TestRunner) runTurnFor(phase string) bool {
	if phase == "acp" {
		d, _, ok := r.startProbeTurn("deferred tools")
		if !ok {
			return false
		}
		d.Close()
		return true
	}
	r.runOneShot("deferred-tools turn", r.harness.HeadlessCmd, r.harness.HeadlessModelArgs)
	return true
}

func findDeferredTool(declared []string) (string, bool) {
	for _, want := range deferredToolNames {
		for _, d := range declared {
			if strings.EqualFold(d, want) {
				return d, true
			}
		}
	}
	return "", false
}

// queryArgumentFor picks the argument to put the query in: a name a search
// tool conventionally uses, else the first declared string argument, which the
// schema orders required-first.
func queryArgumentFor(d server.ToolDeclaration) (string, bool) {
	names := d.ArgumentNames()
	for _, want := range deferredQueryArgs {
		for _, n := range names {
			if strings.EqualFold(n, want) && d.StringArgument(n) {
				return n, true
			}
		}
	}
	for _, n := range names {
		if d.StringArgument(n) {
			return n, true
		}
	}
	return "", false
}

// added returns the names in after that were not in before.
func added(before, after []string) []string {
	seen := map[string]bool{}
	for _, b := range before {
		seen[b] = true
	}
	var out []string
	for _, a := range after {
		if !seen[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}
