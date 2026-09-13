package driver

import (
	"strings"
	"testing"
)

// The JSON-RPC envelope, response routing and content-block flattening that
// used to be tested here now live in github.com/inference-sh/agentprotocol/acp,
// which has its own tests for them. What is still this repo's to hold is that
// each driver satisfies the interface the runner drives, and the ACP policy
// kept in acp.go (see acp_permission_test.go).
func TestDriverInterface(t *testing.T) {
	var _ Driver = (*ACPDriver)(nil)
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Error("long string should be truncated")
	}
}

// The in-flight probe has to undo the registry's own auto-approval, or it
// measures this harness's configuration and reports it as an agent trait.
func TestWithoutAutoApprovalDropsTheFlagsAndTheirValues(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		kept    []string
		removed []string
	}{
		{"kiro", []string{"--trust-all-tools", "--model", "m"}, []string{"--model", "m"}, []string{"--trust-all-tools"}},
		{"qwen", []string{"--auth-type", "openai", "--yolo", "--model", "m"}, []string{"--auth-type", "openai", "--model", "m"}, []string{"--yolo"}},
		{"droid takes a value with it", []string{"--auto", "high", "-m", "m"}, []string{"-m", "m"}, []string{"--auto", "high"}},
		{"nothing to drop", []string{"--cwd", "/repo"}, []string{"--cwd", "/repo"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, removed := withoutAutoApproval(c.args)
			if strings.Join(kept, " ") != strings.Join(c.kept, " ") {
				t.Errorf("kept = %v, want %v", kept, c.kept)
			}
			if strings.Join(removed, " ") != strings.Join(c.removed, " ") {
				t.Errorf("removed = %v, want %v", removed, c.removed)
			}
		})
	}
}
