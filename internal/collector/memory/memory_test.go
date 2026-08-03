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
}
