package runner

import (
	"fmt"
	"strings"
)

// Outcome is what one check came to.
type Outcome string

const (
	OutcomePass    Outcome = "pass"
	OutcomeFail    Outcome = "fail"
	OutcomeSkip    Outcome = "skip"
	OutcomeFinding Outcome = "finding"
)

// Check is one recorded result, in the form --report writes and the regression
// gate compares.
//
// ID names the question, not the answer: "acp/resume.context" is the same id
// whether the resumed session carried the earlier turn or lost it. It is
// "<section>/<check>", where the section is the phase that asked (setup,
// headless, interactive, acp, sdk) and the check is fixed at the call site.
// Nothing measured goes into it — no codenames, session ids, ports, counts or
// durations — so two runs of the same agent produce the same ids.
//
// Answer is the stable variant of an outcome where one outcome covers
// different behaviour: two passes of the in-flight probe mean different
// things when the agent exited on an unanswered approval and when it waited.
// It is empty when the outcome says everything. Message is the human line,
// with everything measured in it, and is never compared.
type Check struct {
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
	Answer  string  `json:"answer,omitempty"`
	Message string  `json:"message"`
}

// Key is what the regression gate compares: the outcome, and the answer when
// there is one ("pass", "finding:not-reached").
func (c Check) Key() string {
	if c.Answer == "" {
		return string(c.Outcome)
	}
	return string(c.Outcome) + ":" + c.Answer
}

// record files a check under the current section. The call site's id is
// "<check>[:<answer>]". A check asked twice in one section (a re-raised
// approval per request) gets "#2", "#3" in the order asked.
func (r *TestRunner) record(o Outcome, id, msg string) {
	id, answer, _ := strings.Cut(id, ":")
	section := r.section
	if section == "" {
		section = "setup"
	}
	full := section + "/" + id
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	r.seen[full]++
	if n := r.seen[full]; n > 1 {
		full = fmt.Sprintf("%s#%d", full, n)
	}
	r.result.Checks = append(r.result.Checks, Check{ID: full, Outcome: o, Answer: answer, Message: msg})
}

// slug turns a fixed label ("user text", "during the load") into an id part.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// probeID is the id prefix for a probe helper shared by several probes, which
// is told its caller by the label it prints.
func probeID(label string) string {
	switch label {
	case "session/load", "resume":
		return "resume"
	case "compaction":
		return "compact"
	case "deferred tools":
		return "deferred"
	}
	return slug(label)
}

// hookID names a hook-event check: "hook.<event>", or "belt-hook.<event>" for
// the belt hooks' own events.
func hookID(prefix, label string) string {
	if strings.TrimSpace(prefix) == "" {
		return "hook." + label
	}
	return slug(prefix) + "-hook." + label
}
