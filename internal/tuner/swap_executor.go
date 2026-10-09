package tuner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	swapinfo "rtop/internal/swap"
	"rtop/internal/sysfs"
)

const swapEvacuationReserve = uint64(64 << 20)
const swapRollbackTimeout = 5 * time.Minute

type swapFileState struct {
	Exists bool
	Size   uint64
	Format swapFormat
}

type swapFormat struct {
	UUID  string
	Label string
}

type zramState struct {
	SizeBytes   uint64
	Algorithm   string
	MaxStreams  string
	MemoryLimit string
	Format      swapFormat
}

type swapRuntime interface {
	SwapOff(context.Context, string) error
	SwapOn(context.Context, string, int) error
	Format(context.Context, string, swapFormat) error
	FileState(string, bool) (swapFileState, error)
	ResizeFile(string, uint64, bool) error
	RemoveManagedFile(string) error
	ZRAMState(string) (zramState, error)
	ResizeZRAM(string, zramState, uint64) error
}

type swapRuntimePreflight interface {
	Preflight(SwapPlan) error
}

type runtimeSwapExecutor struct {
	fs      sysfs.SystemFS
	runtime swapRuntime
}

func newRuntimeSwapExecutor(systemFS sysfs.SystemFS, runtime swapRuntime) SwapPlanExecutor {
	return &runtimeSwapExecutor{fs: systemFS, runtime: runtime}
}

func (e *runtimeSwapExecutor) Apply(ctx context.Context, plan SwapPlan) (func(context.Context) error, error) {
	if preflight, ok := e.runtime.(swapRuntimePreflight); ok {
		if err := preflight.Preflight(plan); err != nil {
			return nil, err
		}
	}
	refreshed, err := refreshSwapPlan(e.fs, plan)
	if err != nil {
		return nil, err
	}
	plan = refreshed
	if err := preflightSwapEvacuation(e.fs, plan); err != nil {
		return nil, err
	}
	tx := &swapTransaction{runtime: e.runtime}
	// Bring replacement capacity online before taking fixed backends offline.
	// The temporary over-capacity is safer than creating a gap in available
	// Swap if allocation or formatting fails.
	if plan.Resize != nil {
		if err := tx.resize(ctx, *plan.Resize, plan.ResizeBytes); err != nil {
			return nil, tx.fail(ctx, err)
		}
	}
	if plan.ManagedBytes > 0 {
		if err := tx.createManaged(ctx, plan.ManagedBytes); err != nil {
			return nil, tx.fail(ctx, err)
		}
	}
	for _, entry := range swapoffOrder(plan.Disable) {
		if err := tx.disable(ctx, entry); err != nil {
			return nil, tx.fail(ctx, err)
		}
	}
	return tx.rollback, nil
}

// refreshSwapPlan closes the discovery-to-mutation race as far as userspace
// can: immediately before the first write, verify that the active topology is
// still the one that was planned and refresh volatile UsedBytes values.
func refreshSwapPlan(systemFS sysfs.SystemFS, plan SwapPlan) (SwapPlan, error) {
	current, err := swapinfo.Discover(systemFS)
	if err != nil {
		return SwapPlan{}, fmt.Errorf("recheck active swap before applying: %w", err)
	}
	expected := make(map[string]swapinfo.Entry)
	addExpected := func(entry swapinfo.Entry) error {
		if _, exists := expected[entry.Path]; exists {
			return fmt.Errorf("invalid swap plan: backend %s appears more than once", entry.Path)
		}
		expected[entry.Path] = entry
		return nil
	}
	for _, entry := range plan.Keep {
		if err := addExpected(entry); err != nil {
			return SwapPlan{}, err
		}
	}
	for _, entry := range plan.Disable {
		if err := addExpected(entry); err != nil {
			return SwapPlan{}, err
		}
	}
	if plan.Resize != nil {
		if err := addExpected(*plan.Resize); err != nil {
			return SwapPlan{}, err
		}
	}
	if len(current) != len(expected) {
		return SwapPlan{}, errors.New("active Swap backends changed while planning; retry the operation")
	}
	currentByPath := make(map[string]swapinfo.Entry, len(current))
	for _, entry := range current {
		planned, ok := expected[entry.Path]
		if !ok || entry.Kind != planned.Kind || entry.KernelType != planned.KernelType ||
			entry.SizeBytes != planned.SizeBytes || entry.Priority != planned.Priority {
			return SwapPlan{}, errors.New("active Swap backends changed while planning; retry the operation")
		}
		currentByPath[entry.Path] = entry
	}
	for index := range plan.Keep {
		plan.Keep[index] = currentByPath[plan.Keep[index].Path]
	}
	for index := range plan.Disable {
		plan.Disable[index] = currentByPath[plan.Disable[index].Path]
	}
	if plan.Resize != nil {
		entry := currentByPath[plan.Resize.Path]
		plan.Resize = &entry
	}
	return plan, nil
}

