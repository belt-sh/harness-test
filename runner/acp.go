package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ACPDriver communicates with an agent via the Agent Client Protocol (JSON-RPC over stdio).
// Implements the Driver interface and the ACP v1 spec: https://agentclientprotocol.com
type ACPDriver struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner
	workDir string

	sessionID string
	output    strings.Builder
	mu        sync.Mutex

	nextID    int
	responses map[int]chan json.RawMessage
	respMu    sync.Mutex

	updates  chan sessionUpdate
	handlers map[string]notificationHandler
	done     chan struct{}
}

type notificationHandler func(msg rpcMessage)

func NewACPDriver(binary string, args []string, dir string, env []string) *ACPDriver {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = os.Stderr

	d := &ACPDriver{
		cmd:       cmd,
		workDir:   dir,
		responses: make(map[int]chan json.RawMessage),
		updates:   make(chan sessionUpdate, 64),
		done:      make(chan struct{}),
	}
	d.handlers = map[string]notificationHandler{
		"session/update":             d.handleUpdate,
		"session/request_permission": d.handlePermission,
		"fs/write_text_file":         d.handleFsWrite,
		"fs/read_text_file":          d.handleFsRead,
		"elicitation/create":         d.handleElicitation,
	}
	return d
}

func (d *ACPDriver) Start() error {
	var err error
	if d.stdin, err = d.cmd.StdinPipe(); err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	if d.stdout, err = d.cmd.StdoutPipe(); err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := d.cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	d.scanner = bufio.NewScanner(d.stdout)
	d.scanner.Buffer(make([]byte, 1<<20), 1<<20)
	go d.readLoop()

	initResult, err := d.call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]string{"name": "harness-test", "version": "1.0.0"},
		"capabilities":    map[string]any{"permissionRequests": true},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	d.appendOutput(fmt.Sprintf("[acp] initialized: %s\n", truncate(string(initResult), 100)))

	cwd, _ := filepath.Abs(d.workDir)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessResult, err := d.call("session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
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
	return d.send("session/prompt", map[string]any{
		"sessionId": d.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
}

func (d *ACPDriver) WaitForResponse(patterns []string, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	for {
		select {
		case upd := <-d.updates:
			if text := d.extractText(upd); text != "" {
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
	if d.sessionID != "" {
		d.send("session/close", map[string]any{"sessionId": d.sessionID})
	}
	d.stdin.Close()
	return d.cmd.Wait()
}

// --- internal ---

func (d *ACPDriver) appendOutput(s string) {
	d.mu.Lock()
	d.output.WriteString(s)
	d.mu.Unlock()
}

func (d *ACPDriver) send(method string, params any) error {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	d.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, err := d.stdin.Write(data)
	return err
}

func (d *ACPDriver) call(method string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	d.mu.Unlock()

	ch := make(chan json.RawMessage, 1)
	d.respMu.Lock()
	d.responses[id] = ch
	d.respMu.Unlock()
	defer func() {
		d.respMu.Lock()
		delete(d.responses, id)
		d.respMu.Unlock()
	}()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := d.stdin.Write(data); err != nil {
		return nil, err
	}

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case result := <-ch:
		return result, nil
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for %s response", method)
	case <-d.done:
		return nil, fmt.Errorf("agent exited during %s", method)
	}
}

func (d *ACPDriver) readLoop() {
	defer close(d.done)
	for d.scanner.Scan() {
		line := d.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if json.Unmarshal(line, &msg) != nil {
			continue
		}

		// Response to a call we made
		if msg.ID != nil && msg.Method == "" {
			d.routeResponse(msg)
			continue
		}

		// Server-initiated notification or request
		if handler, ok := d.handlers[msg.Method]; ok {
			handler(msg)
		}
	}
}

func (d *ACPDriver) routeResponse(msg rpcMessage) {
	d.respMu.Lock()
	defer d.respMu.Unlock()
	ch, ok := d.responses[*msg.ID]
	if !ok {
		return
	}
	if msg.Error != nil {
		d.appendOutput(fmt.Sprintf("[acp] error on request %d: %s\n", *msg.ID, msg.Error.Message))
	}
	ch <- msg.Result
}

func (d *ACPDriver) handleUpdate(msg rpcMessage) {
	var notif sessionUpdateNotification
	if json.Unmarshal(msg.Params, &notif) == nil {
		d.updates <- notif.Update
	}
}

func (d *ACPDriver) handlePermission(msg rpcMessage) {
	if msg.ID == nil {
		return
	}
	d.respond(*msg.ID, map[string]any{"outcome": "approved"})
	d.appendOutput("[acp] auto-approved permission\n")
}

func (d *ACPDriver) handleFsWrite(msg rpcMessage) {
	if msg.ID == nil {
		return
	}
	var req fsWriteRequest
	json.Unmarshal(msg.Params, &req)
	if req.Path != "" {
		os.MkdirAll(filepath.Dir(req.Path), 0755)
		os.WriteFile(req.Path, []byte(req.Content), 0644)
	}
	d.respond(*msg.ID, map[string]any{})
}

func (d *ACPDriver) handleFsRead(msg rpcMessage) {
	if msg.ID == nil {
		return
	}
	var req fsReadRequest
	json.Unmarshal(msg.Params, &req)
	content, err := os.ReadFile(req.Path)
	if err != nil {
		d.respondError(*msg.ID, -32600, err.Error())
		return
	}
	d.respond(*msg.ID, map[string]any{"content": string(content)})
}

func (d *ACPDriver) handleElicitation(msg rpcMessage) {
	if msg.ID == nil {
		return
	}
	d.respond(*msg.ID, map[string]any{"action": "confirm"})
	d.appendOutput("[acp] auto-confirmed elicitation\n")
}

func (d *ACPDriver) respond(id int, result any) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	d.stdin.Write(data)
}

func (d *ACPDriver) respondError(id int, code int, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	d.stdin.Write(data)
}

func (d *ACPDriver) extractText(upd sessionUpdate) string {
	if len(upd.Content) == 0 {
		return ""
	}
	var block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(upd.Content, &block) == nil && block.Text != "" {
		return block.Text
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
