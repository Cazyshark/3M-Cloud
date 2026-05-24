package executor

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

type Result struct {
	ExitCode int
	Output   string
	Error    string
}

func RunScript(ctx context.Context, script string, timeout int, streamCh chan<- string) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script)

	var stdout, stderr bytes.Buffer

	if streamCh != nil {
		sw := &streamWriter{ch: streamCh, buf: &stdout}
		ew := &streamWriter{ch: streamCh, buf: &stderr}
		cmd.Stdout = sw
		cmd.Stderr = ew
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	err := cmd.Run()

	result := Result{
		Output: stdout.String(),
		Error:  stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = err.Error()
		}
	}

	return result
}

type streamWriter struct {
	ch  chan<- string
	buf *bytes.Buffer
}

func (w *streamWriter) Write(p []byte) (n int, err error) {
	if w.buf != nil {
		w.buf.Write(p)
	}
	if w.ch != nil {
		w.ch <- string(p)
	}
	return len(p), nil
}
