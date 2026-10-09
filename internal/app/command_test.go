package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rtop/internal/model"
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
			contains: []string{`"cpu":`, `"memory":`, `"free":`, `"shared":`, `"buff_cache":`, `"swap_free":`, `"swap_cached":`, `"swappiness":`, `"swap_devices":`, `"kind": "file"`, `"accelerators":`, `"pressure":`, `"cma_total":`, `"zram":`, `"trips":`, `"cooling_devices":`, `"runtime_status":`, `"power_sensors":`, `"fans":`},
		},
		{
			name:     "snapshot without processes",
			args:     []string{"--root", root, "--cpu-sample", "1ns", "snapshot", "--json", "--no-processes"},
			contains: []string{`"processes": []`},
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

func TestPrintSnapshotIncludesAcceleratorSessions(t *testing.T) {
	var output bytes.Buffer
	printSnapshot(&output, model.Snapshot{
		Accelerators: []model.Accelerator{{
			Name: "Rockchip RGA",
			Sessions: []model.AcceleratorSession{{
				Session: 6, Status: "active", TGID: 225320, Process: "/opt/broker --serve",
			}},
		}},
	})
	if !strings.Contains(output.String(), "session 6: status active  TGID 225320  process /opt/broker --serve") {
		t.Fatalf("snapshot output is missing RGA session:\n%s", output.String())
	}
}

func TestPrintSnapshotIncludesFreeMemoryBreakdown(t *testing.T) {
	now := time.Now()
	metric := func(value float64) model.Metric {
		return model.AvailableMetric(value, "MiB", "/proc/meminfo", now)
	}
	var output bytes.Buffer
	printSnapshot(&output, model.Snapshot{Memory: model.MemoryInfo{
		Total: metric(8000), Used: metric(4000), Free: metric(1000), Shared: metric(64),
		Buffers: metric(250), Cache: metric(2125), BufferCache: metric(2375), Available: metric(4000),
		SwapTotal: metric(2000), SwapUsed: metric(500), SwapFree: metric(1500), SwapCached: metric(32),
		Swappiness: model.AvailableMetric(60, "", "/proc/sys/vm/swappiness", now),
		SwapDevices: []model.SwapDevice{{
			Path: "/dev/zram0", Kind: model.SwapZRAM, KernelType: "partition",
			Size: metric(1024), Used: metric(128), Priority: 100,
		}},
	}})
	view := output.String()
	for _, expected := range []string{
		"1000MiB free", "64MiB shared", "2375MiB buff/cache", "4000MiB available",
		"250MiB buffers", "2125MiB cache", "2000MiB total", "500MiB used",
		"1500MiB free", "32MiB cached", "swappiness 60",
		"Swap backend: zram", "1024MiB total", "priority 100", "/dev/zram0",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("snapshot output is missing %q:\n%s", expected, view)
		}
	}
}

func TestCommandAllowsUnusedSamplingFlags(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	for _, args := range [][]string{
		{"--root", root, "--interval", "0s", "--cpu-sample", "0s", "--process-interval", "0s", "--thermal-interval", "0s", "info", "--json"},
		{"--root", root, "--interval", "0s", "--cpu-sample", "1ns", "--process-interval", "0s", "--thermal-interval", "0s", "snapshot", "--json"},
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
		{args: []string{"--root", root, "--process-interval", "0s", "snapshot", "--watch"}, message: "--process-interval must be greater than zero"},
		{args: []string{"--root", root, "--thermal-interval", "0s", "snapshot", "--watch"}, message: "--thermal-interval must be greater than zero"},
		{args: []string{"--root", root, "snapshot", "--top=-1"}, message: "--top must be zero or greater"},
	}
	for _, test := range tests {
		_, _, err := executeTestCommand(test.args...)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("execute %v: error = %v", test.args, err)
		}
	}
}

