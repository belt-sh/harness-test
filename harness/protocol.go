package harness

// Agent protocol facts that belt needs at runtime, kept next to the
// registry so the runner verifies them and belt consumes them from the
// same source: how a command hook hands context back to the agent, and
// why a harness cannot be tested in a given mode.

import (
	"encoding/json"
	"strings"
)

// ContextChannel says how a command hook's stdout becomes model context.
type ContextChannel int

const (
	ContextNone            ContextChannel = iota // event fires but stdout is ignored (exit code only)
	ContextPlainText                             // stdout text is appended to context as-is
	ContextHookSpecific                          // {"hookSpecificOutput":{"hookEventName":E,"additionalContext":T}} (Claude family)
	ContextAdditionalCamel                       // {"additionalContext":T} (Copilot)
	ContextAdditionalSnake                       // {"additional_context":T} (Cursor)
	ContextKey                                   // {"context":T} (Hermes)
	ContextPlugin                                // TS plugin/extension: context set in code, no stdout channel
)

// IsPlain reports whether the hook prints the text as-is rather than wrapping
// it in an envelope. ContextPlugin is plain because the generated plugin file,
// not the hook's stdout, is what hands the text to the agent.
func (c ContextChannel) IsPlain() bool {
	return c == ContextPlainText || c == ContextPlugin
}

func (c ContextChannel) String() string {
	switch c {
	case ContextPlainText:
		return "plain"
	case ContextHookSpecific:
		return "hookSpecificOutput"
	case ContextAdditionalCamel:
		return "additionalContext"
	case ContextAdditionalSnake:
		return "additional_context"
	case ContextKey:
		return "context"
	case ContextPlugin:
		return "plugin"
	default:
		return "none"
	}
}

// HookContext is the per-event context channel of a harness.
//
// A map rather than two named fields: as a struct it could only describe
// SessionStart and PromptSubmit, so an agent whose context channels are the
// tool and stop hooks — grok is one — was recorded as having none at all,
// which is a different claim from the one the evidence supports.
type HookContext map[HookEvent]ContextChannel

