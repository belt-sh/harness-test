# harness-test

Conformance test suite for AI coding agent CLIs. Tests 13 agents across 3 control modes (CLI, PTY, ACP), 4 API formats, and 7 hook formats.

Verifies: prompt/response, tool calls, hook lifecycle, streaming, model selection, context injection. Agent-agnostic — bring your own hooks or use the built-in test suite.

[![CI](https://github.com/belt-sh/harness-test/actions/workflows/ci.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/ci.yml)

## Use cases

### "I'm building a plugin/hooks product and need to test it works across agents"

You have a product (like [belt](https://belt.sh), an MCP server, or a custom hook system) and need to verify it works inside Claude, Codex, Cursor, Grok, and others. The harness-test installs each agent CLI, points it at a mock LLM, writes your hook configs, runs a prompt, and verifies everything fires.

```bash
# Test your hooks against all 13 agents
docker compose run test --harness all

# Test against specific agents
docker compose run test --harness claude,codex,grok
```

### "I'm building an agent control surface and need to test ACP conformance"

You're building something like [T3 Code](https://github.com/pingdotgg/t3code) — an editor or app that controls agents via ACP. The harness-test verifies agents respond correctly to ACP session lifecycle, handle tool permissions, and stream responses properly.

```bash
# Test ACP protocol with agents that support it
harness-test --harness claude,grok,opencode --mode acp
```

### "I'm building a coding agent and want to verify my hook/API implementation"

You're building a new agent CLI and want to verify it speaks the right API format, fires hooks at the right time, and handles the prompt/response lifecycle correctly. Add your agent to the registry and get instant conformance testing.

```go
// Add to harness/registry.go
"myagent": {
    Name: "myagent", Binary: "myagent",
    APIFormat: OpenAI,
    HookFormat: JSONNested,
    Events: Events{PromptSubmit: "UserPromptSubmit", Stop: "Stop"},
    HeadlessCmd: []string{"myagent", "-p"},
    HooksInHeadless: true,
},
```

```bash
harness-test --harness myagent
```

## Quick start

```bash
# Build
go build -o harness-test .

# Run in Docker (recommended — installs CLIs automatically)
cd tests && docker compose run test --harness claude

# List harnesses
harness-test --list

# Detect what's installed
harness-test --detect
```

### Use as a CI step

```yaml
# .github/workflows/test.yml
jobs:
  harness:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      # Option A: test against installed agents
      - run: |
          go install github.com/belt-sh/harness-test@latest
          harness-test --harness claude,codex --mode headless

      # Option B: test against all agents in Docker
      - run: |
          git clone https://github.com/belt-sh/harness-test.git
          cd harness-test/tests
          docker compose run test --harness all
```

### Use the mock server standalone

```bash
harness-test --server
# Listening at http://127.0.0.1:PORT
#
# Now point your agent at it:
# ANTHROPIC_BASE_URL=http://127.0.0.1:PORT claude -p "hello"
# OPENAI_BASE_URL=http://127.0.0.1:PORT/v1 codex exec "hello"
```

The mock server speaks all 4 API formats on the same port. Useful for developing hooks without burning API credits.

## What it verifies

Each run checks:

| Check | What it tests |
|-------|--------------|
| **Prompt/response** | Agent sends a request, mock server responds, agent produces output |
| **Hook events** | Lifecycle hooks fire (SessionStart, PromptSubmit, PreToolUse, PostToolUse, Stop, PreCompact) |
| **API requests** | Mock server received requests in the correct format |
| **Streaming** | Agent uses SSE streaming (not blocking request/response) |
| **Model selection** | Correct model name appears in API requests |
| **Tool calls** | Agent makes tool calls and sends results back |
| **Context injection** | Hook output (injected context) appears in the agent's response |

## Three control modes

```
harness-test --harness claude --mode headless      # CLI args
harness-test --harness claude --mode interactive    # PTY/TUI
harness-test --harness claude --mode acp            # Agent Client Protocol
harness-test --harness claude --mode both           # headless + interactive (default)
```

| Mode | Transport | What it tests |
|------|-----------|--------------|
| **Headless** | CLI args + stdout | `agent -p "prompt"` — fast, deterministic |
| **Interactive** | PTY terminal | Full TUI flow: onboarding, typing, `/compact`, exit |
| **ACP** | JSON-RPC over stdio | Programmatic session control (editors, T3 Code, Zed) |

### ACP protocol support

The ACP driver handles:

| Method | Direction | Status |
|--------|-----------|--------|
| `initialize` | client → agent | ✓ (declares capabilities) |
| `session/new` | client → agent | ✓ |
| `session/prompt` | client → agent | ✓ |
| `session/update` | agent → client | ✓ (text, tool calls) |
| `session/request_permission` | agent → client | ✓ (auto-approve) |
| `session/close` | client → agent | ✓ |

Agents with ACP: claude (`claude acp`), grok (`grok agent stdio`), opencode (`opencode acp`).

## Mock server

Built into the binary. Starts on a random port and speaks all 4 API formats:

| Endpoint | Format | Harnesses |
|---|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions | copilot, hermes, pi, kimi, goose, qwen, droid |
| `POST /v1/responses` | OpenAI Responses | codex, grok, opencode, kilo |
| `POST /v1/messages` | Anthropic Messages | claude |
| `POST /v1beta/models/:model:streamGenerateContent` | Gemini | gemini |

All endpoints support streaming (SSE) and non-streaming.

### Tool calls

When `PrepareToolCall` is set, the first response includes a tool call (e.g., `Read README.md`). The runner sends a tool result back, testing the full tool-call lifecycle.

### Server utilities

```
GET  /log        — show request log (JSON array)
GET  /log/count  — request count
DELETE /log      — clear log
POST /response   — set custom response body
```

## Hook formats

The tool generates test hooks in 7 formats:

| Format | Harnesses | Config file |
|---|---|---|
| JSONNested | claude, codex, grok, droid, goose, qwen, gemini, cursor, windsurf | `hooks.json` / `settings.json` |
| JSONCopilot | copilot | `hooks.json` with `version:1`, `bash` field |
| TOML | kimi | `config.toml` with `[[hooks]]` entries |
| YAML | hermes | `config.yaml` hooks block |
| TypeScript (extension) | pi | `.ts` with `pi.on(event, ...)` |
| TypeScript (plugin) | opencode, kilo | `.ts` exporting plugin object |

### Writing your own hooks

Test hooks are shell commands that log to a file:

```bash
# SessionStart hook
echo SESSION_START >> /tmp/hook-events.log

# PromptSubmit hook (with context injection)
echo PROMPT >> /tmp/hook-events.log && echo 'Injected context here.'
```

The runner checks the log file after each phase. To test your own product's hooks, replace the commands with your own — e.g., `my-product hook session-start`.

## Harness matrix

| Harness | Binary | API | Hook Format | Events | Headless | Interactive | ACP |
|---|---|---|---|---|---|---|---|
| claude | `claude` | Anthropic | JSONNested | 6 | ✓ | ✓ | ✓ |
| codex | `codex` | Responses | JSONNested | 6 | ✓ | ✓ | — |
| copilot | `copilot` | OpenAI | JSONCopilot | 2 | ✓ | ✓ | — |
| droid | `droid` | OpenAI | JSONNested | 6 | ✓ | ✓ | — |
| gemini | `gemini` | Gemini | JSONNested | 6 | ✓ | ✓ | — |
| goose | `goose` | OpenAI | JSONNested | 4 | ✓ | ✓ | — |
| grok | `grok` | Responses | JSONNested | 4 | ✓ | ✓ | ✓ |
| hermes | `hermes` | OpenAI | YAML | 4 | ✓ | ✓ | — |
| kilo | `kilo` | Responses | TSPlugin | 4 | ✓ | ✓ | — |
| kimi | `kimi` | OpenAI | TOML | 4 | ✓ | ✓ | — |
| opencode | `opencode` | Responses | TSPlugin | 4 | ✓ | ✓ | ✓ |
| pi | `pi` | OpenAI | TSExtension | 2 | ✓ | ✓ | — |
| qwen | `qwen` | OpenAI | JSONNested | 6 | ✓ | ✓ | — |

## Architecture

```
harness-test
├── main.go          CLI: --harness, --detect, --install, --server, --hooks, --mode
├── harness/
│   ├── harness.go   Harness struct (API format, hook format, events, commands, ACP)
│   ├── registry.go  13 harness configs (pure data, no per-harness code)
│   ├── detect.go    5-probe detection (config-dir, PATH, known-path, npm, env-var)
│   └── install.go   Hook config generation for 7 formats
├── runner/
│   ├── runner.go    Orchestrator (install → config → hooks → run → verify)
│   ├── driver.go    Driver interface (all control modes implement this)
│   ├── checks.go    Verification checks (hooks, API, streaming, model, tool calls)
│   ├── pty.go       PTY driver — terminal session management
│   └── acp.go       ACP driver — JSON-RPC over stdio, auto-approve permissions
└── server/
    ├── server.go    Mock LLM server + request log + tool call mode
    ├── chat.go      OpenAI Chat Completions (streaming + non-streaming)
    ├── responses.go OpenAI Responses format
    ├── anthropic.go Anthropic Messages format
    ├── gemini.go    Gemini generateContent format
    └── types.go     Shared request/response types
```

Each harness is a pure data struct — no per-harness code paths. Adding a new harness means adding an entry to the registry. The runner, mock server, and drivers are fully generic.

## Docker

The Dockerfile installs Node.js, Go, Python, git, and bubblewrap (for Codex sandboxing). Runs as non-root `testuser`.

```bash
cd tests
docker compose build
docker compose run test --harness all               # all harnesses, both modes
docker compose run test --harness claude,grok        # specific harnesses
docker compose run test --harness all --mode headless  # headless only
docker compose run test --harness claude --mode acp  # ACP mode
```

## Detection

`harness-test --detect` probes the system for installed harnesses using 5 strategies:

| Probe | What it checks | Example |
|---|---|---|
| config-dir | `~/.claude/`, `~/.codex/` exist | Agent was used on this machine |
| path-lookup | Binary in PATH | Currently installed |
| known-path | Binary at `~/.local/bin/`, `~/.grok/bin/` | Installed but not in PATH |
| package-reg | `npm list -g --json` | npm-installed globally |
| env-var | `CLAUDECODE`, `CODEX_SANDBOX` set | Running inside this agent now |

## Contributing

Add a harness: one entry in `harness/registry.go`. Add a check: one function in `runner/checks.go`. Add a control mode: implement `Driver` in a new file.
