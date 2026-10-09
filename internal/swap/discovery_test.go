package swap

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"rtop/internal/model"
)

func TestParseProcSwaps(t *testing.T) {
	entries, err := Parse([]byte(
		"Filename\tType\tSize\tUsed\tPriority\n" +
			"/dev/zram0 partition 1048572 262144 100\n" +
			"/swap\\040file file 2097148 0 -2\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].SizeBytes != 1048572*1024 ||
		entries[0].UsedBytes != 262144*1024 || entries[0].Priority != 100 ||
		entries[1].Path != "/swap file" {
		t.Fatalf("entries=%#v", entries)
	}
	devices := ToModel(entries, time.Now())
	if len(devices) != 2 || devices[0].Size.Value == nil || *devices[0].Size.Value != 1023.99609375 {
		t.Fatalf("devices=%#v", devices)
	}
}

func TestDiscoverClassifiesBackendsAtRuntime(t *testing.T) {
	filesystem := &discoveryFS{files: map[string]string{
		ProcSwapsPath:               "Filename Type Size Used Priority\n/dev/zram0 partition 1020 0 100\n/dev/mmcblk0p3 partition 2048 0 -2\n/swapfile file 4096 0 -3\n",
		"/sys/block/zram0/disksize": "1048576\n",
	}}
	entries, err := Discover(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.SwapKind{model.SwapZRAM, model.SwapBlock, model.SwapFile}
	for index := range want {
		if entries[index].Kind != want[index] {
			t.Fatalf("entry %d kind=%q, want %q", index, entries[index].Kind, want[index])
		}
	}
}

func TestParseProcSwapsRejectsMalformedRows(t *testing.T) {
	for _, input := range []string{
		"bad header\n",
		"Filename Type Size Used Priority\n/swap file nope 0 -2\n",
		"Filename Type Size Used Priority\n/swap file 1 2 -2\n",
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("Parse(%q) succeeded", input)
		}
	}
}

func TestTotalBytesRejectsOverflow(t *testing.T) {
	_, err := TotalBytes([]Entry{{SizeBytes: math.MaxUint64}, {SizeBytes: 1}})
	if err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestDecodeProcPathDoesNotDecodeEscapesTwice(t *testing.T) {
	if got := decodeProcPath(`/swap\134040literal\040space`); got != `/swap\040literal space` {
		t.Fatalf("decoded path=%q", got)
	}
}

type discoveryFS struct{ files map[string]string }

func (f *discoveryFS) ReadFile(path string) ([]byte, error) {
	value, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(value), nil
}

func (f *discoveryFS) ReadDir(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist }
func (f *discoveryFS) Readlink(string) (string, error)       { return "", os.ErrNotExist }
func (f *discoveryFS) Glob(string) ([]string, error)         { return nil, nil }