type swapTransaction struct {
	runtime  swapRuntime
	disabled []swapinfo.Entry
	resized  *resizedSwap
	managed  *managedSwap
}

type resizedSwap struct {
	entry     swapinfo.Entry
	file      *swapFileState
	zram      *zramState
	activated bool
}

type managedSwap struct {
	previous  swapFileState
	activated bool
}

func (tx *swapTransaction) disable(ctx context.Context, entry swapinfo.Entry) error {
	if err := tx.runtime.SwapOff(ctx, entry.Path); err != nil {
		return fmt.Errorf("disable swap %s: %w", entry.Path, err)
	}
	tx.disabled = append(tx.disabled, entry)
	return nil
}

func (tx *swapTransaction) resize(ctx context.Context, entry swapinfo.Entry, size uint64) error {
	state := &resizedSwap{entry: entry}
	switch entry.Kind {
	case model.SwapZRAM:
		current, err := tx.runtime.ZRAMState(entry.Path)
		if err != nil {
			return fmt.Errorf("inspect zram %s: %w", entry.Path, err)
		}
		state.zram = &current
	case model.SwapFile:
		current, err := tx.runtime.FileState(entry.Path, entry.Path == ManagedSwapPath)
		if err != nil {
			return fmt.Errorf("inspect swapfile %s: %w", entry.Path, err)
		}
		if !current.Exists {
			return fmt.Errorf("inspect swapfile %s: backing file does not exist", entry.Path)
		}
		state.file = &current
	default:
		return fmt.Errorf("swap backend %s (%s) cannot be resized directly", entry.Path, entry.Kind)
	}
	if err := tx.runtime.SwapOff(ctx, entry.Path); err != nil {
		return fmt.Errorf("disable swap %s: %w", entry.Path, err)
	}
	tx.resized = state

	var err error
	if state.zram != nil {
		err = tx.runtime.ResizeZRAM(entry.Path, *state.zram, size)
	} else {
		err = tx.runtime.ResizeFile(entry.Path, size, entry.Path == ManagedSwapPath)
	}
	if err != nil {
		return fmt.Errorf("resize swap %s to %s: %w", entry.Path, FormatSwapSize(size), err)
	}
	format := resizedSwapFormat(state)
	if err := tx.runtime.Format(ctx, entry.Path, format); err != nil {
		return fmt.Errorf("format swap %s: %w", entry.Path, err)
	}
	if err := tx.runtime.SwapOn(ctx, entry.Path, entry.Priority); err != nil {
		return fmt.Errorf("enable swap %s: %w", entry.Path, err)
	}
	state.activated = true
	return nil
}

func (tx *swapTransaction) createManaged(ctx context.Context, size uint64) error {
	previous, err := tx.runtime.FileState(ManagedSwapPath, true)
	if err != nil {
		return fmt.Errorf("inspect managed swapfile: %w", err)
	}
	tx.managed = &managedSwap{previous: previous}
	if err := tx.runtime.ResizeFile(ManagedSwapPath, size, true); err != nil {
		return fmt.Errorf("allocate managed swapfile: %w", err)
	}
	if err := tx.runtime.Format(ctx, ManagedSwapPath, previous.Format); err != nil {
		return fmt.Errorf("format managed swapfile: %w", err)
	}
	if err := tx.runtime.SwapOn(ctx, ManagedSwapPath, -1); err != nil {
		return fmt.Errorf("enable managed swapfile: %w", err)
	}
	tx.managed.activated = true
	return nil
}

func (tx *swapTransaction) fail(ctx context.Context, original error) error {
	if rollbackErr := tx.rollback(ctx); rollbackErr != nil {
		return errors.Join(original, fmt.Errorf("restore previous swap configuration: %w", rollbackErr))
	}
	return original
}

