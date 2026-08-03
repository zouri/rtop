package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestParseStatHandlesSpacesAndParentheses(t *testing.T) {
	input := []byte("42 (worker ) pool) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 7 18 1234 20 30")
	stat, err := ParseStat(input)
	if err != nil {
		t.Fatal(err)
	}
	if stat.PID != 42 || stat.Name != "worker ) pool" {
		t.Fatalf("stat = %#v", stat)
	}
	if stat.Ticks != 23 || stat.Threads != 7 || stat.StartTime != 1234 || stat.RSSPages != 30 {
		t.Fatalf("stat = %#v", stat)
	}
}

func TestParseStatRejectsMalformedInput(t *testing.T) {
	tests := [][]byte{
		[]byte("nope"),
		[]byte("0 (x) S"),
		[]byte("1 (x) S 1 2 3 4 5 6 7 8 9 nope 11 12 13 14 15 1 17 18 19 20"),
	}
	for _, input := range tests {
		if _, err := ParseStat(input); err == nil {
			t.Fatalf("ParseStat(%q) succeeded", input)
		}
	}
}

func TestParseStatus(t *testing.T) {
	status, err := ParseStatus([]byte("VmRSS:\t20480 kB\nThreads:\t4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if status.RSSBytes != 20*1024*1024 || status.Threads != 4 {
		t.Fatalf("status = %#v", status)
	}
}

func TestCollectorCalculatesCPUAndMemory(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	base := sysfs.MustNew(root)
	nextSystem, err := os.ReadFile(filepath.Join(root, "proc", "stat.next"))
	if err != nil {
		t.Fatal(err)
	}
	nextProcess, err := os.ReadFile(filepath.Join(root, "proc", "100", "stat.next"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &sequenceFS{
		SystemFS: base,
		next: map[string][]byte{
			"/proc/stat":     nextSystem,
			"/proc/100/stat": nextProcess,
		},
	}
	processes, warnings := NewCollector(fixture, time.Nanosecond).Collect(context.Background())
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(processes) != 1 {
		t.Fatalf("processes = %#v", processes)
	}
	process := processes[0]
	if process.CPU.Status != model.Available || process.CPU.Value == nil {
		t.Fatalf("CPU = %#v", process.CPU)
	}
	if *process.CPU.Value < 54.4 || *process.CPU.Value > 54.7 {
		t.Fatalf("CPU = %f", *process.CPU.Value)
	}
	if process.Memory.Value == nil || *process.Memory.Value != 20 {
		t.Fatalf("memory = %#v", process.Memory)
	}
	if process.Threads != 4 {
		t.Fatalf("threads = %d", process.Threads)
	}
}

type sequenceFS struct {
	sysfs.SystemFS
	reads map[string]int
	next  map[string][]byte
}

func (f *sequenceFS) ReadFile(path string) ([]byte, error) {
	if f.reads == nil {
		f.reads = make(map[string]int)
	}
	f.reads[path]++
	if f.reads[path] == 2 {
		if data, ok := f.next[path]; ok {
			return data, nil
		}
	}
	return f.SystemFS.ReadFile(path)
}
