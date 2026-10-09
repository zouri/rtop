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
	if check := findCheck(t, report, "metric.pressure.memory"); check.Status != StatusPass {
		t.Fatalf("memory pressure check = %#v", check)
	}
	if check := findCheck(t, report, "metric.memory.swappiness"); check.Status != StatusPass || check.Source != "/proc/sys/vm/swappiness" {
		t.Fatalf("swappiness check = %#v", check)
	}
	if check := findCheck(t, report, "metric.memory.swap_backends"); check.Status != StatusPass ||
		!strings.Contains(check.Message, "file=1") || check.Source != "/proc/swaps" {
		t.Fatalf("Swap backend check = %#v", check)
	}
	if check := findCheck(t, report, "metric.memory.swap_backend0"); check.Status != StatusInfo ||
		!strings.Contains(check.Message, "/swapfile") || !strings.Contains(check.Message, "priority -2") {
		t.Fatalf("Swap backend detail = %#v", check)
	}
	if check := findCheck(t, report, "metric.gpu.runtime_status"); check.Status != StatusPass || check.Message != "active" {
		t.Fatalf("GPU runtime check = %#v", check)
	}
	if check := findCheck(t, report, "metric.rga.usage"); check.Status != StatusPass {
		t.Fatalf("RGA usage check = %#v", check)
	}
	if check := findCheck(t, report, "metric.rga.frequency"); check.Status != StatusInfo {
		t.Fatalf("RGA frequency check = %#v", check)
	}
	if check := findCheck(t, report, "metric.power.sensor0"); check.Status != StatusPass {
		t.Fatalf("power sensor check = %#v", check)
	}
	if check := findCheck(t, report, "metric.fan.device0.control_mode"); check.Status != StatusPass || check.Message != "automatic" {
		t.Fatalf("fan mode check = %#v", check)
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
		CPU: model.CPUInfo{Overall: available(10, "%", "/proc/stat")},
		Memory: model.MemoryInfo{
			Total:      available(8000, "MiB", "/proc/meminfo"),
			Swappiness: available(60, "", "/proc/sys/vm/swappiness"),
			CMATotal:   available(512, "MiB", "/proc/meminfo"),
			Pressure: model.PressureInfo{Memory: model.PressureResource{
				Some: model.PressureWindow{Avg10: available(1.25, "%", "/proc/pressure/memory")},
			}},
		},
		Load: model.LoadInfo{OneMinute: available(1, "", "/proc/loadavg")},
		Thermals: []model.ThermalZone{{
			Name: "soc", Temperature: available(40, "°C", "/sys/class/thermal/thermal_zone0/temp"),
			Trips: []model.ThermalTrip{{
				ID: 0, Type: "passive", Temperature: available(80, "°C", "/sys/class/thermal/thermal_zone0/trip_point_0_temp"),
			}},
		}},
		Cooling: []model.CoolingDevice{{
			Name: "cooling_device0", Type: "thermal-cpufreq-0",
			CurrentState: available(1, "state", "/sys/class/thermal/cooling_device0/cur_state"),
			MaximumState: available(4, "state", "/sys/class/thermal/cooling_device0/max_state"),
		}},
		Power: []model.PowerSensor{{
			Name: "system", Chip: "ina3221", Kind: "power",
			Value: available(9, "W", "/sys/class/hwmon/hwmon0/power1_input"),
		}},
		Fans: []model.FanInfo{{
			Name: "board-fan", Chip: "pwm-fan",
			Speed:       available(2400, "RPM", "/sys/class/hwmon/hwmon1/fan1_input"),
			TargetSpeed: available(3000, "RPM", "/sys/class/hwmon/hwmon1/fan1_target"),
			PWM:         available(50, "%", "/sys/class/hwmon/hwmon1/pwm1"),
			ControlMode: model.AvailableTextMetric("automatic", "/sys/class/hwmon/hwmon1/pwm1_enable", now),
		}},
		Processes: []model.ProcessInfo{{PID: 1, Name: "init"}},
		Accelerators: []model.Accelerator{
			{Name: "Mali GPU", Type: "gpu", Usage: available(20, "%", "/sys/gpu/load"), Frequency: available(1000, "MHz", "/sys/gpu/cur_freq"), RuntimeStatus: model.AvailableTextMetric("active", "/sys/gpu/runtime_status", now)},
			{
				Name: "RKNPU", Type: "npu", Usage: available(30, "%", "/sys/npu/load"), Frequency: available(900, "MHz", "/sys/npu/cur_freq"),
				Cores: []model.AcceleratorCore{{
					ID:    0,
					Usage: model.MissingMetric(model.PermissionDenied, "%", "/sys/kernel/debug/rknpu/load", os.ErrPermission.Error(), now),
				}},
			},
			{Name: "DDR/DMC", Type: "ddr", Usage: available(15, "%", "/sys/dmc/load"), Frequency: available(528, "MHz", "/sys/dmc/cur_freq")},
			{
				Name: "Rockchip RGA", Type: "rga", Usage: available(30, "%", "/sys/kernel/debug/rkrga/load"),
				Frequency: model.MissingMetric(model.Unsupported, "MHz", "/sys/kernel/debug/rkrga/load", "not exposed", now),
				Cores:     []model.AcceleratorCore{{ID: 0, Name: "rga3", Usage: available(30, "%", "/sys/kernel/debug/rkrga/load")}},
			},
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
