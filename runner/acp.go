package runner

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
// model the checks are written against (Output/WaitAnswered/WaitIdle).
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

	// ResumeSessionID makes Start attach to an existing session with
	// session/load instead of opening a new one. Set before Start.
	ResumeSessionID string
	load            acp.LoadResult

	// ParkPermission takes a permission request and never answers it, leaving
	// the agent with a tool call in flight. That is the state a client crash
	// leaves behind, and it is the only way to ask an agent what it does about
	// an approval nobody ever gave.
	//
	// Holding an answer costs nothing else since agentprotocol v0.4.0, which
	// answers each agent-initiated request on its own goroutine. Before that a
	// held answer held the whole connection, which is what a human taking a
	// minute over an approval would have done to a real client.
	ParkPermission bool

	// CancelDuringLoad answers a request that arrives while the session is
	// being rebuilt with a cancellation instead of an approval, which is the
	// right policy if such a request is history rather than a live question.
	// Which of those it is, is what probeToolCallInFlight measures.
	CancelDuringLoad bool

	permissions []PermissionObservation
	updateNotes []UpdateNote

	// parked is signalled once a request is held; release lets the held
	// handler return so the read loop is not leaked after the kill.
	parked  chan struct{}
	release chan struct{}
	once    sync.Once
}

// UpdateNote is one session/update, kept for what its kind says rather than
// its text: an agent's openers on a new session (available_commands_update,
// current_mode_update) look exactly like a short replay if all you count is
// how many notifications arrived.
type UpdateNote struct {
	Kind   string
	Replay bool
}

// PermissionObservation is one session/request_permission, recorded whatever
// the driver did about it: what the agent wanted to run, whether it arrived
// while the session was being rebuilt, and what answer it got.
type PermissionObservation struct {
	ToolCallID string
	Title      string
	DuringLoad bool
	Answer     PermissionAnswer
}

// PermissionAnswer is what this driver did about a request.
type PermissionAnswer string

const (
	AnswerApproved  PermissionAnswer = "approved"
	AnswerCancelled PermissionAnswer = "cancelled"
	AnswerParked    PermissionAnswer = "parked" // held, never answered
)

func NewACPDriver(binary string, args []string, dir string, env []string) *ACPDriver {
	return &ACPDriver{
		binary:   binary,
		args:     args,
		workDir:  dir,
		env:      env,
		updates:  make(chan struct{}, 1),
		turnOver: make(chan struct{}, 1),
		parked:   make(chan struct{}, 1),
		release:  make(chan struct{}),
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
	if d.ResumeSessionID != "" {
		return d.loadSession(cwd)
	}

	sessionID, err := d.proc.NewSession(context.Background(), cwd, nil)
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	d.appendOutput(fmt.Sprintf("[acp] session: %s\n", sessionID))
	return nil
}

// SessionID is the session this driver is attached to, for a later resume.
func (d *ACPDriver) SessionID() string { return d.proc.SessionID() }

// LoadResult describes what the agent did when asked to resume: how much of
// the conversation it replayed, and whether it ever answered the call.
func (d *ACPDriver) LoadResult() acp.LoadResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load
}

// CanLoadSession reports the agent's own claim about resuming. Worth showing
// a person and not worth branching on: every agent measured here declares it,
// including the one that refuses the call.
func (d *ACPDriver) CanLoadSession() bool { return d.proc.CanLoadSession() }

// Updates is every session/update this driver saw, in arrival order.
func (d *ACPDriver) Updates() []UpdateNote {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]UpdateNote(nil), d.updateNotes...)
}

// ReplayedKinds is the kinds of the updates the agent sent while rebuilding a
// loaded session.
func (d *ACPDriver) ReplayedKinds() []string {
	var kinds []string
	for _, n := range d.Updates() {
		if n.Replay {
			kinds = append(kinds, n.Kind)
		}
	}
	return kinds
}

// TurnDone reports whether a turn-done update has arrived since the last
// prompt was sent.
func (d *ACPDriver) TurnDone() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.turnEnded
}

// Alive reports whether the agent's stream is still open, without waiting.
func (d *ACPDriver) Alive() bool {
	select {
	case <-d.proc.Done():
		return false
	default:
		return true
	}
}

// Permissions is every session/request_permission this driver saw, in arrival
// order.
func (d *ACPDriver) Permissions() []PermissionObservation {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]PermissionObservation(nil), d.permissions...)
}

func (d *ACPDriver) recordPermission(obs PermissionObservation, answer PermissionAnswer) {
	obs.Answer = answer
	d.mu.Lock()
	d.permissions = append(d.permissions, obs)
	d.mu.Unlock()
}

// WaitParked waits for the agent to ask permission for something, which
// ParkPermission then holds. False means the agent never asked: it either
// never reached the tool call or runs tools without consulting its client.
func (d *ACPDriver) WaitParked(timeout time.Duration) bool {
	select {
	case <-d.parked:
		return true
	case <-d.proc.Done():
		return false
	case <-time.After(timeout):
		return false
	}
}

