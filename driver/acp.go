package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/inference-sh/agentprotocol/acp"
)

// ACPDriver drives an agent over the Agent Client Protocol.
//
// The transport — framing, the handshake, request routing, answering
// agent-initiated requests — belongs to github.com/inference-sh/agentprotocol/acp,
// which belt's runner mode uses too. What stays here is this suite's policy:
// approve everything, serve files, and accumulate updates into the polling
// model the checks are written against (Output/WaitForResponse/WaitIdle).
// Quirks found against a real agent belong in the library, not here, so belt
// inherits them; see the note on turnOver for the one asymmetry worth
// watching.
type ACPDriver struct {
	binary  string
	args    []string
	workDir string
	env     []string

	proc *acp.Process

	mu         sync.Mutex
	output     strings.Builder
	lastUpdate time.Time

	// updates wakes a waiter; the text itself is appended as it arrives, so a
	// dropped wakeup costs nothing.
	updates chan struct{}

	// turnOver is signalled by whichever end-of-turn arrives first. The spec
	// says session/prompt returns a stopReason, so its return is the
	// authoritative end; the update-borne signals are kept because this suite
	// predates the library and thirteen agents were measured against them. An
	// agent that ends its turn without answering the call is a library bug
	// worth reporting, so the asymmetry is logged.
	turnOver  chan struct{}
	turnEnded bool // a turn-done update arrived before the prompt call returned
}

func NewACPDriver(binary string, args []string, dir string, env []string) *ACPDriver {
	return &ACPDriver{
		binary:   binary,
		args:     args,
		workDir:  dir,
		env:      env,
		updates:  make(chan struct{}, 1),
		turnOver: make(chan struct{}, 1),
	}
}

func (d *ACPDriver) Start() error {
	proc, err := acp.Spawn(context.Background(), acp.ProcessConfig{
		Command: d.binary,
		Args:    d.args,
		Dir:     d.workDir,
		Env:     d.env,
		Stderr:  os.Stderr,
	}, acp.ClientInfo{Name: "harness-test", Version: "1.0.0"}, d.handler())
	if err != nil {
		return err
	}
	d.proc = proc
	d.appendOutput("[acp] initialized\n")

	cwd, _ := filepath.Abs(d.workDir)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessionID, err := d.proc.NewSession(context.Background(), cwd, nil)
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	d.appendOutput(fmt.Sprintf("[acp] session: %s\n", sessionID))
	return nil
}

// handler is everything this suite decides for itself: it approves, it serves
// the repo's files, and it feeds updates to the waiters.
func (d *ACPDriver) handler() acp.Handler {
	return acp.Handler{
		OnUpdate: func(n acp.UpdateNotification) {
			d.noteUpdate(n.Update)
		},

		// Auto-approve so a turn runs unattended. PickOption matches the
		// agent's own option IDs by kind and reports false rather than
		// guessing, so an agent offering nothing recognisable is cancelled
		// instead of silently authorised.
		OnPermission: func(_ context.Context, r acp.PermissionRequest) (acp.PermissionResponse, error) {
			if id, ok := r.PickOption(acp.OptionKindAllowOnce, acp.OptionKindAllowAlways); ok {
				d.appendOutput("[acp] approved permission (" + id + ")\n")
				return acp.Selected(id), nil
			}
			// An agent whose option kinds the library does not recognise still
			// has to be answered, but picking blind can select a refusal, so
			// anything that reads like one is passed over.
			for _, o := range r.Options {
				if looksLikeRefusal(o.Kind) || looksLikeRefusal(o.Name) {
					continue
				}
				d.appendOutput("[acp] approved permission, unrecognised kind (" + o.OptionID + ")\n")
				return acp.Selected(o.OptionID), nil
			}
			d.appendOutput("[acp] permission request offered no options; cancelled\n")
			return acp.Cancelled(), nil
		},

		OnReadTextFile: func(_ context.Context, p acp.ReadTextFileParams) (acp.ReadTextFileResult, error) {
			content, err := os.ReadFile(p.Path)
			if err != nil {
				d.appendOutput("[acp] fs/read " + p.Path + ": " + err.Error() + "\n")
				return acp.ReadTextFileResult{}, err
			}
			d.appendOutput("[acp] fs/read " + p.Path + "\n")
			return acp.ReadTextFileResult{Content: string(content)}, nil
		},

		OnWriteTextFile: func(_ context.Context, p acp.WriteTextFileParams) error {
			if p.Path == "" {
				return nil
			}
			os.MkdirAll(filepath.Dir(p.Path), 0755)
			return os.WriteFile(p.Path, []byte(p.Content), 0644)
		},

		OnElicitation: func(context.Context, acp.ElicitationParams) (acp.ElicitationResponse, error) {
			d.appendOutput("[acp] auto-confirmed elicitation\n")
			return acp.ElicitationResponse{Action: acp.ElicitationConfirm}, nil
		},

		// grok asks x.ai/hooks/run, which nothing here implements. The library
		// answers method-not-found either way; logging it keeps a record of
		// what an agent expected of its client.
		OnUnhandled: func(method string, _ json.RawMessage) {
			d.appendOutput("[acp] unhandled request: " + method + "\n")
		},

		// An error the agent reports without attributing it to a request. kiro
		// answers the session/close notification with one ("Method not found"),
		// which is a trait of the agent worth seeing rather than noise: the
		// same line crashed the client until agentprotocol v0.2.1, precisely
		// because nobody was looking at it.
		OnPeerError: func(e *acp.Error) {
			d.appendOutput(fmt.Sprintf("[acp] agent reported an unattributed error: %d %s %s\n", e.Code, e.Message, string(e.Data)))
		},
	}
}

