package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func npmPackageName(name string) string {
	h, ok := All[name]
	if !ok {
		return ""
	}
	if len(h.InstallCmd) >= 4 && h.InstallCmd[0] == "npm" && h.InstallCmd[1] == "install" {
		return h.InstallCmd[len(h.InstallCmd)-1]
	}
	return ""
}

// Probe identifies which detection strategy found evidence of a harness.
type Probe string

const (
	ProbeConfigDir  Probe = "config-dir"  // ~/.claude/, ~/.codex/ etc. exist — harness was used here
	ProbePathLookup Probe = "path-lookup" // binary found via exec.LookPath (in PATH)
	ProbeKnownPath  Probe = "known-path"  // binary at well-known install location (not in PATH)
	ProbePackageReg Probe = "package-reg" // found in npm/pip global package list
	ProbeEnvVar     Probe = "env-var"     // environment variable set (running inside this agent)
)

// DetectResult describes a detected harness installation.
type DetectResult struct {
	Name      string
	Binary    string  // resolved binary path
	Version   string  // from --version
	ConfigDir string  // detected config directory
	HooksPath string  // where hooks should be written (user-level)
	Probes    []Probe // which strategies matched, in order of detection
}

func (r DetectResult) Installed() bool {
	return r.Binary != "" || r.HasProbe(ProbePackageReg)
}

func (r DetectResult) Configured() bool {
	return r.HasProbe(ProbeConfigDir)
}

func (r DetectResult) HasProbe(p Probe) bool {
	for _, m := range r.Probes {
		if m == p {
			return true
		}
	}
	return false
}

// --- Strategy registry ---

// strategy is a detection function that may add probes and binary info to a result.
type strategy struct {
	Name string
	Run  func(name, binary, home string, r *DetectResult)
}

var strategies = []strategy{
	{"config-dir", probeConfigDir},
	{"path-lookup", probePathLookup},
	{"known-path", probeKnownPath},
	{"package-reg", probePackageReg},
	{"env-var", probeEnvVar},
}

// detectConfigDirs returns config directories to probe for detection.
func detectConfigDirs(name string) []string {
	h, ok := All[name]
	if !ok {
		return nil
	}
	if len(h.DetectConfigDirs) > 0 {
		return h.DetectConfigDirs
	}
	d := h.HookConfigDir
	if d == "" {
		return nil
	}
	if i := strings.IndexByte(d, '/'); i > 0 {
		d = d[:i]
	}
	return []string{d}
}

// hooksTarget returns the hook file path relative to HOME (derived from registry).
func hooksTarget(name string) string {
	h, ok := All[name]
	if !ok {
		return ""
	}
	return filepath.Join(h.HookConfigDir, hookFileName(h))
}

var wellKnownBinDirs = []string{
	".local/bin",
	".npm-global/bin",
	".grok/bin",
	".cargo/bin",
}


// --- Public API ---

// KnownNames returns the names of all agents in the registry.
func KnownNames() []string {
	names := make([]string, 0, len(All))
	for name := range All {
		names = append(names, name)
	}
	return names
}

func DetectAll() []DetectResult {
	home, _ := os.UserHomeDir()
	var results []DetectResult
	for name, h := range All {
		results = append(results, runDetection(name, h.Binary, home))
	}
	return results
}

// DetectInstalled returns results only for agents that are installed or configured.
func DetectInstalled() []DetectResult {
	var found []DetectResult
	for _, r := range DetectAll() {
		if r.Installed() || r.Configured() {
			found = append(found, r)
		}
	}
	return found
}

func DetectOne(name string) DetectResult {
	h, ok := All[name]
	if !ok {
		return DetectResult{Name: name}
	}
	home, _ := os.UserHomeDir()
	return runDetection(name, h.Binary, home)
}

func runDetection(name, binary, home string) DetectResult {
	r := DetectResult{Name: name}

	for _, s := range strategies {
		s.Run(name, binary, home, &r)
	}

	if r.Binary != "" {
		r.Version = getVersion(r.Binary)
	}
	if target := hooksTarget(name); target != "" && home != "" {
		r.HooksPath = filepath.Join(home, target)
	}

	return r
}

// --- Strategy implementations ---

// probeConfigDir checks for config directory existence.
// Most reliable signal — if ~/.claude/ exists, claude has been run on this machine.
// No exec, no PATH dependency, cross-platform.
func probeConfigDir(name, _, home string, r *DetectResult) {
	dirs := detectConfigDirs(name)
	if len(dirs) == 0 || home == "" {
		return
	}
	for _, d := range dirs {
		full := filepath.Join(home, d)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			r.ConfigDir = full
			r.Probes = append(r.Probes, ProbeConfigDir)
			return
		}
	}
}

// probePathLookup uses exec.LookPath to find the binary in PATH.
// Cross-platform (works on Windows, Linux, macOS). Confirms current installation.
func probePathLookup(_, binary, _ string, r *DetectResult) {
	if r.Binary != "" {
		return
	}
	if path, err := exec.LookPath(binary); err == nil {
		r.Binary = path
		r.Probes = append(r.Probes, ProbePathLookup)
	}
}

// probeKnownPath checks well-known install directories that may not be in PATH.
// Catches npm global installs, pip --user installs, cargo installs, and
// harness-specific directories (e.g. ~/.grok/bin/).
func probeKnownPath(_, binary, home string, r *DetectResult) {
	if r.Binary != "" || home == "" {
		return
	}
	for _, dir := range wellKnownBinDirs {
		candidate := filepath.Join(home, dir, binary)
		if _, err := os.Stat(candidate); err == nil {
			r.Binary = candidate
			r.Probes = append(r.Probes, ProbeKnownPath)
			return
		}
	}
}

// probePackageReg queries the npm global package list.
// Catches npm-installed packages regardless of PATH configuration.
// Uses a cached single call to `npm list -g --json`.
func probePackageReg(name, _, _ string, r *DetectResult) {
	if r.Binary != "" {
		return
	}
	pkg := npmPackageName(name)
	if pkg == "" {
		return
	}
	if npmHasGlobal(pkg) {
		r.Probes = append(r.Probes, ProbePackageReg)
	}
}

// probeEnvVar checks for environment variables that agents set at runtime.
// Only matches when belt is running INSIDE an agent (e.g. from a hook script).
// Not useful for interactive detection, but confirms the calling agent.
func probeEnvVar(name, _, _ string, r *DetectResult) {
	h, ok := All[name]
	if !ok {
		return
	}
	for _, v := range h.DetectEnvVars {
		if os.Getenv(v) != "" {
			r.Probes = append(r.Probes, ProbeEnvVar)
			return
		}
	}
}

// --- Helpers ---

func getVersion(binary string) string {
	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	if i := strings.IndexByte(v, '\n'); i > 0 {
		v = v[:i]
	}
	return v
}

var npmGlobalCache map[string]bool

func npmHasGlobal(pkg string) bool {
	if npmGlobalCache == nil {
		npmGlobalCache = make(map[string]bool)
		out, err := exec.Command("npm", "list", "-g", "--depth=0", "--json").Output()
		if err != nil {
			return false
		}
		var result struct {
			Dependencies map[string]any `json:"dependencies"`
		}
		if json.Unmarshal(out, &result) == nil {
			for k := range result.Dependencies {
				npmGlobalCache[k] = true
			}
		}
	}
	return npmGlobalCache[pkg]
}
