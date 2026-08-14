package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestParseMeminfoFixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	data, err := os.ReadFile(filepath.Join(root, "proc", "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := ParseMeminfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if values["MemTotal"] != 8192000*1024 {
		t.Fatalf("MemTotal = %d", values["MemTotal"])
	}
}

func TestParseMeminfoRejectsInvalidInput(t *testing.T) {
	tests := [][]byte{
		[]byte("MemTotal:\n"),
		[]byte("MemTotal: nope kB\n"),
		[]byte("MemTotal: 10 bananas\n"),
		[]byte("MemFree: 10 kB\n"),
		[]byte("MemTotal: 18446744073709551615 GB\n"),
	}
	for _, input := range tests {
		if _, err := ParseMeminfo(input); err == nil {
			t.Fatalf("ParseMeminfo(%q) succeeded", input)
		}
	}
}

func TestParseLoadavg(t *testing.T) {
	values, err := ParseLoadavg([]byte("1.25 0.75 0.50 2/512 12345\n"))
	if err != nil {
		t.Fatal(err)
	}
	if values.OneMinute != 1.25 || values.Runnable != 2 || values.Tasks != 512 {
		t.Fatalf("values = %#v", values)
	}
	if _, err := ParseLoadavg([]byte("bad")); err == nil {
		t.Fatal("malformed loadavg succeeded")
	}
}

func TestParsePressure(t *testing.T) {
	values, err := ParsePressure([]byte(
		"some avg10=1.25 avg60=0.75 avg300=0.50 total=234567\n" +
			"full avg10=0.40 avg60=0.20 avg300=0.10 total=34567\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if values["some"].Avg10 != 1.25 || values["full"].Total != 34567 {
		t.Fatalf("values = %#v", values)
	}
	for _, input := range []string{
		"",
		"some avg10=nope avg60=0 avg300=0 total=0\n",
		"other avg10=0 avg60=0 avg300=0 total=0\n",
	} {
		if _, err := ParsePressure([]byte(input)); err == nil {
			t.Fatalf("ParsePressure(%q) succeeded", input)
		}
	}
}

func TestCollectorFromFixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	memoryInfo, loadInfo, warnings := NewCollector(sysfs.MustNew(root)).Collect()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if memoryInfo.Total.Status != model.Available || memoryInfo.Total.Value == nil || *memoryInfo.Total.Value != 8000 {
		t.Fatalf("total = %#v", memoryInfo.Total)
	}
	if memoryInfo.Used.Value == nil || *memoryInfo.Used.Value != 4000 {
		t.Fatalf("used = %#v", memoryInfo.Used)
	}
	for name, test := range map[string]struct {
		metric model.Metric
		want   float64
	}{
		"free":        {memoryInfo.Free, 1000},
		"shared":      {memoryInfo.Shared, 62.5},
		"buffers":     {memoryInfo.Buffers, 250},
		"cache":       {memoryInfo.Cache, 2125},
		"buff/cache":  {memoryInfo.BufferCache, 2375},
		"available":   {memoryInfo.Available, 4000},
		"swap total":  {memoryInfo.SwapTotal, 2000},
		"swap used":   {memoryInfo.SwapUsed, 500},
		"swap free":   {memoryInfo.SwapFree, 1500},
		"swap cached": {memoryInfo.SwapCached, 0},
	} {
		if test.metric.Status != model.Available || test.metric.Value == nil || *test.metric.Value != test.want {
			t.Errorf("%s = %#v, want %.1f MiB", name, test.metric, test.want)
		}
	}
	if loadInfo.Tasks.Value == nil || *loadInfo.Tasks.Value != 512 {
		t.Fatalf("tasks = %#v", loadInfo.Tasks)
	}
	if memoryInfo.CMATotal.Value == nil || *memoryInfo.CMATotal.Value != 500 ||
		memoryInfo.CMAUsed.Value == nil || *memoryInfo.CMAUsed.Value != 125 {
		t.Fatalf("CMA = total %#v used %#v", memoryInfo.CMATotal, memoryInfo.CMAUsed)
	}
	if memoryInfo.Pressure.Memory.Some.Avg10.Value == nil ||
		*memoryInfo.Pressure.Memory.Some.Avg10.Value != 1.25 ||
		memoryInfo.Pressure.Memory.Full.Avg10.Value == nil ||
		*memoryInfo.Pressure.Memory.Full.Avg10.Value != 0.40 {
		t.Fatalf("pressure = %#v", memoryInfo.Pressure.Memory)
	}
	if len(memoryInfo.ZRAM) != 1 || memoryInfo.ZRAM[0].MemoryUsed.Value == nil ||
		*memoryInfo.ZRAM[0].MemoryUsed.Value != 224 || memoryInfo.ZRAM[0].Algorithm.Value == nil ||
		*memoryInfo.ZRAM[0].Algorithm.Value != "zstd" {
		t.Fatalf("zram = %#v", memoryInfo.ZRAM)
	}
}

func TestCollectorDoesNotReportMissingBreakdownFieldsAsZero(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("MemTotal: 1024 kB\nMemFree: 256 kB\nBuffers: 64 kB\nCached: 128 kB\nSwapTotal: 512 kB\nSwapFree: 256 kB\n")
	if err := os.WriteFile(filepath.Join(proc, "meminfo"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	memoryInfo, warning := NewCollector(sysfs.MustNew(root)).collectMemory(time.Now())
	if warning != nil {
		t.Fatalf("unexpected warning: %#v", warning)
	}
	for name, metric := range map[string]model.Metric{
		"shared": memoryInfo.Shared, "swap cached": memoryInfo.SwapCached,
	} {
		if metric.Status != model.Unsupported || metric.Value != nil {
			t.Errorf("missing %s = %#v, want unsupported without a value", name, metric)
		}
	}
	if memoryInfo.Cache.Status != model.Available || memoryInfo.BufferCache.Status != model.Available {
		t.Fatalf("derived cache metrics = cache %#v buff/cache %#v", memoryInfo.Cache, memoryInfo.BufferCache)
	}
}

func TestParseZRAMMMStat(t *testing.T) {
	original, compressed, used, err := parseZRAMMMStat([]byte("100 50 75 0 0\n"))
	if err != nil || original != 100 || compressed != 50 || used != 75 {
		t.Fatalf("values = %d %d %d, err=%v", original, compressed, used, err)
	}
	if _, _, _, err := parseZRAMMMStat([]byte("100 nope")); err == nil {
		t.Fatal("malformed mm_stat succeeded")
	}
}
