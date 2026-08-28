package runner

import "time"

// Driver abstracts how we communicate with a coding agent.
// Three implementations: CLI args (headless), PTY (interactive TUI), ACP (JSON-RPC over stdio).
type Driver interface {
	// Start launches the agent process.
	Start() error

	// SendPrompt sends a user message to the agent.
	SendPrompt(prompt string) error

	// WaitForResponse waits for the agent to produce output matching any of the patterns.
	WaitForResponse(patterns []string, timeout time.Duration) (string, error)

	// SendCommand sends a slash command (e.g. /compact, /exit).
	SendCommand(cmd string) error

	// Output returns all captured output so far.
	Output() string

	// Close terminates the agent process.
	Close() error
}
