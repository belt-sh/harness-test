package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	ap "github.com/inference-sh/agentprotocol"
	"github.com/inference-sh/agentprotocol/acp"
	"github.com/inference-sh/agentprotocol/driver"
)

// SessionDriver runs one session through agentprotocol's driver.Backend, the
// contract belt's runner uses for every agent: ACP agents over ACPBackend,
// Claude Code over ClaudeBackend, Codex over CodexBackend, pi over PiBackend.
// The probes are written once against this type, so the same measurement runs
// against each backend.
//
// Transport and protocol quirks belong in the library, so belt inherits them.
// What stays here is this suite's policy — approve, cancel or park each
// approval request — and the polling model the checks are written against
// (Output/WaitAnswered/WaitIdle).
type SessionDriver struct {
	newBackend BackendFactory
	workDir    string

	// Model is SessionConfig.Model. The native backends take it; ACP agents
	// get theirs from ACPArgs and ACPBackend ignores it.
	Model string

	// ResumeSessionID reopens an existing session instead of starting one.
	// Set before Start.
	ResumeSessionID string

	// ParkPermission takes each approval request and never resolves it,
	// leaving the agent with a tool call in flight. That is the state a
	// client crash leaves behind, and it is the only way to ask an agent what
	// it does about an approval nobody ever gave.
	ParkPermission bool

	// CancelDuringLoad denies a request raised while a resumed session was
	// being rebuilt instead of approving it, which is the right policy if such
	// a request is history rather than a live question. Which of those it is,
	// is what probeToolCallInFlight measures.
	CancelDuringLoad bool

	backend driver.Backend
	sess    driver.Session
	done    chan struct{} // closed when the session's event stream closes

	mu          sync.Mutex
	output      strings.Builder
	lastUpdate  time.Time
	turnEnded   bool
	notes       []UpdateNote
	permissions []PermissionObservation
	load        LoadResult

	// updates wakes a waiter; the text itself is appended as it arrives, so a
	// dropped wakeup costs nothing. turnOver is signalled when a turn ends.
	updates  chan struct{}
	turnOver chan struct{}
	parked   chan struct{}
}

// BackendFactory builds the backend a SessionDriver opens its session on.
// diagnose receives the backend's OnDiagnostic lines: they carry what the
// agent did that is not an event, including how a resumed ACP session was
// rebuilt.
type BackendFactory func(diagnose func(string)) driver.Backend

// UpdateNote is one event the session emitted, kept for its type. Replay
// marks an event that arrived while a resumed session was being rebuilt.
type UpdateNote struct {
	Kind   string
	Replay bool
}

// PermissionObservation is one approval request, recorded whatever the driver
// did about it: what the agent wanted to run, whether it arrived while the
// session was being rebuilt, and what answer it got.
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

// LoadResult is what a resumed ACP session reported about its rebuild.
// ACPBackend reports it as a diagnostic line, not a value, so it is read back
// out of that line; Reported is false when no such line arrived, which is
// always the case for the native backends: they resume by id and replay
// nothing to the client.
type LoadResult struct {
	Reported             bool
	Replayed             int
	Conversation         int
	RestoredConversation bool
	Answered             bool
	Elapsed              time.Duration
}

// errNoKill is returned by Kill when the session does not implement
// driver.Killer. Every backend in agentprotocol v0.9.2 does; one that does not
// is reported rather than silently closed.
var errNoKill = errors.New("the session does not implement driver.Killer")

func NewSessionDriver(newBackend BackendFactory, dir string) *SessionDriver {
	return &SessionDriver{
		newBackend: newBackend,
		workDir:    dir,
		done:       make(chan struct{}),
		updates:    make(chan struct{}, 1),
		turnOver:   make(chan struct{}, 1),
		parked:     make(chan struct{}, 1),
	}
}

