package driver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

type PTYSession struct {
	cmd  *exec.Cmd
	ptmx *os.File

	mu     sync.Mutex
	output bytes.Buffer

	done chan error
}

func StartPTY(name string, args []string, dir string, env []string) (*PTYSession, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	s := &PTYSession{
		cmd:  cmd,
		ptmx: ptmx,
		done: make(chan error, 1),
	}

	go func() {
		var buf [4096]byte
		for {
			n, err := ptmx.Read(buf[:])
			if n > 0 {
				s.mu.Lock()
				s.output.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		s.done <- cmd.Wait()
	}()

	return s, nil
}

func (s *PTYSession) SendLine(text string) {
	s.ptmx.WriteString(text + "\r")
}

func (s *PTYSession) SendCtrlC() {
	s.ptmx.Write([]byte{0x03})
}

func (s *PTYSession) SendUp() {
	s.ptmx.Write([]byte{0x1b, '[', 'A'})
}

// WaitForAny returns the first target to appear in the output. It scans only
// the bytes that arrived since the last look, overlapping by the longest
// target so a match straddling two reads is still found: a TUI writes
// megabytes of escape sequences, and copying the whole buffer every 200ms to
// re-scan text already scanned is quadratic in the length of the wait.
func (s *PTYSession) WaitForAny(targets []string, timeout time.Duration) (string, bool) {
	overlap := 0
	for _, t := range targets {
		if len(t) > overlap {
			overlap = len(t)
		}
	}
	deadline := time.Now().Add(timeout)
	scanned := 0
	for {
		s.mu.Lock()
		buf := s.output.Bytes()
		from := max(scanned-overlap+1, 0)
		hit := ""
		for _, t := range targets {
			if bytes.Contains(buf[from:], []byte(t)) {
				hit = t
				break
			}
		}
		scanned = len(buf)
		s.mu.Unlock()
		if hit != "" {
			return hit, true
		}
		if !time.Now().Before(deadline) {
			return "", false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Len is how many bytes the TUI has written. A caller watching for the screen
// to settle wants the length, and Output copies the whole buffer into a string
// to get it — every poll, for the length of the wait.
func (s *PTYSession) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.Len()
}

func (s *PTYSession) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}

func (s *PTYSession) Close() {
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.ptmx.Close()
}

func (s *PTYSession) Wait(timeout time.Duration) error {
	select {
	case err := <-s.done:
		return err
	case <-time.After(timeout):
		s.Close()
		return fmt.Errorf("timeout after %s", timeout)
	}
}

// SendText types text without a trailing carriage return.
func (s *PTYSession) SendText(text string, charDelay time.Duration) {
	for _, c := range text {
		s.ptmx.WriteString(string(c))
		if charDelay > 0 {
			time.Sleep(charDelay)
		}
	}
}

// SendRaw writes bytes as they are.
func (s *PTYSession) SendRaw(b string) { s.ptmx.WriteString(b) }
