# harness-test

Conformance test suite for coding agent CLIs. Tests 16 agents across 4 control modes, 4 API formats, and 7 hook formats.

Verifies: prompt/response, tool calls, hook lifecycle, streaming, model selection, context injection.

Built for [belt.sh](https://belt.sh) — connect your agent to skills, knowledge, and tools.

[![Build](https://github.com/belt-sh/harness-test/actions/workflows/build.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/build.yml)
[![Nightly](https://github.com/belt-sh/harness-test/actions/workflows/nightly.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/nightly.yml)

[![claude](https://github.com/belt-sh/harness-test/actions/workflows/claude.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/claude.yml)
[![codex](https://github.com/belt-sh/harness-test/actions/workflows/codex.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/codex.yml)
[![copilot](https://github.com/belt-sh/harness-test/actions/workflows/copilot.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/copilot.yml)
[![cursor](https://github.com/belt-sh/harness-test/actions/workflows/cursor.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/cursor.yml)
[![droid](https://github.com/belt-sh/harness-test/actions/workflows/droid.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/droid.yml)
[![gemini](https://github.com/belt-sh/harness-test/actions/workflows/gemini.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/gemini.yml)
[![goose](https://github.com/belt-sh/harness-test/actions/workflows/goose.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/goose.yml)
[![grok](https://github.com/belt-sh/harness-test/actions/workflows/grok.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/grok.yml)
[![hermes](https://github.com/belt-sh/harness-test/actions/workflows/hermes.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/hermes.yml)
[![kilo](https://github.com/belt-sh/harness-test/actions/workflows/kilo.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/kilo.yml)
[![kimi](https://github.com/belt-sh/harness-test/actions/workflows/kimi.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/kimi.yml)
[![kiro](https://github.com/belt-sh/harness-test/actions/workflows/kiro.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/kiro.yml)
[![omp](https://github.com/belt-sh/harness-test/actions/workflows/omp.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/omp.yml)
[![opencode](https://github.com/belt-sh/harness-test/actions/workflows/opencode.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/opencode.yml)
[![pi](https://github.com/belt-sh/harness-test/actions/workflows/pi.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/pi.yml)
[![qwen](https://github.com/belt-sh/harness-test/actions/workflows/qwen.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/qwen.yml)

## Compatibility matrix

<!-- Updated 2026-08-29. Versions from latest CI run in Docker. -->

| Agent | Version | Headless | Interactive | ACP | SDK | Hook Format | API |
|-------|---------|:--------:|:-----------:|:---:|:---:|-------------|-----|
| [Claude Code](https://github.com/anthropics/claude-code) | 2.1.x | ✅ | ✅ | — | ✅¹ | JSONNested | Anthropic |
| [Codex](https://github.com/openai/codex) | 1.x | ✅ | ✅ | — | ✅² | JSONNested | Responses |
| [Copilot](https://github.com/github/copilot) | 1.0.x | ✅ | ✅ | ✅ | — | JSONCopilot | OpenAI |
| [Cursor](https://cursor.com/docs/cli) | 2026.09 | ✅ | ✅ | — | — | JSONFlat | Cursor⁵ |
| [Droid](https://docs.factory.ai/cli) | 0.208.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | 0.57.x | ✅ | ✅ | ✅ | — | JSONNested | Gemini |
| [Goose](https://github.com/block/goose) | 1.50.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |
| [Grok](https://x.ai/grok-build) | 1.0.x | ✅ | ✅ | ✅ | — | JSONNested | Responses |
| [Hermes](https://github.com/hermes-ai/hermes-agent) | 0.19.x | ✅ | ✅ | ✅ | — | YAML | OpenAI |
| [Kilo](https://github.com/nicepkg/kilo) | 7.5.x | ✅ | ✅ | ✅ | — | TSPlugin | Responses |
| [Kimi Code](https://github.com/nicepkg/gpt-runner) | 1.49.x | ✅ | ✅ | ✅ | — | TOML | OpenAI |
| [Kiro](https://kiro.dev) | 2.21.x | ✅ | ✅ | ✅ | — | JSONKiro | OpenAI |
| [Oh My Pi](https://omp.sh) | 18.x | ✅ | ✅ | ✅ | ✅⁴ | TSExtension | OpenAI |
| [OpenCode](https://github.com/nicepkg/opencode) | 1.18.x | ✅ | ✅ | ✅ | — | TSPlugin | Responses |
| [Pi](https://github.com/earendil-works/pi) | 0.x | ✅ | ✅ | — | ✅³ | TSExtension | OpenAI |
| [Qwen Code](https://github.com/nicepkg/qwen-code) | 0.22.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |

**16/16** headless · **12/16** ACP · **4/16** SDK · **33 mode-tests in CI**

¹ `claude -p --output-format stream-json` — claude's own streaming protocol, not ACP.
² `codex exec --experimental-json` — JSONL event stream over stdout.
³ `pi --mode json` — structured JSONL output (provider URL not overridable).
⁴ `omp --mode json` — Oh My Pi is a Pi fork (bun runtime) with native ACP, plugins, and multi-model roles.
⁵ Cursor's `agent` CLI has no BYOK endpoint: the mock speaks its Connect-protobuf `agent.v1.AgentService` (RunSSE + BidiAppend, schemas read out of the CLI bundle). Server config pins the stream to HTTP/1.1. Headless fires sessionStart and the tool hooks; the TUI also fires beforeSubmitPrompt and stop.

### Instruction files

Each agent loads instruction files into its system prompt at user scope and at project scope. The runner writes a distinct codename into each (`INSTR-USER-<AGENT>-<ts>`, `INSTR-PROJ-<AGENT>-<ts>`, marker-wrapped, restored afterwards) and checks per file that it reaches the mock model, so a wrong path fails the run. Verified 2026-09 for all 15 CLI agents (kiro in interactive mode with `--intercept`); cursor is verified through its `agent` CLI (project scope; the CLI reads `.cursor/rules` and `AGENTS.md`, and has no global rules file). windsurf is IDE-only and cannot be driven by the runner; its hook config (`~/.codeium/windsurf/hooks.json`, snake_case events) follows the vendor docs and is unverified.

| Agent | User scope (`~/`) | Project scope |
|-------|-------------------|---------------|
| claude | `.claude/CLAUDE.md` | `CLAUDE.md` |
| codex | `.codex/AGENTS.md` | `AGENTS.md` |
| copilot | `.copilot/instructions/belt.instructions.md` | `.github/instructions/belt.instructions.md` |
| cursor | — (settings UI) | `.cursor/rules/belt.mdc` ✅ |
| droid | `.factory/AGENTS.md` | `AGENTS.md` |
| gemini | `.gemini/GEMINI.md` | `GEMINI.md` |
| goose | `.config/goose/.goosehints` | `.goosehints` |
| grok | `.grok/AGENTS.md` | `AGENTS.md` |
| hermes | — (SOUL.md is identity only) | `AGENTS.md` |
| kilo | `.kilocode/rules/belt.md` | `AGENTS.md` |
| kimi | `.kimi-code/AGENTS.md` | `AGENTS.md` |
| kiro | `.kiro/steering/belt.md` | `.kiro/steering/belt.md` |
| omp | `.omp/agent/AGENTS.md` | `AGENTS.md` |
| opencode | `.config/opencode/AGENTS.md` | `AGENTS.md` |
| pi | `.pi/agent/AGENTS.md` | `AGENTS.md` |
| qwen | `.qwen/QWEN.md` | `QWEN.md` |
| windsurf | `.codeium/windsurf/memories/global_rules.md` | `.windsurf/rules/belt.md` |

Registry: `instructionFiles`, `skillsDirs`, and `configDirEnvs` in `harness/registry.go`. The belt CLI imports this package and derives its install targets (skills dir, hooks path, instruction file) from it; there is no second copy.

### Hook context protocol

`harness/protocol.go` records, per agent and event, how a command hook hands context back to the model, and `harness.HookStdout(agent, event, text)` renders it. The runner's mock prompt hook prints that payload and the "prompt hook context reached the model" check verifies it; belt's `plugin hook` prints its suggestions through the same function, so the two cannot drift.

| Channel | Agents |
|---------|--------|
| `{"hookSpecificOutput":{"hookEventName":…,"additionalContext":…}}` | claude, codex, droid, gemini, qwen |
| `{"additionalContext":…}` | copilot |
| `{"additional_context":…}` | cursor (the prompt hook runs when the backend requests it; see below) |
| `{"context":…}` | hermes |
| plain stdout | kimi, kiro |
| in-plugin (TS) | kilo, omp, opencode, pi |
| none | goose (hooks are observation-only), grok (prompt and session-start stdout is read only for a block decision; its tool and Stop hooks can add context), windsurf (exit code only) |

Kiro hooks are part of the agent config, not `.kiro/hooks/*.json` (those are Kiro IDE documents; kiro-cli never runs them). `Install("kiro")` merges an `agentSpawn`/`userPromptSubmit`/`preToolUse`/`postToolUse`/`stop` hooks object into belt's own agent, `~/.kiro/agents/belt.json`, and selects it with `chat.defaultAgent` in `~/.kiro/settings/cli.json` (project scope: `.kiro/agents/belt.json` and `.kiro/settings/cli.json`). kiro-cli 2.21 has two engines, and the V1 engine ignores a `kiro_default.json` override and runs its built-in agent, so the override earlier belt versions installed never fired on V1; `chat.defaultAgent` works on both, and install strips belt's entries from a leftover `kiro_default.json`. A default agent the user chose stays the default (`belt plugin doctor` reports it). belt's agent stands in for the built-in one, and a config without `tools` has no tools at all, so the scaffold carries `"tools": ["*"]` and `"includeMcpJson": true`; with it, the requests carry the same steering, README and skill descriptions as the built-in agent's. An existing file keeps its prompt, tools, and own hooks, and uninstall removes only belt's entries and the `chat.defaultAgent` it set.

Which engine runs is not always the user's choice: `kiro-cli chat --no-interactive` starts V1 for 75% of installs and V2 for the rest (the `v2_non_interactive` rollout embedded in the binary, `treatment_percent: 25`), so the registry pins headless to V1 with `--agent-engine v1`; the TUI and ACP run V2. Until 2026-09 headless and ACP were switched off with `HooksInHeadless: false` and `HooksInACP: false`, which skipped every check in those modes and printed "does not support ACP mode". Switched on, every ACP hook fired at once.

### Checks fail; a skip needs a reason

A hook event that does not fire fails the run unless `Harness.KnownIssues` records why it cannot fire there, keyed `<mode>:event:<TAG>`:

```go
KnownIssues: map[string]string{
    "headless:event:PRE_COMPACT": "/compact is TUI-only (slash_dispatch.rs); codex exec never auto-compacts",
},
```

The same rule covers the rest of a phase: no requests reaching the mock is always a failure, and a missing stream or a model the agent never asked for fails unless `KnownIssues` records why, keyed `<mode>:streaming` and `<mode>:model`. That last one earned its keep immediately: pi in interactive and qwen over ACP were quietly running someone else's model, `moonshotai/kimi-k2.6` and `qwen3.7-max`, because this registry never passed `--model` in those two modes.

Until 2026-09 every event was a skip, so hooks that stopped firing entirely still passed; that is how kiro sat broken for weeks. Each of the 14 entries was measured in Docker in both hook sources, mock and belt, which agreed on every one.

The measurement and the explanation are different claims, so the table says which it is: a reason that begins `observed only` states what happened and not why. All 8 entries now cite a cause: codex runs `exec`, where `/compact` is only a user message (headless and SDK); droid fires PreCompact only from its TUI's manual compaction and never in `exec` (headless and ACP, droid 0.217 source); droid's ACP agent (`exec --output-format acp`, and every child the ACP daemon spawns) calls its turn runner directly, while `UserPromptSubmit` runs only in the JSON-RPC `processUserMessage` path (droid 0.217 source); and gemini's ACP agent runs tools with `invocation.execute()` directly and fires `SessionStart` only in its non-interactive `main()` (gemini-cli 0.59 source).

Revisiting the table in 2026-09 took it from 14 entries to 8, and every removal was a fault in this harness, not the agent: claude's SDK compaction needed the runner to send `/compact`; gemini's ACP tool calls failed on this driver's invalid permission reply (`{"outcome":"approved"}` instead of `{"outcome":{"outcome":"selected","optionId":...}}`), which hid that gemini's ACP path bypasses tool hooks altogether; Cursor's prompt, stop and compaction hooks need a backend request the mock never sent; and droid's TUI compaction needed a context window on the custom model, the right command (`/compress`, confirmed), the trust dialog out of the way, and the model actually selected. Treat `observed only` as an open question.

Add an entry only after a Docker run shows the agent cannot do it, never to quiet a flaky test, and `TestKnownIssueKeysAreWellFormed` rejects a key whose mode, tag, or event the harness does not have.

### The mock must not steer the agent

Two ways a mock quietly changes what it is measuring, both found on gemini in 2026-09:

- **Answering a request that cannot use the answer.** One turn is routed across models with different toolsets, so the prepared tool call is served only to a request that declares that tool. Served to gemini's small flash toolset it came back "Tool `write_file` not found", nothing ran, and both tool hooks were reported missing.
- **Answering a routing question badly.** Agents ask the model to score a request and pick a tier. `synthFromSchema` answers `responseJsonSchema` requests, and numbers come back at the top of the range, because a low score routes the turn to a cheaper model and a smaller toolset.

### Cursor hooks are requested by the backend

The Cursor CLI runs every hook through one dispatcher that answers a backend request: `ExecServerMessage` field 27 `execute_hook_args { request: ExecuteHookRequest }`, whose oneof names the hook (`pre_compact` 1, `pre_tool_use` 4, `post_tool_use` 5, `before_submit_prompt` 7, `stop` 11), answered by `ExecClientMessage` field 27 `execute_hook_result` (agent.v1 schema, Cursor CLI 2026.09). The mock sends those requests, listed per mode in `Harness.ServerRequestedHooks`: the TUI runs the prompt and stop hooks itself as well, so in interactive mode the mock requests only compaction, while in headless it requests all three. Before this, the mock requested none, and "Cursor never runs the prompt or stop hook in headless" sat in the known-issue table.

What this does and does not show: when Cursor's backend asks for a hook, the CLI runs belt's hook and hands back its `additional_context`. Whether Cursor's real backend asks for the prompt and stop hooks in `agent -p` is not something a mock can answer; nobody here has seen its traffic.

### The runner must not decide the outcome either

- **Waiting for the answer.** The interactive runner used to wait for any of a list of words and then kill the session three seconds later. The list included `codename`, which is in the prompt, and `build`, which is in Cursor's empty input box, so it matched the question as soon as the TUI drew. It held only because mock hooks finish in milliseconds; belt's take seconds. It now waits until the mock has served an answer (`MockServer.AnswersServed`) and then until neither the request log nor the hook log has changed for four seconds.
- **Onboarding screens.** The dismissal loop matched patterns against all output so far, so once a dialog had appeared it pressed Enter on every pass, up to fifteen times. It now handles the earliest dialog in output it has not yet consumed and moves past just that dialog's text, because agents draw several screens in one burst (claude's API-key question and "Press Enter to continue"; skipping to the end of the buffer left claude on the second screen for 90 seconds). A `Required` dismissal (droid's and kimi's "Trust this folder?") is waited for instead of given up on once the splash has drawn, and the runner waits for the screen to stop changing before it types — a prompt typed into a startup screen is lost.
- **Typing.** pi-tui (kimi, pi, omp) reads a fast run of keystrokes as a paste and turns an Enter within 120 ms of it into a newline. The runner sent Enter 50 ms after the text, so kimi's prompt sat in the composer until the next typed line, `/exit`, submitted both: kimi's model was sent "…codename.\n/exit" on every interactive run and `/exit` never ran. The runner now pauses 250 ms before Enter.
- **Droid's TUI**, which the model check exposed, had four problems of the harness's making: no model on the session (above), a custom model without `maxContextLimit`, so "Context Usage — Failed to load" and compaction could not run; `/compact`, which droid 0.217 maps to the Context Usage panel (`/compress` compacts, behind a confirmation the runner now accepts); and the trust dialog swallowing the typed prompt.
- **The model check.** It searched the whole request body, so any mention of the model passed, and the registry had grown prefix entries (`gemini-3`, `grok-4`, `claude-`) that accepted a whole family. It now reads only the model field the mock parsed, or a URL path segment (gemini), and matches exactly. Made strict, it found six harnesses not running the model they claimed: droid's TUI has no `-m` flag, so `-m mock-model` became the user's prompt and the session ran Factory's default; gemini was never given a model in headless or ACP and chose its own, and maps `gemini-2.5-flash` to `gemini-3.5-flash` when it is; grok takes its model from the backend's settings, where the mock serves `mock-model`, not the `grok-3-mini` on record; kiro sent an empty `modelId`, and the mock recorded a constant `kiro-default` that the check then compared with itself; kilo and opencode send the bare model for `openai/gpt-4o-mini`. Command templates (`{{.Model}}`) are now expanded in the command as well as its args; kiro had received the literal text.

### Codenames

Each check writes a distinct codename into the model's context and looks for it in the recorded requests: `HOOK-<AGENT>-<ts>` from the prompt hook, `INSTR-USER-<AGENT>-<ts>` and `INSTR-PROJ-<AGENT>-<ts>` from the instruction files. The prefixes must stay distinct — a bare `<AGENT>-<ts>` hook code is a substring of the instruction codes, so the injection check passed whenever the instruction file loaded and hid three real failures (2026-09).

### Pushing

`git config core.hooksPath .githooks` once per clone; the pre-push hook runs build, vet and the unit tests and refuses a push that fails them. CI runs every harness on push, so a broken build otherwise turns all 17 badges red until the next fix.

### Real belt hooks (`--hooks belt`)

`--hooks belt` installs belt's actual hook commands through `harness.Install` and checks the events belt logs. `tests/fetch-belt.sh` downloads the released CLI from `dist.inference.sh` into `tests/belt` (`BELT_VERSION=vX.Y.Z` pins one); the Docker build copies it in. CI runs every harness in both mock and belt mode on push and nightly. Run all agents non-root (Claude Code refuses to skip permissions as root) and kiro separately with `--user root --intercept`.

`Harness.KnownIssues` records agent defects the runner cannot work around, keyed by `<mode>:<check>`; a failing check with a known issue reports as a skip that carries the note. None recorded today.

`harness.SkipFor(mode)` gives a typed reason (`ide-only`, `no-such-mode`) when a harness cannot be run; `--harness all` reports those in the summary instead of failing.

### Control modes

| Mode | Transport | What it tests |
|------|-----------|--------------|
| **Headless** | CLI args + stdout | `agent -p "prompt"` — fast, deterministic |
| **Interactive** | PTY terminal | Full TUI flow: onboarding, typing, `/compact`, exit |
| **ACP** | JSON-RPC over stdio | [Agent Client Protocol](https://agentclientprotocol.com) — programmatic session control |
| **SDK** | Agent-specific stdio | Claude's `--output-format stream-json` protocol |

### ACP protocol support

The ACP driver implements ACP v1 with a handler registry:

| Method | Direction | Handler |
|--------|-----------|---------|
| `initialize` | client → agent | Capability exchange |
| `session/new` | client → agent | Create session (cwd + mcpServers) |
| `session/prompt` | client → agent | Send prompt (fire-and-forget) |
| `session/update` | agent → client | Stream content chunks |
| `session/request_permission` | agent → client | Auto-approve |
| `fs/write_text_file` | agent → client | Write files to disk |
| `fs/read_text_file` | agent → client | Read files from disk |
| `elicitation/create` | agent → client | Auto-confirm |
| `session/close` | client → agent | End session |

## Use cases

### Plugin/hooks testing

You have a product (like [belt](https://belt.sh), an MCP server, or a custom hook system) and need to verify it works inside multiple agents.

```bash
docker compose run test --harness all
docker compose run test --harness claude,codex,grok
```

### ACP conformance testing

You're building an editor or app that controls agents via ACP (like [Zed](https://zed.dev), [T3 Code](https://github.com/pingdotgg/t3code)).

```bash
harness-test --harness copilot,grok,opencode --mode acp
```

### Agent development

You're building a new agent CLI and want to verify your hook/API implementation.

```go
// Add to harness/registry.go
"myagent": {
    Name: "myagent", Binary: "myagent",
    APIFormat: OpenAI,
    HookFormat: JSONNested,
    Events: Events{PromptSubmit: "UserPromptSubmit", Stop: "Stop"},
    HeadlessCmd: []string{"myagent", "-p"},
    HooksInHeadless: true,
    ACPCmd: []string{"myagent", "--acp"},
    HooksInACP: true,
},
```

## Quick start

```bash
go build -o harness-test .

# Docker (recommended)
cd tests && docker compose run test --harness claude

# List harnesses
harness-test --list

# Detect installed agents
harness-test --detect
```

### CI

```yaml
jobs:
  harness:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        harness: [claude, codex, copilot, grok]
    steps:
      - uses: actions/checkout@v4
      - run: |
          docker build -f tests/Dockerfile -t harness-test .
          docker run --rm harness-test --harness ${{ matrix.harness }} --mode headless
```

### Mock server

```bash
harness-test --server
# Speaks all 4 API formats on one port:
# POST /v1/chat/completions   (OpenAI)
# POST /v1/responses          (Responses)
# POST /v1/messages           (Anthropic)
# POST /v1beta/...            (Gemini)
```

## What it verifies

| Check | What it tests |
|-------|--------------|
| **Prompt/response** | Agent sends a request, mock server responds, agent produces output |
| **Hook events** | Lifecycle hooks fire (SessionStart, PromptSubmit, PreToolUse, PostToolUse, Stop, PreCompact) |
| **API requests** | Mock server received requests in the correct format |
| **Streaming** | Agent uses SSE streaming |
| **Model selection** | Correct model name in API requests |
| **Tool calls** | Agent makes tool calls and sends results back |
| **Version** | Agent binary version is detected and reported |

## Hook formats

| Format | Agents | Config |
|---|---|---|
| JSONNested | claude, codex, grok, droid, goose, qwen, gemini | `settings.json` / `hooks.json` |
| JSONCopilot | copilot | `hooks.json` (v1, bash field) |
| TOML | kimi | `config.toml` |
| YAML | hermes | `config.yaml` |
| TSExtension | pi | `.ts` with `pi.on(event, ...)` |
| TSPlugin | opencode, kilo | `.ts` exporting plugin object |

## Architecture

```
harness-test
├── main.go           CLI entry point
├── harness/
│   ├── harness.go    Harness type definitions
│   ├── registry.go   13 agent configs (pure data)
│   ├── detect.go     5-probe detection
│   └── install.go    Hook config generation
├── runner/
│   ├── runner.go     Test orchestrator (install → config → hooks → run → verify)
│   ├── driver.go     Driver interface
│   ├── acp.go        ACP driver (JSON-RPC, handler registry)
│   ├── protocol.go   JSON-RPC + ACP message types
│   ├── pty.go        PTY driver (terminal sessions)
│   └── checks.go     Verification checks
└── server/
    ├── server.go     Mock LLM server
    ├── chat.go       OpenAI Chat Completions
    ├── responses.go  OpenAI Responses
    ├── anthropic.go  Anthropic Messages
    ├── gemini.go     Gemini generateContent
    └── types.go      Shared types
```

Each harness is a pure data struct — no per-harness code. Adding a new agent means adding one entry to the registry.

## License

MIT