// Start opens the session. For a resume, whatever the session emitted before
// Open returned is marked as replay, and an approval among it as raised
// during the load.
func (d *SessionDriver) Start() error {
	d.backend = d.newBackend(d.diagnose)
	cwd, _ := filepath.Abs(d.workDir)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sess, err := d.backend.Open(context.Background(), driver.SessionConfig{
		WorkDir:         cwd,
		Model:           d.Model,
		ResumeSessionID: d.ResumeSessionID,
	})
	if err != nil {
		close(d.done)
		return err
	}
	d.sess = sess
	// ACPBackend buffers events while Open runs a session/load, so what is
	// queued now is the rebuild. The native backends queue nothing here.
	during := 0
	if d.ResumeSessionID != "" {
		during = len(sess.Events())
	}
	d.appendOutput(fmt.Sprintf("[%s] session: %s\n", d.Kind(), sess.ID()))
	go d.read(sess.Events(), during)
	return nil
}

// Kind is the backend's kind: acp, claude-code or codex.
func (d *SessionDriver) Kind() string {
	if d.backend == nil {
		return "session"
	}
	return d.backend.Kind()
}

// SessionID is the session this driver is attached to, for a later resume.
func (d *SessionDriver) SessionID() string {
	if d.sess == nil {
		return ""
	}
	return d.sess.ID()
}

// LoadResult describes what the agent did when asked to resume.
func (d *SessionDriver) LoadResult() LoadResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load
}

// Updates is every event this driver saw, in arrival order, less the ones
// its own calls produce (run.started, turn.started, approval.resolved).
func (d *SessionDriver) Updates() []UpdateNote {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]UpdateNote(nil), d.notes...)
}

// ReplayedKinds is the kinds of the events a resumed session emitted while it
// was being rebuilt. Only ACP agents replay, and only with EmitReplay on.
func (d *SessionDriver) ReplayedKinds() []string {
	var kinds []string
	for _, n := range d.Updates() {
		if n.Replay {
			kinds = append(kinds, n.Kind)
		}
	}
	return kinds
}

// TurnDone reports whether a turn has ended since the last prompt was sent.
func (d *SessionDriver) TurnDone() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.turnEnded
}

// Alive reports whether the session's event stream is still open. The native
// backends close it when the process exits; ACPBackend only on Close or Kill,
// so an ACP agent that exits on its own still reads as alive.
func (d *SessionDriver) Alive() bool {
	select {
	case <-d.done:
		return false
	default:
		return true
	}
}

// Permissions is every approval request this driver saw, in arrival order.
func (d *SessionDriver) Permissions() []PermissionObservation {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]PermissionObservation(nil), d.permissions...)
}

// WaitParked waits for the agent to ask permission for something, which
// ParkPermission then holds. False means the agent never asked: it either
// never reached the tool call or runs tools without consulting its client.
func (d *SessionDriver) WaitParked(timeout time.Duration) bool {
	select {
	case <-d.parked:
		return true
	case <-d.done:
		return false
	case <-time.After(timeout):
		return false
	}
}

func (d *SessionDriver) read(events <-chan ap.AgentEvent, during int) {
	defer close(d.done)
	i := 0
	for ev := range events {
		d.onEvent(ev, i < during)
		i++
	}
}

