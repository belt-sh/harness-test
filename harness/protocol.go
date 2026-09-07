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
type HookContext struct {
	SessionStart ContextChannel
	PromptSubmit ContextChannel
}

// hookContexts is the verified table (runner check "prompt hook context
// reached the model", Docker, 2026-09). Sources: each agent's hook docs or
// bundle; goose hooks are observation-only; windsurf hooks are exit-code
// only; kiro hooks did not fire on kiro-cli 2.21.
var hookContexts = map[string]HookContext{
	"claude":   {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"codex":    {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"copilot":  {SessionStart: ContextNone, PromptSubmit: ContextAdditionalCamel},
	"cursor":   {SessionStart: ContextAdditionalSnake, PromptSubmit: ContextAdditionalSnake},
	"droid":    {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"gemini":   {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"goose":    {SessionStart: ContextNone, PromptSubmit: ContextNone},
	"grok":     {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"hermes":   {SessionStart: ContextNone, PromptSubmit: ContextKey},
	"kilo":     {SessionStart: ContextPlugin, PromptSubmit: ContextPlugin},
	"kimi":     {SessionStart: ContextPlainText, PromptSubmit: ContextPlainText},
	"kiro":     {SessionStart: ContextNone, PromptSubmit: ContextPlainText},
	"omp":      {SessionStart: ContextPlugin, PromptSubmit: ContextPlugin},
	"opencode": {SessionStart: ContextPlugin, PromptSubmit: ContextPlugin},
	"pi":       {SessionStart: ContextPlugin, PromptSubmit: ContextPlugin},
	"qwen":     {SessionStart: ContextHookSpecific, PromptSubmit: ContextHookSpecific},
	"windsurf": {SessionStart: ContextNone, PromptSubmit: ContextNone},
}

// ContextChannelFor returns how a hook on beltEvent ("session-start" or
// "user-prompt-submit") returns context for the named agent.
func ContextChannelFor(name, beltEvent string) ContextChannel {
	hc, ok := hookContexts[name]
	if !ok {
		return ContextNone
	}
	switch beltEvent {
	case "session-start":
		return hc.SessionStart
	case "user-prompt-submit":
		return hc.PromptSubmit
	}
	return ContextNone
}

// HookStdout renders text as the stdout payload a hook must print for the
// agent to pick it up as context. ok is false when the event has no
// context channel for that agent (print nothing, rely on rules/skills).
func HookStdout(name, beltEvent, text string) (payload string, ok bool) {
	h, known := All[name]
	if !known {
		return "", false
	}
	eventName := ""
	switch beltEvent {
	case "session-start":
		eventName = h.Events.SessionStart
	case "user-prompt-submit":
		eventName = h.Events.PromptSubmit
	}
	switch ContextChannelFor(name, beltEvent) {
	case ContextPlainText:
		return text, true
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
