package harness

var standardEvents = Events{
	SessionStart: "SessionStart",
	PromptSubmit: "UserPromptSubmit",
	PreToolUse:   "PreToolUse",
	PostToolUse:  "PostToolUse",
	Stop:         "Stop",
	PreCompact:   "PreCompact",
}

func withACP(h Harness, cmd []string, args []string) Harness {
	h.ACPCmd = cmd
	h.ACPArgs = args
	return h
}

// withDetectEnvVars sets the runtime variables an agent exports, for entries
// built by a helper rather than written out as a literal.
func withDetectEnvVars(h Harness, vars ...string) Harness {
	h.DetectEnvVars = vars
	return h
}

func withTSPluginExport(h Harness, export string) Harness {
	h.TSPluginExport = export
	return h
}

// gatedShellArgs is what the in-flight probe asks an agent to run: a command
// every risk classifier flags, on a path that is absolute and fabricated, so
// it exists nowhere and can do nothing if an agent runs it anyway.
const gatedShellArgs = `{"command":"rm -rf /tmp/gated-probe-does-not-exist"}`

// withGatedTool names the tool this agent is expected to ask permission for,
// for harnesses built by a helper rather than written out as a literal.
func withGatedTool(h Harness, name, args string) Harness {
	h.ToolCallGated = ToolCall{Name: name, Args: args}
	return h
}

func withACPProvider(h Harness, cmd []string, args []string) Harness {
	h = withACP(h, cmd, args)
	h.ACPNeedsTempHome = true
	h.ACPConfigFiles = []ConfigFile{
		{Path: ".config/" + h.Name + "/" + h.Name + ".json",
			Content: `{"model":"openai/{{.Model}}","provider":{"openai":{"apiKey":"mock-key","baseURL":"{{.BaseURL}}/v1","models":{"{{.Model}}":{}}}}}`},
		{Path: ".local/share/" + h.Name + "/auth.json",
			Content: `{"openai":{"apiKey":"mock-key"}}`},
	}
	return h
}

