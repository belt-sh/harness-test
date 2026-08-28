package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

func (s *PTYSession) SendLineDelayed(text string, charDelay time.Duration) {
	for _, c := range text {
		s.ptmx.WriteString(string(c))
		time.Sleep(charDelay)
	}
	time.Sleep(50 * time.Millisecond)
	s.ptmx.Write([]byte{'\r'})
}

func (s *PTYSession) SendCtrlC() {
	s.ptmx.Write([]byte{0x03})
}

func (s *PTYSession) SendUp() {
	s.ptmx.Write([]byte{0x1b, '[', 'A'})
}

func (s *PTYSession) WaitForAny(targets []string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		out := s.output.String()
		s.mu.Unlock()
		for _, t := range targets {
			if strings.Contains(out, t) {
				return t, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", false
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
