package harness

import (
	"strings"
	"testing"
)

// A directory is taken as proof the agent was used on this machine, so it has
// to be a directory only that agent creates. opencode's hook path is
// .config/opencode/plugins, and truncating at the first slash made its
// detection directory ".config" — present on every Linux and macOS machine,
// so belt reported opencode installed for everyone.
func TestDetectionDirectoriesAreNotSharedRoots(t *testing.T) {
	for _, name := range KnownNames() {
		for _, d := range detectConfigDirs(name) {
			if sharedConfigRoots[d] {
				t.Errorf("%s detects on %q, a directory many programs share", name, d)
			}
			if d == "" || d == "." || d == ".." {
				t.Errorf("%s detects on %q", name, d)
			}
		}
	}
}

// The directory should name the agent. Where it cannot — a vendor name like
// .factory, or a shared plugin root — the registry must say so explicitly
// rather than leaving it to the truncation rule.
func TestDetectionDirectoriesNameTheirAgentOrAreExplicit(t *testing.T) {
	explicit := map[string]string{
		"droid": "Factory is the vendor; .factory is theirs alone",
		"goose": ".agents is goose's plugin root",
		"kimi":  ".kimi-code is kimi's own",
		"kiro":  ".kiro is kiro's own",
	}
	for _, name := range KnownNames() {
		dirs := detectConfigDirs(name)
		if len(dirs) == 0 {
			continue
		}
		d := strings.TrimPrefix(dirs[0], ".")
		if strings.Contains(d, name) || strings.Contains(name, strings.Split(d, "/")[0]) {
			continue
		}
		if _, ok := explicit[name]; !ok {
			t.Errorf("%s detects on %q, which does not name it and has no recorded reason", name, dirs[0])
		}
	}
}
