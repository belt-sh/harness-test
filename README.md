# harness-test

Conformance test suite for AI coding agent CLIs. Tests 13 agents across 3 control modes (CLI, PTY, ACP), 4 API formats, and 7 hook formats.

Verifies: prompt/response, tool calls, hook lifecycle, streaming, model selection, context injection. Agent-agnostic — bring your own hooks or use the built-in test suite.

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

## How it works

For each harness, the tool:

1. **Installs the CLI** if missing (npm, pip, curl)
2. **Starts a mock inference server** that speaks the right API format
3. **Writes hook configs** with test commands that log events to a file
4. **Runs headless mode** (`<agent> -p "prompt"`) and checks output
5. **Runs interactive mode** (PTY session — types prompt, waits for response)
6. **Verifies** hook events fired and the mock server received API requests

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

When `PrepareToolCall` is set, the first response includes a tool call (e.g., `Read README.md`). The runner sends a tool result back, triggering PreToolUse/PostToolUse hooks. This tests the full tool-call lifecycle.

### Server-only mode

```bash
harness-test --server
```

Starts just the mock server — useful for manual testing or developing hooks. Endpoints:

- `GET /log` — show request log
- `GET /log/count` — request count
- `DELETE /log` — clear log
- `POST /response` — set custom response body

## Hook formats

The tool generates test hooks in 7 formats:

| Format | Harnesses | Config file |
|---|---|---|
| JSONNested | claude, codex, grok, droid, goose, qwen, gemini, cursor, windsurf | `hooks.json` or `settings.json` |
| JSONCopilot | copilot | `hooks.json` with `version:1`, `bash` field |
| TOML | kimi | `config.toml` with `[[hooks]]` entries |
| YAML | hermes | `config.yaml` hooks block |
| TypeScript (extension) | pi | `.ts` with `pi.on(event, ...)` |
| TypeScript (plugin) | opencode, kilo | `.ts` exporting plugin object |

### Writing your own hooks

Test hooks are shell commands that log an event tag to a file:

```bash
echo SESSION_START >> /tmp/hook-events.log
```

For PromptSubmit hooks, the command also outputs context to inject:

```bash
echo PROMPT >> /tmp/hook-events.log && echo 'The project codename is TEST-123.'
```

The runner checks `/tmp/belt-hook-events.log` for event tags after each phase. To test your own hook commands, modify the `writeHooks()` method in `runner/runner.go` or create a custom hook config and use `--server` mode.

## Three control modes

The tool drives agents in three ways:

```
harness-test --harness claude --mode headless      # CLI args
harness-test --harness claude --mode interactive    # PTY/TUI
harness-test --harness claude --mode acp            # Agent Client Protocol
harness-test --harness claude --mode both           # headless + interactive (default)
```

### Headless (CLI args)

Runs `<agent> -p "prompt"` and captures stdout. Fast, deterministic. Most harnesses support this via `-p`, `exec`, or `--prompt` flags.

### Interactive (PTY)

Starts a real terminal session via `github.com/creack/pty`. Types the prompt character by character, waits for response patterns, sends `/compact` if supported, then exits. Tests the full TUI flow including onboarding dismissal, slow input for anti-paste protection, and exit commands.

### ACP (Agent Client Protocol)

Launches the agent in ACP mode (`<agent> acp`) and communicates via JSON-RPC over stdio. Structured, programmatic — no terminal emulation, no output parsing. The flow:

1. `initialize` — negotiate protocol version and capabilities
2. `session/new` — create a session
3. `session/prompt` — send user turns
4. `session/update` — receive agent messages, tool calls, status
5. `session/close` — end the session

ACP is the standard protocol for editors (T3 Code, Zed, Cursor) to control agents. Testing hooks via ACP verifies they fire through the programmatic path, not just CLI/TUI.

Agents with ACP support: claude (`claude acp`). More agents are adopting ACP.

Key PTY features:
- `SlowInput` — types characters with 5ms delay (bypasses crossterm paste detection)
- `OnboardingDismiss` — pattern-matched dialog dismissal (theme, trust, continue prompts)
- `WaitForAny` — waits for any of several patterns in terminal output
- `CompactCommand` — tests `/compact` or `/compress` after a warmup prompt

