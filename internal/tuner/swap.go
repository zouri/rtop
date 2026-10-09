package tuner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"rtop/internal/model"
	swapinfo "rtop/internal/swap"
	"rtop/internal/sysfs"
)

const (
	ManagedSwapPath = "/var/lib/rtop/swapfile"
	SwapSizeMinimum = uint64(1 << 20)
	swapTolerance   = uint64(1 << 20)
)

type SwapStrategy string

const (
	SwapNoChange SwapStrategy = "no_change"
	SwapResize   SwapStrategy = "resize_backend"
	SwapManaged  SwapStrategy = "managed_swapfile"
	SwapDisable  SwapStrategy = "disable_swap"
)

type SwapPlan struct {
	Strategy     SwapStrategy
	BeforeBytes  uint64
	TargetBytes  uint64
	Resize       *swapinfo.Entry
	ResizeBytes  uint64
	ManagedBytes uint64
	Keep         []swapinfo.Entry
	Disable      []swapinfo.Entry
}

type SwapResizeResult struct {
	Strategy    SwapStrategy
	BeforeBytes uint64
	AfterBytes  uint64
	Devices     []swapinfo.Entry
}

// SwapPlanExecutor applies one already validated plan. On success it returns a
// rollback function that restores the pre-plan runtime configuration. On
// failure it must restore any partial changes before returning.
type SwapPlanExecutor interface {
	Apply(context.Context, SwapPlan) (func(context.Context) error, error)
}

type SwapManager struct {
	fs       sysfs.SystemFS
	executor SwapPlanExecutor
	mu       sync.Mutex
}

func NewSwapManager(systemFS sysfs.SystemFS, executor SwapPlanExecutor) *SwapManager {
	return &SwapManager{fs: systemFS, executor: executor}
}

// ResizeTotal always re-reads /proc/swaps before planning and after applying.
// No device type or capacity discovered at process startup is trusted here.
func (m *SwapManager) ResizeTotal(ctx context.Context, targetBytes uint64) (SwapResizeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	before, err := swapinfo.Discover(m.fs)
	if err != nil {
		return SwapResizeResult{}, fmt.Errorf("discover active swap: %w", err)
	}
	plan, err := BuildSwapPlan(before, targetBytes)
	if err != nil {
		return SwapResizeResult{}, err
	}
	if plan.Strategy == SwapNoChange {
		return SwapResizeResult{
			Strategy: SwapNoChange, BeforeBytes: plan.BeforeBytes,
			AfterBytes: plan.BeforeBytes, Devices: before,
		}, nil
	}
	if m.executor == nil {
		return SwapResizeResult{}, errors.New("swap capacity control is unavailable in this session")
	}
	rollback, err := m.executor.Apply(ctx, plan)
	if err != nil {
		if restoreErr := verifySwapRestored(m.fs, before); restoreErr != nil {
			return SwapResizeResult{}, errors.Join(err, restoreErr)
		}
		return SwapResizeResult{}, err
	}
	after, verifyErr := swapinfo.Discover(m.fs)
	var afterBytes uint64
	if verifyErr == nil {
		afterBytes, verifyErr = swapinfo.TotalBytes(after)
	}
	if verifyErr == nil && swapSizeMatches(afterBytes, targetBytes) {
		return SwapResizeResult{
			Strategy: plan.Strategy, BeforeBytes: plan.BeforeBytes,
			AfterBytes: afterBytes, Devices: after,
		}, nil
	}
	if verifyErr == nil {
		verifyErr = fmt.Errorf(
			"verify swap capacity: kernel reports %s, requested %s",
			FormatSwapSize(afterBytes), FormatSwapSize(targetBytes),
		)
	} else {
		verifyErr = fmt.Errorf("verify active swap: %w", verifyErr)
	}
	if rollback == nil {
		return SwapResizeResult{}, verifyErr
	}
	rollbackErr := rollback(ctx)
	restoreErr := verifySwapRestored(m.fs, before)
	if rollbackErr != nil || restoreErr != nil {
		var errorsToJoin []error
		errorsToJoin = append(errorsToJoin, verifyErr)
		if rollbackErr != nil {
			errorsToJoin = append(errorsToJoin, fmt.Errorf("restore previous swap configuration: %w", rollbackErr))
		}
		if restoreErr != nil {
			errorsToJoin = append(errorsToJoin, restoreErr)
		}
		return SwapResizeResult{}, errors.Join(errorsToJoin...)
	}
	return SwapResizeResult{}, verifyErr
}

