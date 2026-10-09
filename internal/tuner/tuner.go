// Package tuner contains the small, explicit write surface used by rtop's
// optional system controls. Monitoring collectors do not depend on it.
package tuner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"rtop/internal/sysfs"
)

const (
	SwappinessMinimum = 0
	SwappinessMaximum = 200
	SwappinessPath    = "/proc/sys/vm/swappiness"
)

type CPUProfile string

const (
	CPUPerformance CPUProfile = "performance"
	CPUPowersave   CPUProfile = "powersave"
)

type CPUResult struct {
	Profile  CPUProfile
	Policies []string
	Changed  int
}

type Controller struct {
	fs          sysfs.WritableSystemFS
	swapManager *SwapManager
}

func New(systemFS sysfs.WritableSystemFS) *Controller {
	return &Controller{fs: systemFS}
}

func NewWithSwapExecutor(systemFS sysfs.WritableSystemFS, executor SwapPlanExecutor) *Controller {
	return &Controller{fs: systemFS, swapManager: NewSwapManager(systemFS, executor)}
}

func (c *Controller) SetSwapSize(ctx context.Context, bytes uint64) (SwapResizeResult, error) {
	if c.swapManager == nil {
		return SwapResizeResult{}, errors.New("swap capacity control is unavailable in this session")
	}
	return c.swapManager.ResizeTotal(ctx, bytes)
}

func ParseCPUProfile(value string) (CPUProfile, error) {
	profile := CPUProfile(strings.ToLower(strings.TrimSpace(value)))
	switch profile {
	case CPUPerformance, CPUPowersave:
		return profile, nil
	default:
		return "", fmt.Errorf("CPU mode must be %q or %q", CPUPerformance, CPUPowersave)
	}
}

// SetSwappiness updates the kernel's relative preference for swapping. Linux
// accepts 0 through 200; values over 100 are useful when swap I/O is cheaper
// than filesystem I/O, for example with compressed RAM swap.
func (c *Controller) SetSwappiness(value int) error {
	if value < SwappinessMinimum || value > SwappinessMaximum {
		return fmt.Errorf("swappiness must be between %d and %d", SwappinessMinimum, SwappinessMaximum)
	}
	requested := strconv.Itoa(value)
	previousData, err := c.fs.ReadFile(SwappinessPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", SwappinessPath, err)
	}
	previous := strings.TrimSpace(string(previousData))
	if previous == requested {
		return nil
	}
	if err := c.fs.WriteFile(SwappinessPath, []byte(requested)); err != nil {
		return writeError(SwappinessPath, err)
	}
	data, err := c.fs.ReadFile(SwappinessPath)
	if err != nil {
		return rollbackSwappiness(c.fs, previous, fmt.Errorf("verify %s: %w", SwappinessPath, err))
	}
	actual := strings.TrimSpace(string(data))
	if actual != requested {
		return rollbackSwappiness(c.fs, previous, fmt.Errorf(
			"verify %s: kernel reports %q after writing %q", SwappinessPath, actual, requested,
		))
	}
	return nil
}

type cpuPolicy struct {
	path      string
	governor  string
	available []string
}

