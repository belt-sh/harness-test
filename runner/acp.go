package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ACPDriver communicates with an agent via the Agent Client Protocol (JSON-RPC over stdio).
type ACPDriver struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	scanner   *bufio.Scanner
	sessionID string
	output    strings.Builder
	mu        sync.Mutex
	nextID    int
	responses chan json.RawMessage
	updates   chan acpUpdate
	done      chan struct{}
}

type acpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *acpError       `json:"error,omitempty"`
}

type acpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type acpUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		Kind    string `json:"kind"`
		Content string `json:"content,omitempty"`
		Text    string `json:"text,omitempty"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content,omitempty"`
		} `json:"message,omitempty"`
	} `json:"update"`
}

func NewACPDriver(binary string, args []string, dir string, env []string) *ACPDriver {
	return &ACPDriver{
		cmd:       buildACPCmd(binary, args, dir, env),
		responses: make(chan json.RawMessage, 16),
		updates:   make(chan acpUpdate, 64),
		done:      make(chan struct{}),
	}
}

func buildACPCmd(binary string, args []string, dir string, env []string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = os.Stderr
	return cmd
}

func (d *ACPDriver) Start() error {
	var err error
	d.stdin, err = d.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	d.stdout, err = d.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := d.cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	d.scanner = bufio.NewScanner(d.stdout)
	d.scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	go d.readLoop()

	// Initialize with capabilities
	initResult, err := d.call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]string{"name": "harness-test", "version": "1.0.0"},
		"capabilities": map[string]any{
			"permissionRequests": true,
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	d.appendOutput(fmt.Sprintf("[acp] initialized: %s\n", string(initResult)))

	// Create session
	sessResult, err := d.call("session/new", map[string]any{})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(sessResult, &sess)
	d.sessionID = sess.SessionID
	d.appendOutput(fmt.Sprintf("[acp] session: %s\n", d.sessionID))

	return nil
}

func (d *ACPDriver) SendPrompt(prompt string) error {
	_, err := d.call("session/prompt", map[string]any{
		"sessionId": d.sessionID,
		"turns": []map[string]any{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": prompt},
				},
			},
		},
	})
	return err
}

func (d *ACPDriver) WaitForResponse(patterns []string, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	for {
		select {
		case upd := <-d.updates:
			text := extractUpdateText(upd)
			if text != "" {
				d.appendOutput(text)
			}
			for _, p := range patterns {
				if strings.Contains(d.Output(), p) {
					return d.Output(), nil
				}
			}
		case <-deadline:
			return d.Output(), fmt.Errorf("timeout waiting for response")
		case <-d.done:
			return d.Output(), nil
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

func (d *ACPDriver) Close() error {
	d.call("session/close", map[string]any{"sessionId": d.sessionID})
	d.stdin.Close()
	return d.cmd.Wait()
}

// --- internal ---

func (d *ACPDriver) appendOutput(s string) {
	d.mu.Lock()
	d.output.WriteString(s)
	d.mu.Unlock()
}

func (d *ACPDriver) call(method string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	d.mu.Unlock()

	req := acpRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')

	if _, err := d.stdin.Write(data); err != nil {
		return nil, err
	}

	// Wait for response with matching ID
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-d.responses:
			return result, nil
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for %s response", method)
		case <-d.done:
			return nil, fmt.Errorf("agent exited during %s", method)
		}
	}
}

func (d *ACPDriver) readLoop() {
	defer close(d.done)
	for d.scanner.Scan() {
		line := d.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg acpResponse
		if json.Unmarshal(line, &msg) != nil {
			continue
		}

		if msg.ID != nil && msg.Method == "" {
			// Response to a request we made
			d.responses <- msg.Result
		} else if msg.Method == "session/update" {
			var upd acpUpdate
			if json.Unmarshal(msg.Params, &upd) == nil {
				d.updates <- upd
			}
		} else if msg.Method == "session/request_permission" {
			// Agent wants tool approval — auto-approve
			d.autoApprove(msg)
		}
	}
}

// autoApprove responds to permission requests with "allow".
func (d *ACPDriver) autoApprove(msg acpResponse) {
	if msg.ID == nil {
		return
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      *msg.ID,
		"result": map[string]any{
			"decision": "allow",
		},
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	d.stdin.Write(data)
	d.appendOutput("[acp] auto-approved tool permission\n")
}

func extractUpdateText(upd acpUpdate) string {
	if upd.Update.Text != "" {
		return upd.Update.Text
	}
	if upd.Update.Content != "" {
		return upd.Update.Content
	}
	if upd.Update.Message != nil {
		var parts []string
		for _, c := range upd.Update.Message.Content {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}