func tsPluginHarness(name, binary, installPkg, hookDir string) Harness {
	return Harness{
		Name: name, Binary: binary,
		InstallCmd: []string{"npm", "install", "-g", installPkg},
		APIFormat:  Responses,
		EnvVars: map[string]string{
			"OPENAI_BASE_URL": "{{.BaseURL}}/v1",
		},
		APIKeyEnvVar: "OPENAI_API_KEY",
		DefaultModel: "openai/gpt-4o-mini",
		// The CLI takes provider/model; the wire request carries the bare model.
		AcceptedModels: []string{"gpt-4o-mini"},
		ToolCallName:   "read",
		ToolCallArgs:   `{"filePath":"README.md"}`,
		HookFormat:     TSPlugin,
		HookConfigDir:  hookDir,
		Events: Events{
			PromptSubmit: "experimental.chat.system.transform",
			PreToolUse:   "tool.execute.before",
			PostToolUse:  "tool.execute.after",
			Stop:         "session.idle",
		},
		PreserveHome:            true,
		NeedsGitRepo:            true,
		HeadlessCmd:             []string{binary, "run"},
		HeadlessModelArgs:       []string{"-m", "{{.Model}}", "--auto"},
		InteractiveCmd:          []string{binary, "--mini"},
		InteractiveArgs:         []string{"--auto", "-m", "{{.Model}}", "--prompt", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
	}
}

var codexProviderArgs = []string{
	"-c", `model="{{.Model}}"`,
	"-c", `model_provider="mock"`,
	"-c", `model_providers.mock.name="Mock"`,
	"-c", `model_providers.mock.base_url="{{.BaseURL}}"`,
	"-c", `model_providers.mock.env_key="OPENAI_API_KEY"`,
	"-c", `model_providers.mock.wire_api="responses"`,
	"-c", `model_context_window=2048`,
}

var All = map[string]Harness{
	"claude": {
		Name: "claude", Binary: "claude",
		InstallCmd:    []string{"npm", "install", "-g", "@anthropic-ai/claude-code"},
		DetectEnvVars: []string{"CLAUDECODE", "CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT"},
		APIFormat:     Anthropic,
		EnvVars: map[string]string{
			"ANTHROPIC_BASE_URL":      "{{.BaseURL}}",
			"ANTHROPIC_AUTH_TOKEN":    "mock-auth-token",
			"CLAUDE_CODE_OAUTH_TOKEN": "mock-oauth-token",
		},
		APIKeyEnvVar:  "ANTHROPIC_API_KEY",
		DefaultModel:  "claude-haiku-4-5-20251001",
		HookFormat:    JSONNested,
		HookConfigDir: ".claude",
		HookFileName:  "settings.json",
		HookWrapper:   `{"permissions":{"allow":["Bash(*)","Read(*)","Write(*)"]},"skipDangerousModePermissionPrompt":true,"hooks":%s}`,
		ConfigFiles: []ConfigFile{
			{Path: ".claude.json", Content: `{"projects":{"{{.RepoDir}}":{"hasTrustDialogAccepted":true}}}`},
			{Path: ".claude/settings.local.json", Content: `{"theme":"dark","hasCompletedOnboarding":true}`},
		},
		Events:                  standardEvents,
		SkillsDir:               ".claude/skills",
		HeadlessCmd:             []string{"claude", "-p"},
		HeadlessModelArgs:       []string{"--model", "{{.Model}}", "--dangerously-skip-permissions", "--max-turns", "2"},
		PostHeadlessCmd:         [][]string{{"-p", "--continue", "--dangerously-skip-permissions", "/compact"}},
		NeedsGitRepo:            true,
		InteractiveCmd:          []string{"claude"},
		InteractiveArgs:         []string{"--model", "{{.Model}}", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		ExitCommand:             "/exit",
		CompactCommand:          "/compact",
		OnboardingDismiss: []DismissAction{
			{Pattern: "theme"}, {Pattern: "Theme"}, {Pattern: "style"},
			{Pattern: "trust"}, {Pattern: "Trust"}, {Pattern: "onboarding"},
		},
		SDKCmd:  []string{"claude", "-p", "--output-format", "stream-json"},
		SDKArgs: []string{"--model", "{{.Model}}", "--dangerously-skip-permissions", "--verbose"},
	},
	"codex": {
		Name: "codex", Binary: "codex",
		InstallCmd:    []string{"npm", "install", "-g", "@openai/codex"},
		DetectEnvVars: []string{"CODEX_SANDBOX", "CODEX_THREAD_ID", "CODEX_MANAGED_BY_NPM"},
		PostInstall: [][]string{
			{"sh", "-c", "codex plugin marketplace add https://github.com/belt-sh/skills.git 2>/dev/null || true"},
			{"sh", "-c", "codex plugin add belt@belt-sh-skills 2>/dev/null || true"},
		},
		APIFormat:       Responses,
		EnvVars:         map[string]string{},
		APIKeyEnvVar:    "OPENAI_API_KEY",
		DefaultModel:    "gpt-4o-mini",
		ToolCallName:    "exec_command",
		ToolCallArgs:    `{"cmd":"cat README.md"}`,
		HookToolMatcher: "Bash",
		HookFormat:      JSONNested,
		HookConfigDir:   ".codex",
		HookFileName:    "hooks.json",
		// Measured in Docker 2026-09 (mock and belt hooks agreed): these events do
		// not fire here. A reason marked "observed only" states what happened and
		// not why; the others cite an investigation.
		KnownIssues: map[string]string{
			"headless:event:PRE_COMPACT": "/compact is TUI-only (slash_dispatch.rs); codex exec never auto-compacts",
			"sdk:event:PRE_COMPACT":      "SDK mode is codex exec, where /compact is only a user message and nothing auto-compacts (same investigation as headless)",
		},
		Events:      standardEvents,
		SkillsDir:   ".agents/skills",
		HeadlessCmd: []string{"codex", "exec"},
		HeadlessModelArgs: append([]string{
			"--dangerously-bypass-hook-trust",
			"--dangerously-bypass-approvals-and-sandbox",
		}, codexProviderArgs...),
		PromptViaStdin: true,
		NeedsGitRepo:   true,
		InteractiveCmd: []string{"codex"},
		InteractiveArgs: append(append([]string{
			"--dangerously-bypass-hook-trust",
			"-a", "never",
		}, codexProviderArgs...), "What is the project codename? Reply ONLY the codename."),
		InteractivePromptInArgs: true,
		SlowInput:               true,
		ExitCommand:             "/exit",
		CompactCommand:          "/compact",
		OnboardingDismiss: []DismissAction{
			{Pattern: "Yes, continue"},
		},
		SDKCmd:  []string{"codex", "exec", "--experimental-json"},
		SDKArgs: append([]string{"--dangerously-bypass-hook-trust", "--dangerously-bypass-approvals-and-sandbox"}, codexProviderArgs...),
	},
	"copilot": {
		Name: "copilot", Binary: "copilot",
		ToolCallGated: ToolCall{Name: "bash", Args: gatedShellArgs},
		InstallCmd:    []string{"npm", "install", "-g", "@github/copilot"},
		DetectEnvVars: []string{"COPILOT_MODEL", "COPILOT_GITHUB_TOKEN", "COPILOT_CLI", "COPILOT_LOADER_PID"},
		APIFormat:     OpenAI,
		EnvVars: map[string]string{
			"COPILOT_PROVIDER_BASE_URL": "{{.BaseURL}}",
			"COPILOT_MODEL":             "{{.Model}}",
			"COPILOT_ALLOW_ALL":         "true",
		},
		APIKeyEnvVar:  "COPILOT_PROVIDER_API_KEY",
		DefaultModel:  "gpt-4o-mini",
		HookFormat:    JSONCopilot,
		HookConfigDir: ".copilot/hooks",
		Events: Events{
			PromptSubmit: "userPromptSubmitted",
			Stop:         "sessionEnd",
		},
		SkillsDir:               ".copilot/skills",
		HeadlessCmd:             []string{"copilot", "--prompt"},
		InteractiveCmd:          []string{"copilot", "-i", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		ExitCommand:             "/exit",
		ACPCmd:                  []string{"copilot", "--acp"},
	},
	"grok": {
		Name: "grok", Binary: "grok",
		DetectEnvVars:  []string{"GROK_SESSION_ID", "GROK_WORKSPACE_ROOT"},
		ToolCallGated:  ToolCall{Name: "run_terminal_command", Args: gatedShellArgs},
		InstallCmd:     []string{"sh", "-c", "curl -fsSL https://x.ai/cli/install.sh | bash"},
		InstallBinDirs: []string{".grok/bin"},
		APIFormat:      Responses,
		ToolCallName:   "read_file",
		ToolCallArgs:   `{"target_file":"README.md"}`,
		ToolCallPath:   "chat/completions",
		EnvVars: map[string]string{
			"GROK_CLI_CHAT_PROXY_BASE_URL": "{{.BaseURL}}",
			"GROK_XAI_API_BASE_URL":        "{{.BaseURL}}",
			"GROK_MODELS_BASE_URL":         "{{.BaseURL}}",
			"GROK_FEEDBACK_BASE_URL":       "{{.BaseURL}}",
			"GROK_TRACE_UPLOAD_URL":        "{{.BaseURL}}",
			"GROK_MANAGED_CONFIG_URL":      "{{.BaseURL}}",
			"GROK_CONVERSATIONS_BASE_URL":  "{{.BaseURL}}",
		},
		APIKeyEnvVar: "XAI_API_KEY",
		// grok takes its model from the backend's /settings default, which the
		// mock serves as mock-model; "grok-3-mini" was never requested, and
		// AcceptedModels "grok-4" matched the title side call instead.
		DefaultModel:  "mock-model",
		HookFormat:    JSONNested,
		HookConfigDir: ".grok/hooks",
		Events: Events{
			PromptSubmit: "UserPromptSubmit",
			PreToolUse:   "PreToolUse",
			PostToolUse:  "PostToolUse",
			Stop:         "Stop",
		},
		ConfigFiles: []ConfigFile{
			{Path: ".grok/auth.json", Content: `{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"mock-test-token","auth_mode":"oidc","create_time":"2026-01-01T00:00:00Z","user_id":"mock-user","email":"mock@test.invalid","expires_at":"2030-01-01T00:00:00Z","refresh_token":"mock-refresh-token","oidc_issuer":"https://auth.x.ai","oidc_client_id":"b1a00492-073a-47ea-816f-4c329264a828","coding_data_retention_opt_out":false}}`},
			{Path: ".grok/trusted_folders.toml", Content: "[folders.\"{{.RepoDir}}\"]\ntrusted = true\ndecided_at = 1786997000\n"},
		},
		SkillsDir:      ".grok/skills",
		HeadlessCmd:    []string{"grok", "-p"},
		InteractiveCmd: []string{"grok", "--always-approve"},
		ExitCommand:    "/exit",
		CompactCommand: "/compact",
		ACPCmd:         []string{"grok", "agent", "stdio"},
	},
	"pi": {
		Name: "pi", Binary: "pi",
		InstallCmd:        []string{"npm", "install", "-g", "--ignore-scripts", "@earendil-works/pi-coding-agent"},
		DetectEnvVars:     []string{"PI_CODING_AGENT"},
		APIFormat:         OpenAI,
		EnvVars:           map[string]string{},
		APIKeyEnvVar:      "OPENROUTER_API_KEY",
		NeedsIntercept:    true,
		DefaultModel:      "openai/gpt-4o-mini",
		HookFormat:        TSExtension,
		HookConfigDir:     ".pi/agent/extensions",
		Events:            Events{PromptSubmit: "before_agent_start", Stop: "agent_end"},
		HeadlessCmd:       []string{"pi", "-p"},
		HeadlessModelArgs: []string{"--provider", "openrouter", "--model", "{{.Model}}", "--no-session"},
		InteractiveCmd:    []string{"pi"},
		InteractiveArgs:   []string{"--provider", "openrouter", "--model", "{{.Model}}", "--no-session"},
		ExitCommand:       "/exit",
		SDKCmd:            []string{"pi", "--mode", "json"},
		SDKArgs:           []string{"--provider", "openrouter", "--model", "{{.Model}}", "--no-session"},
	},
	"kiro": {
		Name: "kiro", Binary: "kiro-cli",
		DetectEnvVars:  []string{"KIRO_SESSION_ID", "KIRO_VERSION"},
		ToolCallGated:  ToolCall{Name: "shell", Args: gatedShellArgs},
		InstallCmd:     []string{"sh", "-c", "curl -fsSL https://cli.kiro.dev/install | bash"},
		InstallBinDirs: []string{".local/bin"},
		APIFormat:      OpenAI,
		EnvVars:        map[string]string{},
		APIKeyEnvVar:   "KIRO_API_KEY",
		DefaultModel:   "kiro-default",
		NeedsGitRepo:   true,
		ToolCallName:   "read",
		ToolCallArgs:   `{"operations":[{"mode":"Line","path":"README.md"}]}`,
		// kiro-cli runs hooks from the agent config, not from .kiro/hooks/*.json
		// (those are Kiro IDE hook documents; the CLI never fires them — verified
		// 2026-09 on kiro-cli 2.21 with the documented PromptSubmit/AgentStop
		// triggers). belt installs its own agent, belt.json, and selects it with
		// chat.defaultAgent: the V1 engine ignores a kiro_default.json override
		// and runs the built-in agent, so hooks there never fired on V1.
		HookFormat:    JSONKiro,
		HookConfigDir: ".kiro/agents",
		HookFileName:  "belt.json",
		HookTimeoutMs: true,
		Events: Events{
			SessionStart: "agentSpawn",
			PromptSubmit: "userPromptSubmit",
			PreToolUse:   "preToolUse",
			PostToolUse:  "postToolUse",
			Stop:         "stop",
		},
		NeedsIntercept: true,
		// kiro-cli has two engines. `chat --no-interactive` starts V1 for 75% of
		// installs and V2 for the rest (the embedded "v2_non_interactive"
		// rollout, treatment_percent 25); fresh Docker installs landed on
		// either. Headless pins V1, the one most scripted runs get; the
		// TUI and ACP run V2. V2's non-interactive client also drops --model
		// ("failed to set model 'kiro-default': Method not found", 2.21.3).
		HeadlessCmd: []string{"kiro-cli", "chat", "--no-interactive", "--trust-all-tools", "--agent-engine", "v1", "--model", "{{.Model}}"},
		ToolCallByMode: map[Mode]ToolCall{
			ModeHeadless: {Name: "fs_read", Args: `{"operations":[{"mode":"Line","path":"README.md"}]}`},
		},
		// Without --model, kiro sends an empty modelId and the backend picks.
		InteractiveCmd:          []string{"kiro-cli", "chat", "--trust-all-tools", "--model", "{{.Model}}"},
		InteractiveArgs:         []string{"What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		ExitCommand:             "/exit",
		OnboardingDismiss: []DismissAction{
			{Pattern: "navigate"}, {Pattern: "select"}, {Pattern: "Welcome"},
		},
		ACPCmd:             []string{"kiro-cli", "acp"},
		ACPArgs:            []string{"--model", "{{.Model}}"},
		ACPAutoApproveArgs: []string{"--trust-all-tools"},
	},

	"omp": {
		Name: "omp", Binary: "omp",
		ToolCallGated:     ToolCall{Name: "bash", Args: gatedShellArgs},
		InstallCmd:        []string{"npm", "install", "-g", "@oh-my-pi/pi-coding-agent"},
		APIFormat:         OpenAI,
		EnvVars:           map[string]string{},
		NeedsIntercept:    true,
		APIKeyEnvVar:      "OPENAI_API_KEY",
		DefaultModel:      "gpt-4o-mini",
		HookFormat:        TSExtension,
		HookConfigDir:     ".omp/agent/extensions",
		Events:            Events{PromptSubmit: "before_agent_start", Stop: "agent_end"},
		NeedsGitRepo:      true, // project AGENTS.md is only read inside a git project
		HeadlessCmd:       []string{"omp", "-p"},
		HeadlessModelArgs: []string{"--model", "{{.Model}}", "--approval-mode", "yolo"},
		// omp >= 18.1.15 drops a line typed right after the welcome screen; the
		// documented "omp <message>" form starts the TUI with the prompt queued.
		InteractiveCmd:          []string{"omp"},
		InteractiveArgs:         []string{"--model", "{{.Model}}", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		ExitCommand:             "/exit",
		ACPCmd:                  []string{"omp", "acp"},
		ACPArgs:                 []string{"--model", "{{.Model}}"},
		SDKCmd:                  []string{"omp", "--mode", "json"},
		SDKArgs:                 []string{"--model", "{{.Model}}", "--approval-mode", "yolo"},
	},
	"hermes": {
		Name: "hermes", Binary: "hermes",
		DetectEnvVars:  []string{"HERMES_SESSION_ID", "HERMES_INTERACTIVE"},
		ToolCallGated:  ToolCall{Name: "terminal", Args: gatedShellArgs},
		InstallCmd:     []string{"pip", "install", "--break-system-packages", "hermes-agent[acp]"},
		InstallBinDirs: []string{".local/bin"},
		APIFormat:      OpenAI,
		ToolCallName:   "read_file",
		ToolCallArgs:   `{"path":"README.md"}`,
		EnvVars: map[string]string{
			"OPENROUTER_BASE_URL": "{{.BaseURL}}/v1",
		},
		APIKeyEnvVar:  "OPENROUTER_API_KEY",
		DefaultModel:  "openai/gpt-4o-mini",
		HookFormat:    YAML,
		HookConfigDir: ".hermes",
		Events: Events{
			PromptSubmit: "pre_llm_call",
			PreToolUse:   "pre_tool_call",
			PostToolUse:  "post_tool_call",
			Stop:         "on_session_end",
		},
		ConfigFiles: []ConfigFile{
			{Path: ".hermes/.env", Content: "OPENROUTER_API_KEY={{.APIKey}}\n"},
			{Path: ".hermes/config.yaml", Content: "model:\n  default: {{.Model}}\n  provider: openrouter\n  base_url: {{.BaseURL}}/v1\nhooks: {}\nhooks_auto_accept: true\n"},
		},
		HeadlessCmd:       []string{"hermes", "chat", "-q"},
		HeadlessModelArgs: []string{"-m", "{{.Model}}", "--provider", "openrouter", "--accept-hooks"},
		InteractiveCmd:    []string{"hermes"},
		ExitCommand:       "/exit",
		ACPCmd:            []string{"hermes", "acp"},
	},
	"kilo": withGatedTool(withACPProvider(withTSPluginExport(
		withDetectEnvVars(tsPluginHarness("kilo", "kilo", "@kilocode/cli", ".kilo/plugins"),
			"KILO_TREE_SITTER_WASM_DIR"),
		`export default { id: "belt", server: BeltPlugin };`),
		[]string{"kilo", "acp"}, []string{"--cwd", "{{.RepoDir}}"}),
		"bash", gatedShellArgs),
	"kimi": {
		Name: "kimi", Binary: "kimi",
		ToolCallGated: ToolCall{Name: "Bash", Args: gatedShellArgs},
		InstallCmd:    []string{"npm", "install", "-g", "@moonshot-ai/kimi-code"},
		APIFormat:     OpenAI,
		EnvVars: map[string]string{
			"KIMI_CODE_BASE_URL": "{{.BaseURL}}/coding/v1",
		},
		APIKeyEnvVar:  "",
		DefaultModel:  "gpt-4o-mini",
		ToolCallName:  "Read",
		ToolCallArgs:  `{"path":"{{.RepoDir}}/README.md"}`,
		HookFormat:    TOML,
		HookConfigDir: ".kimi-code",
		Events: Events{
			SessionStart: "SessionStart",
			PromptSubmit: "UserPromptSubmit",
			PostToolUse:  "PostToolUse",
			Stop:         "Stop",
		},
		TokenHashInput: `{"oauthHost":"https://auth.kimi.com","baseUrl":"{{.BaseURL}}/coding/v1"}`,
		ConfigFiles: []ConfigFile{
			{Path: ".kimi-code/config.toml", Content: "default_model = \"mock\"\n\n[providers.mock]\ntype = \"openai\"\napi_key = \"mock-key\"\nbase_url = \"{{.BaseURL}}/v1\"\n\n[models.mock]\nprovider = \"mock\"\nmodel = \"{{.Model}}\"\nmax_context_size = 128000\nmax_output_size = 4096\n"},
			{Path: ".kimi-code/credentials/kimi-code-env-{{.TokenHash16}}.json", Content: `{"access_token":"mock-kimi-token","refresh_token":"mock-refresh","expires_at":99999999999,"scope":"","token_type":"Bearer","expires_in":86400}`},
		},
		NeedsGitRepo:   true,
		HeadlessCmd:    []string{"kimi", "-p"},
		InteractiveCmd: []string{"kimi"},
		ExitCommand:    "/exit",
		// "Trust this folder" is preselected; Enter confirms it.
		OnboardingDismiss: []DismissAction{{Pattern: "Trust this folder?", Required: true}},
		ACPCmd:            []string{"kimi", "acp"},
	},
	"goose": {
		Name: "goose", Binary: "goose",
		DetectEnvVars:  []string{"GOOSE_DISABLE_KEYRING"},
		ToolCallGated:  ToolCall{Name: "shell", Args: gatedShellArgs},
		InstallCmd:     []string{"sh", "-c", "mkdir -p $HOME/.local/bin && curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/goose-x86_64-unknown-linux-gnu.tar.bz2 | tar -xj --strip-components=0 -C $HOME/.local/bin"},
		InstallBinDirs: []string{".local/bin"},
		PluginManifest: ".agents/plugins/belt/plugin.json",
		APIFormat:      OpenAI,
		EnvVars: map[string]string{
			"GOOSE_PROVIDER":               "mock",
			"GOOSE_MODEL":                  "{{.Model}}",
			"GOOSE_MODE":                   "auto",
			"GOOSE_DISABLE_SESSION_NAMING": "true",
			"OPENAI_API_KEY":               "mock-key",
		},
		APIKeyEnvVar: "",
		DefaultModel: "gpt-4o-mini",
		ToolCallName: "shell",
		ToolCallArgs: `{"command":"cat README.md"}`,
		ConfigFiles: []ConfigFile{
			{Path: ".config/goose/custom_providers/mock.json", Content: `{"name":"mock","engine":"openai","display_name":"Mock","api_key_env":"OPENAI_API_KEY","base_url":"{{.BaseURL}}/v1/chat/completions","models":[{"name":"gpt-4o-mini","context_limit":128000}],"supports_streaming":true,"requires_auth":true}`},
			{Path: ".agents/plugins/belt-test/plugin.json", Content: `{"name":"belt-test","version":"1.0.0","description":"Belt harness test hooks"}`},
			// goose >= 1.50 opens a provider wizard (OpenRouter OAuth) in the TUI
			// unless the provider is set in config.yaml; env vars alone only cover `goose run`.
			{Path: ".config/goose/config.yaml", Content: "GOOSE_PROVIDER: mock\nGOOSE_MODEL: {{.Model}}\nGOOSE_MODE: auto\n"},
		},
		HookFormat:    JSONNested,
		HookConfigDir: GoosePluginDir + "/hooks",
		HookFileName:  "hooks.json",
		Events: Events{
			SessionStart: "SessionStart",
			PromptSubmit: "UserPromptSubmit",
			PostToolUse:  "PostToolUse",
			Stop:         "Stop",
		},
		NeedsGitRepo:   true,
		HeadlessCmd:    []string{"goose", "run", "-t"},
		InteractiveCmd: []string{"goose"},
		ExitCommand:    "/exit",
		ACPCmd:         []string{"goose", "acp"},
	},
	"gemini": {
		Name: "gemini", Binary: "gemini",
		ToolCallGated: ToolCall{Name: "run_shell_command", Args: gatedShellArgs},
		InstallCmd:    []string{"npm", "install", "-g", "@google/gemini-cli"},
		DetectEnvVars: []string{"GEMINI_CLI", "GEMINI_SESSION_ID", "GEMINI_CWD"},
		APIFormat:     Gemini,
		EnvVars: map[string]string{
			"GOOGLE_GEMINI_BASE_URL":     "{{.BaseURL}}",
			"GEMINI_CLI_TRUST_WORKSPACE": "true",
			"GEMINI_API_KEY":             "mock-key",
		},
		APIKeyEnvVar:  "",
		DefaultModel:  "gemini-3.5-flash",
		ToolCallName:  "write_file",
		ToolCallArgs:  `{"file_path":"{{.RepoDir}}/test-output.txt","content":"test"}`,
		HookFormat:    JSONNested,
		HookConfigDir: ".gemini",
		HookFileName:  "settings.json",
		HookWrapper:   `{"baseUrl":"{{.BaseURL}}","security":{"auth":{"selectedType":"gemini-api-key","useExternal":true}},"hooks":%s}`,
		HookTimeoutMs: true,
		// Measured in Docker 2026-09 (mock and belt hooks agreed): these events do
		// not fire here. A reason marked "observed only" states what happened and
		// not why; the others cite an investigation.
		KnownIssues: map[string]string{
			"acp:event:POST_TOOL":     "gemini ACP runs tools with invocation.execute() directly and never enters executeToolWithHooks (gemini-cli 0.59 source)",
			"acp:event:PRE_TOOL":      "gemini ACP runs tools with invocation.execute() directly and never enters executeToolWithHooks (gemini-cli 0.59 source)",
			"acp:event:SESSION_START": "gemini fires SessionStart only in the non-interactive main(); the ACP newSession never does (gemini-cli 0.59 source)",
		},
		Events: Events{
			SessionStart: "SessionStart",
			PromptSubmit: "BeforeAgent",
			PreToolUse:   "BeforeTool",
			PostToolUse:  "AfterTool",
			Stop:         "SessionEnd",
			PreCompact:   "PreCompress",
		},
		NeedsGitRepo: true,
		HeadlessCmd:  []string{"gemini", "-p"},
		// Pin the model in every mode. Unpinned, gemini picked its own (a
		// flash-lite router plus gemini-3.1-pro-preview), and AcceptedModels
		// once listed the prefixes "gemini-3"/"gemini-2" so any of them passed.
		HeadlessModelArgs:       []string{"--yolo", "-m", "gemini-3.5-flash"},
		ACPArgs:                 []string{"-m", "gemini-3.5-flash"},
		InteractiveCmd:          []string{"gemini", "--yolo", "-m", "gemini-3.5-flash", "-i", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		SlowInput:               true,
		ExitCommand:             "/exit",
		CompactCommand:          "/compress",
		ACPCmd:                  []string{"gemini", "--acp"},
	},
	"qwen": {
		Name: "qwen", Binary: "qwen",
		DetectEnvVars: []string{"QWEN_CODE_CLI", "QWEN_CODE_SESSION_ID"},
		ToolCallGated: ToolCall{Name: "run_shell_command", Args: gatedShellArgs},
		InstallCmd:    []string{"npm", "install", "-g", "@qwen-code/qwen-code"},
		APIFormat:     OpenAI,
		EnvVars: map[string]string{
			"OPENAI_BASE_URL": "{{.BaseURL}}/v1",
		},
		APIKeyEnvVar:      "OPENAI_API_KEY",
		DefaultModel:      "gpt-4o-mini",
		ToolCallName:      "read_file",
		ToolCallArgs:      `{"file_path":"{{.RepoDir}}/README.md"}`,
		HookFormat:        JSONNested,
		HookTimeoutMs:     true, // gemini-cli fork: "timeout" is milliseconds
		HookConfigDir:     ".qwen",
		HookFileName:      "settings.json",
		HookWrapper:       `{"permissions":{"allow":["Bash(*)","Read(*)","Write(*)"]},"hooks":%s}`,
		Events:            standardEvents,
		NeedsGitRepo:      true,
		HeadlessCmd:       []string{"qwen", "-p"},
		HeadlessModelArgs: []string{"--model", "{{.Model}}", "--yolo", "--auth-type", "openai"},
		// -i/--prompt-interactive runs the prompt and stays in the TUI; a bare
		// positional prompt would run one-shot. Typing it instead (SendLine)
		// made qwen fire UserPromptSubmit after the turn had already started,
		// so the hook's context never reached the model.
		InteractiveCmd:          []string{"qwen"},
		InteractiveArgs:         []string{"--model", "{{.Model}}", "--yolo", "--auth-type", "openai", "-i", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		SlowInput:               true,
		PostHeadlessCmd:         [][]string{{"-p", "--continue", "--yolo", "--auth-type", "openai", "/compress"}},
		ExitCommand:             "/exit",
		CompactCommand:          "/compress",
		ACPCmd:                  []string{"qwen", "--acp"},
		ACPArgs:                 []string{"--auth-type", "openai", "--model", "{{.Model}}"},
		ACPAutoApproveArgs:      []string{"--yolo"},
	},
	"opencode": withGatedTool(withACPProvider(tsPluginHarness("opencode", "opencode", "opencode-ai", ".config/opencode/plugins"),
		[]string{"opencode", "acp"}, []string{"--cwd", "{{.RepoDir}}"}),
		"bash", gatedShellArgs),
	"droid": {
		Name: "droid", Binary: "droid",
		DetectEnvVars: []string{"DROID_PROJECT_DIR", "FACTORY_UPSTREAM_CLIENT_TYPE"},
		// droid mangles the project path into the directory name.
		SessionDir:    "{{.HomeDir}}/.factory/sessions/{{.MangledRepoDir}}",
		SessionExt:    ".jsonl",
		ToolCallGated: ToolCall{Name: "Execute", Args: gatedShellArgs},
		InstallCmd:    []string{"npm", "install", "-g", "droid"},
		APIFormat:     OpenAI,
		EnvVars: map[string]string{
			"FACTORY_API_BASE_URL":    "{{.BaseURL}}",
			"FACTORY_API_KEY":         "fk-mock-key-0123456789abcdef0123",
			"FACTORY_DISABLE_KEYRING": "1",
		},
		APIKeyEnvVar:   "",
		DefaultModel:   "mock-model",
		ToolCallName:   "Read",
		ToolCallArgs:   `{"file_path":"{{.RepoDir}}/README.md"}`,
		HookFormat:     JSONNested,
		HookConfigDir:  ".factory",
		HookFileName:   "hooks.json",
		HookWrapper:    "%s",
		HookNoEnvelope: true,
		// Measured in Docker 2026-09 (mock and belt hooks agreed): these events do
		// not fire here. A reason marked "observed only" states what happened and
		// not why; the others cite an investigation.
		KnownIssues: map[string]string{
			"acp:event:PRE_COMPACT":      "ACP mode is droid exec; PreCompact fires only from the TUI's manual compaction (droid 0.217 source)",
			"acp:event:PROMPT":           "the ACP agent (exec --output-format acp, also each acp-daemon child) calls the turn runner directly; UserPromptSubmit runs only in the JSON-RPC processUserMessage path (droid 0.217 source)",
			"headless:event:PRE_COMPACT": "PreCompact fires only from the TUI's manual compaction; exec never compacts (droid 0.217 source)",
			"sdk:event:PRE_COMPACT":      "SDK mode is droid exec -o stream-jsonrpc; PreCompact still fires only from the TUI's manual compaction (same investigation as headless)",
			"headless:event:PROMPT":      "plain exec (-o text/json/stream-json) runs the turn through kDA, which never calls executeUserPromptSubmitHooks; only -o stream-jsonrpc goes through the JSON-RPC processUserMessage path (droid 0.217 source)",
		},
		Events: standardEvents,
		ConfigFiles: []ConfigFile{
			{Path: ".factory/settings.json", Content: `{"customModels":[{"model":"mock-model","displayName":"Mock","baseUrl":"{{.BaseURL}}/v1","apiKey":"mock-key","provider":"openai","maxOutputTokens":4096,"maxContextLimit":128000}],"sessionDefaultSettings":{"model":"custom:mock-model"}}`},
		},
		SkillsDir:    ".factory/skills",
		NeedsGitRepo: true,
		HeadlessCmd:  []string{"droid", "exec"},
		// -o json is the plain exec path scripts use. -o stream-jsonrpc goes
		// through the JSON-RPC worker the TUI uses, the only exec path that runs
		// the prompt hook; testing it here hid that plain exec never does.
		HeadlessModelArgs: []string{"--auto", "high", "-m", "{{.Model}}", "-o", "json"},
		PostHeadlessCmd:   [][]string{{"exec", "--session-id", "{{.SessionID}}", "--auto", "high", "-m", "{{.Model}}", "/compact"}},
		InteractiveCmd:    []string{"droid"},
		// The TUI has no -m flag: "-m mock-model" became the prompt text and the
		// session ran Factory's default model. It takes the model from
		// sessionDefaultSettings (settings.json above) instead.
		InteractiveArgs: []string{"--auto", "high"},
		// droid 0.217 asks to trust the project folder before the first turn;
		// "1. Trust this folder" is preselected. A prompt typed into the dialog
		// was swallowed and its Enter confirmed the trust instead.
		OnboardingDismiss: []DismissAction{{Pattern: "Trust this folder", Required: true}},
		ExitCommand:       "/exit",
		// /compact opens the Context Usage panel in droid 0.217; /compress
		// compacts, after a "Confirm /compress" dialog.
		CompactCommand: "/compress",
		CompactConfirm: true,
		// SDK mode covers the other exec path: -o stream-jsonrpc runs the turn
		// through the JSON-RPC worker the TUI drives, the only exec mode that
		// runs the prompt hook. Headless deliberately tests plain exec, so
		// without this nothing exercised the path that works.
		SDKCmd:             []string{"droid", "exec"},
		SDKArgs:            []string{"--auto", "high", "-m", "{{.Model}}", "-o", "stream-jsonrpc"},
		ACPCmd:             []string{"droid", "exec", "--output-format", "acp"},
		ACPArgs:            []string{"-m", "{{.Model}}"},
		ACPAutoApproveArgs: []string{"--auto", "high"},
	},

	// IDE-only agents: detection and hook install only, no test support.
	// Cursor agent CLI (`agent`, alias cursor-agent). Same ~/.cursor tree as the
	// editor: hooks.json (v1, camelCase events), rules, AGENTS.md. Talks
	// Connect-protobuf to CURSOR_API_ENDPOINT; the mock speaks enough of
	// agent.v1.AgentService (RunSSE + BidiAppend) to run a turn — see
	// server/cursor.go. beforeSubmitPrompt cannot inject context; sessionStart
	// can (additional_context).
	"cursor": {
		// cursor-agent installs two names for the same binary, "agent" and
		// "cursor-agent". Detection uses the specific one: grok's installer
		// also drops an "agent" into ~/.grok/bin, so the generic name made
		// every machine with grok report cursor as installed. The commands
		// below keep calling "agent", which is what cursor documents.
		Name: "cursor", Binary: "cursor-agent",
		InstallCmd:       []string{"sh", "-c", "curl -fsSL https://cursor.com/install | bash"},
		InstallBinDirs:   []string{".local/bin"},
		DetectEnvVars:    []string{"CURSOR_TRACE_ID", "CURSOR_AGENT", "CURSOR_INVOKED_AS", "CURSOR_VERSION"},
		DetectConfigDirs: []string{".cursor"},
		APIFormat:        Cursor,
		EnvVars: map[string]string{
			"CURSOR_API_ENDPOINT": "{{.BaseURL}}",
		},
		APIKeyEnvVar: "CURSOR_API_KEY",
		DefaultModel: "gpt-4o-mini",
		ToolCallName: "read",
		ToolCallArgs: `{"path":"README.md"}`,
		HookFormat:   JSONFlat,
		ServerRequestedHooks: map[Mode][]string{
			ModeHeadless:    {PromptSubmit.Tag(), Stop.Tag(), PreCompact.Tag()},
			ModeInteractive: {PreCompact.Tag()},
		},
		HookConfigDir: ".cursor",
		HookFileName:  "hooks.json",
		HookWrapper:   `{"version":1,"hooks":%s}`,
		Events: Events{
			SessionStart: "sessionStart",
			PromptSubmit: "beforeSubmitPrompt",
			PreToolUse:   "preToolUse",
			PostToolUse:  "postToolUse",
			Stop:         "stop",
			PreCompact:   "preCompact",
		},
		NeedsGitRepo:            true,
		HeadlessCmd:             []string{"agent", "-p", "--trust", "--force", "--output-format", "text"},
		HeadlessModelArgs:       []string{"--model", "{{.Model}}"},
		InteractiveCmd:          []string{"agent"},
		InteractiveArgs:         []string{"--trust", "--force", "--model", "{{.Model}}", "What is the project codename? Reply ONLY the codename."},
		InteractivePromptInArgs: true,
		ExitCommand:             "/exit",
	},
	// Windsurf: ~/.codeium/windsurf/hooks.json, {"hooks":{event:[{command}]}} with
	// snake_case events (docs 2026-09). Exit code is the only feedback channel;
	// no context injection at all. No CLI, so unverified by the runner.
	"windsurf": {
		Name: "windsurf", Binary: "windsurf",
		DetectEnvVars:    []string{"WINDSURF_EXTENSION_HOST_ROLE"},
		DetectConfigDirs: []string{".windsurf", ".codeium/windsurf"},
		HookFormat:       JSONFlat,
		HookFlatBare:     true,
		HookConfigDir:    ".codeium/windsurf",
		HookFileName:     "hooks.json",
		Events: Events{
			PromptSubmit: "pre_user_prompt",
			PreToolUse:   "pre_run_command",
			PostToolUse:  "post_run_command",
			Stop:         "post_cascade_response",
		},
	},
}

// InstructionFiles lists, per harness, the file the agent loads into its
// system prompt at user scope (relative to $HOME) and at project scope
// (relative to the repo root). belt writes its rules block there. Sources:
// each agent's docs, 2026-09. cursor and hermes have no user-scope file:
// Cursor keeps global rules in its settings UI, Hermes loads only SOUL.md
// (identity, not instructions) globally.
type InstructionFiles struct {
	User, Project, Frontmatter string
	MaxBytes                   int
	Note                       string
}

const (
	fmCursor  = "---\ndescription: belt\nalwaysApply: true\n---\n"
	fmCopilot = "---\napplyTo: \"**\"\n---\n"
	fmKiro    = "---\ninclusion: always\n---\n"
)

var instructionFiles = map[string]InstructionFiles{
	"claude":   {User: ".claude/CLAUDE.md", Project: "CLAUDE.md"},
	"codex":    {User: ".codex/AGENTS.md", Project: "AGENTS.md"},
	"copilot":  {User: ".copilot/instructions/belt.instructions.md", Project: ".github/instructions/belt.instructions.md", Frontmatter: fmCopilot},
	"cursor":   {Project: ".cursor/rules/belt.mdc", Frontmatter: fmCursor, Note: "Cursor has no global rules file on disk; global rules live in Cursor Settings → Rules"},
	"droid":    {User: ".factory/AGENTS.md", Project: "AGENTS.md"},
	"gemini":   {User: ".gemini/GEMINI.md", Project: "GEMINI.md"},
	"goose":    {User: ".config/goose/.goosehints", Project: ".goosehints"},
	"grok":     {User: ".grok/AGENTS.md", Project: "AGENTS.md"},
	"hermes":   {Project: "AGENTS.md", Note: "Hermes loads only SOUL.md globally, which is the agent identity rather than instructions"},
	"kilo":     {User: ".kilocode/rules/belt.md", Project: "AGENTS.md", Note: "legacy rules dir; newer Kilo prefers the instructions key in ~/.config/kilo/kilo.jsonc"},
	"kimi":     {User: ".kimi-code/AGENTS.md", Project: "AGENTS.md"},
	"kiro":     {User: ".kiro/steering/belt.md", Project: ".kiro/steering/belt.md", Frontmatter: fmKiro},
	"omp":      {User: ".omp/agent/AGENTS.md", Project: "AGENTS.md"},
	"opencode": {User: ".config/opencode/AGENTS.md", Project: "AGENTS.md"},
	"pi":       {User: ".pi/agent/AGENTS.md", Project: "AGENTS.md"},
	"qwen":     {User: ".qwen/QWEN.md", Project: "QWEN.md"},
	"windsurf": {User: ".codeium/windsurf/memories/global_rules.md", Project: ".windsurf/rules/belt.md", MaxBytes: 6000, Note: "Windsurf caps global_rules.md at 6000 characters"},
}

// skillsDirs is the user-scope Agent Skills directory per harness (relative
// to $HOME), from each agent's docs, 2026-09. Entries built by helper
// functions (kilo, opencode) and IDE-only agents get theirs here; the rest
// set SkillsDir inline above.
var skillsDirs = map[string]string{
	"cursor":   ".cursor/skills",
	"gemini":   ".gemini/skills",
	"goose":    ".config/goose/skills",
	"hermes":   ".hermes/skills",
	"kilo":     ".kilocode/skills",
	"kimi":     ".kimi-code/skills",
	"kiro":     ".kiro/skills",
	"omp":      ".omp/agent/skills",
	"opencode": ".config/opencode/skills",
	"pi":       ".pi/agent/skills",
	"qwen":     ".qwen/skills",
	"windsurf": ".codeium/windsurf/skills",
}

// configDirEnvs names the env var that relocates an agent's config dir.
var configDirEnvs = map[string]string{
	"claude":   "CLAUDE_CONFIG_DIR",
	"codex":    "CODEX_HOME",
	"copilot":  "COPILOT_HOME",
	"hermes":   "HERMES_HOME",
	"kimi":     "KIMI_CODE_HOME",
	"goose":    "XDG_CONFIG_HOME",
	"opencode": "XDG_CONFIG_HOME",
}

// decorate applies f to a registered harness, and refuses to invent one.
//
// The satellite tables are keyed by the same names as All, and one of these
// loops used to read All[name] without checking: a typo there inserted a
// zero-valued Harness under the misspelled name, which then looked like an
// agent with no binary, no hooks and no events.
func decorate(table string, name string, f func(*Harness)) {
	h, ok := All[name]
	if !ok {
		panic(table + ": unknown harness " + name)
	}
	f(&h)
	All[name] = h
}

func init() {
	for name, dir := range skillsDirs {
		decorate("skillsDirs", name, func(h *Harness) {
			if h.SkillsDir == "" {
				h.SkillsDir = dir
			}
		})
	}
	for name, env := range configDirEnvs {
		decorate("configDirEnvs", name, func(h *Harness) { h.ConfigDirEnv = env })
	}
	for name, f := range instructionFiles {
		decorate("instructionFiles", name, func(h *Harness) {
			h.InstructionFile = f.User
			h.ProjectInstructionFile = f.Project
			h.InstructionFrontmatter = f.Frontmatter
			h.InstructionMaxBytes = f.MaxBytes
			h.InstructionNote = f.Note
		})
	}
}

// Investigated but not added:
//
// Amp (@ampcode/cli) — Uses Rivet WebSocket protocol (/actors endpoint).
//   The CLI is a thin client; the agent loop runs server-side on ampcode.com.
//   Cannot mock with an HTTP LLM endpoint. Would need a full Rivet actor server.
//   Has TypeScript plugins via amp.on() with 5 events (tool.call, tool.result,
//   agent.start, agent.end, session.start). Headless: amp -x "prompt".
//
// Kiro — now added above with NeedsIntercept. Hooks live in the agent config
//   (~/.kiro/agents/kiro_default.json, keys agentSpawn/userPromptSubmit/
//   preToolUse/postToolUse/stop, `command` + `timeout_ms`); userPromptSubmit
//   stdout is appended to the prompt as plain text. Uses Amazon Q backend
//   (q.us-east-1.amazonaws.com, runtime.*.kiro.dev), NOT direct Bedrock.
//   Rust binary (reqwest + rustls). Supports HTTPS_PROXY since v1.8.0.
//   No BYOK: github.com/kirodotdev/Kiro/issues/695. MITM proxy approach
//   intercepts via HTTPS_PROXY. TLS verification may fail if kiro uses
//   webpki-roots (compiled-in CAs) instead of rustls-native-certs (system store).
//
// Aider (aider-chat on PyPI) — No hook/plugin system. Has --lint-cmd and
//   --test-cmd post-edit hooks only. BYOK via OPENAI_API_BASE + OPENAI_API_KEY.
//   Headless: aider --message "prompt" --yes-always.
//
// Cursor `agent` CLI — added above (2026-09). Notes from the investigation:
//   the public build has no client-side provider; a hidden "agent-cli-local"
//   mode (--base-url, --authless, CURSOR_LOCAL_AGENT_BASE_URL) exists in the
//   option parser but its runtime is not shipped. After
//   /auth/exchange_user_api_key the CLI speaks Connect-protobuf to
//   aiserver.v1.* (unary) and agent.v1.AgentService/RunSSE (server stream,
//   request = BidiRequestId) with client messages pushed through
//   BidiService/BidiAppend as hex-encoded AgentClientMessage, gzip above a
//   few hundred bytes. GetServerConfig.http2_config=FORCE_ALL_DISABLED(1)
//   keeps it on HTTP/1.1; otherwise it opens an h2c bidi Run stream. The
//   server asks for rules via ExecServerMessage.request_context_args and
//   gets RequestContext{rules[]} back. Schemas: see server/cursor.go.
// Windsurf / Cline / Roo — IDE extensions only, no standalone CLI.
//
// Remaining skip investigations (3 skips across 2 harnesses, 249/3):
//
// Codex PreCompact H (1 skip): /compact is TUI-only slash command
//   (slash_dispatch.rs). codex exec resume --last exists but treats
//   /compact as user message. No auto-compact in exec mode — context
//   managed by trimming skills/truncating, not summarizing. Tested 3x
//   exec resume --last with model_context_window=64, no PreCompact fired.
//
// Droid PreCompact H (1 skip): exec --session-id treats /compact as user
//   message. Slash dispatch is TUI-only. No auto-compact trigger: tested
//   compactionTokenLimit:100 (top-level + general nested + per-model +
//   --settings override), high Anthropic token counts (5000+500), usage
//   in message_start SSE — none triggered auto-compact. Custom model
//   lacks tokenizer metadata for context tracking.
//
// Droid PreCompact I (1 skip): /compact in TUI shows "Context Usage
//   Failed to load" dialog — can't compute context for custom model.
//   Auto-compact also doesn't trigger (same token tracking issue).
//   Canonical command is /compress (alias: /compact, /handoff). The
//   handler requires showCompactConfirmation callback with TUI dialog.
//
// Fixed (session 1):
//   Codex PreToolUse + PostToolUse (3 skips → 0): Hook matcher mismatch.
//     exec_command exposes as "Bash" via HookToolName::bash() in
//     codex-rs/core/src/tools/hook_names.rs. Added HookToolMatcher
//     field to decouple API tool name from hook matcher.
//   Gemini interactive (4 skips → 0): Two fixes. (1) selectedType:
//     "gemini-api-key" (was "gateway") with GEMINI_API_KEY env var.
//     GATEWAY routed through flash-lite planning model (no tools).
//     gemini-api-key gives full agent model access. (2) -m gemini-2.5-flash
//     skips flash-lite routing, uses streamGenerateContent with full
//     functionDeclarations (56KB+ tool payload).
//   Qwen interactive PreCompact (1 skip → 0): Positional prompt arg made
//     qwen run in headless mode. Removed prompt from InteractiveArgs,
//     now uses SendLine + SlowInput in actual TUI mode.
//
// Fixed (session 2):
//   Droid interactive PreToolUse + PostToolUse (2 skips → 0): TUI routes
//     LLM requests through Factory API proxy at /api/llm/a/v1/messages
//     (Anthropic Messages format), not /v1/chat/completions. Mock server
//     had no handler for this path — requests hit catch-all, got empty
//     200 responses. Added Factory proxy path handlers. Droid TUI sends
//     claude-haiku (tools=0, planning) then claude-opus (tools=18, agent).
//     hasTools check correctly skips planning request, fires tool call on
//     agent request.
//   Copilot interactive Prompt (1 skip → 0): Not a race condition after
//     all. The -i useEffect gates on VI (hooks ready). The real issue:
//     repo hooks at .copilot/hooks/ require folder trust ($a===1). Without
//     trust, hooks are skipped (VI set true without loading). Fix:
//     COPILOT_ALLOW_ALL=true bypasses folder trust, loads hooks eagerly.
//     User hooks (~/.config/copilot/hooks/) load eagerly regardless, but
//     repo hooks don't. Headless --prompt works because it awaits
//     loadDeferredRepoHooks synchronously before send().
//   Codex interactive PreCompact (1 skip → 0): Three combined fixes.
//     (1) SlowInput: crossterm can't handle burst SendLine post-startup,
//     needs char-by-char input (5ms delay). (2) OnboardingDismiss for
//     "Yes, continue" prompt. (3) Warmup prompt before /compact — context
//     was too short for compact without it. Regular SendLine sent /compact
//     but it was lost/garbled; with SlowInput the TUI processes it.
//
// TUI technology map:
//   Ink (React): claude, grok, droid, kimi, pi, qwen, gemini, opencode, kilo
//   Custom React renderer: copilot (S6 class, not Ink)
//   crossterm (Rust): codex (Bun-bundled codex-rs, startup input quarantine)
//   Ratatui (Rust): goose (crossterm works, no quarantine)
//   prompt_toolkit (Python): hermes
//
// PTY input compatibility:
//   SendLine works: claude, grok, goose, droid, kimi, pi, hermes, qwen
//   SendLine + SlowInput: gemini (anti-paste 30ms), qwen (/compress)
//   Prompt-in-args only: codex (startup quarantine), copilot (ICRNL race)
//   --prompt flag: opencode, kilo (TSPlugin via --prompt flag)