// SetCPUProfile changes every discovered CPUFreq policy as one operation. All
// policies are checked first; if a later write or verification fails, earlier
// writes are restored to keep heterogeneous SoCs out of a partial mode.
func (c *Controller) SetCPUProfile(profile CPUProfile) (CPUResult, error) {
	normalized, err := ParseCPUProfile(string(profile))
	if err != nil {
		return CPUResult{}, err
	}
	profile = normalized
	paths, err := discoverCPUPolicies(c.fs)
	if err != nil {
		return CPUResult{}, err
	}
	if len(paths) == 0 {
		return CPUResult{}, errors.New("CPUFreq policies are not exposed by this kernel")
	}

	policies := make([]cpuPolicy, 0, len(paths))
	for _, path := range paths {
		policy, err := readCPUPolicy(c.fs, path)
		if err != nil {
			return CPUResult{}, err
		}
		if len(policy.available) > 0 && !contains(policy.available, string(profile)) {
			return CPUResult{}, fmt.Errorf(
				"%s does not provide governor %q (available: %s)",
				path, profile, strings.Join(policy.available, ", "),
			)
		}
		policies = append(policies, policy)
	}

	result := CPUResult{Profile: profile, Policies: append([]string(nil), paths...)}
	changed := make([]cpuPolicy, 0, len(policies))
	for _, policy := range policies {
		if policy.governor == string(profile) {
			continue
		}
		governorPath := filepath.Join(policy.path, "scaling_governor")
		if err := c.fs.WriteFile(governorPath, []byte(profile)); err != nil {
			return CPUResult{}, rollbackError(c.fs, changed, writeError(governorPath, err))
		}
		changed = append(changed, policy)
		data, err := c.fs.ReadFile(governorPath)
		if err != nil {
			return CPUResult{}, rollbackError(c.fs, changed, fmt.Errorf("verify %s: %w", governorPath, err))
		}
		if actual := strings.TrimSpace(string(data)); actual != string(profile) {
			return CPUResult{}, rollbackError(c.fs, changed, fmt.Errorf(
				"verify %s: kernel reports %q after writing %q", governorPath, actual, profile,
			))
		}
	}
	result.Changed = len(changed)
	return result, nil
}

func discoverCPUPolicies(systemFS sysfs.SystemFS) ([]string, error) {
	paths, err := systemFS.Glob("/sys/devices/system/cpu/cpufreq/policy[0-9]*")
	if err != nil {
		return nil, fmt.Errorf("discover CPUFreq policies: %w", err)
	}
	if len(paths) == 0 {
		governors, globErr := systemFS.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor")
		if globErr != nil {
			return nil, fmt.Errorf("discover per-CPU frequency controls: %w", globErr)
		}
		paths = make([]string, 0, len(governors))
		for _, governor := range governors {
			paths = append(paths, filepath.Dir(governor))
		}
	}

	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	sort.Strings(unique)
	return unique, nil
}

func readCPUPolicy(systemFS sysfs.SystemFS, path string) (cpuPolicy, error) {
	governorPath := filepath.Join(path, "scaling_governor")
	data, err := systemFS.ReadFile(governorPath)
	if err != nil {
		return cpuPolicy{}, fmt.Errorf("read %s: %w", governorPath, err)
	}
	governor := strings.TrimSpace(string(data))
	if governor == "" {
		return cpuPolicy{}, fmt.Errorf("read %s: empty governor", governorPath)
	}

	availablePath := filepath.Join(path, "scaling_available_governors")
	data, err = systemFS.ReadFile(availablePath)
	var available []string
	switch {
	case err == nil:
		available = strings.Fields(string(data))
	case errors.Is(err, fs.ErrNotExist):
		// Some vendor drivers omit this optional discovery node. In that case
		// the scaling_governor write itself remains authoritative.
	default:
		return cpuPolicy{}, fmt.Errorf("read %s: %w", availablePath, err)
	}
	return cpuPolicy{path: path, governor: governor, available: available}, nil
}

func rollbackError(systemFS sysfs.WritableSystemFS, changed []cpuPolicy, original error) error {
	var rollbackErrors []error
	for index := len(changed) - 1; index >= 0; index-- {
		policy := changed[index]
		path := filepath.Join(policy.path, "scaling_governor")
		if err := systemFS.WriteFile(path, []byte(policy.governor)); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %s to %q: %w", path, policy.governor, err))
		}
	}
	if len(rollbackErrors) == 0 {
		return original
	}
	return errors.Join(append([]error{original}, rollbackErrors...)...)
}

func rollbackSwappiness(systemFS sysfs.WritableSystemFS, previous string, original error) error {
	if err := systemFS.WriteFile(SwappinessPath, []byte(previous)); err != nil {
		return errors.Join(original, fmt.Errorf("restore %s to %q: %w", SwappinessPath, previous, err))
	}
	return original
}

func writeError(path string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("write %s: %w (root privileges are normally required)", path, err)
	}
	return fmt.Errorf("write %s: %w", path, err)
}

func contains(values []string, requested string) bool {
	for _, value := range values {
		if value == requested {
			return true
		}
	}
	return false
}
