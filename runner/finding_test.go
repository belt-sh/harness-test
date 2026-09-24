package runner

import (
	"strings"
	"testing"

	"github.com/inference-sh/agentprotocol/harness"
)

// A skip means the question could not be asked. A finding means it was asked
// and the answer is no. Counting the second as the first made six agents that
// had been measured look untested, which is what this separation exists to
// prevent — so the two counters must not be the same counter.
func TestAFindingIsNotASkip(t *testing.T) {
	r := &TestRunner{harness: harness.Harness{Name: "test"}}
	r.skip("t", "could not ask")
	r.finding("t", "asked, and the answer is no")

	if r.result.Skipped != 1 {
		t.Errorf("want 1 skipped, got %d", r.result.Skipped)
	}
	if r.result.Findings != 1 {
		t.Errorf("want 1 finding, got %d", r.result.Findings)
	}
	if r.result.Passed != 0 || r.result.Failed != 0 {
		t.Errorf("a finding is neither a pass nor a failure, got %+v", r.result)
	}
}

// A finding must not fail the run: the agent answered, and the answer being
// "no" is a fact about the agent, not a defect in it or in this suite.
func TestAFindingDoesNotFailTheRun(t *testing.T) {
	r := &TestRunner{harness: harness.Harness{Name: "test"}}
	r.finding("t", "does not re-raise the parked approval on resume")
	if r.failed {
		t.Error("a finding marked the run failed")
	}
}

// The in-flight probe's "does not re-raise" result is the one this was built
// for. Guard that it is recorded as a finding, so a later edit cannot quietly
// return it to the skip column.
func TestTheUnraisedApprovalIsRecordedAsAFinding(t *testing.T) {
	src := readSource(t, "testrunner.go")
	i := strings.Index(src, "does not re-raise the parked approval on resume")
	if i < 0 {
		t.Fatal("the in-flight result is gone")
	}
	line := src[strings.LastIndex(src[:i], "\n")+1 : i]
	if !strings.Contains(line, "r.finding(") {
		t.Errorf("recorded with %q, want r.finding — a measured answer is not a skip", strings.TrimSpace(line))
	}
}

// An agent whose binary is on PATH but answers no version flag is broken, and
// saying so beats reporting the symptoms that follow.
func TestAnUnrunnableBinaryFailsWithTheCause(t *testing.T) {
	src := readSource(t, "testrunner.go")
	i := strings.Index(src, "func (r *TestRunner) detectVersion()")
	if i < 0 {
		t.Fatal("detectVersion is gone")
	}
	body := src[i : i+1800]
	if !strings.Contains(body, "installed but will not run") {
		t.Error("detectVersion returns silently when no flag answers; an empty version must fail with the cause")
	}
}
