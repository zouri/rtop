//go:build linux

package tuner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSwapFormatPreservesUUIDAndLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swapfile")
	header := make([]byte, 4096)
	copy(header[4086:], "SWAPSPACE2")
	copy(header[1036:1052], []byte{
		0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0,
		0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0,
	})
	copy(header[1052:1068], "rtop-test")
	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatal(err)
	}
	format, err := readSwapFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format.UUID != "12345678-9abc-def0-1234-56789abcdef0" || format.Label != "rtop-test" {
		t.Fatalf("format=%#v", format)
	}
}

func TestReadSwapFormatIgnoresOrdinaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordinary")
	if err := os.WriteFile(path, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	format, err := readSwapFormat(path)
	if err != nil || format != (swapFormat{}) {
		t.Fatalf("format=%#v error=%v", format, err)
	}
}
