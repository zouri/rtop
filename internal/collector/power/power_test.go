package power

import (
	"os"
	"path/filepath"
	"testing"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestCollectorScansPowerAndFanSensors(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	collector := NewCollector(systemFS)
	sensors, fans, warnings := collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(sensors) != 4 {
		t.Fatalf("sensors = %#v", sensors)
	}
	assertSensor(t, sensors, "voltage", 12)
	assertSensor(t, sensors, "current", 0.75)
	assertSensor(t, sensors, "power", 9)
	assertSensor(t, sensors, "energy", 123.456789)
	if len(fans) != 1 {
		t.Fatalf("fans = %#v", fans)
	}
	if fans[0].Name != "board-fan" || fans[0].Speed.Value == nil || *fans[0].Speed.Value != 2400 ||
		fans[0].TargetSpeed.Value == nil || *fans[0].TargetSpeed.Value != 3000 ||
		fans[0].PWM.Value == nil || *fans[0].PWM.Value < 50.19 || *fans[0].PWM.Value > 50.20 ||
		fans[0].ControlMode.Value == nil || *fans[0].ControlMode.Value != "automatic" {
		t.Fatalf("fan = %#v", fans[0])
	}
	firstGlobs := systemFS.globCalls
	collector.Collect()
	if systemFS.globCalls != firstGlobs {
		t.Fatalf("discovery repeated: %d -> %d globs", firstGlobs, systemFS.globCalls)
	}
}

func TestCollectorPreservesPowerPermissionError(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	collector := NewCollector(&permissionFS{SystemFS: sysfs.MustNew(root)})
	sensors, _, warnings := collector.Collect()
	for _, sensor := range sensors {
		if sensor.Kind == "power" {
			if sensor.Value.Status != model.PermissionDenied {
				t.Fatalf("power sensor = %#v", sensor)
			}
			if len(warnings) == 0 {
				t.Fatal("permission error did not produce a warning")
			}
			return
		}
	}
	t.Fatal("power sensor not found")
}

func TestChannelIndexRejectsRelatedFiles(t *testing.T) {
	if index, ok := channelIndex("pwm12", "pwm", ""); !ok || index != 12 {
		t.Fatalf("channelIndex pwm12 = %d, %v", index, ok)
	}
	if _, ok := channelIndex("pwm12_enable", "pwm", ""); ok {
		t.Fatal("pwm enable file was treated as a PWM value")
	}
}

func assertSensor(t *testing.T, sensors []model.PowerSensor, kind string, want float64) {
	t.Helper()
	for _, sensor := range sensors {
		if sensor.Kind == kind {
			if sensor.Value.Status != model.Available || sensor.Value.Value == nil || *sensor.Value.Value != want {
				t.Fatalf("%s sensor = %#v", kind, sensor)
			}
			return
		}
	}
	t.Fatalf("%s sensor not found", kind)
}

type countingFS struct {
	sysfs.SystemFS
	globCalls int
}

func (f *countingFS) Glob(pattern string) ([]string, error) {
	f.globCalls++
	return f.SystemFS.Glob(pattern)
}

type permissionFS struct {
	sysfs.SystemFS
}

func (f *permissionFS) ReadFile(path string) ([]byte, error) {
	if path == "/sys/class/hwmon/hwmon0/power1_input" {
		return nil, os.ErrPermission
	}
	return f.SystemFS.ReadFile(path)
}