## Harness matrix

| Harness | Binary | API | Hook Format | Events | Headless | Interactive |
|---|---|---|---|---|---|---|
| claude | `claude` | Anthropic | JSONNested | 6 | ✅ | ✅ |
| codex | `codex` | Responses | JSONNested | 6 | ✅ | ✅ |
| copilot | `copilot` | OpenAI | JSONCopilot | 2 | ✅ | ✅ |
| droid | `droid` | OpenAI | JSONNested | 6 | ✅ | ✅ |
| gemini | `gemini` | Gemini | JSONNested | 6 | ✅ | ✅ |
| goose | `goose` | OpenAI | JSONNested | 4 | ✅ | ✅ |
| grok | `grok` | Responses | JSONNested | 4 | ✅ | ✅ |
| hermes | `hermes` | OpenAI | YAML | 4 | ✅ | ✅ |
| kilo | `kilo` | Responses | TSPlugin | 4 | ✅ | ✅ |
| kimi | `kimi` | OpenAI | TOML | 4 | ✅ | ✅ |
| opencode | `opencode` | Responses | TSPlugin | 4 | ✅ | ✅ |
| pi | `pi` | OpenAI | TSExtension | 2 | ✅ | ✅ |
| qwen | `qwen` | OpenAI | JSONNested | 6 | ✅ | ✅ |

## Adding a new harness

Add an entry to `harness.All` in `harness/registry.go`:

```go
"myagent": {
    Name: "myagent", Binary: "myagent",
    InstallCmd: []string{"npm", "install", "-g", "myagent-cli"},
    APIFormat: OpenAI,                          // or Responses, Anthropic, Gemini
    EnvVars: map[string]string{
        "MYAGENT_BASE_URL": "{{.BaseURL}}",     // mock server URL
    },
    APIKeyEnvVar: "MYAGENT_API_KEY",
    DefaultModel: "gpt-4o-mini",
    HookFormat:    JSONNested,                  // or JSONCopilot, TOML, YAML, TSExtension, TSPlugin
    HookConfigDir: ".myagent",                  // relative to $HOME
    Events: Events{
        PromptSubmit: "UserPromptSubmit",        // harness-specific event name
        Stop:         "Stop",
    },
    HeadlessCmd:         []string{"myagent", "-p"},
    HooksInHeadless:     true,
    InteractiveCmd:      []string{"myagent"},
    ExitCommand:         "/exit",
    HooksInInteractive:  true,
},
```

The runner handles everything from this config — no per-harness code needed.

## Architecture

```
harness-test
├── main.go         CLI: --harness, --detect, --install, --server, --hooks
├── harness/
│   ├── harness.go  Harness struct (API format, hook format, events, commands)
│   ├── registry.go 13 harness configs (pure data, no code)
│   ├── detect.go   5-probe system detection (config-dir, PATH, known-path, npm, env-var)
│   └── install.go  Hook config generation for 7 formats
├── runner/
│   ├── runner.go   Test runner (install → config → hooks → run → verify)
│   ├── driver.go   Driver interface (headless, PTY, ACP all implement this)
│   ├── pty.go      PTY driver — terminal session management
│   └── acp.go      ACP driver — JSON-RPC over stdio
└── server/
    ├── server.go   Mock server + request log + tool call mode
    ├── chat.go     OpenAI Chat Completions (streaming + non-streaming)
    ├── responses.go OpenAI Responses format
    ├── anthropic.go Anthropic Messages format
    ├── gemini.go   Gemini generateContent format
    └── types.go    Shared request/response types
```

## Docker

The Dockerfile installs Node.js, Go, Python, git, and bubblewrap (for Codex sandboxing). Runs as non-root `testuser` with npm globals in `~/.npm-global/`.

```bash
cd tests
docker compose build
docker compose run test --harness all          # all harnesses, both modes
docker compose run test --harness claude,grok   # specific harnesses
docker compose run test --harness all --mode headless  # headless only
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