func (d *SessionDriver) onEvent(ev ap.AgentEvent, replay bool) {
	d.mu.Lock()
	d.lastUpdate = time.Now()
	switch ev.Type {
	case ap.AgentEventRunStarted, ap.AgentEventTurnStarted, ap.AgentEventApprovalResolved:
	default:
		d.notes = append(d.notes, UpdateNote{Kind: string(ev.Type), Replay: replay})
	}
	d.mu.Unlock()

	if os.Getenv("HARNESS_DEBUG") != "" {
		d.appendOutput(fmt.Sprintf("[%s] event %s\n", d.Kind(), ev.Type))
	}

	// Appended here, where every event passes exactly once: text that
	// arrives while nothing waits still reaches the transcript the checks
	// read.
	switch ev.Type {
	case ap.AgentEventContentDelta:
		var p ap.ContentDeltaPayload
		if ev.DecodePayload(&p) == nil {
			d.appendOutput(p.Delta)
		}
	case ap.AgentEventToolCompleted:
		var p ap.ToolCompletedPayload
		if ev.DecodePayload(&p) == nil {
			d.appendOutput(p.Result)
		}
	case ap.AgentEventApprovalRequired:
		var p ap.ApprovalRequiredPayload
		if ev.DecodePayload(&p) == nil {
			d.onApproval(p, replay)
		}
	case ap.AgentEventError:
		var p ap.ErrorPayload
		_ = ev.DecodePayload(&p)
		d.appendOutput(fmt.Sprintf("[%s] turn failed: %s\n", d.Kind(), p.Message))
		d.endTurn()
	case ap.AgentEventTurnCompleted:
		d.endTurn()
	}
	d.wake(d.updates)
}

func (d *SessionDriver) endTurn() {
	d.mu.Lock()
	d.turnEnded = true
	d.mu.Unlock()
	d.wake(d.turnOver)
}

// onApproval is this suite's policy: park, cancel a request raised during a
// load, or approve so a turn runs unattended. The allow is the library's
// choice of option (ResponseForResolution for ACP), so an agent offering
// nothing it recognises as an allow is cancelled rather than guessed at.
func (d *SessionDriver) onApproval(p ap.ApprovalRequiredPayload, duringLoad bool) {
	obs := PermissionObservation{ToolCallID: p.ToolInvocationID, Title: p.ToolName, DuringLoad: duringLoad}
	where := ""
	if duringLoad {
		where = " during the load"
	}
	kind := d.Kind()

	if d.ParkPermission {
		d.recordPermission(obs, AnswerParked)
		d.appendOutput(fmt.Sprintf("[%s] parked approval%s (%s), answering never\n", kind, where, obs.ToolCallID))
		d.wake(d.parked)
		return
	}
	res, answer := driver.Allow(), AnswerApproved
	if duringLoad && d.CancelDuringLoad {
		res, answer = driver.Deny("cancelled by harness-test"), AnswerCancelled
	}
	d.recordPermission(obs, answer)
	d.appendOutput(fmt.Sprintf("[%s] %s approval%s (%s)\n", kind, answer, where, obs.ToolCallID))
	// Off the read loop: a backend may emit while resolving.
	go func() {
		if err := d.sess.Resolve(context.Background(), p.ToolInvocationID, res); err != nil {
			d.appendOutput(fmt.Sprintf("[%s] resolve %s: %v\n", kind, p.ToolInvocationID, err))
		}
	}()
}

func (d *SessionDriver) recordPermission(obs PermissionObservation, answer PermissionAnswer) {
	obs.Answer = answer
	d.mu.Lock()
	d.permissions = append(d.permissions, obs)
	d.mu.Unlock()
}

// resumedRe reads ACPBackend's account of a session/load.
var resumedRe = regexp.MustCompile(`^resumed session \S+ in (\S+): (\d+) update\(s\) replayed, (\d+) of them conversation, (history restored|NO user turn)[^;]*; agent (answered the call|replayed and went quiet)`)

// diagnose records a backend diagnostic in the output, and reads a resume
// report into LoadResult.
func (d *SessionDriver) diagnose(msg string) {
	kind := "session"
	if d.backend != nil {
		kind = d.backend.Kind()
	}
	d.appendOutput(fmt.Sprintf("[%s] %s\n", kind, msg))
	m := resumedRe.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	elapsed, _ := time.ParseDuration(m[1])
	replayed, _ := strconv.Atoi(m[2])
	conversation, _ := strconv.Atoi(m[3])
	d.mu.Lock()
	d.load = LoadResult{
		Reported:             true,
		Replayed:             replayed,
		Conversation:         conversation,
		RestoredConversation: m[4] == "history restored",
		Answered:             m[5] == "answered the call",
		Elapsed:              elapsed,
	}
	d.mu.Unlock()
}

