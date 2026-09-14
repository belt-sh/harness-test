package harness

// HookFormat describes how a harness expects hook configuration.
type HookFormat int

const (
	JSONNested  HookFormat = iota // Claude, Codex, Grok, Droid, Goose, Gemini, Qwen
	JSONFlat                      // Cursor, Windsurf: {"hooks":{event:[{command}]}} with optional wrapper
	JSONCopilot                   // Copilot v1 format (version field, bash field)
	JSONKiro                      // Kiro CLI agent config: "hooks" object inside .kiro/agents/kiro_default.json
	TOML                          // Kimi
	YAML                          // Hermes
	TSExtension                   // Pi
	TSPlugin                      // OpenCode, Kilo
)

// APIFormat describes what LLM API protocol the harness speaks.
type APIFormat int

const (
	OpenAI    APIFormat = iota // /v1/chat/completions (Copilot, Hermes, Pi, Kimi, Goose, Qwen, Droid)
	Responses                  // /v1/responses (Codex, Grok, OpenCode, Kilo)
	Anthropic                  // /v1/messages (Claude)
	Gemini                     // /v1beta/models/:model:streamGenerateContent (Gemini CLI)
	Cursor                     // Connect-protobuf agent.v1.AgentService (Cursor agent CLI)
)

func (f APIFormat) String() string {
	switch f {
	case Cursor:
		return "Cursor"
	case OpenAI:
		return "OpenAI"
	case Responses:
		return "Resp"
	case Anthropic:
		return "Anthro"
	case Gemini:
		return "Gemini"
	default:
		return "?"
	}
}

func (f HookFormat) String() string {
	switch f {
	case JSONNested:
		return "JSON"
	case JSONFlat:
		return "JSON-flat"
	case JSONCopilot:
		return "Copilot"
	case JSONKiro:
		return "Kiro"
	case TOML:
		return "TOML"
	case YAML:
		return "YAML"
	case TSExtension:
		return "TS-ext"
	case TSPlugin:
		return "TS-plug"
	default:
		return "?"
	}
}

// ToolCall is the tool the mock offers the agent, and the arguments it sends.
type ToolCall struct {
	Name string
	Args string
}

// ConfigFile is a file to write relative to $HOME before running the harness.
// Content supports {{.BaseURL}}, {{.Model}}, {{.RepoDir}}, {{.APIKey}} placeholders.
type ConfigFile struct {
	Path    string // relative to $HOME
	Content string
}

