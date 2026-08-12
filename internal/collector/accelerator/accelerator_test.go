package accelerator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestCollectorFromKernel61Fixture(t *testing.T) {
	collector := newFixtureCollector(t, "rk3588-kernel61")
	accelerators, warnings := collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	assertMetric(t, accelerators[0].Usage, 12)
	assertMetric(t, accelerators[0].Frequency, 1000)
	if accelerators[0].Policy == nil || accelerators[0].Policy.Minimum.Value == nil ||
		*accelerators[0].Policy.Minimum.Value != 200 || accelerators[0].Policy.Maximum.Value == nil ||
		*accelerators[0].Policy.Maximum.Value != 1000 || accelerators[0].Policy.Governor.Value == nil ||
		*accelerators[0].Policy.Governor.Value != "simple_ondemand" {
		t.Fatalf("GPU policy = %#v", accelerators[0].Policy)
	}
	if accelerators[0].RuntimeStatus.Value == nil || *accelerators[0].RuntimeStatus.Value != "active" {
		t.Fatalf("GPU runtime status = %#v", accelerators[0].RuntimeStatus)
	}
	assertMetric(t, accelerators[1].Usage, 20)
	assertMetric(t, accelerators[1].Frequency, 900)
	assertMetric(t, accelerators[2].Usage, 28)
	assertMetric(t, accelerators[2].Frequency, 528)
	assertMetric(t, accelerators[3].Usage, 16)
	if accelerators[3].Type != rgaType || accelerators[3].Frequency.Status != model.Unsupported {
		t.Fatalf("RGA = %#v", accelerators[3])
	}
	if accelerators[3].DriverVersion == nil || accelerators[3].DriverVersion.Value == nil ||
		*accelerators[3].DriverVersion.Value != "rga: v1.3.0" {
		t.Fatalf("RGA driver version = %#v", accelerators[3].DriverVersion)
	}
	for id, want := range []float64{18, 24, 6} {
		assertMetric(t, accelerators[3].Cores[id].Usage, want)
	}
	for id, want := range []float64{10, 20, 30} {
		assertMetric(t, accelerators[1].Cores[id].Usage, want)
	}
}

func TestCollectorFromBSP510Fixture(t *testing.T) {
	collector := newFixtureCollector(t, "rk3588-bsp510")
	accelerators, warnings := collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	assertMetric(t, accelerators[0].Usage, 35)
	assertMetric(t, accelerators[0].Frequency, 800)
	assertMetric(t, accelerators[1].Usage, 15)
	assertMetric(t, accelerators[1].Frequency, 800)
	if accelerators[2].Usage.Status != model.Unsupported {
		t.Fatalf("DDR usage = %#v", accelerators[2].Usage)
	}
	assertMetric(t, accelerators[2].Frequency, 1560)
	assertMetric(t, accelerators[3].Usage, 30)
	if accelerators[3].Usage.Source != "/proc/rkrga/load" {
		t.Fatalf("RGA source = %q", accelerators[3].Usage.Source)
	}
	if len(accelerators[3].Cores) != 2 || accelerators[3].Cores[0].Name != "rga3" ||
		accelerators[3].Cores[1].Name != "rga2" {
		t.Fatalf("RGA schedulers = %#v", accelerators[3].Cores)
	}
	for id, want := range []float64{5, 15, 25} {
		assertMetric(t, accelerators[1].Cores[id].Usage, want)
	}
}

func TestCollectorCachesDynamicSourcesAndStaticRanges(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-bsp510")
	tracked := &readCountingFS{SystemFS: sysfs.MustNew(root), reads: make(map[string]int)}
	collector := NewCollector(tracked)
	collector.Collect()
	collector.Collect()

	for _, path := range []string{
		"/sys/class/devfreq/mali/available_frequencies",
		"/sys/class/devfreq/rknpu/available_frequencies",
		"/sys/class/devfreq/rockchip-dmc/available_frequencies",
		"/sys/class/devfreq/rockchip-dmc/load",
		"/sys/class/devfreq/rockchip-dmc/utilization",
		"/sys/class/devfreq/rockchip-dmc/utilisation",
		"/sys/class/devfreq/rockchip-dmc/busy_time",
		"/sys/class/devfreq/rockchip-dmc/total_time",
	} {
		if tracked.reads[path] != 1 {
			t.Fatalf("reads[%s] = %d, want 1", path, tracked.reads[path])
		}
	}
	if tracked.reads["/sys/class/devfreq/mali/load"] != 2 ||
		tracked.reads["/sys/class/devfreq/mali/cur_freq"] != 2 {
		t.Fatalf("selected GPU sources were not refreshed: %#v", tracked.reads)
	}
}

