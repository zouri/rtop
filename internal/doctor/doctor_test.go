package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestAnalyzeReportsSourcesAndPermissionWarnings(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	report := Analyze(sysfs.MustNew(root), completeSnapshot())
	if report.Summary.Failed != 0 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if check := findCheck(t, report, "debugfs.mount"); check.Status != StatusPass {
		t.Fatalf("debugfs mount = %#v", check)
	}
	if check := findCheck(t, report, "platform.kernel_build"); check.Status != StatusInfo || !strings.Contains(check.Message, "fixture") {
		t.Fatalf("kernel build = %#v", check)
	}
	core := findCheck(t, report, "metric.npu.core0")
	if core.Status != StatusWarning || core.Source != "/sys/kernel/debug/rknpu/load" {
		t.Fatalf("core check = %#v", core)
	}
	if core.Suggestion == "" {
		t.Fatal("permission check has no suggestion")
	}
}

func TestAnalyzeReportsDebugFSDirectoryPermission(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS := &readDirPermissionFS{SystemFS: sysfs.MustNew(root)}
	report := Analyze(systemFS, completeSnapshot())
	check := findCheck(t, report, "debugfs.access")
	if check.Status != StatusWarning || !strings.Contains(check.Message, "not readable") {
		t.Fatalf("check = %#v", check)
	}
}

func TestRequiredMetricFailure(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.CPU.Overall = model.MissingMetric(
		model.Unavailable, "%", "/proc/stat", "read failed", time.Now(),
	)
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	report := Analyze(sysfs.MustNew(root), snapshot)
	if check := findCheck(t, report, "metric.cpu"); check.Status != StatusFail {
		t.Fatalf("CPU check = %#v", check)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("summary = %#v", report.Summary)
	}
}

func TestMountParsers(t *testing.T) {
	if !parseProcMounts([]byte("debugfs /sys/kernel/debug debugfs rw 0 0\n")) {
		t.Fatal("proc mounts did not detect debugfs")
	}
	if !parseMountInfo([]byte("30 20 0:10 / /sys/kernel/debug rw - debugfs debugfs rw\n")) {
		t.Fatal("mountinfo did not detect debugfs")
	}
	if parseProcMounts([]byte("proc /proc proc rw 0 0\n")) {
		t.Fatal("procfs was detected as debugfs")
	}
}

func TestWriteText(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	var output bytes.Buffer
	if err := WriteText(&output, Analyze(sysfs.MustNew(root), completeSnapshot())); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"rtop doctor", "SoC detection", "Summary:"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output is missing %q:\n%s", expected, output.String())
		}
	}
}

func completeSnapshot() model.Snapshot {
	now := time.Now()
	available := func(value float64, unit, source string) model.Metric {
		return model.AvailableMetric(value, unit, source, now)
	}
	return model.Snapshot{
		Timestamp: now,
		System: model.SystemInfo{
			Kernel: "6.1", Arch: "arm64", SoC: "RK3588", Board: "Fixture",
			Drivers: []model.DriverInfo{
				{Type: "gpu", Name: "mali", Device: "gpu", Governor: "simple_ondemand"},
				{Type: "npu", Name: "RKNPU", Device: "npu", Governor: "rknpu_ondemand"},
				{Type: "ddr", Name: "rockchip-dmc", Device: "dmc", Governor: "dmc_ondemand"},
			},
		},
		CPU:    model.CPUInfo{Overall: available(10, "%", "/proc/stat")},
		Memory: model.MemoryInfo{Total: available(8000, "MiB", "/proc/meminfo")},
		Load:   model.LoadInfo{OneMinute: available(1, "", "/proc/loadavg")},
		Thermals: []model.ThermalZone{{
			Name: "soc", Temperature: available(40, "°C", "/sys/class/thermal/thermal_zone0/temp"),
		}},
		Processes: []model.ProcessInfo{{PID: 1, Name: "init"}},
		Accelerators: []model.Accelerator{
			{Name: "Mali GPU", Type: "gpu", Usage: available(20, "%", "/sys/gpu/load"), Frequency: available(1000, "MHz", "/sys/gpu/cur_freq")},
			{
				Name: "RKNPU", Type: "npu", Usage: available(30, "%", "/sys/npu/load"), Frequency: available(900, "MHz", "/sys/npu/cur_freq"),
				Cores: []model.AcceleratorCore{{
					ID:    0,
					Usage: model.MissingMetric(model.PermissionDenied, "%", "/sys/kernel/debug/rknpu/load", os.ErrPermission.Error(), now),
				}},
			},
			{Name: "DDR/DMC", Type: "ddr", Usage: available(15, "%", "/sys/dmc/load"), Frequency: available(528, "MHz", "/sys/dmc/cur_freq")},
		},
	}
}

func findCheck(t *testing.T, report Report, id string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %q not found", id)
	return Check{}
}

type readDirPermissionFS struct {
	sysfs.SystemFS
}

func (f *readDirPermissionFS) ReadDir(path string) ([]os.DirEntry, error) {
	if path == "/sys/kernel/debug" {
		return nil, os.ErrPermission
	}
	return f.SystemFS.ReadDir(path)
}
