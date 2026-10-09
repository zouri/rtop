package sysfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"rtop/internal/model"
)

func TestRootFSMapsAbsolutePaths(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := systemFS.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected fixture data")
	}
	matches, err := systemFS.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != "/sys/class/thermal/thermal_zone0" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestStatusForError(t *testing.T) {
	if got := StatusForError(os.ErrPermission); got != model.PermissionDenied {
		t.Fatalf("permission status = %q", got)
	}
	if got := StatusForError(os.ErrNotExist); got != model.Unsupported {
		t.Fatalf("not-exist status = %q", got)
	}
}

func TestRootFSWriteFileOnlyUpdatesExistingNodes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proc", "sys", "vm", "swappiness")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemFS := MustNew(root)
	if err := systemFS.WriteFile("/proc/sys/vm/swappiness", []byte("10")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "10" {
		t.Fatalf("swappiness=%q, want 10", got)
	}

	err = systemFS.WriteFile("/proc/sys/vm/missing", []byte("1"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing write error=%v, want not-exist", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "proc", "sys", "vm", "missing")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing control was unexpectedly created: %v", statErr)
	}
}

func TestRootFSWriteFileRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "governor")
	if err := os.WriteFile(outside, []byte("schedutil"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_governor")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	err := MustNew(root).WriteFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", []byte("performance"))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("outside symlink write error=%v, want permission denied", err)
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil || string(data) != "schedutil" {
		t.Fatalf("outside file data=%q error=%v", data, readErr)
	}
}

func TestRootFSWriteFileAllowsSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	path := filepath.Join(realRoot, "proc", "sys", "vm", "swappiness")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("60"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	if err := MustNew(rootLink).WriteFile("/proc/sys/vm/swappiness", []byte("20")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "20" {
		t.Fatalf("swappiness data=%q error=%v", data, err)
	}
}
