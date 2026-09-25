package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/runner"
	"github.com/inference-sh/agentprotocol/harness"
)

// This file is the machine-readable side of the suite: the registry's modes
// as CI reads them, the --report a run writes, and the regression gate that
// compares reports with tests/expected/<agent>.json.

// modesOf is every phase the registry says this agent has. CI reads it
// (--modes-for, --list --json) instead of keeping a list per workflow, so a
// registry change is a CI change.
func modesOf(h harness.Harness) []string {
	var modes []string
	if len(h.HeadlessCmd) > 0 {
		modes = append(modes, "headless")
	}
	if len(h.InteractiveCmd) > 0 {
		modes = append(modes, "interactive")
	}
	if h.DriverKind() != "" {
		modes = append(modes, "acp")
	}
	if len(h.SDKCmd) > 0 {
		modes = append(modes, "sdk")
	}
	return modes
}

// ciExcluded are modes the registry has that CI does not run, each with the
// measurement that put it here. An entry is a statement about the container
// CI runs in, so it needs a run that shows it, not a guess.
var ciExcluded = map[string]map[string]string{}

// ciModes is modesOf without the exclusions.
func ciModes(h harness.Harness) []string {
	var out []string
	for _, m := range modesOf(h) {
		if _, skip := ciExcluded[h.Name][m]; !skip {
			out = append(out, m)
		}
	}
	return out
}

type listEntry struct {
	Name           string            `json:"name"`
	Binary         string            `json:"binary"`
	Session        string            `json:"session,omitempty"`
	Modes          []string          `json:"modes"`
	CIModes        []string          `json:"ci_modes"`
	CIExcluded     map[string]string `json:"ci_excluded,omitempty"`
	NeedsIntercept bool              `json:"needs_intercept,omitempty"`
}

