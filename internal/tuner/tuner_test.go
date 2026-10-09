package tuner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rtop/internal/sysfs"
)

func TestSetSwappiness(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, SwappinessPath, "60\n")
	controller := New(sysfs.MustNew(root))
	if err := controller.SetSwappiness(120); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, root, SwappinessPath); got != "120" {
		t.Fatalf("swappiness=%q, want 120", got)
	}
	for _, value := range []int{-1, 201} {
		if err := controller.SetSwappiness(value); err == nil || !strings.Contains(err.Error(), "between 0 and 200") {
			t.Fatalf("SetSwappiness(%d) error=%v", value, err)
		}
	}
}

func TestSetSwappinessRollsBackVerificationFailure(t *testing.T) {
	filesystem := &swappinessVerifyFS{value: "60\n"}
	err := New(filesystem).SetSwappiness(10)
	if err == nil || !strings.Contains(err.Error(), "kernel reports") {
		t.Fatalf("error=%v", err)
	}
	if got := strings.TrimSpace(filesystem.value); got != "60" {
		t.Fatalf("swappiness was not restored: %q", got)
	}
}

func TestSetCPUProfileUpdatesEveryPolicy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"policy0", "policy4", "policy6"} {
		base := "/sys/devices/system/cpu/cpufreq/" + name
		writeFixture(t, root, base+"/scaling_governor", "schedutil\n")
		writeFixture(t, root, base+"/scaling_available_governors", "performance powersave schedutil\n")
	}
	result, err := New(sysfs.MustNew(root)).SetCPUProfile(CPUPerformance)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 3 || len(result.Policies) != 3 || result.Profile != CPUPerformance {
		t.Fatalf("result=%#v", result)
	}
	for _, path := range result.Policies {
		if got := readFixture(t, root, filepath.Join(path, "scaling_governor")); got != "performance" {
			t.Fatalf("%s governor=%q", path, got)
		}
	}

	result, err = New(sysfs.MustNew(root)).SetCPUProfile(CPUPerformance)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 0 {
		t.Fatalf("already-applied changed=%d, want 0", result.Changed)
	}
}

func TestSetCPUProfilePreflightsAvailableGovernors(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"policy0", "policy4"} {
		base := "/sys/devices/system/cpu/cpufreq/" + name
		writeFixture(t, root, base+"/scaling_governor", "schedutil\n")
		available := "performance powersave schedutil\n"
		if name == "policy4" {
			available = "performance schedutil\n"
		}
		writeFixture(t, root, base+"/scaling_available_governors", available)
	}
	_, err := New(sysfs.MustNew(root)).SetCPUProfile(CPUPowersave)
	if err == nil || !strings.Contains(err.Error(), `does not provide governor "powersave"`) {
		t.Fatalf("error=%v", err)
	}
	for _, name := range []string{"policy0", "policy4"} {
		path := "/sys/devices/system/cpu/cpufreq/" + name + "/scaling_governor"
		if got := readFixture(t, root, path); got != "schedutil" {
			t.Fatalf("%s changed during preflight: %q", path, got)
		}
	}
}

func TestSetCPUProfileRollsBackPartialWrite(t *testing.T) {
	filesystem := &faultFS{
		files: map[string]string{
			"/sys/devices/system/cpu/cpufreq/policy0/scaling_governor":            "schedutil\n",
			"/sys/devices/system/cpu/cpufreq/policy0/scaling_available_governors": "performance powersave schedutil\n",
			"/sys/devices/system/cpu/cpufreq/policy4/scaling_governor":            "schedutil\n",
			"/sys/devices/system/cpu/cpufreq/policy4/scaling_available_governors": "performance powersave schedutil\n",
		},
		policies: []string{
			"/sys/devices/system/cpu/cpufreq/policy0",
			"/sys/devices/system/cpu/cpufreq/policy4",
		},
		failPath: "/sys/devices/system/cpu/cpufreq/policy4/scaling_governor",
	}
	_, err := New(filesystem).SetCPUProfile(CPUPerformance)
	if err == nil || !strings.Contains(err.Error(), "policy4/scaling_governor") {
		t.Fatalf("error=%v", err)
	}
	if got := strings.TrimSpace(filesystem.files["/sys/devices/system/cpu/cpufreq/policy0/scaling_governor"]); got != "schedutil" {
		t.Fatalf("policy0 was not rolled back: %q", got)
	}
}

func TestSetCPUProfileFallsBackToPerCPUControls(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"cpu0", "cpu1"} {
		path := "/sys/devices/system/cpu/" + name + "/cpufreq/scaling_governor"
		writeFixture(t, root, path, "schedutil\n")
	}
	result, err := New(sysfs.MustNew(root)).SetCPUProfile(CPUPowersave)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 2 {
		t.Fatalf("changed=%d, want 2", result.Changed)
	}
}

func TestParseCPUProfile(t *testing.T) {
	if profile, err := ParseCPUProfile(" PERFORMANCE "); err != nil || profile != CPUPerformance {
		t.Fatalf("profile=%q error=%v", profile, err)
	}
	if _, err := ParseCPUProfile("turbo"); err == nil {
		t.Fatal("invalid profile was accepted")
	}
}

func writeFixture(t *testing.T, root, logicalPath, value string) {
	t.Helper()
	path := filepath.Join(root, strings.TrimPrefix(filepath.Clean(logicalPath), string(filepath.Separator)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, root, logicalPath string) string {
	t.Helper()
	path := filepath.Join(root, strings.TrimPrefix(filepath.Clean(logicalPath), string(filepath.Separator)))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

type faultFS struct {
	files    map[string]string
	policies []string
	failPath string
}

type swappinessVerifyFS struct {
	value            string
	firstWrite       bool
	verificationRead bool
}

func (f *swappinessVerifyFS) ReadFile(path string) ([]byte, error) {
	if path != SwappinessPath {
		return nil, os.ErrNotExist
	}
	if f.firstWrite && !f.verificationRead {
		f.verificationRead = true
		return []byte("11\n"), nil
	}
	return []byte(f.value), nil
}

func (f *swappinessVerifyFS) ReadDir(string) ([]os.DirEntry, error) { return nil, nil }
func (f *swappinessVerifyFS) Readlink(string) (string, error)       { return "", os.ErrNotExist }
func (f *swappinessVerifyFS) Glob(string) ([]string, error)         { return nil, nil }

func (f *swappinessVerifyFS) WriteFile(path string, data []byte) error {
	if path != SwappinessPath {
		return os.ErrNotExist
	}
	f.value = string(data)
	f.firstWrite = true
	return nil
}

func (f *faultFS) ReadFile(path string) ([]byte, error) {
	value, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(value), nil
}

func (f *faultFS) ReadDir(string) ([]os.DirEntry, error) { return nil, nil }
func (f *faultFS) Readlink(string) (string, error)       { return "", os.ErrNotExist }

func (f *faultFS) Glob(pattern string) ([]string, error) {
	if strings.Contains(pattern, "/cpufreq/policy") {
		return append([]string(nil), f.policies...), nil
	}
	return nil, nil
}

func (f *faultFS) WriteFile(path string, data []byte) error {
	if path == f.failPath {
		return errors.New("injected write failure")
	}
	if _, ok := f.files[path]; !ok {
		return os.ErrNotExist
	}
	f.files[path] = string(data)
	return nil
}
