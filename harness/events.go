package harness

// HookEvent is one of belt's hook events, named as belt names it. It is the
// single vocabulary for a thing that used to be spelled three ways — belt's
// name, the agent's name for the same moment, and the suite's log tag — and
// mapped between them by hand in ten places. Adding an event is now one entry
// in hookEvents.
type HookEvent string

const (
	SessionStart HookEvent = "session-start"
	PromptSubmit HookEvent = "user-prompt-submit"
	PreToolUse   HookEvent = "pre-tool-use"
	PostToolUse  HookEvent = "post-tool-use"
	Stop         HookEvent = "stop"
	PreCompact   HookEvent = "pre-compact"
)

// hookEvents is the canonical order, used wherever hooks are generated or
// checked. tag is what the suite's mock hooks write to their log; agentName
// reads the harness's own name for the event.
var hookEvents = []struct {
	Event     HookEvent
	Tag       string
	Tool      bool // fires around a tool call, so formats with matchers need one
	agentName func(Events) string
}{
	{SessionStart, "SESSION_START", false, func(e Events) string { return e.SessionStart }},
	{PromptSubmit, "PROMPT", false, func(e Events) string { return e.PromptSubmit }},
	{PreToolUse, "PRE_TOOL", true, func(e Events) string { return e.PreToolUse }},
	{PostToolUse, "POST_TOOL", true, func(e Events) string { return e.PostToolUse }},
	{Stop, "STOP", false, func(e Events) string { return e.Stop }},
	{PreCompact, "PRE_COMPACT", false, func(e Events) string { return e.PreCompact }},
}

// HookEvents is belt's hook events in the order hooks are generated in.
func HookEvents() []HookEvent {
	out := make([]HookEvent, 0, len(hookEvents))
	for _, e := range hookEvents {
		out = append(out, e.Event)
	}
	return out
}

// Tag is the label the suite's mock hooks write to their log for this event.
// Empty for an event this package does not know, which cannot happen for the
// constants above and is the honest answer for anything else — the old
// reverse lookup invented a tag by upper-casing whatever it was given.
func (e HookEvent) Tag() string {
	for _, spec := range hookEvents {
		if spec.Event == e {
			return spec.Tag
		}
	}
	return ""
}

// IsToolEvent reports whether the event fires around a tool call, which is
// what decides whether a hook format needs a tool matcher for it.
func (e HookEvent) IsToolEvent() bool {
	for _, spec := range hookEvents {
		if spec.Event == e {
			return spec.Tool
		}
	}
	return false
}

// EventFor is the inverse of Tag.
func EventFor(tag string) (HookEvent, bool) {
	for _, spec := range hookEvents {
		if spec.Tag == tag {
			return spec.Event, true
		}
	}
	return "", false
}

// AgentName is what this harness calls the event, or "" when the agent has no
// such hook.
func (e HookEvent) AgentName(h Harness) string {
	for _, spec := range hookEvents {
		if spec.Event == e {
			return spec.agentName(h.Events)
		}
	}
	return ""
}

// Defined lists the events this harness actually has, in canonical order.
func (h Harness) Defined() []HookEvent {
	var out []HookEvent
	for _, spec := range hookEvents {
		if spec.agentName(h.Events) != "" {
			out = append(out, spec.Event)
		}
	}
	return out
}