// hookContexts is the verified table (runner check "prompt hook context
// reached the model", Docker, 2026-09). Sources: each agent's hook docs or
// bundle; goose hooks are observation-only; windsurf hooks are exit-code
// only; kiro hooks did not fire on kiro-cli 2.21.
var hookContexts = map[string]HookContext{
	"claude":  {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"codex":   {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"copilot": {SessionStart: ContextNone, PromptSubmit: ContextAdditionalCamel},
	"cursor":  {SessionStart: ContextAdditionalSnake, PromptSubmit: ContextAdditionalSnake},
	"droid":   {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"gemini":  {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"goose":   {SessionStart: ContextNone, PromptSubmit: ContextNone},
	// grok reads UserPromptSubmit stdout only for a block decision; its docs:
	// "an allowing hook's stdout / additionalContext is discarded rather than
	// added as context", and SessionStart stdout "is ignored". Only
	// PreToolUse, PostToolUse and Stop can add additionalContext.
	"grok":     {SessionStart: ContextNone, PromptSubmit: ContextNone},
	"hermes":   {SessionStart: ContextNone, PromptSubmit: ContextKey},
	"kilo":     {PromptSubmit: ContextPlugin},
	"kimi":     {SessionStart: ContextPlainText, PromptSubmit: ContextPlainText},
	"kiro":     {SessionStart: ContextNone, PromptSubmit: ContextPlainText},
	"omp":      {PromptSubmit: ContextPlugin},
	"opencode": {PromptSubmit: ContextPlugin},
	"pi":       {PromptSubmit: ContextPlugin},
	"qwen":     {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"windsurf": {SessionStart: ContextNone, PromptSubmit: ContextNone},
}

// ContextChannelFor returns how a hook on beltEvent returns context for the
// named agent. An event with no entry has no channel, which is the honest
// answer for the events this suite has not measured.
func ContextChannelFor(name, beltEvent string) ContextChannel {
	ch, ok := hookContexts[name][HookEvent(beltEvent)]
	if !ok {
		return ContextNone
	}
	return ch
}

// HookStdout renders text as the stdout payload a hook must print for the
// agent to pick it up as context. ok is false when the event has no
// context channel for that agent (print nothing, rely on rules/skills).
func HookStdout(name, beltEvent, text string) (payload string, ok bool) {
	h, known := All[name]
	if !known {
		return "", false
	}
	eventName := HookEvent(beltEvent).AgentName(h)
	ch := ContextChannelFor(name, beltEvent)
	if ch.IsPlain() {
		return text, true
	}
	switch ch {
	case ContextHookSpecific:
		return jsonObj(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": eventName, "additionalContext": text}}), true
	case ContextAdditionalCamel:
		return jsonObj(map[string]any{"additionalContext": text}), true
	case ContextAdditionalSnake:
		return jsonObj(map[string]any{"additional_context": text}), true
	case ContextKey:
		return jsonObj(map[string]any{"context": text}), true
	}
	return "", false
}

// HookContextText is the inverse of HookStdout: it reads back the text an
// agent's hook channel carries. ok is false when payload is not that channel's
// shape, which is how a caller tells "belt printed the wrong envelope" from
// "belt printed nothing".
//
// It lives next to HookStdout so the shapes are written once. A second copy in
// the test runner only knew four of them and silently ignored the rest.
func HookContextText(name, beltEvent, payload string) (string, bool) {
	ch := ContextChannelFor(name, beltEvent)
	if ch.IsPlain() {
		return payload, !strings.HasPrefix(strings.TrimSpace(payload), "{")
	}
	switch ch {
	case ContextHookSpecific:
		return jsonField(payload, "hookSpecificOutput", "additionalContext")
	case ContextAdditionalCamel:
		return jsonField(payload, "", "additionalContext")
	case ContextAdditionalSnake:
		return jsonField(payload, "", "additional_context")
	case ContextKey:
		return jsonField(payload, "", "context")
	}
	return "", false
}

// AnyHookContext reads the text out of any agent's envelope shape. A caller
// uses it to notice that text meant for the model is itself an envelope.
func AnyHookContext(payload string) (string, bool) {
	for _, try := range []struct{ outer, inner string }{
		{"hookSpecificOutput", "additionalContext"},
		{"", "additionalContext"},
		{"", "additional_context"},
		{"", "context"},
	} {
		if text, ok := jsonField(payload, try.outer, try.inner); ok {
			return text, true
		}
	}
	return "", false
}

// jsonField decodes the first JSON value in payload and returns a string
// field, optionally nested one level. Agents print progress lines and log
// lines around the envelope, so decoding stops at the first value that has
// the field rather than insisting the whole of stdout is JSON.
func jsonField(payload, outer, inner string) (string, bool) {
	i := strings.Index(payload, "{")
	if i < 0 {
		return "", false
	}
	dec := json.NewDecoder(strings.NewReader(payload[i:]))
	for {
		var obj map[string]any
		if dec.Decode(&obj) != nil {
			return "", false
		}
		if outer != "" {
			nested, _ := obj[outer].(map[string]any)
			obj = nested
		}
		if text, _ := obj[inner].(string); text != "" {
			return text, true
		}
	}
}

func jsonObj(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// SkipReason is why a harness is not run in a mode. Typed so callers can
// tell "cannot be tested" from "failed".
type SkipReason string

const (
	SkipNone    SkipReason = ""
	SkipIDEOnly SkipReason = "ide-only"     // no CLI to drive (windsurf)
	SkipNoMode  SkipReason = "no-such-mode" // harness has no command for this mode
)

// SkipFor reports whether the harness must be skipped for mode
// ("headless", "interactive", "acp", "sdk", "both"). The runner enables
// the MITM intercept itself for NeedsIntercept harnesses, so that is not
// a skip. The detail is human-readable.
func (h Harness) SkipFor(mode string) (SkipReason, string) {
	hasHeadless := len(h.HeadlessCmd) > 0
	hasInteractive := len(h.InteractiveCmd) > 0
	hasACP := len(h.ACPCmd) > 0
	hasSDK := len(h.SDKCmd) > 0
	if !hasHeadless && !hasInteractive && !hasACP && !hasSDK {
		return SkipIDEOnly, h.Name + " is an IDE extension with no CLI; hooks and rules are installed from docs, not verified"
	}
	var want bool
	switch strings.ToLower(mode) {
	case "headless":
		want = hasHeadless
	case "interactive":
		want = hasInteractive
	case "acp":
		want = hasACP
	case "sdk":
		want = hasSDK
	default:
		want = hasHeadless || hasInteractive
	}
	if !want {
		return SkipNoMode, h.Name + " has no " + mode + " mode"
	}
	return SkipNone, ""
}

// EventKnownMissing returns the recorded reason why a hook event does not fire
// for this harness in the given mode, and whether one is recorded.
func (h Harness) EventKnownMissing(mode, tag string) (string, bool) {
	reason, ok := h.KnownIssues[mode+":event:"+tag]
	return reason, ok
}