func listJSON() []listEntry {
	names := make([]string, 0, len(harness.All))
	for name := range harness.All {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []listEntry
	for _, name := range names {
		h := harness.All[name]
		out = append(out, listEntry{
			Name: name, Binary: h.Binary, Session: h.DriverKind(),
			Modes: nonNil(modesOf(h)), CIModes: nonNil(ciModes(h)),
			CIExcluded: ciExcluded[name], NeedsIntercept: h.NeedsIntercept,
		})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Report is what --report writes: one entry per agent the run tested.
type Report struct {
	Mode    string         `json:"mode"`
	Hooks   string         `json:"hooks"`
	Probes  string         `json:"probes,omitempty"`
	Results []ReportResult `json:"results"`
}

type ReportResult struct {
	Harness    string         `json:"harness"`
	Version    string         `json:"version,omitempty"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Skipped    int            `json:"skipped"`
	Findings   int            `json:"findings"`
	Seconds    float64        `json:"seconds"`
	SkipReason string         `json:"skip_reason,omitempty"`
	SkipDetail string         `json:"skip_detail,omitempty"`
	Checks     []runner.Check `json:"checks"`
}

func writeReport(path, mode, hooks, probes string, results []runner.Result) error {
	rep := Report{Mode: mode, Hooks: hooks, Probes: probes}
	for _, r := range results {
		checks := r.Checks
		if checks == nil {
			checks = []runner.Check{}
		}
		rep.Results = append(rep.Results, ReportResult{
			Harness: r.Harness, Version: r.Version,
			Passed: r.Passed, Failed: r.Failed, Skipped: r.Skipped, Findings: r.Findings,
			Seconds:    r.Duration.Round(time.Second).Seconds(),
			SkipReason: string(r.SkipReason), SkipDetail: r.SkipDetail, Checks: checks,
		})
	}
	return writeJSON(path, rep)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// Expected is tests/expected/<agent>.json: the runs the nightly makes for one
// agent and what each check came to the last time someone accepted the
// result. The runs are part of the file, so an agent that needs a different
// probe set (gemini's resumeafter) says so where its expectations live, and
// --update-expected keeps them.
type Expected struct {
	Harness string        `json:"harness"`
	Runs    []ExpectedRun `json:"runs"`
}

type ExpectedRun struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Probes string `json:"probes"`
	// Note says why this run is shaped the way it is, when it is not the
	// default.
	Note string `json:"note,omitempty"`
	// Checks maps a check id to its outcome key: "pass", "skip",
	// "finding:not-reached". Written by --update-expected.
	Checks map[string]string `json:"checks"`
	// Nondeterministic lists checks that were measured to vary between
	// identical runs, the outcomes seen, and why. Written by hand, kept by
	// --update-expected; a check in it must still come out as one of the
	// outcomes listed.
	Nondeterministic map[string]Nondeterministic `json:"nondeterministic,omitempty"`
}

type Nondeterministic struct {
	Outcomes []string `json:"outcomes"`
	Reason   string   `json:"reason"`
}

// defaultRuns is the nightly probe set for an agent with no expected file
// yet. resume=kill and the two other in-flight answers are runs of their own
// because each changes what the first process or the resumed one does.
func defaultRuns() []ExpectedRun {
	return []ExpectedRun{
		{Name: "probes", Mode: "acp", Probes: "resume,inflight,compact,transcript,seed,seedkinds=shapes,tools,deferred,hidden"},
		{Name: "resume-kill", Mode: "acp", Probes: "resume=kill"},
		{Name: "inflight-cancel", Mode: "acp", Probes: "inflight=cancel"},
		{Name: "inflight-hold", Mode: "acp", Probes: "inflight=hold"},
	}
}

// loadExpected reads an expected file; a missing one is the default runs with
// no expectations, which is how a new agent's first file is generated.
func loadExpected(path, name string) (Expected, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(path), ".json")
		}
		return Expected{Harness: name, Runs: defaultRuns()}, nil
	}
	if err != nil {
		return Expected{}, err
	}
	var e Expected
	if err := json.Unmarshal(data, &e); err != nil {
		return Expected{}, fmt.Errorf("%s: %w", path, err)
	}
	if e.Harness == "" || len(e.Runs) == 0 {
		return Expected{}, fmt.Errorf("%s: needs a harness and at least one run", path)
	}
	return e, nil
}

// printRuns lists an expected file's runs for tests/probes.sh, one per line:
// name, mode, probes.
func printRuns(e Expected) {
	for _, r := range e.Runs {
		fmt.Printf("%s %s %s\n", r.Name, r.Mode, r.Probes)
	}
}

// readRunReport reads <dir>/<run>.json and returns the agent's checks keyed
// by id.
func readRunReport(dir, run, name string) (map[string]runner.Check, ReportResult, error) {
	path := filepath.Join(dir, run+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ReportResult{}, fmt.Errorf("no report for run %s (%v): the run did not finish", run, err)
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, ReportResult{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, res := range rep.Results {
		if res.Harness != name {
			continue
		}
		out := map[string]runner.Check{}
		for _, c := range res.Checks {
			out[c.ID] = c
		}
		return out, res, nil
	}
	return nil, ReportResult{}, fmt.Errorf("%s: no result for %s", path, name)
}

// Difference is one way a run's result departs from what is expected.
type Difference struct {
	Run, ID, Want, Got, Message string
}

func (d Difference) String() string {
	switch {
	case d.Want == "":
		return fmt.Sprintf("%s: %s is new: %s — %s", d.Run, d.ID, d.Got, d.Message)
	case d.Got == "":
		return fmt.Sprintf("%s: %s expected %s, not reported", d.Run, d.ID, d.Want)
	}
	return fmt.Sprintf("%s: %s expected %s, got %s — %s", d.Run, d.ID, d.Want, d.Got, d.Message)
}

// compareRun lists every difference between a run's checks and its
// expectations. Any difference counts: a new failure, a finding that appears
// or disappears, a pass that becomes a skip, a check that is no longer asked.
func compareRun(run ExpectedRun, got map[string]runner.Check) []Difference {
	var diffs []Difference
	ids := map[string]bool{}
	for id := range run.Checks {
		ids[id] = true
	}
	for id := range run.Nondeterministic {
		ids[id] = true
	}
	for id := range got {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		c, have := got[id]
		if nd, ok := run.Nondeterministic[id]; ok {
			if have && !contains(nd.Outcomes, c.Key()) {
				diffs = append(diffs, Difference{run.Name, id, strings.Join(nd.Outcomes, "|"), c.Key(), c.Message})
			}
			if !have && !contains(nd.Outcomes, "absent") {
				diffs = append(diffs, Difference{Run: run.Name, ID: id, Want: strings.Join(nd.Outcomes, "|")})
			}
			continue
		}
		want := run.Checks[id]
		switch {
		case !have:
			diffs = append(diffs, Difference{Run: run.Name, ID: id, Want: want})
		case want != c.Key():
			diffs = append(diffs, Difference{run.Name, id, want, c.Key(), c.Message})
		}
	}
	return diffs
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// compareExpected checks every run's report in dir against the expected file
// and, with updateTo set, writes the file those reports describe instead of
// failing. It returns the number of differences.
func compareExpected(path, name, dir, updateTo string) (int, error) {
	e, err := loadExpected(path, name)
	if err != nil {
		return 0, err
	}
	total := 0
	for i, run := range e.Runs {
		got, res, err := readRunReport(dir, run.Name, e.Harness)
		if err != nil {
			fmt.Printf("✗ %s\n", err)
			total++
			continue
		}
		fmt.Printf("=== %s/%s (%s, probes %s): %d passed, %d failed, %d skipped, %d found, %s ===\n",
			e.Harness, run.Name, orDash(res.Version), run.Probes, res.Passed, res.Failed, res.Skipped, res.Findings,
			time.Duration(res.Seconds*float64(time.Second)))
		diffs := compareRun(run, got)
		for _, d := range diffs {
			fmt.Printf("  ✗ %s\n", d)
		}
		if len(diffs) == 0 {
			fmt.Printf("  ✓ %d checks as expected\n", len(got))
		}
		total += len(diffs)
		if updateTo != "" {
			e.Runs[i].Checks = map[string]string{}
			for id, c := range got {
				if _, nd := run.Nondeterministic[id]; nd {
					continue
				}
				e.Runs[i].Checks[id] = c.Key()
			}
		}
	}
	if updateTo != "" {
		if err := writeJSON(updateTo, e); err != nil {
			return total, err
		}
		fmt.Printf("wrote %s\n", updateTo)
	}
	return total, nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// writeSurface installs the agent, reads its --help and writes the snapshot,
// the version and the raw help into dir.
func writeSurface(h harness.Harness, dir string) error {
	s, ok := runner.ReadSurface(h)
	if !ok {
		return fmt.Errorf("%s: could not read the CLI surface", h.Name)
	}
	if err := os.WriteFile(filepath.Join(dir, h.Name+".surface.txt"), []byte(s.Text(h.Name)), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, h.Name+".version"), []byte(s.Version+"\n"), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, h.Name+".help.txt"), []byte(s.Help), 0644)
}
