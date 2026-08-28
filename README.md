# harness-test

Cross-harness plugin testing tool for belt. Installs AI coding agent CLIs, configures
hooks pointing at a built-in mock inference server, runs each harness in headless and
interactive (PTY) modes, and verifies that hooks fire and the agent talks to the mock.

## Quick start

```bash
# Build
cd tools/harness-test && go build -o harness-test .

# Run all harnesses in a container (the intended way)
docker build -t harness-test -f tests/Dockerfile .
docker run harness-test --harness all

# Run a single harness
docker run harness-test --harness claude

# List available harnesses
docker run harness-test --list
```

## What it tests

For each harness the tool runs these phases:

1. **Install** — installs the CLI if not found (`npm install -g`, `pip install`, `curl | bash`)
2. **Endpoint** — sets env vars pointing at the built-in mock server
3. **Config** — writes auth/trust/provider config files to a temp HOME
4. **Hooks** — registers hook commands for all supported events (SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, PreCompact)
5. **Headless** — runs `<harness> -p "prompt"` and checks output
6. **Interactive** — starts a PTY session, sends a prompt, waits for response
7. **Verify** — checks `/tmp/belt-hook-events.log` for hook events, checks mock server request log

## Mock server

Built into the binary. Serves three API formats on a random localhost port:

| Endpoint | Format | Used by |
|---|---|---|
| `POST /v1/chat/completions` | OpenAI Chat | Copilot, Grok, Pi, Hermes, OpenCode |
| `POST /v1/responses` | OpenAI Responses | Codex |
| `POST /v1/messages` | Anthropic Messages | Claude |

All endpoints support streaming (SSE) and non-streaming. When `toolCallMode` is on,
the first response is a tool call (`Read README.md`), triggering PreToolUse/PostToolUse
hooks.

Utilities: `GET /log`, `GET /log/count`, `DELETE /log`, `POST /response`.

## Harness matrix

| Harness | Binary | Install | API | Hook Format | Hooks Headless | Hooks Interactive |
|---|---|---|---|---|---|---|
| claude | `claude` | `npm -g @anthropic-ai/claude-code` | Anthropic | JSON (settings.json) | ✅ | ✅ |
| codex | `codex` | `npm -g @openai/codex` + belt plugin | Responses | JSON (plugin) | ✅ | ✅ |
| copilot | `copilot` | `npm -g @github/copilot` | OpenAI | JSON (Copilot v1) | ✅ | ✅ |
| grok | `grok` | `curl x.ai/cli/install.sh` | OpenAI | JSON (nested) | ❌ | ✅ (PTY) |
| pi | `pi` | `npm -g @earendil-works/pi-coding-agent` | OpenAI | TS extension | ✅ | ✅ |
| hermes | `hermes` | `pip install hermes-agent` | OpenAI | YAML + shell script | ✅ | ✅ |
| opencode | `opencode` | `npm -g opencode-ai` | OpenAI | TS plugin (belt) | ✅ | ✅ |

## Event coverage

The Claude plugin is the reference. Each harness maps these lifecycle events to its native names:

| Event | Claude | Codex | Copilot | Grok | Pi | Hermes |
|---|---|---|---|---|---|---|
| SessionStart | `SessionStart` | `SessionStart` | — | — | — | — |
| PromptSubmit | `UserPromptSubmit` | `UserPromptSubmit` | `userPromptSubmitted` | `UserPromptSubmit` | `before_agent_start` | `pre_llm_call` |
| PreToolUse | `PreToolUse` | `PreToolUse` | — | `PreToolUse` | — | `pre_tool_call` |
| PostToolUse | `PostToolUse` | `PostToolUse` | — | `PostToolUse` | — | `post_tool_call` |
| Stop | `Stop` | `Stop` | `agentStop` | `Stop` | `agent_end` | `subagent_stop` |
| PreCompact | `PreCompact` | `PreCompact` | — | — | — | — |

## Architecture

```
harness-test binary
├── server/     Mock inference server (OpenAI + Anthropic + Responses)
├── harness/    Data-driven harness configs (registry.go)
├── runner/     Test runner (install → config → hooks → run → verify)
│   └── pty.go  PTY session for interactive/TUI testing
└── main.go     CLI entry point
```

Each harness is a pure data struct — no per-harness code paths in the runner.
Adding a new harness means adding an entry to `harness.All` in `registry.go`.

## Adding a new harness

```go
"myharness": {
    Name: "myharness", Binary: "myharness",
    InstallCmd: []string{"npm", "install", "-g", "myharness"},
    APIFormat: harness.OpenAI,
    EnvVars: map[string]string{
        "MYHARNESS_BASE_URL": "{{.BaseURL}}",
    },
    APIKeyEnvVar: "MYHARNESS_API_KEY",
    DefaultModel: "gpt-4o-mini",
    HookFormat:    harness.JSONNested,
    HookConfigDir: ".myharness",
    Events: harness.Events{
        PromptSubmit: "UserPromptSubmit",
        Stop:         "Stop",
    },
    HeadlessCmd:     []string{"myharness", "-p"},
    HooksInHeadless: true,
    InteractiveCmd:  []string{"myharness"},
    ExitCommand:     "/exit",
    HooksInInteractive: true,
    CanInject:       true,
},
```

## Special cases

**Codex** — hooks only load through the plugin system. Uses `PreserveHome` (real HOME
with installed belt plugin) and passes model provider config via `-c` flags.

**Grok** — `-p` mode skips hooks (dispatcher is in TUI/pager layer). Tests use
interactive PTY mode with mock auth (`auth.json`) and trusted folders.

**Hermes** — `-z` mode is silent. Uses `hermes chat -q` instead. Config `base_url` is
ignored when `--provider` is set; `OPENROUTER_BASE_URL` env var overrides the hardcoded URL.

**OpenCode** — uses TypeScript plugin modules, not JSON hooks. Belt plugin provides
context injection via `experimental.chat.system.transform`.
