package runner

import (
	"strings"
	"testing"

	"github.com/inference-sh/agentprotocol/harness"
)

func TestPinnedInstallCmd(t *testing.T) {
	cases := []struct {
		agent, version, want string
	}{
		{"claude", "2.1.281", "npm install -g @anthropic-ai/claude-code@2.1.281"},
		{"pi", "0.87.1", "npm install -g --ignore-scripts @earendil-works/pi-coding-agent@0.87.1"},
		{"hermes", "0.14.0", "pip install --break-system-packages hermes-agent[acp]==0.14.0"},
		{"grok", "1.0.34", "bash -s 1.0.34"},
		{"kiro", "2.23.1", "stable/2.23.1/kirocli-"},
		{"goose", "1.51.0", "/releases/download/v1.51.0/goose-"},
		{"cursor", "2026.05.16-0338208", "/2026.05.16-0338208/g' | bash"},
	}
	for _, c := range cases {
		cmd, err := PinnedInstallCmd(harness.All[c.agent], c.version)
		if err != nil {
			t.Errorf("%s %s: %v", c.agent, c.version, err)
			continue
		}
		if got := strings.Join(cmd, " "); !strings.Contains(got, c.want) {
			t.Errorf("%s %s: %q does not contain %q", c.agent, c.version, got, c.want)
		}
	}
}

func TestPinnedInstallCmdRefuses(t *testing.T) {
	for _, c := range []struct{ agent, version string }{
		{"cursor", "2026.05.16"},        // a build id has the commit
		{"claude", "2.1.281; rm -rf ~"}, // not a version
		{"windsurf", "1.0.0"},           // no install command
	} {
		if cmd, err := PinnedInstallCmd(harness.All[c.agent], c.version); err == nil {
			t.Errorf("%s %s: got %q, want an error", c.agent, c.version, cmd)
		}
	}
	h := harness.Harness{Name: "x", InstallCmd: []string{"sh", "-c", "curl -fsSL https://example.com/install | bash"}}
	if _, err := PinnedInstallCmd(h, "1.0.0"); err == nil || !strings.Contains(err.Error(), "no way to select a version") {
		t.Errorf("unknown installer: %v", err)
	}
}

func TestSameVersion(t *testing.T) {
	for _, c := range []struct {
		printed, want string
		same          bool
	}{
		{"Hermes Agent v0.14.0 (2026.5.1)", "0.14.0", true},
		{"2026.05.16-0338208", "2026.05.16-0338208", true},
		{"2.1.281 (Claude Code)", "2.1.281", true},
		{"1.2", "1.2.0", true},
		{"0.19.0", "0.14.0", false},
		{"", "0.14.0", false},
	} {
		if got := sameVersion(c.printed, c.want); got != c.same {
			t.Errorf("sameVersion(%q, %q) = %v", c.printed, c.want, got)
		}
	}
}