func TestTopProcessesByCPU(t *testing.T) {
	now := time.Now()
	processes := []model.ProcessInfo{
		{PID: 4, CPU: model.MissingMetric(model.Unavailable, "%", "/proc/4/stat", "missing", now)},
		{PID: 3, CPU: model.AvailableMetric(20, "%", "/proc/3/stat", now)},
		{PID: 1, CPU: model.AvailableMetric(30, "%", "/proc/1/stat", now)},
		{PID: 2, CPU: model.AvailableMetric(30, "%", "/proc/2/stat", now)},
	}
	top := topProcessesByCPU(processes, 3)
	if len(top) != 3 || top[0].PID != 1 || top[1].PID != 2 || top[2].PID != 3 {
		t.Fatalf("top processes = %#v", top)
	}
	if processes[0].PID != 4 {
		t.Fatal("top selection mutated its input")
	}
}

func TestDisabledProcessesDoNotRequireProcessInterval(t *testing.T) {
	config := runtimeConfig{
		interval: time.Second, cpuSampleDuration: time.Nanosecond,
		processInterval: 0, thermalInterval: time.Second,
	}
	if err := validateSampling(config, true, false); err != nil {
		t.Fatal(err)
	}
}

func TestCommandRejectsRemovedLogLevel(t *testing.T) {
	_, _, err := executeTestCommand("--log-level", "warn", "info")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --log-level") {
		t.Fatalf("error = %v", err)
	}
}

func TestTuneCommands(t *testing.T) {
	root := t.TempDir()
	writeCommandFixture(t, root, "/proc/sys/vm/swappiness", "60\n")
	writeCommandFixture(t, root, "/proc/swaps", "Filename Type Size Used Priority\n/swapfile file 1048576 262144 -2\n")
	for _, name := range []string{"policy0", "policy4"} {
		base := "/sys/devices/system/cpu/cpufreq/" + name
		writeCommandFixture(t, root, base+"/scaling_governor", "schedutil\n")
		writeCommandFixture(t, root, base+"/scaling_available_governors", "performance powersave schedutil\n")
	}

	stdout, stderr, err := executeTestCommand("--root", root, "tune", "swap", "swappiness", "10")
	if err != nil || stderr != "" || !strings.Contains(stdout, "vm.swappiness set to 10") {
		t.Fatalf("swap stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "proc", "sys", "vm", "swappiness"))
	if err != nil || string(data) != "10" {
		t.Fatalf("swappiness data=%q err=%v", data, err)
	}

	stdout, stderr, err = executeTestCommand("--root", root, "tune", "swap", "list")
	if err != nil || stderr != "" || !strings.Contains(stdout, "Total Swap: 1GiB") ||
		!strings.Contains(stdout, "file\t1GiB\t256MiB\t-2\t/swapfile") {
		t.Fatalf("swap list stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}

	// A no-op capacity request exercises runtime discovery without invoking
	// swapoff against the fixture root.
	stdout, stderr, err = executeTestCommand("--root", root, "tune", "swap", "size", "1G")
	if err != nil || stderr != "" || !strings.Contains(stdout, "1GiB -> 1GiB (no_change") {
		t.Fatalf("swap size stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}

	stdout, stderr, err = executeTestCommand("--root", root, "tune", "cpu", "performance")
	if err != nil || stderr != "" || !strings.Contains(stdout, "2/2 policies changed") {
		t.Fatalf("cpu stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	for _, name := range []string{"policy0", "policy4"} {
		path := filepath.Join(root, "sys", "devices", "system", "cpu", "cpufreq", name, "scaling_governor")
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != "performance" {
			t.Fatalf("%s data=%q err=%v", name, data, readErr)
		}
	}
}

func TestTuneCommandsRejectInvalidValues(t *testing.T) {
	for _, test := range []struct {
		args    []string
		message string
	}{
		{args: []string{"tune", "swap", "swappiness", "201"}, message: "between 0 and 200"},
		{args: []string{"tune", "swap", "swappiness", "many"}, message: "must be an integer"},
		{args: []string{"tune", "swap", "size", "0.5MiB"}, message: "at least 1MiB"},
		{args: []string{"tune", "cpu", "turbo"}, message: "must be \"performance\" or \"powersave\""},
	} {
		_, _, err := executeTestCommand(test.args...)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("execute %v error=%v, want %q", test.args, err, test.message)
		}
	}
}

func writeCommandFixture(t *testing.T, root, logicalPath, value string) {
	t.Helper()
	path := filepath.Join(root, strings.TrimPrefix(filepath.Clean(logicalPath), string(filepath.Separator)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
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