func (d *ACPDriver) noteUpdate(u acp.SessionUpdate) {
	d.mu.Lock()
	d.lastUpdate = time.Now()
	d.mu.Unlock()

	if os.Getenv("HARNESS_DEBUG") != "" {
		d.appendOutput(fmt.Sprintf("[acp] update %s %s\n", u.Kind, u.Status))
	}

	// Append here, where every update passes exactly once. Appending in the
	// waiter instead meant text arriving while nothing waited — during an
	// idle wait, or after the buffer filled — was dropped and never reached
	// the transcript the checks read.
	if text := u.Text(); text != "" {
		d.appendOutput(text)
	}
	d.wake(d.updates)

	if acp.IsTurnDone(u) {
		d.mu.Lock()
		d.turnEnded = true
		d.mu.Unlock()
		d.wake(d.turnOver)
	}
}

// SendPrompt returns as soon as the prompt is on the wire, because the Driver
// interface is send-then-wait for every mode. The call itself runs in the
// background and its return signals turnOver, which is what WaitForResponse
// waits on.
func (d *ACPDriver) SendPrompt(prompt string) error {
	d.mu.Lock()
	d.turnEnded = false
	d.mu.Unlock()

	go func() {
		_, err := d.proc.Prompt(context.Background(), prompt)
		d.mu.Lock()
		ended := d.turnEnded
		d.mu.Unlock()
		if err != nil {
			d.appendOutput("[acp] prompt call failed: " + err.Error() + "\n")
		} else if !ended {
			d.appendOutput("[acp] prompt call returned before any turn-done update\n")
		}
		d.wake(d.turnOver)
	}()
	return nil
}

func (d *ACPDriver) WaitForResponse(patterns []string, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	matched := false
	for {
		select {
		case <-d.updates:
			if !matched {
				for _, p := range patterns {
					if strings.Contains(d.Output(), p) {
						matched = true
						break
					}
				}
			}
		case <-d.turnOver:
			if matched {
				return d.Output(), nil
			}
		case <-deadline:
			if matched {
				return d.Output(), nil
			}
			return d.Output(), fmt.Errorf("timeout waiting for response")
		case <-d.proc.Done():
			return d.Output(), nil
		}
	}
}

// WaitIdle waits until no session/update has arrived for the given duration,
// indicating the agent has settled (tool hooks finished, etc.).
func (d *ACPDriver) WaitIdle(quiet time.Duration) {
	d.mu.Lock()
	last := d.lastUpdate
	d.mu.Unlock()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			cur := d.lastUpdate
			d.mu.Unlock()
			if !cur.Equal(last) {
				last = cur
				continue
			}
			if time.Since(cur) >= quiet {
				return
			}
		case <-deadline:
			return
		case <-d.proc.Done():
			return
		}
	}
}

func (d *ACPDriver) SendCommand(cmd string) error {
	return d.SendPrompt(cmd)
}

func (d *ACPDriver) Output() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.output.String()
}

// Close hands shutdown to the library: since v0.2.1 Wait sends session/close
// and waits out ShutdownGrace itself, which is what this suite needs — Stop
// hooks fire after session/close, and closing stdin at once cut them off.
func (d *ACPDriver) Close() error {
	if d.proc == nil {
		return nil
	}
	return d.proc.Wait()
}

func (d *ACPDriver) appendOutput(s string) {
	d.mu.Lock()
	d.output.WriteString(s)
	d.mu.Unlock()
}

// truncate shortens a value for a log line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// wake signals a one-slot channel without blocking: the waiter only needs to
// know that something happened, not how many times.
func (d *ACPDriver) wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func looksLikeRefusal(s string) bool {
	s = strings.ToLower(s)
	for _, word := range []string{"reject", "deny", "decline", "refuse", "cancel", "abort"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}
