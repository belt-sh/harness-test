package driver

import "testing"

// The JSON-RPC envelope, response routing and content-block flattening that
// used to be tested here now live in github.com/inference-sh/agentprotocol/acp,
// which has its own tests for them. What is still this repo's to hold is the
// ACP policy kept in acp.go (see acp_permission_test.go).
//
// There was a Driver interface here asserting that the three transports were
// interchangeable. Nothing ever took one: ACPDriver was its only implementer,
// PTYSession did not satisfy it, and headless and SDK are plain exec calls.
// An interface with no consumer is a claim about the code rather than a
// constraint on it, and this one was read as the repo's central abstraction
// while providing no polymorphism at all.

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Error("long string should be truncated")
	}
}

// The probes were environment variables with magic values, invisible to
// --help and silent on a typo. As a flag a wrong value is an error.
func TestParseProbes(t *testing.T) {
	ok := func(spec string, want Probes) {
		t.Helper()
		got, err := ParseProbes(spec)
		if err != nil {
			t.Errorf("ParseProbes(%q): %v", spec, err)
			return
		}
		if got != want {
			t.Errorf("ParseProbes(%q) = %+v, want %+v", spec, got, want)
		}
	}
	ok("", Probes{})
	ok("resume", Probes{Resume: true})
	ok("resume=kill", Probes{Resume: true, ResumeKill: true})
	ok("inflight", Probes{InFlight: true, Answer: AnswerApproved})
	ok("inflight=hold", Probes{InFlight: true, Answer: AnswerParked})
	ok("resume=kill,inflight=cancel,compact,tools", Probes{
		Resume: true, ResumeKill: true,
		InFlight: true, Answer: AnswerCancelled,
		Compact: true, DumpTools: true,
	})

	for _, bad := range []string{"nope", "resume=maybe", "inflight=sometimes"} {
		if _, err := ParseProbes(bad); err == nil {
			t.Errorf("ParseProbes(%q) should have failed", bad)
		}
	}
}