func TestParseLoadFormats(t *testing.T) {
	for input, want := range map[string]float64{
		"28@528000000Hz": 28,
		"50%":            50,
		"35":             35,
		"25 100":         25,
	} {
		got, err := parseLoad([]byte(input))
		if err != nil {
			t.Fatalf("parseLoad(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseLoad(%q) = %f, want %f", input, got, want)
		}
	}
	for _, input := range []string{"", "nope", "101", "-1"} {
		if _, err := parseLoad([]byte(input)); err == nil {
			t.Fatalf("parseLoad(%q) succeeded", input)
		}
	}
}

func TestParseCoreLoadFormats(t *testing.T) {
	for _, content := range []string{
		"NPU load: Core0: 10%, Core1: 20%, Core2: 30%,",
		"Core0 load: 10%\nCore1 load: 20%\nCore2 load: 30%\n",
	} {
		loads, err := parseCoreLoad([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		if len(loads) != 3 || loads[0] != 10 || loads[1] != 20 || loads[2] != 30 {
			t.Fatalf("loads = %#v", loads)
		}
	}
	if _, err := parseCoreLoad([]byte("NPU idle")); err == nil {
		t.Fatal("malformed core load succeeded")
	}
}

func TestParseRGALoad(t *testing.T) {
	content := []byte(`num of scheduler = 3
================= load ==================
scheduler[1]: rga3_core1
	 load = 24%
-----------------------------------
scheduler[0]: rga3_core0
	 load = 18%
-----------------------------------
scheduler[2]: rga2
	 load = 6%
`)
	cores, err := parseRGALoad(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(cores) != 3 {
		t.Fatalf("cores = %#v", cores)
	}
	for id, want := range []float64{18, 24, 6} {
		if cores[id].ID != id || cores[id].Usage.Value == nil || *cores[id].Usage.Value != want {
			t.Fatalf("core %d = %#v", id, cores[id])
		}
	}
	if cores[0].Name != "rga3_core0" || cores[2].Name != "rga2" {
		t.Fatalf("scheduler names = %#v", cores)
	}

	for _, malformed := range [][]byte{
		[]byte("RGA idle\n"),
		[]byte("scheduler[0]: rga3\nload = 101%\n"),
		[]byte("scheduler[0]: rga3\nload = 1%\nscheduler[0]: rga2\nload = 2%\n"),
		[]byte("num of scheduler = 2\nscheduler[0]: rga3\nload = 1%\n"),
	} {
		if _, err := parseRGALoad(malformed); err == nil {
			t.Fatalf("parseRGALoad(%q) succeeded", malformed)
		}
	}
}

func TestCollectorCachesMissingRGAProbe(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	tracked := &readCountingFS{SystemFS: sysfs.MustNew(root), reads: make(map[string]int)}
	collector := NewCollector(tracked)
	accelerators, warnings := collector.Collect()
	collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if accelerators[3].Usage.Status != model.Unsupported || accelerators[3].Usage.Value != nil {
		t.Fatalf("missing RGA = %#v", accelerators[3])
	}
	for _, path := range rgaLoadCandidates {
		if tracked.reads[path] != 1 {
			t.Fatalf("reads[%s] = %d, want 1", path, tracked.reads[path])
		}
	}
}

func TestCollectorPreservesPermissionDenied(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	base := sysfs.MustNew(root)
	collector := NewCollector(&permissionFS{SystemFS: base})
	accelerators, warnings := collector.Collect()
	if accelerators[0].Usage.Status != model.PermissionDenied {
		t.Fatalf("GPU usage = %#v", accelerators[0].Usage)
	}
	if len(warnings) < 2 {
		t.Fatalf("warnings = %#v", warnings)
	}
	for _, core := range accelerators[1].Cores {
		if core.Usage.Status != model.PermissionDenied || core.Usage.Value != nil {
			t.Fatalf("core = %#v", core)
		}
	}
	assertMetric(t, accelerators[1].Usage, 60)
	if accelerators[3].Usage.Status != model.PermissionDenied {
		t.Fatalf("RGA usage = %#v", accelerators[3].Usage)
	}
}

func TestCollectorTreatsRuntimeSuspendedNPUAsIdle(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	tracked := &suspendedNPUFS{SystemFS: sysfs.MustNew(root)}
	collector := NewCollector(tracked)
	tracked.watchDynamicReads = true

	accelerators, warnings := collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	npu := accelerators[1]
	assertMetric(t, npu.Usage, 0)
	if npu.Frequency.Status != model.Stale || npu.Frequency.Value != nil {
		t.Fatalf("NPU frequency = %#v", npu.Frequency)
	}
	if npu.RuntimeStatus.Value == nil || *npu.RuntimeStatus.Value != "suspended" {
		t.Fatalf("NPU runtime status = %#v", npu.RuntimeStatus)
	}
	if len(npu.Cores) != 3 {
		t.Fatalf("NPU cores = %#v", npu.Cores)
	}
	for _, core := range npu.Cores {
		assertMetric(t, core.Usage, 0)
	}
	if tracked.runtimeStatusReads != 1 {
		t.Fatalf("runtime status reads = %d, want 1", tracked.runtimeStatusReads)
	}
	if len(tracked.dynamicReads) != 0 {
		t.Fatalf("read stale NPU metrics while suspended: %v", tracked.dynamicReads)
	}
}

func TestAggregateCoreUsageRequiresEveryCore(t *testing.T) {
	timestamp := time.Now()
	cores := []model.AcceleratorCore{
		{ID: 0, Usage: model.AvailableMetric(10, "%", "debugfs", timestamp)},
		{ID: 1, Usage: model.AvailableMetric(20, "%", "debugfs", timestamp)},
		{ID: 2, Usage: model.AvailableMetric(30, "%", "debugfs", timestamp)},
	}
	metric, ok := aggregateCoreUsage(cores, timestamp)
	if !ok {
		t.Fatal("aggregateCoreUsage reported unavailable")
	}
	assertMetric(t, metric, 20)

	cores[1].Usage = model.MissingMetric(model.Unavailable, "%", "debugfs", "missing", timestamp)
	if _, ok := aggregateCoreUsage(cores, timestamp); ok {
		t.Fatal("aggregateCoreUsage accepted an unavailable core")
	}
}

func TestReadLoadPreservesPermissionDenied(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	base := sysfs.MustNew(root)
	device := device{path: "/sys/class/devfreq/fb000000.gpu", kind: gpuType}
	metric := readLoad(&permissionFS{SystemFS: base}, device, time.Now())
	if metric.Status != model.PermissionDenied {
		t.Fatalf("status = %q, error = %q", metric.Status, metric.Error)
	}
}

func newFixtureCollector(t *testing.T, fixture string) *Collector {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", fixture)
	return NewCollector(sysfs.MustNew(root))
}

func assertMetric(t *testing.T, metric model.Metric, want float64) {
	t.Helper()
	if metric.Status != model.Available || metric.Value == nil || *metric.Value != want {
		t.Fatalf("metric = %#v, want %f", metric, want)
	}
}

type permissionFS struct {
	sysfs.SystemFS
}

func (f *permissionFS) ReadFile(path string) ([]byte, error) {
	if path == "/sys/class/devfreq/fb000000.gpu/load" || strings.HasPrefix(path, "/sys/kernel/debug/") {
		return nil, os.ErrPermission
	}
	return f.SystemFS.ReadFile(path)
}

type readCountingFS struct {
	sysfs.SystemFS
	reads map[string]int
}

func (f *readCountingFS) ReadFile(path string) ([]byte, error) {
	f.reads[path]++
	return f.SystemFS.ReadFile(path)
}

type suspendedNPUFS struct {
	sysfs.SystemFS
	watchDynamicReads  bool
	runtimeStatusReads int
	dynamicReads       []string
}

func (f *suspendedNPUFS) ReadFile(path string) ([]byte, error) {
	if strings.Contains(path, "/fdab0000.npu/") && strings.HasSuffix(path, "/device/power/runtime_status") {
		if f.watchDynamicReads {
			f.runtimeStatusReads++
		}
		return []byte("suspended\n"), nil
	}
	if f.watchDynamicReads && isNPUDynamicPath(path) {
		f.dynamicReads = append(f.dynamicReads, path)
	}
	return f.SystemFS.ReadFile(path)
}

func isNPUDynamicPath(path string) bool {
	if strings.HasPrefix(path, "/sys/kernel/debug/rknpu/") {
		return true
	}
	if !strings.Contains(path, "/fdab0000.npu/") {
		return false
	}
	base := filepath.Base(path)
	switch base {
	case "load", "utilization", "utilisation", "busy_time", "total_time", "cur_freq", "target_freq":
		return true
	default:
		return false
	}
}
