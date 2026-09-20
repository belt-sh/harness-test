package runner

import (
	"fmt"
	"strings"
)

// Probes are the opt-in measurements a run can make beyond its phases.
//
// They were five environment variables with undocumented magic values —
// HARNESS_ACP_LOAD=kill, HARNESS_ACP_INFLIGHT=cancel|hold, HARNESS_ACP_COMPACT,
// HARNESS_DUMP_TOOLS — invisible to --help, read in one place to decide
// whether to run and in another to decide how, with the allowed values written
// only in comments. As a flag they are discoverable and a typo is an error
// rather than silence.
type Probes struct {
	// Resume attaches a second process to a session the first one had.
	// ResumeKill ends the first process outright instead of closing it.
	Resume     bool
	ResumeKill bool

	// InFlight parks a permission request, kills the client while the tool
	// call waits on it, and resumes. Answer says what the resumed session
	// does with a re-raised request.
	InFlight bool
	Answer   PermissionAnswer

	// Compact compacts a session before resuming it, to see what the model
	// receives afterwards.
	Compact bool

	// DumpTools prints the tools each agent declared to the model, which is
	// where ToolCallGated entries come from.
	DumpTools bool

	// Deferred serves the agent its own tool-search tool, so the tools it
	// keeps out of a plain turn's declaration become visible. Only agents
	// that declare such a tool have anything to reveal.
	Deferred bool

	// Detect checks what harness.DetectInstalled() claims against the agent
	// this run actually installed. The installed list is what belt consumes;
	// a container that installs exactly one agent is the only place the
	// claim can be checked against ground truth.
	Detect bool

	// DumpEnv records the environment each agent hands its hooks. A hook is
	// a child process of the agent, so this is the only place the variables
	// an agent exports can be observed rather than guessed, and it is where
	// DetectEnvVars entries come from.
	DumpEnv bool
}

// ParseProbes reads a comma-separated probe list:
//
//	resume, resume=kill, inflight, inflight=cancel, inflight=hold, compact,
//	tools, deferred, env, detect
func ParseProbes(spec string) (Probes, error) {
	var p Probes
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		switch name {
		case "resume":
			p.Resume = true
			switch value {
			case "", "close":
			case "kill":
				p.ResumeKill = true
			default:
				return p, fmt.Errorf("probe resume: want close or kill, got %q", value)
			}
		case "inflight":
			p.InFlight = true
			switch value {
			case "", "approve":
				p.Answer = AnswerApproved
			case "cancel":
				p.Answer = AnswerCancelled
			case "hold":
				p.Answer = AnswerParked
			default:
				return p, fmt.Errorf("probe inflight: want approve, cancel or hold, got %q", value)
			}
		case "compact":
			p.Compact = true
		case "tools":
			p.DumpTools = true
		case "deferred":
			p.Deferred = true
		case "env":
			p.DumpEnv = true
		case "detect":
			p.Detect = true
		default:
			return p, fmt.Errorf("unknown probe %q (want resume, inflight, compact, tools, deferred, env or detect)", name)
		}
	}
	return p, nil
}

// How describes how the first process was ended, for a result line.
func (p Probes) How() string {
	if p.ResumeKill {
		return "killed"
	}
	return "closed"
}
