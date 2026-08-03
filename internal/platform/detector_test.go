package platform

import (
	"path/filepath"
	"testing"

	"rtop/internal/sysfs"
)

func TestDetectorFromFixture(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	info, warnings := NewDetector(sysfs.MustNew(root)).Detect()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if info.Hostname != "fixture-board" {
		t.Fatalf("hostname = %q", info.Hostname)
	}
	if info.OS != "Fixture Linux 1.0" {
		t.Fatalf("OS = %q", info.OS)
	}
	if info.SoC != "RK3588" {
		t.Fatalf("SoC = %q", info.SoC)
	}
	if info.Board != "Rockchip RK3588 Fixture Board" {
		t.Fatalf("board = %q", info.Board)
	}
}

func TestParseOSRelease(t *testing.T) {
	values := parseOSRelease("NAME=Linux\nPRETTY_NAME=\"A Linux\"\n# ignored\n")
	if values["PRETTY_NAME"] != "A Linux" {
		t.Fatalf("PRETTY_NAME = %q", values["PRETTY_NAME"])
	}
}

func TestDetectSoCVariant(t *testing.T) {
	if got := detectSoC([]string{"rockchip,rk3588s"}); got != "RK3588S" {
		t.Fatalf("SoC = %q", got)
	}
}
