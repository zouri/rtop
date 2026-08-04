package memory

import (
	"os"
	"path/filepath"
	"testing"

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

func TestParseZRAMMMStat(t *testing.T) {
	original, compressed, used, err := parseZRAMMMStat([]byte("100 50 75 0 0\n"))
	if err != nil || original != 100 || compressed != 50 || used != 75 {
		t.Fatalf("values = %d %d %d, err=%v", original, compressed, used, err)
	}
	if _, _, _, err := parseZRAMMMStat([]byte("100 nope")); err == nil {
		t.Fatal("malformed mm_stat succeeded")
	}
}
