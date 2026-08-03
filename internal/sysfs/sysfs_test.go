package sysfs

import (
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
