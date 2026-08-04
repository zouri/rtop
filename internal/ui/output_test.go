package ui

import (
	"bytes"
	"testing"
)

func TestGhosttyEnvironmentDetection(t *testing.T) {
	tests := []struct {
		environ []string
		want    bool
	}{
		{[]string{"TERM_PROGRAM=ghostty", "TERM=xterm-256color"}, true},
		{[]string{"TERM=xterm-ghostty"}, true},
		{[]string{"GHOSTTY_RESOURCES_DIR=/usr/share/ghostty"}, true},
		{[]string{"TERM_PROGRAM=WezTerm", "TERM=xterm-256color"}, false},
	}
	for _, test := range tests {
		if got := isGhosttyEnvironment(test.environ); got != test.want {
			t.Fatalf("environment %v detected=%v, want %v", test.environ, got, test.want)
		}
	}
}

func TestSynchronizedWriterWrapsEachFrame(t *testing.T) {
	var output bytes.Buffer
	writer := &synchronizedWriter{output: &output}
	written, err := writer.Write([]byte("frame\nnext"))
	if err != nil {
		t.Fatal(err)
	}
	if written != len("frame\nnext") {
		t.Fatalf("written=%d", written)
	}
	want := beginSynchronizedUpdate + "frame\nnext" + endSynchronizedUpdate
	if got := output.String(); got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestSynchronizedWriterLeavesControlWritesAlone(t *testing.T) {
	var output bytes.Buffer
	writer := &synchronizedWriter{output: &output}
	control := "\x1b[?25l"
	if _, err := writer.Write([]byte(control)); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != control {
		t.Fatalf("output=%q, want %q", got, control)
	}
}
