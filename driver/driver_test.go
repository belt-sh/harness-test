package driver

import "testing"

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
