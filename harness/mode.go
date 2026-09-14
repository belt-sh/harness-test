package harness

import "fmt"

// Mode is one way of driving an agent. It is a string because that is what it
// has always been in practice: the registry's per-mode tables and the
// KnownIssues keys are written in these words, and the runner used to carry a
// separate int enum alongside them that nothing tied to the strings — so
// SetMode("ACP") silently meant "both" and a new mode had to be added in four
// places.
type Mode string

const (
	ModeBoth        Mode = "both"
	ModeHeadless    Mode = "headless"
	ModeInteractive Mode = "interactive"
	ModeACP         Mode = "acp"
	ModeSDK         Mode = "sdk"
)

// Modes are the modes a run can be restricted to.
var Modes = []Mode{ModeBoth, ModeHeadless, ModeInteractive, ModeACP, ModeSDK}

// ParseMode rejects a mode this suite does not have, rather than falling back
// to a default and running something the caller did not ask for.
func ParseMode(s string) (Mode, error) {
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("unknown mode %q (want one of %v)", s, Modes)
}

// Phases are the modes a run in this mode actually executes.
func (m Mode) Phases() []Mode {
	if m == ModeBoth {
		return []Mode{ModeHeadless, ModeInteractive}
	}
	return []Mode{m}
}

// Runs reports whether a run in this mode executes the given phase.
func (m Mode) Runs(phase Mode) bool {
	for _, p := range m.Phases() {
		if p == phase {
			return true
		}
	}
	return false
}
