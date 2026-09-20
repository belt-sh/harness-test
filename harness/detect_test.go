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

// "Am I inside this agent" and "is this agent installed here" are different
// questions, and only filesystem evidence answers the second. A runtime
// variable someone exported by hand must never make belt offer to write hooks
// for an agent that is not there.
//
// This already held; nothing pinned it, which is how it came to be reported
// as broken. The test is the pin.
func TestEnvEvidenceAloneIsNotAnInstall(t *testing.T) {
	r := DetectResult{Name: "copilot", Probes: []Probe{ProbeEnvVar}}
	if r.Installed() {
		t.Error("an env-var match reports as installed")
	}
	if r.IsInstalled() {
		t.Error("IsInstalled disagrees with Installed")
	}
	if r.Configured() {
		t.Error("an env-var match reports as configured")
	}
	if !r.IsEnvironment() {
		t.Error("an env-var match must still report as an environment")
	}
	if !r.Found() {
		t.Error("an env-var match is still evidence of something and must be Found")
	}
}

// Each probe answers exactly one of the two questions. A new probe added to
// the wrong side is the failure this catches.
func TestEachProbeAnswersOneQuestion(t *testing.T) {
	filesystem := []Probe{ProbeConfigDir, ProbePathLookup, ProbeKnownPath, ProbePackageReg}
	for _, p := range filesystem {
		r := DetectResult{Probes: []Probe{p}}
		if p != ProbeConfigDir {
			r.Binary = "/usr/bin/thing"
		}
		if !r.Installed() && !r.Configured() {
			t.Errorf("%s is filesystem evidence but answers neither Installed nor Configured", p)
		}
		if r.IsEnvironment() {
			t.Errorf("%s is filesystem evidence but reports as an environment", p)
		}
	}
	env := DetectResult{Probes: []Probe{ProbeEnvVar}}
	if env.Installed() || env.Configured() {
		t.Error("the env probe must not answer the installed question")
	}
}

// belt writes real users' hooks here, so the path must not carry this
// suite's name. goose's was .agents/plugins/belt-test/hooks until 2026-09.
func TestShippedHookPathsAreNotNamedAfterThisSuite(t *testing.T) {
	for _, name := range KnownNames() {
		target := HooksTarget(name)
		if strings.Contains(target, "belt-test") || strings.Contains(target, "harness-test") {
			t.Errorf("%s installs to %s, which is named after this test suite", name, target)
		}
	}
}
