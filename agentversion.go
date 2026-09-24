package main

import (
	"flag"
	"fmt"
	"os"
)

// agentVersion is --agent-version. It lives here rather than in main's flag
// block so the flag and its check stay in one place.
var agentVersion = flag.String("agent-version", "",
	"install this exact version of the --harness agent instead of the latest (npm pkg@v, pip pkg==v, or the agent's pinned installer; see README)")

// checkAgentVersion refuses --agent-version for anything but one agent: a
// version means nothing across agents.
func checkAgentVersion(targets []string) {
	if *agentVersion == "" || len(targets) == 1 {
		return
	}
	fmt.Fprintf(os.Stderr, "--agent-version needs exactly one --harness, got %d\n", len(targets))
	os.Exit(2)
}
