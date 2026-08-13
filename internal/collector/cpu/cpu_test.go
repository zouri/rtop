package cpu

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestParseStatAndUsage(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	firstData, err := os.ReadFile(filepath.Join(root, "proc", "stat"))
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(filepath.Join(root, "proc", "stat.next"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := ParseStat(firstData)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseStat(secondData)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := Usage(first["cpu"], second["cpu"])
	if err != nil {
		t.Fatal(err)
	}
	if usage < 54.4 || usage > 54.7 {
		t.Fatalf("usage = %f, want about 54.5%%", usage)
	}
}

func TestParseStatRejectsMalformedAndOverflow(t *testing.T) {
	tests := [][]byte{
		[]byte("cpu 1 2 nope 4\n"),
		[]byte("cpu 18446744073709551616 0 0 0\n"),
		[]byte("intr 1\n"),
	}
	for _, input := range tests {
		if _, err := ParseStat(input); err == nil {
			t.Fatalf("ParseStat(%q) succeeded", input)
		}
	}
}

func TestCollectorUsesTwoSamplesAndReadsFrequency(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	base := sysfs.MustNew(root)
	next, err := os.ReadFile(filepath.Join(root, "proc", "stat.next"))
	if err != nil {
		t.Fatal(err)
	}
	systemFS := &sequenceFS{SystemFS: base, nextStat: next}
	collector := NewCollector(systemFS, time.Nanosecond)
	info, warnings := collector.Collect(context.Background())
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if info.Overall.Status != model.Available || info.Overall.Value == nil {
		t.Fatalf("overall metric = %#v", info.Overall)
	}
	if len(info.Cores) != 2 {
		t.Fatalf("cores = %d", len(info.Cores))
	}
	if info.Cores[0].Cluster != "little" || info.Cores[1].Cluster != "big" {
		t.Fatalf("clusters = %q, %q", info.Cores[0].Cluster, info.Cores[1].Cluster)
	}
	if info.Cores[1].Frequency.Value == nil || *info.Cores[1].Frequency.Value != 2208 {
		t.Fatalf("frequency = %#v", info.Cores[1].Frequency)
	}
	if info.Cores[1].Policy == nil || info.Cores[1].Policy.Maximum.Value == nil ||
		*info.Cores[1].Policy.Maximum.Value != 2208 || info.Cores[1].Policy.Governor.Value == nil ||
		*info.Cores[1].Policy.Governor.Value != "schedutil" {
		t.Fatalf("policy = %#v", info.Cores[1].Policy)
	}
	if systemFS.statReads != 2 {
		t.Fatalf("first collection read %s %d times, want 2", procStatPath, systemFS.statReads)
	}
	collector.Collect(context.Background())
	if systemFS.statReads != 3 {
		t.Fatalf("second collection read %s %d total times, want 3", procStatPath, systemFS.statReads)
	}
	collector.Reset()
	collector.Collect(context.Background())
	if systemFS.statReads != 5 {
		t.Fatalf("collection after reset read %s %d total times, want 5", procStatPath, systemFS.statReads)
	}
}

func TestCollectorCachesFrequencyTopology(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	collector := NewCollector(systemFS, time.Nanosecond)
	if systemFS.globCalls != 0 || systemFS.maxFrequencyReads != 0 {
		t.Fatal("collector construction eagerly discovered CPU topology")
	}

	collector.Collect(context.Background())
	if systemFS.globCalls != 3 {
		t.Fatalf("topology discovery glob calls = %d, want 3", systemFS.globCalls)
	}
	if systemFS.maxFrequencyReads != 2 {
		t.Fatalf("maximum frequency reads = %d, want 2", systemFS.maxFrequencyReads)
	}

	collector.Collect(context.Background())
	if systemFS.globCalls != 3 {
		t.Fatalf("collection repeated topology discovery: glob calls = %d", systemFS.globCalls)
	}
	if systemFS.maxFrequencyReads != 2 {
		t.Fatalf("collection repeated maximum frequency reads: %d", systemFS.maxFrequencyReads)
	}
}

func TestParseCPUListAcceptsPolicyAndCPUListFormats(t *testing.T) {
	for input, expected := range map[string][]int{
		"0 1 2 3": {0, 1, 2, 3},
		"0-3,6-7": {0, 1, 2, 3, 6, 7},
		"3 1 3 2": {1, 2, 3},
	} {
		got, err := parseCPUList(input)
		if err != nil {
			t.Fatalf("parseCPUList(%q): %v", input, err)
		}
		if len(got) != len(expected) {
			t.Fatalf("parseCPUList(%q)=%v, want %v", input, got, expected)
		}
		for index := range expected {
			if got[index] != expected[index] {
				t.Fatalf("parseCPUList(%q)=%v, want %v", input, got, expected)
			}
		}
	}
	for _, input := range []string{"x", "3-1", "1-2-3"} {
		if _, err := parseCPUList(input); err == nil {
			t.Fatalf("parseCPUList(%q) succeeded", input)
		}
	}
}

func TestFrequencyTopologyMapsSharedRK3588Policies(t *testing.T) {
	const root = "/sys/devices/system/cpu/cpufreq/"
	files := map[string]string{
		root + "policy0/related_cpus":     "0-3\n",
		root + "policy0/scaling_cur_freq": "1200000\n",
		root + "policy0/scaling_min_freq": "408000\n",
		root + "policy0/scaling_max_freq": "1800000\n",
		root + "policy0/cpuinfo_min_freq": "408000\n",
		root + "policy0/cpuinfo_max_freq": "1800000\n",
		root + "policy4/related_cpus":     "4 5\n",
		root + "policy4/scaling_cur_freq": "1800000\n",
		root + "policy4/scaling_min_freq": "408000\n",
		root + "policy4/scaling_max_freq": "2400000\n",
		root + "policy4/cpuinfo_min_freq": "408000\n",
		root + "policy4/cpuinfo_max_freq": "2400000\n",
		// Exercise the affected_cpus fallback used by some older BSPs.
		root + "policy6/affected_cpus":    "6-7\n",
		root + "policy6/scaling_cur_freq": "2208000\n",
		root + "policy6/scaling_min_freq": "408000\n",
		root + "policy6/scaling_max_freq": "2400000\n",
		root + "policy6/cpuinfo_min_freq": "408000\n",
		root + "policy6/cpuinfo_max_freq": "2400000\n",
	}
	systemFS := &mapFS{
		files: files,
		globs: map[string][]string{
			"/sys/devices/system/cpu/cpufreq/policy[0-9]*": {
				root + "policy0", root + "policy4", root + "policy6",
			},
		},
	}
	topology, clusters := discoverFrequencyTopology(systemFS)
	if len(topology) != 8 {
		t.Fatalf("policy-mapped CPUs=%d, want 8: %#v", len(topology), topology)
	}
	for id := 0; id < 8; id++ {
		policy := "policy0"
		if id >= 4 {
			policy = "policy4"
		}
		if id >= 6 {
			policy = "policy6"
		}
		wantBase := root + policy
		if topology[id].basePath != wantBase || topology[id].currentPath != wantBase+"/scaling_cur_freq" {
			t.Fatalf("cpu%d topology=%#v, want policy %s", id, topology[id], policy)
		}
	}
	for id, want := range map[int]string{0: "little", 3: "little", 4: "big", 5: "big", 6: "big", 7: "big"} {
		if clusters[id] != want {
			t.Fatalf("cpu%d cluster=%q, want %q", id, clusters[id], want)
		}
	}
}

func TestCollectorReadsSharedPolicyOncePerSnapshot(t *testing.T) {
	const policy = "/sys/devices/system/cpu/cpufreq/policy0"
	first := []byte("cpu  10 0 10 80\ncpu0 5 0 5 40\ncpu1 5 0 5 40\n")
	second := []byte("cpu  20 0 20 160\ncpu0 10 0 10 80\ncpu1 10 0 10 80\n")
	base := &mapFS{
		files: map[string]string{
			policy + "/related_cpus":     "0 1\n",
			policy + "/scaling_cur_freq": "1200000\n",
			policy + "/scaling_min_freq": "408000\n",
			policy + "/scaling_max_freq": "1800000\n",
			policy + "/cpuinfo_min_freq": "408000\n",
			policy + "/cpuinfo_max_freq": "1800000\n",
			policy + "/scaling_governor": "schedutil\n",
		},
		globs: map[string][]string{
			"/sys/devices/system/cpu/cpufreq/policy[0-9]*": {policy},
		},
	}
	systemFS := &policyCountingFS{SystemFS: base, stats: [][]byte{first, second}}
	collector := NewCollector(systemFS, time.Nanosecond)
	info, warnings := collector.Collect(context.Background())
	if len(warnings) != 0 || len(info.Cores) != 2 {
		t.Fatalf("cores=%d warnings=%v", len(info.Cores), warnings)
	}
	for _, core := range info.Cores {
		if got := metricValue(core.Frequency); got != 1200 {
			t.Fatalf("cpu%d frequency=%v", core.ID, got)
		}
	}
	if got := systemFS.reads[policy+"/scaling_cur_freq"]; got != 1 {
		t.Fatalf("shared scaling_cur_freq reads=%d, want 1", got)
	}
	if got := systemFS.reads[policy+"/scaling_governor"]; got != 1 {
		t.Fatalf("shared scaling_governor reads=%d, want 1", got)
	}
}

func metricValue(metric model.Metric) float64 {
	if metric.Value == nil {
		return -1
	}
	return *metric.Value
}

type sequenceFS struct {
	sysfs.SystemFS
	statReads int
	nextStat  []byte
}

type countingFS struct {
	sysfs.SystemFS
	globCalls         int
	maxFrequencyReads int
}

type mapFS struct {
	files map[string]string
	globs map[string][]string
}

type policyCountingFS struct {
	sysfs.SystemFS
	stats [][]byte
	reads map[string]int
}

func (f *policyCountingFS) ReadFile(path string) ([]byte, error) {
	if path == procStatPath {
		if len(f.stats) == 0 {
			return nil, os.ErrNotExist
		}
		value := f.stats[0]
		f.stats = f.stats[1:]
		return value, nil
	}
	if f.reads == nil {
		f.reads = make(map[string]int)
	}
	f.reads[path]++
	return f.SystemFS.ReadFile(path)
}

func (f *mapFS) ReadFile(path string) ([]byte, error) {
	value, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(value), nil
}

func (f *mapFS) ReadDir(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist }
func (f *mapFS) Readlink(string) (string, error)       { return "", os.ErrNotExist }
func (f *mapFS) Glob(pattern string) ([]string, error) {
	return append([]string(nil), f.globs[pattern]...), nil
}

func (f *countingFS) Glob(pattern string) ([]string, error) {
	f.globCalls++
	return f.SystemFS.Glob(pattern)
}

func (f *countingFS) ReadFile(path string) ([]byte, error) {
	if filepath.Base(path) == "cpuinfo_max_freq" {
		f.maxFrequencyReads++
	}
	return f.SystemFS.ReadFile(path)
}

func (f *sequenceFS) ReadFile(path string) ([]byte, error) {
	if path == procStatPath {
		f.statReads++
		if f.statReads >= 2 {
			return f.nextStat, nil
		}
	}
	return f.SystemFS.ReadFile(path)
}
