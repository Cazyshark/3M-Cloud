package terminal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
)

type Session struct {
	ID       string
	Pty      *os.File
	Cmd      *exec.Cmd
	once     sync.Once
	done     chan struct{}
}

func NewSession(id string, cols, rows uint16) (*Session, error) {
	cmd := exec.Command("/bin/bash")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	return &Session{
		ID:   id,
		Pty:  ptmx,
		Cmd:  cmd,
		done: make(chan struct{}),
	}, nil
}

func (s *Session) ReadOutput() ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := s.Pty.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (s *Session) WriteInput(data []byte) error {
	_, err := s.Pty.Write(data)
	return err
}

func (s *Session) Resize(cols, rows uint16) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		s.Pty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&struct {
			Rows uint16
			Cols uint16
			X    uint16
			Y    uint16
		}{rows, cols, 0, 0})),
	)
	if errno != 0 {
		return fmt.Errorf("ioctl TIOCSWINSZ failed: %v", errno)
	}
	return nil
}

func (s *Session) Close() {
	s.once.Do(func() {
		close(s.done)
		s.Pty.Close()
		if s.Cmd.Process != nil {
			s.Cmd.Process.Kill()
		}
	})
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

// StreamOutput continuously reads from the PTY and sends output to the channel.
// Blocks until the session is closed.
func (s *Session) StreamOutput(ch chan<- []byte) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.done:
			return
		default:
			n, err := s.Pty.Read(buf)
			if err != nil {
				if err != io.EOF {
					// PTY closed
				}
				s.Close()
				return
			}
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				ch <- data
			}
		}
	}
}
