# harness-test

Conformance test suite for coding agent CLIs. Tests 13 agents across 4 control modes, 4 API formats, and 7 hook formats.

Verifies: prompt/response, tool calls, hook lifecycle, streaming, model selection, context injection.

Built for [belt.sh](https://belt.sh) — connect your agent to skills, knowledge, and tools.

[![CI](https://github.com/belt-sh/harness-test/actions/workflows/ci.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/ci.yml)
[![Nightly](https://github.com/belt-sh/harness-test/actions/workflows/nightly.yml/badge.svg)](https://github.com/belt-sh/harness-test/actions/workflows/nightly.yml)

## Compatibility matrix

<!-- Updated 2026-08-29. Versions from latest CI run in Docker. -->

| Agent | Version | Headless | Interactive | ACP | SDK | Hook Format | API |
|-------|---------|:--------:|:-----------:|:---:|:---:|-------------|-----|
| [Claude Code](https://github.com/anthropics/claude-code) | 2.1.x | ✅ | ✅ | — | ✅ | JSONNested | Anthropic |
| [Codex](https://github.com/openai/codex) | 1.x | ✅ | ✅ | — | — | JSONNested | Responses |
| [Copilot](https://github.com/github/copilot) | 1.0.x | ✅ | ✅ | ✅ | — | JSONCopilot | OpenAI |
| [Droid](https://docs.factory.ai/cli) | 0.208.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | 0.57.x | ✅ | ✅ | ✅ | — | JSONNested | Gemini |
| [Goose](https://github.com/block/goose) | 1.48.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |
| [Grok](https://x.ai/grok-build) | 1.0.x | ✅ | ✅ | ✅ | — | JSONNested | Responses |
| [Hermes](https://github.com/hermes-ai/hermes-agent) | 0.19.x | ✅ | ✅ | ✅ | — | YAML | OpenAI |
| [Kilo](https://github.com/nicepkg/kilo) | 7.5.x | ✅ | ✅ | ✅ | — | TSPlugin | Responses |
| [Kimi Code](https://github.com/nicepkg/gpt-runner) | 1.49.x | ✅ | ✅ | ✅ | — | TOML | OpenAI |
| [OpenCode](https://github.com/nicepkg/opencode) | 1.18.x | ✅ | ✅ | ✅ | — | TSPlugin | Responses |
| [Pi](https://github.com/earendil-works/pi) | 0.x | ✅ | ✅ | — | — | TSExtension | OpenAI |
| [Qwen Code](https://github.com/nicepkg/qwen-code) | 0.22.x | ✅ | ✅ | ✅ | — | JSONNested | OpenAI |

**13/13** headless · **10/13** ACP · **1/13** SDK · **24 mode-tests in CI**

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