// SendPrompt returns as soon as the prompt is handed to the backend: every
// mode in this suite is send-then-wait.
func (d *SessionDriver) SendPrompt(prompt string) error {
	d.mu.Lock()
	d.turnEnded = false
	d.mu.Unlock()
	return d.sess.Prompt(context.Background(), driver.TextInput(prompt))
}

// SendCommand sends a slash command. No backend has a command channel, so it
// goes as a prompt; whether the agent treats it as one is what the compaction
// probe checks.
func (d *SessionDriver) SendCommand(cmd string) error { return d.SendPrompt(cmd) }

// WaitAnswered waits for the agent's turn to end, having been answered by the
// mock at least once beyond `after`.
//
// It does not read the transcript: words from the canned answer can be in
// the prompt itself, so an agent that echoes the user's turn would satisfy a
// text match before the model said anything. The mock knows how many answers
// it has served.
func (d *SessionDriver) WaitAnswered(answers func() int, after int, timeout time.Duration) error {
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
		case <-d.done:
			return nil
		}
	}
}

func (d *SessionDriver) WaitForResponse(patterns []string, timeout time.Duration) (string, error) {
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
		case <-d.done:
			return d.Output(), nil
		}
	}
}

// WaitIdle waits until no event has arrived for the given duration,
// indicating the agent has settled (tool hooks finished, etc.).
func (d *SessionDriver) WaitIdle(quiet time.Duration) {
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
		case <-d.done:
			return
		}
	}
}

func (d *SessionDriver) Output() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.output.String()
}

// Close ends the session the backend's way: session/close for ACP, end of
// input and a grace period for the native CLIs. Stop hooks fire during it. A
// parked request is cancelled by the backend.
func (d *SessionDriver) Close() error {
	if d.sess == nil {
		return nil
	}
	return d.sess.Close()
}

// Kill ends the agent without the courtesy of a close, the way a closed
// laptop or a dropped connection does. Resuming after this is the case a
// long-lived client actually hits; a clean exit is the easy path. A session
// whose backend cannot kill is closed instead and errNoKill returned, so a
// probe can say it measured a close.
//
// ACPBackend's Kill cancels a parked approval before it kills, so the agent
// may see that answer if it reads it before the signal lands. The old driver
// killed first and answered into a dead pipe.
func (d *SessionDriver) Kill() error {
	if d.sess == nil {
		return nil
	}
	if k, ok := d.sess.(driver.Killer); ok {
		return k.Kill()
	}
	d.sess.Close()
	return errNoKill
}

// CanKill reports whether Kill really kills.
func (d *SessionDriver) CanKill() bool {
	_, ok := d.sess.(driver.Killer)
	return ok
}

func (d *SessionDriver) appendOutput(s string) {
	d.mu.Lock()
	d.output.WriteString(s)
	d.mu.Unlock()
}

// wake signals a one-slot channel without blocking: the waiter only needs to
// know that something happened, not how many times.
func (d *SessionDriver) wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// acpLoadClaim is what an ACP agent claims about session/load at initialize.
// driver.Backend does not expose the handshake, so this runs one of its own:
// initialize only, no session, then the process is killed. Worth showing a
// person and not worth branching on: every agent measured declares it,
// including the one that refuses the call.
func acpLoadClaim(bin string, args []string, dir string, env []string) string {
	proc, err := acp.Spawn(context.Background(), acp.ProcessConfig{
		Command: bin, Args: args, Dir: dir, Env: env,
	}, acp.ClientInfo{Name: "harness-test", Version: "1.0.0"}, acp.Handler{})
	if err != nil {
		return "initialize failed"
	}
	defer proc.Kill()
	if proc.CanLoadSession() {
		return "declares loadSession"
	}
	return "declares nothing"
}

// truncate shortens a value for a log line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
