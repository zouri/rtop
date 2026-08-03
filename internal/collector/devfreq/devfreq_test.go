package devfreq

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestDiscoverClassifiesKernel61Fixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	devices, warnings := Discover(sysfs.MustNew(root))
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(devices) != 3 {
		t.Fatalf("devices = %#v", devices)
	}
	for _, kind := range []Kind{KindGPU, KindNPU, KindDDR} {
		if _, ok := Find(devices, kind); !ok {
			t.Fatalf("kind %q was not discovered: %#v", kind, devices)
		}
	}
}

func TestDiscoverClassifiesBSP510NamesWithoutAddresses(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-bsp510")
	devices, _ := Discover(sysfs.MustNew(root))
	for _, kind := range []Kind{KindGPU, KindNPU, KindDDR} {
		if _, ok := Find(devices, kind); !ok {
			t.Fatalf("kind %q was not discovered: %#v", kind, devices)
		}
	}
}

func TestParseLoadFormats(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"28@528000000Hz", 28},
		{"50%", 50},
		{"35", 35},
		{"25 100", 25},
	}
	for _, test := range tests {
		got, err := ParseLoad([]byte(test.input))
		if err != nil {
			t.Fatalf("ParseLoad(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("ParseLoad(%q) = %f, want %f", test.input, got, test.want)
		}
	}
	for _, input := range []string{"", "nope", "101", "-1"} {
		if _, err := ParseLoad([]byte(input)); err == nil {
			t.Fatalf("ParseLoad(%q) succeeded", input)
		}
	}
}

func TestReadLoadPreservesPermissionDenied(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	base := sysfs.MustNew(root)
	device := Device{Path: "/sys/class/devfreq/fb000000.gpu", Kind: KindGPU}
	metric := ReadLoad(&permissionFS{SystemFS: base, path: device.Path + "/load"}, device, time.Now())
	if metric.Status != model.PermissionDenied {
		t.Fatalf("status = %q, error = %q", metric.Status, metric.Error)
	}
}

type permissionFS struct {
	sysfs.SystemFS
	path string
}

func (f *permissionFS) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		return nil, os.ErrPermission
	}
	return f.SystemFS.ReadFile(path)
}
