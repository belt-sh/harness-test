package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/inference-sh/agentprotocol/harness"
)

// SetAgentVersion makes the run install exactly this version of the agent
// instead of the latest one its InstallCmd fetches. Empty keeps the latest.
func (r *TestRunner) SetAgentVersion(v string) {
	r.agentVersion = v
}

// PinnedInstallCmd is the command that installs version of h, derived from
// the registry's InstallCmd by install mechanism:
//
//   - npm: the package becomes pkg@version.
//   - pip: the requirement becomes pkg==version.
//   - an installer script: per agent, in pinnedInstallers.
//
// An agent whose mechanism has no way to select a version is an error that
// says so; the run then fails instead of testing the latest under the
// pinned version's name.
func PinnedInstallCmd(h harness.Harness, version string) ([]string, error) {
	if version == "" {
		return nil, fmt.Errorf("no version given")
	}
	if strings.ContainsAny(version, " \t\n'\"`$;&|<>\\") {
		return nil, fmt.Errorf("version %q has characters no release version has", version)
	}
	cmd := h.InstallCmd
	if len(cmd) == 0 {
		return nil, fmt.Errorf("%s has no install command", h.Name)
	}
	if f, ok := pinnedInstallers[h.Name]; ok {
		return f(h, version)
	}
	switch filepath.Base(cmd[0]) {
	case "npm":
		out := append([]string(nil), cmd...)
		pkg := out[len(out)-1]
		if strings.HasPrefix(pkg, "-") {
			return nil, fmt.Errorf("%s: npm install command %q does not end in a package", h.Name, strings.Join(cmd, " "))
		}
		out[len(out)-1] = pkg + "@" + version
		return out, nil
	case "pip", "pip3":
		out := append([]string(nil), cmd...)
		pkg := out[len(out)-1]
		if strings.HasPrefix(pkg, "-") {
			return nil, fmt.Errorf("%s: pip install command %q does not end in a package", h.Name, strings.Join(cmd, " "))
		}
		out[len(out)-1] = pkg + "==" + version
		return out, nil
	}
	return nil, fmt.Errorf("%s installs with %q, which has no way to select a version, and harness-test has no pinned installer for it",
		h.Name, strings.Join(cmd, " "))
}

// cursorBuildRe is a cursor-agent build id: the date version and the
// commit, as the download URLs and `cursor-agent --version` spell it.
var cursorBuildRe = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9a-f]{7}$`)

// pinnedInstallers install a given version of the agents that install with
// a script.
var pinnedInstallers = map[string]func(harness.Harness, string) ([]string, error){
	// https://cursor.com/install is generated per release with the build id
	// written into it (downloads.cursor.com/lab/<build>/<os>/<arch>/
	// agent-cli-package.tar.gz, ~/.local/share/cursor-agent/versions/<build>).
	// Every published build stays downloadable, so the script is fetched and
	// its build id replaced.
	"cursor": func(h harness.Harness, v string) ([]string, error) {
		if !cursorBuildRe.MatchString(v) {
			return nil, fmt.Errorf("cursor versions are build ids like 2026.05.16-0338208 (date and commit, as `cursor-agent --version` prints them), got %q", v)
		}
		return []string{"sh", "-c", `set -e; s=$(curl -fsSL https://cursor.com/install); ` +
			`echo "$s" | grep -Eq '[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9a-f]{7}' || { echo "cursor's installer names no build id to replace" >&2; exit 1; }; ` +
			`echo "$s" | sed -E 's/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9a-f]{7}/` + v + `/g' | bash`}, nil
	},
	// x.ai's installer takes the version as its first argument.
	"grok": func(h harness.Harness, v string) ([]string, error) {
		return []string{"sh", "-c", "curl -fsSL https://x.ai/cli/install.sh | bash -s " + v}, nil
	},
	// Kiro's installer only fetches stable/latest; each release's zip is at
	// stable/<version>/, holding the same install.sh the installer runs.
	// There is no per-version manifest (stable/<version>/manifest.json is
	// 403), so the zip's checksum is not verified.
	"kiro": func(h harness.Harness, v string) ([]string, error) {
		return []string{"sh", "-c", `set -e; d=$(mktemp -d); ` +
			`curl -fsSL -o "$d/k.zip" "https://prod.download.cli.kiro.dev/stable/` + v + `/kirocli-$(uname -m)-linux.zip"; ` +
			`unzip -q "$d/k.zip" -d "$d"; KIRO_CLI_SKIP_SETUP=1 "$d/kirocli/install.sh"; rm -rf "$d"`}, nil
	},
	// goose installs the GitHub release tagged stable; releases are also
	// tagged v<version>.
	"goose": func(h harness.Harness, v string) ([]string, error) {
		out := append([]string(nil), h.InstallCmd...)
		last := out[len(out)-1]
		if !strings.Contains(last, "/releases/download/stable/") {
			return nil, fmt.Errorf("goose's install command no longer downloads the stable release: %q", last)
		}
		out[len(out)-1] = strings.Replace(last, "/releases/download/stable/", "/releases/download/v"+strings.TrimPrefix(v, "v")+"/", 1)
		return out, nil
	},
}

// installPinned installs r.agentVersion, whatever is on PATH, and fails the
// binary check when the installed agent reports another version.
func (r *TestRunner) installPinned() {
	cmdline, err := PinnedInstallCmd(r.harness, r.agentVersion)
	if err != nil {
		r.fail("binary", "--agent-version "+r.agentVersion+": "+err.Error())
		return
	}
	fmt.Printf("  … installing %s %s: %s\n", r.harness.Binary, r.agentVersion, strings.Join(cmdline, " "))
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		r.fail("binary", fmt.Sprintf("install %s %s: %v\n%s", r.harness.Binary, r.agentVersion, err, string(out)))
		return
	}
	for _, d := range r.harness.InstallBinDirs {
		p := filepath.Join(r.home, d)
		if !strings.Contains(os.Getenv("PATH"), p) {
			os.Setenv("PATH", p+":"+os.Getenv("PATH"))
		}
	}
	if _, err := exec.LookPath(r.harness.Binary); err != nil {
		r.fail("binary", r.harness.Binary+" not found after install")
		return
	}
	r.detectVersion()
	if r.result.Version != "" {
		if !sameVersion(r.result.Version, r.agentVersion) {
			r.fail("binary", fmt.Sprintf("asked for %s %s, installed %s reports %q", r.harness.Name, r.agentVersion, r.harness.Binary, r.result.Version))
			return
		}
	}
	r.pass("binary", fmt.Sprintf("%s %s installed", r.harness.Binary, r.agentVersion))
	r.checkDetection()
	for _, postCmd := range r.harness.PostInstall {
		cmd := exec.Command(postCmd[0], postCmd[1:]...)
		cmd.Env = os.Environ()
		cmd.Run()
	}
}

var dottedRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// sameVersion reports whether the first dotted numbers in what an agent's
// --version printed and in the version asked for are the same version:
// "Hermes Agent v0.14.0 (2026.5.1)" and "0.14.0", "2026.05.16-0338208" and
// "2026.05.16-0338208". Components compare numerically, a missing one as 0.
func sameVersion(printed, want string) bool {
	a, b := dottedRe.FindString(printed), dottedRe.FindString(want)
	if a == "" || b == "" {
		return false
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return false
		}
	}
	return true
}
