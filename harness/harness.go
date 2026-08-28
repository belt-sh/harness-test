package harness

// HookFormat describes how a harness expects hook configuration.
type HookFormat int

const (
	JSONNested  HookFormat = iota // Claude, Codex, Grok, Droid, Goose, Gemini, Qwen
	JSONFlat                      // Cursor, Windsurf (IDE-only, no CLI)
	JSONCopilot                   // Copilot v1 format (version field, bash field)
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
)

// ConfigFile is a file to write relative to $HOME before running the harness.
// Content supports {{.BaseURL}}, {{.Model}}, {{.RepoDir}}, {{.APIKey}} placeholders.
type ConfigFile struct {
	Path    string // relative to $HOME
	Content string
}

// Harness describes a coding agent CLI and how belt integrates with it.
type Harness struct {
	Name    string
	Binary  string // CLI binary name

	// Install
	InstallCmd     []string   // command to install the CLI if missing
	InstallBinDirs []string   // dirs relative to $HOME to add to PATH after install
	PostInstall    [][]string // commands to run after install (e.g. register plugin marketplace)

	// API
	APIFormat      APIFormat
	EnvVars map[string]string // env vars to set (supports {{.BaseURL}} and literal values)
	APIKeyEnvVar   string            // env var for API key
	DefaultModel   string

	// Hooks
	HookFormat    HookFormat
	HookConfigDir string // where hook config goes (relative to $HOME)
	HookFileName  string // override hook filename (default: format-dependent)
	HookWrapper    string // JSON to wrap hooks in (e.g. Claude's permissions + hooks)
	HookTimeoutMs  bool   // true = timeout field is milliseconds (gemini), false = seconds
	HookNoEnvelope bool   // true = hooks file is raw hooks object, no {"hooks":...} wrapper (droid)
	Events         Events

	// Mock tool call configuration
	ToolCallName     string // tool name in mock responses (default: "Read")
	ToolCallArgs     string // JSON args for mock tool call (default: {"file_path":"README.md"})
	ToolCallPath     string // only fire tool calls on requests to this path suffix
	HookToolMatcher  string // hook matcher name if different from ToolCallName (e.g. codex: "Bash" matches exec_command)

	// Pre-flight config files (auth, trust, provider config, permissions)
	ConfigFiles []ConfigFile

	// Skills
	SkillsDir string

	// Headless (-p) mode
	HeadlessCmd          []string // command prefix
	HeadlessModelArgs    []string // model selection flags, supports {{.Model}}
	PostHeadlessCmd      [][]string // commands to run after headless (e.g. [["-p","--continue","/compact"]])
	PromptViaStdin       bool     // true = feed prompt on stdin (codex exec)
	NeedsGitRepo         bool
	HooksInHeadless      bool

	// Interactive (PTY/TUI) mode
	InteractiveCmd          []string
	InteractiveArgs         []string // extra flags for interactive mode, supports {{.Model}} etc.
	InteractivePromptInArgs bool     // prompt is part of InteractiveArgs, don't SendLine
	SlowInput               bool     // type characters individually (bypasses anti-paste protection)
	ExitCommand             string
	CompactCommand          string   // slash command to trigger compaction (e.g. "/compact")
	HooksInInteractive      bool
	OnboardingDismiss       []DismissAction

	// Setup
	PreserveHome bool // don't override HOME (harness needs installed plugins)
}

type DismissAction struct {
	Pattern string
	SendUp  bool // send Up arrow before Enter (to select a different menu item)
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
