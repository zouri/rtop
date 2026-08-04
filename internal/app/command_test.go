package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandJSONModesAgainstFixture(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	tests := []struct {
		name     string
		args     []string
		contains []string
	}{
		{
			name:     "info",
			args:     []string{"--root", root, "info", "--json"},
			contains: []string{`"hostname": "fixture-board"`, `"soc": "RK3588"`},
		},
		{
			name:     "snapshot",
			args:     []string{"--root", root, "--cpu-sample", "1ns", "snapshot", "--json"},
			contains: []string{`"cpu":`, `"memory":`, `"accelerators":`},
		},
		{
			name:     "doctor",
			args:     []string{"--root", root, "--cpu-sample", "1ns", "doctor", "--json"},
			contains: []string{`"checks":`, `"summary":`, `"platform.soc"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := executeTestCommand(test.args...)
			if err != nil {
				t.Fatal(err)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q", stderr)
			}
			if !json.Valid([]byte(stdout)) {
				t.Fatalf("output is not valid JSON:\n%s", stdout)
			}
			for _, expected := range test.contains {
				if !strings.Contains(stdout, expected) {
					t.Fatalf("output is missing %q:\n%s", expected, stdout)
				}
			}
		})
	}
}

func TestCommandVersion(t *testing.T) {
	stdout, stderr, err := executeTestCommand("--version")
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	if want := "rtop test-version (commit test-commit, built test-date)\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestCommandAllowsUnusedSamplingFlags(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	for _, args := range [][]string{
		{"--root", root, "--interval", "0s", "--cpu-sample", "0s", "info", "--json"},
		{"--root", root, "--interval", "0s", "--cpu-sample", "1ns", "snapshot", "--json"},
	} {
		stdout, _, err := executeTestCommand(args...)
		if err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		if !json.Valid([]byte(stdout)) {
			t.Fatalf("execute %v returned invalid JSON:\n%s", args, stdout)
		}
	}
}

func TestCommandRejectsInvalidSamplingWhereUsed(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	tests := []struct {
		args    []string
		message string
	}{
		{args: []string{"--root", root, "--cpu-sample", "0s", "snapshot", "--json"}, message: "--cpu-sample must be greater than zero"},
		{args: []string{"--root", root, "--cpu-sample", "0s", "doctor", "--json"}, message: "--cpu-sample must be greater than zero"},
		{args: []string{"--root", root, "--interval", "0s", "--cpu-sample", "1ns", "snapshot", "--watch"}, message: "--interval must be greater than zero"},
	}
	for _, test := range tests {
		_, _, err := executeTestCommand(test.args...)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("execute %v: error = %v", test.args, err)
		}
	}
}

func TestCommandRejectsRemovedLogLevel(t *testing.T) {
	_, _, err := executeTestCommand("--log-level", "warn", "info")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --log-level") {
		t.Fatalf("error = %v", err)
	}
}

func executeTestCommand(args ...string) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := NewCommand("test-version", "test-commit", "test-date", &stdout, &stderr)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}
