# droid: UserPromptSubmit does not fire over ACP or plain `exec`, and PreCompact only fires in the TUI

**Version:** droid 0.225.2 (first measured on 0.217, unchanged since)
**Kind:** feature request / parity gap, not a crash
**Impact:** a hook that adds context to the user's prompt works in the TUI and
not when droid is driven by an editor over ACP, which is how Zed, JetBrains and
other ACP clients run it. Nothing reports the difference: the hook is configured,
the turn succeeds, and the context is quietly missing.

## What fires where

Same hook config, same prompt, same mock model, one run per mode:

| hook | `droid exec` | `droid exec -o stream-jsonrpc` | `droid exec --output-format acp` | TUI |
|------|:-----------:|:------------------------------:|:--------------------------------:|:---:|
| SessionStart | yes | yes | yes | yes |
| **UserPromptSubmit** | **no** | yes | **no** | yes |
| PreToolUse | yes | yes | yes | yes |
| PostToolUse | yes | yes | yes | yes |
| Stop | yes | yes | yes | yes |
| **PreCompact** | **no** | **no** | **no** | yes |

## Why, from the 0.217 source

- **UserPromptSubmit.** `executeUserPromptSubmitHooks` is called only on the
  JSON-RPC `processUserMessage` path, which is what the TUI and
  `-o stream-jsonrpc` go through. Plain `exec` (`-o text`, `json`,
  `stream-json`) runs the turn through a different runner that never calls it,
  and so does the ACP agent (`--output-format acp`, and each `acp-daemon` child),
  which calls the turn runner directly.
- **PreCompact.** Fires only from the TUI's manual compaction. `exec` in every
  output format never compacts, so the hook has nothing to fire on.

The second is arguably correct — no compaction, no hook. The first is the one
that matters: prompt submission happens in every mode, and the hook runs in two
of four.

## Why it matters

UserPromptSubmit is the hook that lets a plugin see the prompt and add context
before the model does — memory, retrieval, project rules. Over ACP it is the
only one of the five per-turn hooks that is missing, so an integration built and
tested in the TUI silently loses its context when the same user opens droid in
an editor. The fix looks like routing ACP and plain `exec` prompts through the
same submission step the JSON-RPC path already uses.

## Reproduction

```
git clone https://github.com/belt-sh/harness-test && cd harness-test/tests
docker compose run --build test --harness droid --mode acp
docker compose run --build test --harness droid --mode both   # headless + TUI
docker compose run --build test --harness droid --mode sdk
```

Each run installs a hook for every event that writes a marker when it fires,
drives one turn against a mock model, and reports which markers appeared.