// Harness describes a coding agent CLI and how belt integrates with it.
type Harness struct {
	Name   string
	Binary string // CLI binary name

	// Install
	InstallCmd     []string   // command to install the CLI if missing
	InstallBinDirs []string   // dirs relative to $HOME to add to PATH after install
	PostInstall    [][]string // commands to run after install (e.g. register plugin marketplace)

	// API
	APIFormat      APIFormat
	EnvVars        map[string]string // env vars to set (supports {{.BaseURL}} and literal values)
	APIKeyEnvVar   string            // env var for API key
	DefaultModel   string
	AcceptedModels []string // additional model names the agent may use instead of DefaultModel

	// Hooks
	HookFormat     HookFormat
	HookConfigDir  string // where hook config goes (relative to $HOME)
	HookFileName   string // override hook filename (default: format-dependent)
	HookWrapper    string // JSON to wrap hooks in (e.g. Claude's permissions + hooks)
	HookTimeoutMs  bool   // true = timeout field is milliseconds (gemini, qwen, kiro), false = seconds
	HookNoEnvelope bool   // true = hooks file is raw hooks object, no {"hooks":...} wrapper (droid)
	HookFlatBare   bool   // JSONFlat entries carry only "command" (windsurf rejects nothing but documents nothing else)
	// KnownIssues records what an agent genuinely cannot do, so the runner can
	// tell "not supported here" apart from "broken". Keys:
	//
	//	"<mode>:<check>"          a named check, e.g. "acp:prompt-context"
	//	"<mode>:event:<TAG>"      a hook event that does not fire in that mode,
	//	                          e.g. "headless:event:PRE_COMPACT"
	//
	// The value is the reason, shown in the run. A check or event listed here
	// is skipped with that reason; one that is NOT listed and does not happen
	// fails the run. Every entry is a claim about the agent that was verified
	// in Docker — never a way to quiet a flaky test.
	KnownIssues map[string]string

	// ServerRequestedHooks lists, per mode, the hook events (runner tags such
	// as "PROMPT", "STOP", "PRE_COMPACT") this agent runs only when its backend
	// asks for them, so the mock must send the request or the hook can never
	// fire. Cursor is the only such agent: every hook goes through a backend
	// request, but its TUI runs the prompt and stop hooks itself as well, so
	// requesting those in interactive mode would run them twice. Measured in
	// Docker 2026-09. It says what the agent does when asked; it does not say
	// whether the vendor's real backend asks.
	ServerRequestedHooks map[Mode][]string

	// CompactConfirm: the interactive compact command opens a confirmation
	// dialog ("Enter to confirm"), so the runner presses Enter after it.
	CompactConfirm bool
	Events         Events

	// Mock tool call configuration
	ToolCallName    string // tool name in mock responses (default: "Read")
	ToolCallArgs    string // JSON args for mock tool call (default: {"file_path":"README.md"})
	ToolCallPath    string // only fire tool calls on requests to this path suffix
	HookToolMatcher string // hook matcher name if different from ToolCallName (e.g. codex: "Bash" matches exec_command)
	// ToolCallByMode overrides ToolCallName/Args for one mode, for an agent
	// whose engine there names its tools differently (kiro's V1 engine, which
	// headless pins, calls the read tool fs_read where V2 calls it read).
	// Keyed like KnownIssues: "headless", "interactive", "acp", "sdk".
	ToolCallByMode map[Mode]ToolCall

	// ToolCallGated is a tool this agent is expected to ask its client about:
	// a write or a shell command, not a read. Agents run reads without
	// consulting anyone, so a probe that wants to see a permission request has
	// to ask for something the agent actually gates. Names are read off the
	// wire with --probe tools rather than guessed, because a tool an
	// agent does not declare is answered with "tool not found" and nothing
	// runs.
	ToolCallGated ToolCall

	// ACPAutoApproveArgs are the arguments that stop this agent asking its
	// client for permission. They are kept apart from ACPArgs so the in-flight
	// probe can compose an invocation without them, rather than trying to
	// recognise them again by pattern after the fact — a denylist that could
	// not see a flag it had not been told about, and reported the resulting
	// silence as an agent trait.
	ACPAutoApproveArgs []string

	// SessionDir is where this agent writes its session transcripts, as a
	// template ({{.HomeDir}}, {{.RepoDir}}). SessionExt is the file extension
	// whose base name is the session id. Only needed by an agent whose
	// follow-up command takes a session id it does not print.
	//
	// It is registry data because it is one vendor's filesystem layout: it
	// used to be droid's directory, mangling scheme and extension written
	// into a generically named helper in the runner, which then went looking
	// for a .factory tree on behalf of claude and qwen.
	SessionDir string
	SessionExt string

	// Pre-flight config files (auth, trust, provider config, permissions)
	ConfigFiles    []ConfigFile
	TokenHashInput string // if set, {{.TokenHash16}} = sha256(this)[:16] (kimi auth)

	// Skills
	SkillsDir string

	// Instruction files — what the agent loads into its system prompt
	// (CLAUDE.md, AGENTS.md, GEMINI.md, steering, rules). Filled from
	// instructionFiles in registry.go.
	InstructionFile        string // user scope, relative to $HOME; "" when the agent has no global file
	ProjectInstructionFile string // project scope, relative to the repo root
	InstructionFrontmatter string // written above the content when the file format requires it
	InstructionMaxBytes    int    // size limit the agent enforces on the user-scope file, 0 for none
	InstructionNote        string // why there is no user-scope file, or a caveat about it

	// ConfigDirEnv names an env var that relocates the agent's config dir
	// (the first segment of HookConfigDir), e.g. CLAUDE_CONFIG_DIR. XDG_CONFIG_HOME
	// relocates ".config" for agents that live under it.
	ConfigDirEnv string

	// Headless (-p) mode
	HeadlessCmd       []string   // command prefix
	HeadlessModelArgs []string   // model selection flags, supports {{.Model}}
	PostHeadlessCmd   [][]string // commands to run after headless (e.g. [["-p","--continue","/compact"]])
	PromptViaStdin    bool       // true = feed prompt on stdin (codex exec)
	NeedsGitRepo      bool

	// Interactive (PTY/TUI) mode
	InteractiveCmd          []string
	InteractiveArgs         []string // extra flags for interactive mode, supports {{.Model}} etc.
	InteractivePromptInArgs bool     // prompt is part of InteractiveArgs, don't SendLine
	SlowInput               bool     // type characters individually (bypasses anti-paste protection)
	ExitCommand             string
	CompactCommand          string // slash command to trigger compaction (e.g. "/compact")
	OnboardingDismiss       []DismissAction

	// ACP (Agent Client Protocol) mode — JSON-RPC over stdio
	ACPCmd           []string     // command to start agent in ACP mode (e.g. ["claude", "acp"])
	ACPArgs          []string     // extra args for ACP mode
	ACPConfigFiles   []ConfigFile // extra config files for ACP mode (provider overrides)
	ACPNeedsTempHome bool         // create temp HOME for ACP (isolate provider config)

	// SDK mode — agent-specific programmatic protocol over stdio (e.g. claude stream-json)
	SDKCmd  []string // command to start agent in SDK mode
	SDKArgs []string // extra args for SDK mode

	// Intercept
	NeedsIntercept bool // agent can't point to mock via env vars; requires MITM interception

	// TS plugin
	TSPluginExport string // appended to TS plugin file (e.g. kilo's default export wrapper)

	// Detection (used by belt CLI to find installed agents)
	DetectEnvVars    []string // env vars the agent sets at runtime (e.g. CLAUDECODE)
	DetectConfigDirs []string // config dirs to check (defaults to HookConfigDir root if empty)
	PluginManifest   string   // write plugin.json here on install (goose)

	// Setup
	PreserveHome bool // don't override HOME (harness needs installed plugins)
}

type DismissAction struct {
	Pattern string
	SendUp  bool // send Up arrow before Enter (to select a different menu item)
	// Required: the dialog always appears (e.g. trusting a fresh folder), so
	// wait for it instead of giving up once the splash screen has drawn;
	// typing into a startup screen loses the prompt.
	Required bool
}

// Events maps belt behaviors to harness-specific event names.
type Events struct {
	SessionStart string
	PromptSubmit string
	PreToolUse   string
	PostToolUse  string
	Stop         string
	PreCompact   string
}
