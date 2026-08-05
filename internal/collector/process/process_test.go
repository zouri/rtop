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
		links: map[string]string{
			"/proc/100/exe": "/usr/bin/fixture-worker",
		},
	}
	collector := NewCollector(fixture, time.Nanosecond)
	processes, warnings := collector.Collect(context.Background())
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
	wantMemory := float64(100*os.Getpagesize()) / (1024 * 1024)
	if process.Memory.Value == nil || *process.Memory.Value != wantMemory {
		t.Fatalf("memory = %#v", process.Memory)
	}
	if process.Memory.Source != "/proc/100/stat" {
		t.Fatalf("memory source = %q", process.Memory.Source)
	}
	if process.Threads != 4 {
		t.Fatalf("threads = %d", process.Threads)
	}
	if process.Path != "/usr/bin/fixture-worker" {
		t.Fatalf("path = %q", process.Path)
	}
	_, _ = collector.Collect(context.Background())
	if fixture.linkReads["/proc/100/exe"] != 1 {
		t.Fatalf("exe link reads = %d, want 1", fixture.linkReads["/proc/100/exe"])
	}
	if fixture.reads["/proc/100/status"] != 0 {
		t.Fatalf("status reads = %d, want 0", fixture.reads["/proc/100/status"])
	}
}

func TestBuildReturnsAllProcessesOrderedByPID(t *testing.T) {
	current := &capture{
		timestamp: time.Now(),
		processes: make(map[int]sample, 250),
	}
	for pid := 250; pid >= 1; pid-- {
		current.processes[pid] = sample{
			stat:         Stat{PID: pid, Name: "process"},
			statPath:     "/proc/stat",
			memorySource: "/proc/status",
		}
	}

	processes := (&Collector{}).build(current, nil)
	if len(processes) != 250 {
		t.Fatalf("process count = %d, want 250", len(processes))
	}
	for i, process := range processes {
		wantPID := i + 1
		if process.PID != wantPID {
			t.Fatalf("processes[%d].PID = %d, want %d", i, process.PID, wantPID)
		}
	}
}

func TestPreviousProcessRejectsReusedPID(t *testing.T) {
	previous := &capture{processes: map[int]sample{
		42: {stat: Stat{PID: 42, StartTime: 100}, executablePath: "/usr/bin/old"},
	}}
	if process, ok := previousProcess(previous, Stat{PID: 42, StartTime: 100}); !ok || process.executablePath != "/usr/bin/old" {
		t.Fatalf("same process was not reused: %#v, %v", process, ok)
	}
	if _, ok := previousProcess(previous, Stat{PID: 42, StartTime: 200}); ok {
		t.Fatal("reused PID inherited the previous executable path")
	}
}

type sequenceFS struct {
	sysfs.SystemFS
	reads     map[string]int
	next      map[string][]byte
	links     map[string]string
	linkReads map[string]int
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

func (f *sequenceFS) Readlink(path string) (string, error) {
	if f.linkReads == nil {
		f.linkReads = make(map[string]int)
	}
	f.linkReads[path]++
	if target, ok := f.links[path]; ok {
		return target, nil
	}
	return f.SystemFS.Readlink(path)
}
