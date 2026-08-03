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
	info, warnings := NewCollector(systemFS, time.Nanosecond).Collect(context.Background())
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
}

type sequenceFS struct {
	sysfs.SystemFS
	statReads int
	nextStat  []byte
}

func (f *sequenceFS) ReadFile(path string) ([]byte, error) {
	if path == procStatPath {
		f.statReads++
		if f.statReads == 2 {
			return f.nextStat, nil
		}
	}
	return f.SystemFS.ReadFile(path)
}