func (tx *swapTransaction) rollback(ctx context.Context) error {
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), swapRollbackTimeout)
	defer cancel()
	ctx = rollbackContext

	var rollbackErrors []error
	disabledRestored := true
	for index := len(tx.disabled) - 1; index >= 0; index-- {
		entry := tx.disabled[index]
		if err := tx.runtime.SwapOn(ctx, entry.Path, entry.Priority); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("reactivate swap %s: %w", entry.Path, err))
			disabledRestored = false
		}
	}
	// If an original backend cannot be reactivated, leave replacement
	// capacity online instead of making the degraded state even smaller.
	if !disabledRestored {
		return errors.Join(rollbackErrors...)
	}
	if tx.managed != nil {
		canRestore := true
		if tx.managed.activated {
			if err := tx.runtime.SwapOff(ctx, ManagedSwapPath); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("disable managed swapfile: %w", err))
				canRestore = false
			}
		}
		if canRestore && tx.managed.previous.Exists {
			if err := tx.runtime.ResizeFile(ManagedSwapPath, tx.managed.previous.Size, true); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore managed swapfile size: %w", err))
			} else if err := tx.runtime.Format(ctx, ManagedSwapPath, tx.managed.previous.Format); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("reformat restored managed swapfile: %w", err))
			}
		} else if canRestore && !tx.managed.previous.Exists {
			if err := tx.runtime.RemoveManagedFile(ManagedSwapPath); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove managed swapfile: %w", err))
			}
		}
	}
	if tx.resized != nil {
		state := tx.resized
		canRestore := true
		if state.activated {
			if err := tx.runtime.SwapOff(ctx, state.entry.Path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("disable resized swap %s: %w", state.entry.Path, err))
				canRestore = false
			}
		}
		var resizeErr error
		if !canRestore {
			resizeErr = errors.New("still active after swapoff failed")
		} else if state.zram != nil {
			resizeErr = tx.runtime.ResizeZRAM(state.entry.Path, *state.zram, state.zram.SizeBytes)
		} else if state.file != nil {
			resizeErr = tx.runtime.ResizeFile(state.entry.Path, state.file.Size, state.entry.Path == ManagedSwapPath)
		}
		if resizeErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore swap %s size: %w", state.entry.Path, resizeErr))
		} else if err := tx.runtime.Format(ctx, state.entry.Path, resizedSwapFormat(state)); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("reformat restored swap %s: %w", state.entry.Path, err))
		} else if err := tx.runtime.SwapOn(ctx, state.entry.Path, state.entry.Priority); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("reactivate restored swap %s: %w", state.entry.Path, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func resizedSwapFormat(state *resizedSwap) swapFormat {
	if state.file != nil {
		return state.file.Format
	}
	if state.zram != nil {
		return state.zram.Format
	}
	return swapFormat{}
}

func preflightSwapEvacuation(systemFS sysfs.SystemFS, plan SwapPlan) error {
	var evacuate uint64
	for _, entry := range plan.Disable {
		var ok bool
		evacuate, ok = addUint64(evacuate, entry.UsedBytes)
		if !ok {
			return errors.New("swap usage overflows uint64")
		}
	}
	if plan.Resize != nil {
		var ok bool
		evacuate, ok = addUint64(evacuate, plan.Resize.UsedBytes)
		if !ok {
			return errors.New("swap usage overflows uint64")
		}
	}
	if evacuate == 0 {
		return nil
	}
	available, err := readMemAvailable(systemFS)
	if err != nil {
		return fmt.Errorf("check memory before swapoff: %w", err)
	}
	if available < evacuate || available-evacuate < swapEvacuationReserve {
		return fmt.Errorf(
			"not enough available memory to move %s out of swap safely (MemAvailable %s; reserve %s)",
			FormatSwapSize(evacuate), FormatSwapSize(available), FormatSwapSize(swapEvacuationReserve),
		)
	}
	return nil
}

func addUint64(left, right uint64) (uint64, bool) {
	if right > math.MaxUint64-left {
		return 0, false
	}
	return left + right, true
}

func readMemAvailable(systemFS sysfs.SystemFS) (uint64, error) {
	data, err := systemFS.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "MemAvailable" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) < 1 {
			break
		}
		kiB, parseErr := strconv.ParseUint(fields[0], 10, 64)
		if parseErr != nil || kiB > ^uint64(0)/1024 {
			break
		}
		return kiB * 1024, nil
	}
	return 0, errors.New("MemAvailable is not exposed")
}

func swapoffOrder(entries []swapinfo.Entry) []swapinfo.Entry {
	result := append([]swapinfo.Entry(nil), entries...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UsedBytes == result[j].UsedBytes {
			return result[i].Priority < result[j].Priority
		}
		return result[i].UsedBytes < result[j].UsedBytes
	})
	return result
}

func zramName(path string) (string, error) {
	name := filepath.Base(filepath.Clean(path))
	if !strings.HasPrefix(name, "zram") || strings.TrimPrefix(name, "zram") == "" {
		return "", fmt.Errorf("%q is not a zram device", path)
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(name, "zram")); err != nil {
		return "", fmt.Errorf("%q is not a zram device", path)
	}
	return name, nil
}
