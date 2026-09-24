package main

import (
	"testing"

	"github.com/belt-sh/harness-test/runner"
)

func TestCompareRunReportsEveryDifference(t *testing.T) {
	run := ExpectedRun{
		Name: "probes",
		Checks: map[string]string{
			"acp/resume.context":  "pass",
			"acp/seed.hand-built": "finding:not-reached",
			"acp/inflight.park":   "pass",
			"acp/gone":            "pass",
			"acp/same":            "skip:by-id",
		},
		Nondeterministic: map[string]Nondeterministic{
			"acp/resume.retried": {Outcomes: []string{"pass", "absent"}, Reason: "timing"},
			"acp/varies":         {Outcomes: []string{"pass", "skip"}, Reason: "timing"},
		},
	}
	got := map[string]runner.Check{
		"acp/resume.context":  {ID: "acp/resume.context", Outcome: runner.OutcomeFail},                     // new failure
		"acp/seed.hand-built": {ID: "acp/seed.hand-built", Outcome: runner.OutcomePass},                    // finding disappears
		"acp/inflight.park":   {ID: "acp/inflight.park", Outcome: runner.OutcomeSkip, Answer: "not-gated"}, // pass becomes skip
		"acp/new":             {ID: "acp/new", Outcome: runner.OutcomeFinding},                             // new check
		"acp/same":            {ID: "acp/same", Outcome: runner.OutcomeSkip, Answer: "by-id"},              // unchanged
		"acp/varies":          {ID: "acp/varies", Outcome: runner.OutcomeFinding},                          // outside its set
	}
	diffs := compareRun(run, got)
	ids := map[string]bool{}
	for _, d := range diffs {
		ids[d.ID] = true
	}
	for _, want := range []string{"acp/resume.context", "acp/seed.hand-built", "acp/inflight.park", "acp/gone", "acp/new", "acp/varies"} {
		if !ids[want] {
			t.Errorf("no difference reported for %s", want)
		}
	}
	for _, not := range []string{"acp/same", "acp/resume.retried"} {
		if ids[not] {
			t.Errorf("difference reported for %s, which is as expected", not)
		}
	}
	if len(diffs) != 6 {
		t.Errorf("got %d differences, want 6: %v", len(diffs), diffs)
	}
}