func verifySwapRestored(systemFS sysfs.SystemFS, expected []swapinfo.Entry) error {
	actual, err := swapinfo.Discover(systemFS)
	if err != nil {
		return fmt.Errorf("verify previous swap configuration after rollback: %w", err)
	}
	if sameSwapTopology(expected, actual) {
		return nil
	}
	return errors.New("verify previous swap configuration after rollback: active backends differ from their pre-operation state")
}

func sameSwapTopology(expected, actual []swapinfo.Entry) bool {
	if len(expected) != len(actual) {
		return false
	}
	byPath := make(map[string]swapinfo.Entry, len(expected))
	for _, entry := range expected {
		if _, duplicate := byPath[entry.Path]; duplicate {
			return false
		}
		byPath[entry.Path] = entry
	}
	for _, entry := range actual {
		original, ok := byPath[entry.Path]
		if !ok || entry.Kind != original.Kind || entry.KernelType != original.KernelType ||
			!swapSizeMatches(entry.SizeBytes, original.SizeBytes) || entry.Priority != original.Priority {
			return false
		}
		delete(byPath, entry.Path)
	}
	return len(byPath) == 0
}

func BuildSwapPlan(entries []swapinfo.Entry, targetBytes uint64) (SwapPlan, error) {
	if targetBytes >= uint64(1)<<63 {
		return SwapPlan{}, errors.New("swap size is too large")
	}
	if targetBytes != 0 && targetBytes < SwapSizeMinimum {
		return SwapPlan{}, fmt.Errorf("swap size must be 0 or at least %s", FormatSwapSize(SwapSizeMinimum))
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Path == "" {
			return SwapPlan{}, errors.New("active swap backend has an empty path")
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return SwapPlan{}, fmt.Errorf("active swap backend %s appears more than once", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if entry.UsedBytes > entry.SizeBytes {
			return SwapPlan{}, fmt.Errorf("active swap backend %s reports used capacity larger than its size", entry.Path)
		}
	}
	current, err := swapinfo.TotalBytes(entries)
	if err != nil {
		return SwapPlan{}, err
	}
	plan := SwapPlan{BeforeBytes: current, TargetBytes: targetBytes}
	if swapSizeMatches(current, targetBytes) {
		plan.Strategy = SwapNoChange
		return plan, nil
	}
	if targetBytes == 0 {
		plan.Strategy = SwapDisable
		plan.Disable = append([]swapinfo.Entry(nil), entries...)
		return plan, nil
	}

	// The least disruptive plan resizes one existing zram/file backend while
	// every other active backend remains online.
	for _, candidate := range resizableCandidates(entries) {
		others := current - candidate.SizeBytes
		if swapSizeMatches(others, targetBytes) {
			plan.Strategy = SwapDisable
			for _, entry := range entries {
				if entry.Path == candidate.Path {
					plan.Disable = append(plan.Disable, entry)
				} else {
					plan.Keep = append(plan.Keep, entry)
				}
			}
			return plan, nil
		}
		if targetBytes < others {
			continue
		}
		resizeBytes := targetBytes - others
		if resizeBytes < SwapSizeMinimum {
			plan.Strategy = SwapDisable
			for _, entry := range entries {
				if entry.Path == candidate.Path {
					plan.Disable = append(plan.Disable, entry)
				} else {
					plan.Keep = append(plan.Keep, entry)
				}
			}
			return plan, nil
		}
		copy := candidate
		plan.Strategy = SwapResize
		plan.Resize = &copy
		plan.ResizeBytes = resizeBytes
		for _, entry := range entries {
			if entry.Path != candidate.Path {
				plan.Keep = append(plan.Keep, entry)
			}
		}
		return plan, nil
	}

	if targetBytes > current {
		plan.Strategy = SwapManaged
		plan.Keep = append([]swapinfo.Entry(nil), entries...)
		plan.ManagedBytes = targetBytes - current
		return plan, nil
	}

	// Fixed-size block swaps cannot be repartitioned safely. For a smaller
	// target, retain as many high-priority block backends as fit, disable the
	// remainder, and fill an exact residual with one resizable backend or the
	// rtop-owned swapfile.
	fixed := make([]swapinfo.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == model.SwapBlock {
			fixed = append(fixed, entry)
		}
	}
	sort.SliceStable(fixed, func(i, j int) bool {
		if fixed[i].Priority == fixed[j].Priority {
			if fixed[i].SizeBytes == fixed[j].SizeBytes {
				return fixed[i].Path < fixed[j].Path
			}
			return fixed[i].SizeBytes < fixed[j].SizeBytes
		}
		return fixed[i].Priority > fixed[j].Priority
	})
	selected, keptBytes := bestFixedSubset(fixed, targetBytes)
	kept := make(map[string]struct{})
	for _, entry := range selected {
		plan.Keep = append(plan.Keep, entry)
		kept[entry.Path] = struct{}{}
	}
	remaining := targetBytes - keptBytes
	candidates := resizableCandidates(entries)
	if remaining > 0 && remaining < SwapSizeMinimum {
		// /proc/swaps reports usable capacity, normally one header page less
		// than the backing store. Do not create an invalid sub-MiB backend
		// merely to compensate for that reporting difference.
		plan.Strategy = SwapDisable
	} else if remaining > 0 && len(candidates) > 0 {
		exact := -1
		for index, candidate := range candidates {
			if swapSizeMatches(candidate.SizeBytes, remaining) {
				exact = index
				break
			}
		}
		if exact >= 0 {
			candidate := candidates[exact]
			plan.Keep = append(plan.Keep, candidate)
			kept[candidate.Path] = struct{}{}
			plan.Strategy = SwapDisable
		} else {
			candidate := candidates[0]
			copy := candidate
			plan.Resize = &copy
			plan.ResizeBytes = remaining
			kept[candidate.Path] = struct{}{}
			plan.Strategy = SwapResize
		}
	} else if remaining > 0 {
		plan.ManagedBytes = remaining
		plan.Strategy = SwapManaged
	} else {
		plan.Strategy = SwapDisable
	}
	for _, entry := range entries {
		if _, ok := kept[entry.Path]; !ok {
			plan.Disable = append(plan.Disable, entry)
		}
	}
	return plan, nil
}

func ParseSwapSize(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("swap size is required")
	}
	index := 0
	for index < len(value) && ((value[index] >= '0' && value[index] <= '9') || value[index] == '.') {
		index++
	}
	numberText := value[:index]
	unit := strings.ToUpper(strings.TrimSpace(value[index:]))
	if numberText == "" {
		return 0, fmt.Errorf("invalid swap size %q", value)
	}
	number, err := strconv.ParseFloat(numberText, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fmt.Errorf("invalid swap size %q", value)
	}
	multipliers := map[string]float64{
		"": 1 << 20, "B": 1,
		"K": 1 << 10, "KB": 1 << 10, "KIB": 1 << 10,
		"M": 1 << 20, "MB": 1 << 20, "MIB": 1 << 20,
		"G": 1 << 30, "GB": 1 << 30, "GIB": 1 << 30,
		"T": 1 << 40, "TB": 1 << 40, "TIB": 1 << 40,
	}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("unsupported swap size unit %q", unit)
	}
	bytes := number * multiplier
	if bytes >= float64(uint64(1)<<63) {
		return 0, errors.New("swap size is too large")
	}
	result := uint64(math.Round(bytes))
	if number != 0 && result < SwapSizeMinimum {
		return 0, fmt.Errorf("swap size must be 0 or at least %s", FormatSwapSize(SwapSizeMinimum))
	}
	return result, nil
}