// loadSession attaches to an existing session. The race the load needs — the
// call against a replay-idle timer and a deadline — and the marking of
// replayed updates as history both live in the library now, so every client
// gets them.
func (d *ACPDriver) loadSession(cwd string) error {
	res, err := d.proc.LoadSession(context.Background(), d.ResumeSessionID, cwd, nil)
	d.mu.Lock()
	d.load = res
	d.mu.Unlock()
	if err != nil {
		return err
	}
	d.appendOutput(fmt.Sprintf("[acp] session/load: %d replayed update(s), answered=%v, %s\n",
		res.Replayed, res.Answered, res.Elapsed.Round(time.Millisecond)))
	return nil
}

// handler is everything this suite decides for itself: it approves, it serves
// the repo's files, and it feeds updates to the waiters.
func (d *ACPDriver) handler() acp.Handler {
	return acp.Handler{
		OnUpdate: func(n acp.UpdateNotification) {
			// n.Replay marks history an agent sends while rebuilding a loaded
			// session; the library reports how much of it there was, and this
			// suite does not treat it as progress.
			d.mu.Lock()
			d.updateNotes = append(d.updateNotes, UpdateNote{Kind: n.Update.Kind, Replay: n.Replay})
			d.mu.Unlock()
			d.noteUpdate(n.Update)
		},

		// Auto-approve so a turn runs unattended. PickOption matches the
		// agent's own option IDs by kind and reports false rather than
		// guessing, so an agent offering nothing recognisable is cancelled
		// instead of silently authorised.
		OnPermission: func(_ context.Context, r acp.PermissionRequest) (acp.PermissionResponse, error) {
			obs := PermissionObservation{DuringLoad: r.DuringLoad}
			if r.ToolCall != nil {
				obs.ToolCallID, obs.Title = r.ToolCall.ToolCallID, r.ToolCall.Title
			}
			where := ""
			if r.DuringLoad {
				where = " during session/load"
			}

			if d.ParkPermission {
				d.recordPermission(obs, AnswerParked)
				d.appendOutput("[acp] parked permission" + where + " (" + obs.ToolCallID + "), answering never\n")
				d.wake(d.parked)
				// Held until the process is taken away. Returning anything at
				// all would be an answer, and the point is that there is none.
				<-d.release
				return acp.Cancelled(), nil
			}
			if r.DuringLoad && d.CancelDuringLoad {
				d.recordPermission(obs, AnswerCancelled)
				d.appendOutput("[acp] cancelled permission during session/load (" + obs.ToolCallID + ")\n")
				return acp.Cancelled(), nil
			}

			if id, ok := r.PickOption(acp.OptionKindAllowOnce, acp.OptionKindAllowAlways); ok {
				d.recordPermission(obs, AnswerApproved)
				d.appendOutput("[acp] approved permission" + where + " (" + id + ")\n")
				return acp.Selected(id), nil
			}
			// An agent whose option kinds the library does not recognise still
			// has to be answered, but picking blind can select a refusal, so
			// anything that reads like one is passed over.
			for _, o := range r.Options {
				if looksLikeRefusal(o.Kind) || looksLikeRefusal(o.Name) {
					continue
				}
				d.recordPermission(obs, AnswerApproved)
				d.appendOutput("[acp] approved permission, unrecognised kind (" + o.OptionID + ")\n")
				return acp.Selected(o.OptionID), nil
			}
			d.recordPermission(obs, AnswerCancelled)
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

// SendPrompt returns as soon as the prompt is on the wire: every mode in this
// suite is send-then-wait. The call itself runs in the
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

// WaitAnswered waits for the agent's turn to end, having been answered by the
// mock at least once beyond `after`.
//
// It does not read the transcript. The old wait scanned the agent's output for
// words from the canned answer, and one of them — "codename" — is in the
// prompt itself, so an agent that echoes the user's turn back as an update
// satisfied the wait before the model had said anything. The mock knows how
// many answers it has served; that is the authority, and the interactive path
// has used it since the same bug was found there.
func (d *ACPDriver) WaitAnswered(answers func() int, after int, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		if answers() > after && d.TurnDone() {
			return nil
		}
		select {
		case <-d.updates:
		case <-d.turnOver:
			if answers() > after {
				return nil
			}
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			if answers() > after {
				return nil
			}
			return fmt.Errorf("timeout waiting for the mock to answer")
		case <-d.proc.Done():
			return nil
		}
	}
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
	d.releaseParked()
	return d.proc.Wait()
}

// Kill ends the agent without the courtesy of session/close, the way a closed
// laptop or a dropped connection does. Resuming after this is the case a
// long-lived client actually hits; a clean exit is the easy path.
func (d *ACPDriver) Kill() error {
	if d.proc == nil {
		return nil
	}
	err := d.proc.Kill()
	// After the release the held handler returns into a dead process, so the
	// answer goes nowhere — which is the point. Releasing only after the kill
	// keeps the agent's view honest and keeps the read loop from leaking.
	d.releaseParked()
	return err
}

func (d *ACPDriver) releaseParked() {
	d.once.Do(func() { close(d.release) })
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
