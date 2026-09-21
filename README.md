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

<!-- Updated 2026-09-21. Versions read from the agents themselves in the latest Docker run. -->

| Agent | Version | Headless | Interactive | ACP | SDK | Hook Format | API |
|-------|---------|:--------:|:-----------:|:---:|:---:|-------------|-----|
| [Claude Code](https://github.com/anthropics/claude-code) | 2.1.x | ✅ | ✅ | — | ✅¹ | JSONNested | anthropic |
| [Codex](https://github.com/openai/codex) | 0.155.x | ✅ | ✅ | — | ✅² | JSONNested | oai responses |
| [Copilot](https://github.com/github/copilot) | 1.0.x | ✅ | ✅ | ✅ | — | JSONCopilot | oai completions |
| [Cursor](https://cursor.com/docs/cli) | 2026.09.18 | ✅ | ✅ | — | — | JSONFlat | cursor⁶ |
| [Droid](https://docs.factory.ai/cli) | 0.223.x | ✅ | ✅ | ✅ | ✅⁵ | JSONNested | oai completions |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | 0.60.x | ✅ | ✅ | ✅ | — | JSONNested | gemini |
| [Goose](https://github.com/block/goose) | 1.51.x | ✅ | ✅ | ✅ | — | JSONNested | oai completions |
| [Grok](https://x.ai/grok-build) | 1.0.34 | ✅ | ✅ | ✅ | — | JSONNested | oai responses |
| [Hermes](https://github.com/hermes-ai/hermes-agent) | 0.19.x | ✅ | ✅ | ✅ | — | YAML | oai completions |
| [Kilo](https://github.com/nicepkg/kilo) | 7.7.x | ✅ | ✅ | ✅ | — | TSPlugin | oai responses |
| [Kimi Code](https://github.com/nicepkg/gpt-runner) | 2.0.x | ✅ | ✅ | ✅ | — | TOML | oai completions |
| [Kiro](https://kiro.dev) | 2.22.x | ✅ | ✅ | ✅ | — | JSONKiro | oai completions |
| [Oh My Pi](https://omp.sh) | 18.2.x | ✅ | ✅ | ✅ | ✅⁴ | TSExtension | oai completions |
| [OpenCode](https://github.com/nicepkg/opencode) | 1.18.x | ✅ | ✅ | ✅ | — | TSPlugin | oai responses |
| [Pi](https://github.com/earendil-works/pi) | 0.86.x | ✅ | ✅ | — | ✅³ | TSExtension | oai completions |
| [Qwen Code](https://github.com/nicepkg/qwen-code) | 0.24.x | ✅ | ✅ | ✅ | — | JSONNested | oai completions |

**16/16** headless · **12/16** ACP · **4/16** SDK · **33 mode-tests in CI**

The API column is the wire format the mock has to speak, not the vendor. **oai**
is OpenAI, and the two oai rows are two different OpenAI APIs with different
request and streaming shapes: `oai completions` is Chat Completions
(`POST /v1/chat/completions`) and `oai responses` is the Responses API
(`POST /v1/responses`). The mock implements them separately.

Most agents on `oai completions` are not OpenAI products. Chat Completions is
the format everyone else clones, so kimi, kiro, goose, droid and hermes all
speak it and one implementation serves them. codex, grok, kilo and opencode
chose Responses instead.

¹ `claude -p --output-format stream-json` — claude's own streaming protocol, not ACP.
² `codex exec --experimental-json` — JSONL event stream over stdout.
³ `pi --mode json` — structured JSONL output (provider URL not overridable).
⁴ `omp --mode json` — Oh My Pi is a Pi fork (bun runtime) with native ACP, plugins, and multi-model roles.

⁵ `droid exec -o stream-jsonrpc` — the JSON-RPC worker the TUI drives, and the only exec path that runs the prompt hook. Headless tests plain `exec`, which does not, so both are covered.
⁶ Cursor's `agent` CLI has no BYOK endpoint: the mock speaks its Connect-protobuf `agent.v1.AgentService` (RunSSE + BidiAppend, schemas read out of the CLI bundle). Server config pins the stream to HTTP/1.1. Headless fires sessionStart and the tool hooks; the TUI also fires beforeSubmitPrompt and stop.

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
| plain stdout, read by the plugin | kilo, omp, opencode, pi |
| none | goose (hooks are observation-only), grok (prompt and session-start stdout is read only for a block decision; its tool and Stop hooks can add context), windsurf (exit code only) |

The four plugin agents have no stdout channel of their own: the generated `.ts`
file runs the hook, reads its stdout and hands the text to the agent —
`output.system.push(out)` for the plugin format, a returned `systemPrompt` for
the extension format. Both ends of that were broken until 2026-09 and the suite
could not see it, because `writeHooks` wrote its own TS file with the injection
in it rather than installing belt's. It was testing a plugin no user ever got.
belt's generator ran the command with `execSync` and discarded the result, and
`belt plugin hook` printed its suggestions to stderr for exactly these agents.
Every format now comes from `harness/install.go`.

Fixing both ends was still not enough, and the third fault is the instructive
one. belt reads the prompt from stdin and returns without printing when there
is none, and the generated plugin ran the hook with `stdio: ["ignore", ...]` —
chosen so the hook could not eat the agent's own stdin, which over ACP is the
JSON-RPC stream. belt was handed `/dev/null`, printed nothing, and injected
nothing: the channel correct end to end with nothing flowing through it. The
payload now goes through `execSync`'s `input` option, which supplies its own
pipe and leaves the agent's stdin alone.

Where the prompt comes from differs by format, and was measured rather than
assumed:

| Format | Hook | Carries the prompt? |
|--------|------|---------------------|
| TSExtension (pi, omp) | `before_agent_start` | yes — `{type, prompt, systemPrompt}` |
| TSPlugin (opencode, kilo) | `experimental.chat.system.transform` | no — only `{sessionID, model}` |

opencode and kilo therefore take the prompt from `chat.message`, which carries
`parts: [{type:"text", text}]`. Measured order: `chat.message` once at the
start of a turn, then the transform once per model request within it. That
repetition was a bug of its own — belt ran three times for one prompt and
pushed three copies of the same suggestions into the system prompt — so a
pending flag set in `chat.message` and cleared by the first transform makes it
one invocation per prompt, as every other agent's prompt hook is.

Three guards, because each of these hid the next:
`TestPluginContextChannelIsWiredIntoTheGeneratedFile` fails if a plugin channel
stops reaching its sink; `TestPluginContextHookIsGivenThePrompt` fails if the
command is run without one; and `checkHookInjection` no longer lets
`ContextPlugin` skip — that skip dated from when these agents had no channel at
all, and while it stood the plugin agents could inject nothing and the run
stayed green. The mock's prompt hook now also prints only when stdin carried a
prompt, so it tests the contract belt actually honours instead of a laxer one.

Kiro hooks are part of the agent config, not `.kiro/hooks/*.json` (those are Kiro IDE documents; kiro-cli never runs them). `Install("kiro")` merges an `agentSpawn`/`userPromptSubmit`/`preToolUse`/`postToolUse`/`stop` hooks object into belt's own agent, `~/.kiro/agents/belt.json`, and selects it with `chat.defaultAgent` in `~/.kiro/settings/cli.json` (project scope: `.kiro/agents/belt.json` and `.kiro/settings/cli.json`). kiro-cli 2.21 has two engines, and the V1 engine ignores a `kiro_default.json` override and runs its built-in agent, so the override earlier belt versions installed never fired on V1; `chat.defaultAgent` works on both, and install strips belt's entries from a leftover `kiro_default.json`. A default agent the user chose stays the default (`belt plugin doctor` reports it). belt's agent stands in for the built-in one, and a config without `tools` has no tools at all, so the scaffold carries `"tools": ["*"]` and `"includeMcpJson": true`; with it, the requests carry the same steering, README and skill descriptions as the built-in agent's. An existing file keeps its prompt, tools, and own hooks, and uninstall removes only belt's entries and the `chat.defaultAgent` it set.

Which engine runs is not always the user's choice: `kiro-cli chat --no-interactive` starts V1 for 75% of installs and V2 for the rest (the `v2_non_interactive` rollout embedded in the binary, `treatment_percent: 25`), so the registry pins headless to V1 with `--agent-engine v1`; the TUI and ACP run V2. Until 2026-09 headless and ACP were switched off with `HooksInHeadless: false` and `HooksInACP: false`, which skipped every check in those modes — model, instruction files, API requests, not just the hooks — and printed "does not support ACP mode". Switched on, every ACP hook fired at once. Those four flags are gone: a mode with a command runs, and an agent that fires no hook there says why in `KnownIssues`, which a test checks.

### Checks fail; a skip needs a reason

A hook event that does not fire fails the run unless `Harness.KnownIssues` records why it cannot fire there, keyed `<mode>:event:<TAG>`:

```go
KnownIssues: map[string]string{
    "headless:event:PRE_COMPACT": "/compact is TUI-only (slash_dispatch.rs); codex exec never auto-compacts",
},
```

The same rule covers the rest of a phase: no requests reaching the mock is always a failure, and a missing stream or a model the agent never asked for fails unless `KnownIssues` records why, keyed `<mode>:streaming` and `<mode>:model`. That last one earned its keep immediately: pi in interactive and qwen over ACP were quietly running someone else's model, `moonshotai/kimi-k2.6` and `qwen3.7-max`, because this registry never passed `--model` in those two modes.

Until 2026-09 every event was a skip, so hooks that stopped firing entirely still passed; that is how kiro sat broken for weeks. Each of the 14 entries was measured in Docker in both hook sources, mock and belt, which agreed on every one.

The measurement and the explanation are different claims, so the table says which it is: a reason that begins `observed only` states what happened and not why. All 10 entries now cite a cause: codex runs `exec`, where `/compact` is only a user message (headless and SDK); droid fires PreCompact only from its TUI's manual compaction and never in `exec` (headless and ACP, droid 0.217 source); droid runs `UserPromptSubmit` only in its JSON-RPC `processUserMessage` path, which the TUI's worker and `exec -o stream-jsonrpc` use, while plain `exec` (text, json, stream-json) and the ACP agent call the turn runner directly (headless and ACP, droid 0.217 source; headless used `-o stream-jsonrpc` until 2026-09, which hid the plain-exec case — SDK mode now covers that path deliberately); and gemini's ACP agent runs tools with `invocation.execute()` directly and fires `SessionStart` only in its non-interactive `main()` (gemini-cli 0.59 source).

Revisiting the table in 2026-09 took it from 14 entries to 8, and every removal was a fault in this harness, not the agent: claude's SDK compaction needed the runner to send `/compact`; gemini's ACP tool calls failed on this driver's invalid permission reply (`{"outcome":"approved"}` instead of `{"outcome":{"outcome":"selected","optionId":...}}`), which hid that gemini's ACP path bypasses tool hooks altogether; Cursor's prompt, stop and compaction hooks need a backend request the mock never sent; and droid's TUI compaction needed a context window on the custom model, the right command (`/compress`, confirmed), the trust dialog out of the way, and the model actually selected. Treat `observed only` as an open question.

Add an entry only after a Docker run shows the agent cannot do it, never to quiet a flaky test, and `TestKnownIssueKeysAreWellFormed` rejects a key whose mode, tag, or event the harness does not have.

### The mock must not steer the agent

Two ways a mock quietly changes what it is measuring, both found on gemini in 2026-09:

- **Answering a request that cannot use the answer.** One turn is routed across models with different toolsets, so the prepared tool call is served only to a request that declares that tool. Served to gemini's small flash toolset it came back "Tool `write_file` not found", nothing ran, and both tool hooks were reported missing.
- **Answering a routing question badly.** Agents ask the model to score a request and pick a tier. `synthFromSchema` answers `responseJsonSchema` requests, and numbers come back at the top of the range, because a low score routes the turn to a cheaper model and a smaller toolset.

### Resuming a session: all 12 after a clean close, 11 after a kill

`--probe resume` adds a probe to the ACP phase: run a turn, end the
process, then attach to that same session from a second process with
`session/load`. `--probe resume=kill` ends the first process outright
instead of closing its session, which is what a closed laptop does.

Measured 2026-09 in Docker, both paths:

| Path | Resumed the conversation | Did not | Passes |
|------|--------------------------|---------|--------|
| first process closed its session | all 12 | — | 1 |
| first process killed | 11, every pass | gemini, 7 times in 8 | 3 |

Every resume replayed the user's own turn and carried the earlier turn to the
model. Loads took 15ms to 1.8s, and every session was loadable the instant its
process ended.

The kill path was run three times end to end because one run cannot tell a
property from a coincidence, and the eleven agree with themselves across all
three. The clean-close pass has been run once and its table is one sample.

**gemini does not reliably survive a kill: it resumed once in eight runs.**
Closed, it resumes every time at 0s. Killed, it usually answers `session/load`
with `Internal error` at 0s, 2s, 5s, 10s and 20s — and the retries cannot help,
because the problem is not that the session has not been written yet but that
it never will be. The error carries the reason in its `data`, which is worth
reading rather than reporting the bare `Internal error`:

```
Invalid session identifier "<uuid>".
  Searched for sessions in <home>/.gemini/tmp/<project>/chats.
```

**A session file on disk is not evidence the session is recoverable.** After a
failed load there is often a `.jsonl` in that directory whose first line
carries the very id the load asked for, and gemini's own `--list-sessions`
does not offer it — while it does list the session the same run closed
cleanly. Two files can carry the same session id. Checking for the file is
what this suite tried first, and it pointed the wrong way.

**A clean close is not sufficient either, once the session has been compacted.**
The compaction probe closes the first process rather than killing it, and
gemini still could not load the session back, with the same
`Invalid session identifier` naming the same `chats` directory. So there are
two ways to lose a gemini session and only one of them is a crash: killing the
process mid-turn, and running `/compress`. A plain session, closed cleanly,
resumes every time at 0s — which is what makes the other two easy to miss.

For a runner: a gemini session that ends in a crash or a compaction is usually
gone, you cannot tell by looking for its file, and the remaining eleven agents
are unaffected.

**An agent's own claim is not an answer.** All twelve declare `loadSession` at
initialize, gemini included, on both paths. The claim is worth showing a
person; it is not worth gating on.

#### What this probe got wrong three times

Both corrections were the harness, and both came from the probe borrowing the
phase's session instead of running its own.

- **gemini was recorded as refusing `session/load` for weeks.** It refuses
  after a kill and resumes after a clean close, and the old probe ran at
  whatever moment the phase reached it. One attempt cannot tell "will not
  resume" from "has not finished writing", so the probe now retries at 0s, 2s,
  5s, 10s and 20s and reports how long the session took to become loadable.
- **qwen was recorded as accepting the load and attaching to nothing** — a
  full-looking replay, three model requests, and the earlier turn in none of
  them. The ACP phase sends qwen `/compress` before the probe ran, so the
  history had been compacted into a summary and the verbatim turn was
  correctly absent. The check was right about the session; the session was the
  wrong one to ask.

That case is worth keeping for what it shows: a replay can be genuine at the
protocol level while the model receives a summary instead of the original
wording. A resume that works is not the same as no context being lost.

- **gemini's kill-path failure was reported as a clean property** — persists at
  close, never before — from a single run of each path. It is intermittent:
  one resume in eight. A result measured once is a result measured under
  whatever the timing happened to be that time, and that applies to a pass as
  much as to a failure, which is why the kill path now has three passes behind
  it and the table says how many.

#### Counting replayed notifications proves nothing

An agent that accepts the call, ignores the id, opens a blank session and sends
its usual openers produces exactly what a short replay produces. Two to six
notifications is what openers alone produce, and two to six is what every agent
replied with, so the earlier "11 of 12 replayed the conversation" was never
evidence for the claim it was making.

What settles it is asking the resumed session a follow-up question and reading
what reached the mock. If the request carries the earlier turn, the session came
back. `LoadResult.RestoredConversation` from `agentprotocol/acp` is the cheap
corroborator — a fresh session cannot replay the user's own turn, because on a
fresh session the user has not spoken — and the probe also compares the replayed
update kinds against the kinds that same agent sends on a session with no
history.

A negative carries its evidence: when no request holds the earlier turn, the
probe describes the requests that were sent, so the claim cannot be read without
the thing that would falsify it.

The race and the replay marking live in `agentprotocol/acp` (`LoadSession`), not
here, so every client gets them: `session/load` does not reliably return — some
agents answer only after the replay finishes — and replayed updates arrive on
the same channel as live progress. A client that conflates the two re-raises
every historical tool call as a new approval request.

### A tool call in flight: every agent drops it

`--probe inflight` parks a permission request and never answers it, kills
the client while the tool call is still waiting, then resumes the session from a
new process and records whether the agent raises the approval again, with what
`toolCallId`, and whether answering it leads anywhere. `inflight=cancel` refuses the
re-raised request instead of approving it; `inflight=hold` answers nothing on
the resumed session either.

**Six agents put an approval in flight, and not one of them mentions it again.**
hermes, kilo, kimi, omp, opencode and qwen each asked before running a shell
command, each resumed the session after the client was killed mid-approval, and
each came back with nothing to say about the tool call that was waiting. No
error, no repeated request; the work is simply gone. All six also resumed the
same session twice.

For a runner that is the shape to design around: an approval interrupted by a
crash is not recoverable by resuming, so the pending operation has to be tracked
outside the agent or it disappears silently. `PermissionRequest.DuringLoad` in
`agentprotocol` exists for the opposite case — an agent that re-raises a parked
call during the replay — and no agent measured here does that.

gemini is the one agent that could not be asked: it gates shell commands, so the
approval parked, but it cannot resume a session whose process was killed.

### Agents gate shell commands. They do not gate reads

The first run of this probe concluded that agents do not consult their client at
all. That was a fact about the tool the registry offered them — nearly every
entry asks the agent to read `README.md`, and reading is something agents do
without asking. Asked instead to run `rm -rf` on a path that does not exist,
through each agent's own shell tool:

| Asked the client first | Ran it without asking |
|------------------------|-----------------------|
| gemini, hermes, kilo, kimi, omp, opencode, qwen | copilot, droid, goose, grok, kiro |

Seven of twelve. The earlier "none of them ask" was the measurement, not the
agents.

Tool names are read off the wire with `--probe tools`, which prints
what each agent declared to the model, and stored per agent as `ToolCallGated`.
They are not guessed: a tool an agent does not declare comes back "tool not
found" and never runs, so a guess produces a silent negative.
`TestEveryACPAgentHasAGatedTool` keeps the registry honest, and the probe says
so when it has to fall back to a read.

The probe also strips the registry's own auto-approval flags
(`--trust-all-tools`, `--yolo`, `--auto high`) and reports which it dropped, so
a "never asked" result is not this harness's configuration reported as an agent
trait. Blanket approval granted by a config file rather than a flag — kimi's
`permissions` block — is not something it can see.

Holding an approval used to hold the whole connection: agentprotocol answered
agent-initiated requests on its read loop, so a blocked handler stopped every
update and every pending reply behind it, which is what a human taking a minute
over an approval would have done to a real client. Fixed in v0.4.0, and pinned
here from the client's side.

### The default tool surface

`--probe tools` prints what each agent offered the model on the turn the mock
served, read off the wire rather than out of documentation. 12 agents measured
in ACP, claude/codex/pi in headless. cursor is absent because it declares no
tools on the protocol path this suite drives — see "what the packages contain"
below for where its declaration lives and what it would take to read it.

| capability | claude | codex | copilot | droid | gemini | goose | grok | hermes | kilo | kimi | kiro | omp | opencode | pi | qwen |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| read a file | Read | ↳shell | view | Read | read_file | ↳shell | read_file | read_file | read | Read | read | read | read | read | read_file |
| write a file | Write | ↳shell | ↳shell | Create | write_file | write | write | write_file | write | Write | write | write | write | write | write_file |
| edit in place | Edit | ↳shell | apply_patch | Edit | replace | edit | search_replace | patch | edit | Edit | code | edit | edit | edit | edit |
| run a shell cmd | Bash | exec_command | bash | Execute | run_shell_command | shell | run_terminal_command | terminal | bash | Bash | shell | bash | bash | bash | run_shell_command |
| find files | ↳shell | ↳shell | glob | Glob | glob | tree | list_dir | search_files | glob | Glob | glob | glob | glob | ↳shell | glob |
| search contents | ↳shell | ↳shell | rg | Grep | grep_search | ↳shell | grep | ↳shell | grep | Grep | grep | grep | grep | ↳shell | grep_search |
| todo list | — | — | — | TodoWrite | — | todo__todo_write | todo_write | todo | todowrite | TodoList | todo_list | todo | todowrite | — | — |
| subagent | Agent | spawn_agent | task | Task | invoke_agent | delegate | spawn_subagent | delegate_task | task | AgentSwarm | subagent | task | task | — | agent |
| skills | Skill | — | skill | Skill | activate_skill | load_skill | — | skill_manage | skill | Skill | — | — | skill | — | skill |
| fetch a url | WebFetch | — | web_fetch | — | web_fetch | — | — | — | webfetch | FetchURL | web_fetch | — | webfetch | — | — |
| web search | WebSearch | — | — | — | google_web_search | — | web_search | — | — | — | web_search | web_search | — | — | — |
| plan mode | — | — | — | ExitSpecMode | enter_plan_mode | — | enter_plan_mode | — | — | EnterPlanMode | — | — | — | — | enter_plan_mode |
| ask the user | — | request_user_input | — | — | — | — | ask_user_question | — | question | AskUserQuestion | — | — | — | — | ask_user_question |
| deferred tools | — | — | — | ToolSearch | — | — | search_tool | — | — | — | — | — | — | — | tool_search |

`↳shell` means the agent declared no tool for that row but did declare a shell,
so it reaches the capability by running a command. A bare `—` means neither.

**A shell is the only thing all 15 have.** Every other row has holes. The next
most common is edit-in-place at 14, and every agent spells it differently.

**codex declares no file tools at all.** Its 13 tools are `exec_command`,
`write_stdin`, `send_input`, agent spawning, goals and `view_image`; reading,
writing, editing and searching all happen by running commands. codex's
`apply_patch` is a CLI invoked inside the shell, not a tool the model is
offered. An integration that binds to a `read`-shaped tool has nothing to
attach to here.

**claude declares no `Glob` and no `Grep`.** 2.1.278 offered 27 tools and
neither was among them; it finds and searches through `Bash`. This was checked
against the raw request bodies rather than the parser, because it is the kind
of result that is usually a bug in the reader — the only occurrence of either
word in claude's request is inside this suite's own "do not call any tool"
instruction.

**Three agents declared a capability their peers have and then dropped it.**
goose offered no text read and no content search (`read_image` is images,
`tree` lists); hermes has `search_files` and no grep; copilot has no `write`,
because `apply_patch` creates files too. All four reach those through their
shell instead.

pi is the minimal extreme: four tools, `bash edit read write`, and no search,
no subagent, no skills.

Skills matter for belt specifically: 10 of 15 declare a skill tool, under five
names (`Skill` `skill` `load_skill` `activate_skill` `skill_manage`; hermes also
carries `skill_view` and `skills_list`). codex, grok, kiro, omp and pi declare
none, so a skill has to reach those five through the instruction file rather
than a tool call.

**What this table is not.** It is one turn's declaration, not the agent's
catalogue. Plan mode swaps the set and MCP servers add to it. Three agents also
ship a deferred-tool mechanism, which `--probe deferred` measures below.

### Deferred tools: what a plain turn does not show

droid, grok and qwen declare a tool whose job is to find other tools, so their
turn-one declaration is partial by design. `--probe deferred` reads the search
tool's own schema off the wire, fills its query argument, serves the call, and
re-reads the declarations. Measured 2026-09 in Docker, headless:

| agent | search tool | what the call did |
|-------|-------------|-------------------|
| droid | `ToolSearch(query)` | declared 4 more tools: `FetchUrl` `Loop` `WebSearch` `slack_post_file` |
| grok | `search_tool(query)` | declared nothing new |
| qwen | `tool_search(query)` | declared nothing new |

**These are two different architectures, not one working agent and two failures.**
droid expands the declaration, so its real surface is 17 tools and the matrix
above undercounts it by four. grok and qwen return search results to the model
as text and invoke the match through a second tool — `use_tool` and `tool_call`
— so nothing is ever added to the declaration. For those two the table is a
complete record of what is declared, and their reachable surface cannot be read
off the wire at all, because it never goes over it.

The probe reports "ran it and declared nothing new" separately from "was never
offered it". They look the same in the tool list and mean opposite things: one
is a measurement, the other is an untested agent.

A search tool answers a query, so droid's four are the surface reachable under
one broad query, not a total. Running a different query is how that number
grows.

Both halves of the mechanism are declared, and only one expands anything. qwen
ships `tool_search` and `tool_call`; the first version of this probe took
whichever name sorted first, called `tool_call`, revealed nothing, and reported
that as qwen having nothing to reveal.

Everything outside the shared rows is the agent's own idea of the job: goose
manages extensions and creates apps (`apps__create_app`), grok generates and
edits images and runs a scheduler, kimi and kiro track goals, kimi and claude
schedule cron, kiro talks to AWS (`use_aws`), kilo posts to a board, copilot
runs SQL against its session store, hermes has `memory` and `vision_analyze`.

#### What the packages contain that no turn declares

The matrix and the deferred probe both read the wire, so both measure what an
agent offers. A third question is what it *implements*, which is answered by
reading the shipped package instead.

The sweep takes the tool names an agent was measured declaring, finds the file
in its package where they cluster, and reports the other tool-shaped strings
around them. Run 2026-09 against the versions the suite installs, in the
container, so the packages match the matrix.

| agent | declared | tool-shaped names in the package that no turn declared |
|-------|---------:|--------------------------------------------------------|
| claude | 27 | `Glob` `Grep` `ToolSearch` `Task` `TodoWrite` `AskUserQuestion` `SendUserMessage` `Cd` `MultiEdit` `NotebookRead` `BashOutput` `KillShell` `ExitPlanMode` |
| qwen | 17 | `zoom_image` `propose_goal` `send_message` `save_memory` `lsp` `monitor` `loop_wakeup` `cron_create` `cron_list` `cron_delete` `create_sub_session` `read_mcp_resource` `web_fetch` `list_directory` `todo_write` |
| droid | 15 | `AskUser` `ApplyPatch` `FetchUrl` `WebSearch` `Loop` `GenerateDroid` `Script` `ProposeMission` `RecommendMission` `StartMissionRun` `EndFeatureRun` `ExitMissionPlanning` `DismissHandoffItems` `slack_post_message` |
| kimi | 26 | `WebSearch` `NotifyUser` `Think` `TowerInit` `TowerStatus` `TowerTeardown` |
| gemini | 15 | `read_many_files` `ask_user` |
| kilo | 15 | `apply_patch` |
| opencode | 10 | `apply_patch` |
| omp, pi | 11, 4 | swept, no tool cluster — the best-matching file was a model catalogue and an SDK type declaration |
| codex, copilot | 13, 16 | no cluster — their packages do not carry the declared names as quoted strings |
| cursor, goose, grok, hermes, kiro | — | not swept: they install outside the npm tree this sweep walks |

**Every agent the sweep could read ships more than it declares**, and for some
the gap is large: qwen declares 17 and carries goals, cron, an LSP client and a
sub-session spawner; droid declares 15 and carries a mission-planning system and
a Slack poster; kimi carries `NotifyUser` and three `Tower*` tools.

**claude implements `Glob` and `Grep` and did not declare them.** They are
tool-name constants in the binary (`var eo="Glob"`, `var zr="Grep"`) and appear
in its `filePatternTools` permission table beside `Read`, `Write` and `Edit`.
The matrix row above is still right about what claude offered — it searched
through `Bash` on that turn — but "claude has no Glob" would be wrong, and the
declared set is a configured subset of the implemented one.

**cursor has a tool declaration, and it is not on the path this suite drives.**
`supported_tools` is a repeated `aiserver.v1.ClientSideToolV2` enum, and it is
carried by `aiserver.v1.StreamUnifiedChatRequest` and
`aiserver.v1.ConversationMessage`. The mock implements the `agent.v1`
RunSSE + BidiAppend flow instead, and those messages do not carry the field: a
run was captured with every request body dumped, every packed-varint run
decoded, and `supported_tools` appears in none of them.

The CLI does not declare its tools on that path at all. Its richest message —
the 2764-byte `BidiAppend` carrying the request context — sends the hook names
it supports (`sessionStart`, `beforeSubmitPrompt`, `preToolUse`, `postToolUse`,
`stop`, `preCompact`), the shell, the OS, the timezone, the working directory,
the git branch and status, and the rules file. There is no tool list anywhere
in it.

That the bundle carries `StreamUnifiedChat` code is not evidence the CLI sends
it: the CLI ships the whole schema, and every message it was observed sending
is on the `agent.v1` path. So cursor's row cannot be measured here, and the
only thing that would settle whether it declares tools to its own backend is a
run against that backend with a real key.

This is the third answer this file has given about cursor — invisible, then
declared-and-unreadable, now declared-nowhere-we-can-see. The first two were
inferred from the schema; this one is read off a captured run, which is the
difference.

Until then the enum below is the protocol's whole vocabulary, which is an upper
bound and not an answer: it includes entries the CLI cannot run at all
(`computer_use`, `record_screen`, `background_composer_followup`,
`ai_attribution`), and the ids skip 2, 4, 10, 13, 14, 17, 20-22, 36 and 37,
which are retired.

| id | tool | id | tool | id | tool |
|---:|------|---:|------|---:|------|
| 1 | `read_semsearch_files` | 30 | `read_lints` | 47 | `update_project` |
| 3 | `ripgrep_search` | 31 | `go_to_definition` | 48 | `task_v2` |
| 5 | `read_file` | 32 | `task` | 49 | `call_mcp_tool` |
| 6 | `list_dir` | 33 | `await_task` | 50 | `apply_agent_diff` |
| 7 | `edit_file` | 34 | `todo_read` | 51 | `ask_question` |
| 8 | `file_search` | 35 | `todo_write` | 52 | `switch_mode` |
| 9 | `semantic_search_full` | 38 | `edit_file_v2` | 53 | `generate_image` |
| 11 | `delete_file` | 39 | `list_dir_v2` | 54 | `computer_use` |
| 12 | `reapply` | 40 | `read_file_v2` | 55 | `write_shell_stdin` |
| 15 | `run_terminal_command_v2` | 41 | `ripgrep_raw_search` | 56 | `record_screen` |
| 16 | `fetch_rules` | 42 | `glob_file_search` | 57 | `web_fetch` |
| 18 | `web_search` | 43 | `create_plan` | 58 | `report_bugfix_results` |
| 19 | `mcp` | 44 | `list_mcp_resources` | 59 | `ai_attribution` |
| 23 | `search_symbols` | 45 | `read_mcp_resource` | 60 | `mcp_auth` |
| 24 | `background_composer_followup` | 46 | `read_project` | 61 | `reflect` |
| 25 | `knowledge_base` | 26 | `fetch_pull_request` | 62 | `await` |
| 27 | `deep_search` | 28 | `create_diagram` | 63 | `get_mcp_tools` |
| 29 | `fix_lints` | | | | |

**The method checks out against a known result.** droid's package contains
`FetchUrl`, `WebSearch`, `Loop` and `slack_post_file`, which are exactly the
four `--probe deferred` revealed by serving `ToolSearch` a query. Static
reading and dynamic expansion agree where they overlap, which is the only
reason to trust either on the names where they do not.

**A name in a package is not an available tool.** Static extraction cannot tell
a live tool from dead code, a legacy alias, a permission-rule label or a
feature behind a flag, and the sweep reads strings near other strings, so a
parameter name or a status value can survive the filter. Nothing in this table
is a measurement of what an agent will do; it is a list of what to go and
measure. Five agents are missing from it because they install outside the npm
tree, and two more because their packages do not carry their tool names as
quoted strings — absence from this table means the sweep could not look, not
that there is nothing there. The wire is still the only
place an answer comes from, which is what the three columns are for: declared,
revealed by asking, and present in the package.

### Resuming a compacted session

`--probe compact` runs a session several turns deep, compacts it with the
agent's own command, resumes it from a new process, and reads what reaches the
model: the original wording, a summary, or nothing but the new prompt. The last
is a failure rather than a trait — the resume reported success and the
conversation is gone.

This probe needs two guards, and without either it passes on nothing. A single
exchange is below every agent's compaction threshold, so the probe fills the
session first. And a request to the model is not a compaction: over ACP a slash
command can arrive as an ordinary user message — the registry already records
that codex treats `/compact` that way — so the probe requires the request to
carry summarisation instructions before it believes a compaction happened.
Both were added after the first run reported a cheerful "nothing was lost" for
every agent, which meant only that nothing had happened.

Four of the twelve ACP agents have a compaction command. With the session
filled first, all four compact for real (2026-09, one pass):

| Agent | Command | Compacted over | What the resume carried |
|-------|---------|----------------|-------------------------|
| droid | `/compress` | 10 messages | the original wording |
| gemini | `/compress` | 12 messages | nothing — the session could not be loaded back |
| grok | `/compact` | 14 messages | the original wording |
| qwen | `/compress` | 12 messages | a summary: 6 messages, not the original wording |

gemini's row is not a compaction result. It compacted 12 messages and then
failed `session/load` with `Invalid session identifier`, the same error its
killed sessions give and pointing at the same `.gemini/tmp/<project>/chats`
directory. The compaction probe closes the first process cleanly, so this is
the same session-durability defect reached without a crash — `/compress`
appears to replace the session rather than rewrite it, and the id the client
holds stops resolving. Whether gemini's compaction preserves the conversation
is still unmeasured here, because nothing survives to read.

An earlier run had droid and gemini "sending `/compress` to the model as an
ordinary user message rather than compacting". That was the missing filler:
their sessions were too short to be worth compacting, and the agent forwarded
the command. The reading was of the probe, not of the agent.

### Two questions about an agent, and they are not the same question

"Is this agent installed on this machine" and "am I running inside it right
now" have different evidence, different consumers and different failure modes.
They must not share a table. Cursor is the case that proves it: the registry's
`cursor` is the `cursor-agent` CLI, while `cursor` in belt's own naming is the
IDE, and one table would have to call both by one name.

The installed list is what belt consumes — it decides which agents get hooks
written. The runtime answer only labels a survey row.

| question | evidence | API |
|----------|----------|-----|
| installed here | binary on PATH, well-known bin dir, package registry entry, config directory | `DetectInstalled()` |
| running inside now | environment variables the surrounding process exported, marker files | `DetectRunning()` |

Detection reports three tiers, because "found" alone hides the difference
between an agent that can be driven and a directory someone left behind:
**running** (its variable is set, so belt is a child of it), **installed** (a
binary or a package registry entry), **configured** (a config directory and
nothing else — typically an IDE with no CLI).

**Environment evidence alone is not an install.** `Installed()` is a binary or
a package registry entry, and nothing else; the env-var probe cannot promote an
agent into the installed list on its own. Being inside an agent says a process
exists, not that anything is on disk to install into.

#### The installed list claimed two agents that were not there

`--probe detect` runs `DetectInstalled()` inside a container that installed
exactly one agent, which is the only place the claim can be checked against
ground truth. It found two false positives, both of which had been shipping:

**opencode, on every Linux and macOS machine.** The config directory was
derived by taking the first segment of the hook path, which works for `.claude`
and `.gemini` but truncated `.config/opencode/plugins` to `.config`. Every
machine has a `~/.config`. Under a shared root — `.config`, `.local`, `.cache`
— detection now keeps the segment that actually names the agent.

**cursor, on every machine with grok.** Cursor's binary was recorded as
`agent`, and grok installs `~/.grok/bin/agent`, which is one of the well-known
bin directories the search walks. The binary is `cursor-agent`.

Both are the same shape of mistake: a name generic enough to belong to someone
else. Neither would have been found by reading the code, and both were found on
the first run that asked the question out loud. 16/16 clean since.

#### Three agents export nothing that names them

`--probe env` dumps the environment an agent hands its hooks. A hook is a child
process of the agent, so this is the only place an agent's exported variables
can be observed rather than guessed, and it is where the registry's
`DetectEnvVars` entries come from.

Measured 2026-09: opencode 1.18.31 exports 13 variables and not one names it —
the `OPENCODE_CLIENT` belt had been checking for is never set. kimi 0.43.1
exports 12, the only `KIMI_*` one being the base URL this suite configured.
omp 18.2.1 exports `__PI_NATIVE_VARIANT_CACHE`, a pi-family internal whose
value does not name omp.

All three run belt from a TS plugin rather than a command hook, so the
generated plugin is the natural place to say which agent it is, and belt's
generated config for those three declares `AI_AGENT` itself. It is deliberately
not every agent: claude and pi set `AI_AGENT` themselves and carry their
version in it, and overwriting that would throw the version away.

The three are listed in `UndetectableByEnv` so the gap is a recorded
measurement rather than an entry someone later "fixes" with a guess.

**An artefact in an environment dump may have been put there by the thing
running the test.** This suite sets `AI_AGENT` itself when driving belt's real
hooks, so a variable found in a dump is not automatically the agent's. The pi
observation above survived that check; it was made in mock mode, where the
suite sets nothing.

#### A version suffix that changed under us

Some runtimes put `<name>_<version>_<surface>` in `AI_AGENT`, e.g.
`claude-code_2-1-220_agent`. The trailing word is not stable: Claude Code
2.1.263 emits `_agent` and 2.1.278 emits `_harness`. A pattern anchored on
`_agent` stops matching at that upgrade, the whole string becomes the name, and
every version silently opens its own survey row. The pattern accepts any
trailing word.

#### Shipped paths are not named after this suite

goose's hooks were being installed into `.agents/plugins/belt-test`, a
directory named after this test suite, on real machines. They go to
`.agents/plugins/belt`, and the install removes the old directory. A test
harness that writes its own name into a user's config is a bug with a long
tail, because the wrong path keeps working.

### Cursor hooks are requested by the backend

The Cursor CLI runs every hook through one dispatcher that answers a backend request: `ExecServerMessage` field 27 `execute_hook_args { request: ExecuteHookRequest }`, whose oneof names the hook (`pre_compact` 1, `pre_tool_use` 4, `post_tool_use` 5, `before_submit_prompt` 7, `stop` 11), answered by `ExecClientMessage` field 27 `execute_hook_result` (agent.v1 schema, Cursor CLI 2026.09). The mock sends those requests, listed per mode in `Harness.ServerRequestedHooks`: the TUI runs the prompt and stop hooks itself as well, so in interactive mode the mock requests only compaction, while in headless it requests all three. Before this, the mock requested none, and "Cursor never runs the prompt or stop hook in headless" sat in the known-issue table.

What this does and does not show: when Cursor's backend asks for a hook, the CLI runs belt's hook and hands back its `additional_context`. Whether Cursor's real backend asks for the prompt and stop hooks in `agent -p` is not something a mock can answer; nobody here has seen its traffic.

### The runner must not decide the outcome either

- **Waiting for the answer.** The interactive runner used to wait for any of a list of words and then kill the session three seconds later. The list included `codename`, which is in the prompt, and `build`, which is in Cursor's empty input box, so it matched the question as soon as the TUI drew. It held only because mock hooks finish in milliseconds; belt's take seconds. It now waits until the mock has served an answer (`MockServer.AnswersServed`) and then for the agent's own stop hook to fire, which is positive evidence the turn is over. An agent with no stop hook falls back to waiting until neither the request log nor the hook log has changed for four seconds. The counters are taken before the prompt goes in, because a fast agent fires its stop hook while the runner is still setting up, and what a fired hook looks like depends on the hook source: the mock hooks append this suite's tag, belt writes its own event name in brackets to a log of its own.
- **Onboarding screens.** The dismissal loop matched patterns against all output so far, so once a dialog had appeared it pressed Enter on every pass, up to fifteen times. It now handles the earliest dialog in output it has not yet consumed and moves past just that dialog's text, because agents draw several screens in one burst (claude's API-key question and "Press Enter to continue"; skipping to the end of the buffer left claude on the second screen for 90 seconds). A `Required` dismissal (droid's and kimi's "Trust this folder?") is waited for instead of given up on once the splash has drawn, and the runner waits for the screen to stop changing before it types — a prompt typed into a startup screen is lost.
- **Typing.** pi-tui (kimi, pi, omp) reads a fast run of keystrokes as a paste and turns an Enter within 120 ms of it into a newline. The runner sent Enter 50 ms after the text, so kimi's prompt sat in the composer until the next typed line, `/exit`, submitted both: kimi's model was sent "…codename.\n/exit" on every interactive run and `/exit` never ran. The runner now pauses 250 ms before Enter.
- **Droid's TUI**, which the model check exposed, had four problems of the harness's making: no model on the session (above), a custom model without `maxContextLimit`, so "Context Usage — Failed to load" and compaction could not run; `/compact`, which droid 0.217 maps to the Context Usage panel (`/compress` compacts, behind a confirmation the runner now accepts); and the trust dialog swallowing the typed prompt.
- **The model check.** It searched the whole request body, so any mention of the model passed, and the registry had grown prefix entries (`gemini-3`, `grok-4`, `claude-`) that accepted a whole family. It now reads only the model field the mock parsed, or a URL path segment (gemini), and matches exactly. Made strict, it found six harnesses not running the model they claimed: droid's TUI has no `-m` flag, so `-m mock-model` became the user's prompt and the session ran Factory's default; gemini was never given a model in headless or ACP and chose its own, and maps `gemini-2.5-flash` to `gemini-3.5-flash` when it is; grok takes its model from the backend's settings, where the mock serves `mock-model`, not the `grok-3-mini` on record; kiro sent an empty `modelId`, and the mock recorded a constant `kiro-default` that the check then compared with itself; kilo and opencode send the bare model for `openai/gpt-4o-mini`. Command templates (`{{.Model}}`) are now expanded in the command as well as its args; kiro had received the literal text.

### Codenames

Each check writes a distinct codename into the model's context and looks for it in the recorded requests: `HOOK-<AGENT>-<ts>` from the prompt hook, `INSTR-USER-<AGENT>-<ts>` and `INSTR-PROJ-<AGENT>-<ts>` from the instruction files. The prefixes must stay distinct — a bare `<AGENT>-<ts>` hook code is a substring of the instruction codes, so the injection check passed whenever the instruction file loaded and hid three real failures (2026-09).

### Pushing

`git config core.hooksPath .githooks` once per clone; the pre-push hook runs build, vet and the unit tests and refuses a push that fails them. CI runs every harness on push, so a broken build otherwise turns all 17 badges red until the next fix.

### Real belt hooks (`--hooks belt`)

`--hooks belt` installs belt's actual hook commands through `harness.Install`, checks the events belt logs, and checks the shape of what belt's prompt hook prints: the text must not be another envelope, and JSON must not go to an agent whose channel is plain stdout. Every hook check passed while belt handed copilot a JSON blob and kimi raw JSON, because a hook firing says nothing about what it hands over (fixed in belt 1.18.32). The suite also sets `INFSH_NO_AUTOUPDATE`, since the released binary otherwise re-execs into a newer one mid-run. `tests/fetch-belt.sh` downloads the released CLI from `dist.inference.sh` into `tests/belt` (`BELT_VERSION=vX.Y.Z` pins one); the Docker build copies it in. CI runs every harness in both mock and belt mode on push and nightly. Run all agents non-root (Claude Code refuses to skip permissions as root) and kiro separately with `--user root --intercept`.

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
    ACPCmd: []string{"myagent", "--acp"},
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
├── driver/
│   ├── testrunner.go Test orchestrator (install → config → hooks → run → verify)
│   ├── driver.go     Driver interface
│   ├── acp.go        ACP policy over github.com/inference-sh/agentprotocol/acp
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