func FormatSwapSize(bytes uint64) string {
	const (
		kiB = uint64(1 << 10)
		miB = uint64(1 << 20)
		giB = uint64(1 << 30)
		tiB = uint64(1 << 40)
	)
	unit, divisor := "B", uint64(1)
	switch {
	case bytes >= tiB:
		unit, divisor = "TiB", tiB
	case bytes >= giB:
		unit, divisor = "GiB", giB
	case bytes >= miB:
		unit, divisor = "MiB", miB
	case bytes >= kiB:
		unit, divisor = "KiB", kiB
	}
	value := float64(bytes) / float64(divisor)
	precision := 0
	if bytes%divisor != 0 {
		precision = 2
	}
	return strconv.FormatFloat(value, 'f', precision, 64) + unit
}

func resizableCandidates(entries []swapinfo.Entry) []swapinfo.Entry {
	result := make([]swapinfo.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == model.SwapZRAM || entry.Kind == model.SwapFile {
			result = append(result, entry)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := candidateRank(result[i]), candidateRank(result[j])
		if left == right {
			return result[i].Priority > result[j].Priority
		}
		return left < right
	})
	return result
}

func candidateRank(entry swapinfo.Entry) int {
	if entry.Path == ManagedSwapPath {
		return 0
	}
	if entry.Kind == model.SwapZRAM {
		return 1
	}
	return 2
}

