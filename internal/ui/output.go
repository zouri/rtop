package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	beginSynchronizedUpdate = "\x1b[?2026h"
	endSynchronizedUpdate   = "\x1b[?2026l"
)

// synchronizedOutputForTerminal makes each renderer write atomic on Ghostty.
// Ghostty buffers content between DEC mode 2026 markers and displays the
// completed frame at once, avoiding visible partial redraws in dense bars.
func synchronizedOutputForTerminal(output io.Writer) io.Writer {
	if !isGhosttyEnvironment(os.Environ()) {
		return output
	}
	if file, ok := output.(terminalFile); ok {
		return &synchronizedTerminalWriter{terminalFile: file}
	}
	return &synchronizedWriter{output: output}
}

type terminalFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

type synchronizedWriter struct {
	output io.Writer
	mu     sync.Mutex
}

func (w *synchronizedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !isFrameWrite(data) {
		return w.output.Write(data)
	}
	return writeSynchronized(w.output, data)
}

// synchronizedTerminalWriter retains Fd, Read and Close so Bubble Tea can
// still detect terminal dimensions and subscribe to resize events.
type synchronizedTerminalWriter struct {
	terminalFile
	mu sync.Mutex
}

func (w *synchronizedTerminalWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !isFrameWrite(data) {
		return w.terminalFile.Write(data)
	}
	return writeSynchronized(w.terminalFile, data)
}

func isFrameWrite(data []byte) bool {
	// Bubble Tea emits cursor/alternate-screen control sequences as short
	// writes, while rendered frames contain line movement. Synchronizing only
	// frames keeps terminal setup and teardown simple.
	return bytes.IndexByte(data, '\n') >= 0 || len(data) >= 256
}

func writeSynchronized(output io.Writer, data []byte) (int, error) {
	if _, err := io.WriteString(output, beginSynchronizedUpdate); err != nil {
		return 0, err
	}
	written, writeErr := output.Write(data)
	_, endErr := io.WriteString(output, endSynchronizedUpdate)
	if writeErr != nil {
		return written, writeErr
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if endErr != nil {
		return written, endErr
	}
	return written, nil
}

func isGhosttyEnvironment(environ []string) bool {
	for _, variable := range environ {
		name, value, found := strings.Cut(variable, "=")
		if !found {
			continue
		}
		switch name {
		case "TERM_PROGRAM":
			if strings.EqualFold(value, "ghostty") {
				return true
			}
		case "TERM":
			if strings.Contains(strings.ToLower(value), "ghostty") {
				return true
			}
		case "GHOSTTY_RESOURCES_DIR":
			if value != "" {
				return true
			}
		}
	}
	return false
}