type subsetOption struct {
	sum  uint64
	used uint64
	mask uint64
}

// bestFixedSubset finds the largest total of fixed-size backends that does not
// exceed the requested capacity. Linux exposes at most a small number of
// active swap areas, so a meet-in-the-middle search avoids the incorrect
// choices a simple greedy pass can make without tying work to target bytes.
func bestFixedSubset(entries []swapinfo.Entry, target uint64) ([]swapinfo.Entry, uint64) {
	if len(entries) == 0 || target == 0 {
		return nil, 0
	}
	if len(entries) > 40 {
		// This is above Linux's normal active-swap limit. Keep behavior bounded
		// for synthetic inputs while still producing a safe plan.
		return greedyFixedSubset(entries, target)
	}

	middle := len(entries) / 2
	left := enumerateSubsets(entries[:middle], target)
	right := enumerateSubsets(entries[middle:], target)
	sort.Slice(right, func(i, j int) bool {
		if right[i].sum == right[j].sum {
			if right[i].used != right[j].used {
				return right[i].used > right[j].used
			}
			return preferredMask(right[i].mask, right[j].mask, len(entries)-middle)
		}
		return right[i].sum < right[j].sum
	})
	// Retain the preferred mask for each duplicate sum.
	uniqueRight := right[:0]
	for _, option := range right {
		if len(uniqueRight) == 0 || uniqueRight[len(uniqueRight)-1].sum != option.sum {
			uniqueRight = append(uniqueRight, option)
		}
	}
	right = uniqueRight

	var bestSum, bestUsed, bestMask uint64
	for _, leftOption := range left {
		remaining := target - leftOption.sum
		index := sort.Search(len(right), func(index int) bool { return right[index].sum > remaining }) - 1
		if index < 0 {
			continue
		}
		sum := leftOption.sum + right[index].sum
		used := leftOption.used + right[index].used
		mask := leftOption.mask | right[index].mask<<middle
		if sum > bestSum || sum == bestSum && (used > bestUsed ||
			used == bestUsed && preferredMask(mask, bestMask, len(entries))) {
			bestSum, bestUsed, bestMask = sum, used, mask
		}
	}

	selected := make([]swapinfo.Entry, 0, len(entries))
	for index, entry := range entries {
		if bestMask&(uint64(1)<<index) != 0 {
			selected = append(selected, entry)
		}
	}
	return selected, bestSum
}

func enumerateSubsets(entries []swapinfo.Entry, target uint64) []subsetOption {
	options := []subsetOption{{}}
	for index, entry := range entries {
		count := len(options)
		for optionIndex := 0; optionIndex < count; optionIndex++ {
			option := options[optionIndex]
			if entry.SizeBytes > target-option.sum {
				continue
			}
			options = append(options, subsetOption{
				sum: option.sum + entry.SizeBytes, used: option.used + entry.UsedBytes,
				mask: option.mask | uint64(1)<<index,
			})
		}
	}
	return options
}

func preferredMask(candidate, current uint64, count int) bool {
	for index := 0; index < count; index++ {
		candidateHas := candidate&(uint64(1)<<index) != 0
		currentHas := current&(uint64(1)<<index) != 0
		if candidateHas != currentHas {
			return candidateHas
		}
	}
	return false
}

func greedyFixedSubset(entries []swapinfo.Entry, target uint64) ([]swapinfo.Entry, uint64) {
	var total uint64
	selected := make([]swapinfo.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.SizeBytes <= target-total {
			selected = append(selected, entry)
			total += entry.SizeBytes
		}
	}
	return selected, total
}

func swapSizeMatches(actual, requested uint64) bool {
	if actual == requested {
		return true
	}
	if requested == 0 || actual == 0 {
		return false
	}
	if actual > requested {
		return actual-requested < swapTolerance
	}
	return requested-actual < swapTolerance
}
