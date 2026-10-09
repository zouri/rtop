package tuner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"rtop/internal/model"
	swapinfo "rtop/internal/swap"
)

func TestSwapManagerResizesRuntimeDetectedZRAM(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/dev/zram0", Kind: model.SwapZRAM, KernelType: "partition",
		SizeBytes: 2 << 30, Priority: 100,
	})
	result, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != SwapResize || result.BeforeBytes != 2<<30 || result.AfterBytes != 3<<30 {
		t.Fatalf("result=%#v", result)
	}
	assertFakeSwap(t, runtime, "/dev/zram0", true, 3<<30)
	if runtime.procReads != 3 {
		t.Fatalf("/proc/swaps reads=%d, want planning, pre-write, and post-apply reads", runtime.procReads)
	}
	assertOperations(t, runtime.operations,
		"zram-state /dev/zram0",
		"swapoff /dev/zram0",
		"resize-zram /dev/zram0",
		"format /dev/zram0",
		"swapon /dev/zram0",
	)
}

func TestSwapManagerResizesRuntimeDetectedSwapfile(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/swapfile", Kind: model.SwapFile, KernelType: "file",
		SizeBytes: 2 << 30, Priority: -2,
	})
	result, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != SwapResize {
		t.Fatalf("strategy=%q", result.Strategy)
	}
	assertFakeSwap(t, runtime, "/swapfile", true, 3<<30)
	assertOperations(t, runtime.operations,
		"file-state /swapfile",
		"swapoff /swapfile",
		"resize-file /swapfile",
		"format /swapfile",
		"swapon /swapfile",
	)
}

func TestSwapManagerKeepsBlockBackendAndFillsWithManagedSwapfile(t *testing.T) {
	runtime := newFakeSwapRuntime(
		swapinfo.Entry{Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 2 << 30, Priority: 2},
		swapinfo.Entry{Path: "/dev/block1", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 4 << 30, Priority: 1},
	)
	result, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != SwapManaged || result.AfterBytes != 3<<30 {
		t.Fatalf("result=%#v", result)
	}
	assertFakeSwap(t, runtime, "/dev/block0", true, 2<<30)
	assertFakeSwap(t, runtime, "/dev/block1", false, 4<<30)
	assertFakeSwap(t, runtime, ManagedSwapPath, true, 1<<30)
	assertOperations(t, runtime.operations,
		"file-state "+ManagedSwapPath,
		"resize-file "+ManagedSwapPath,
		"format "+ManagedSwapPath,
		"swapon "+ManagedSwapPath,
		"swapoff /dev/block1",
	)
}

func TestSwapResizeFailureRestoresOriginalBackend(t *testing.T) {
	for _, failedOperation := range []string{"format /dev/zram0", "swapon /dev/zram0"} {
		t.Run(strings.Fields(failedOperation)[0], func(t *testing.T) {
			runtime := newFakeSwapRuntime(swapinfo.Entry{
				Path: "/dev/zram0", Kind: model.SwapZRAM, KernelType: "partition",
				SizeBytes: 2 << 30, Priority: 100,
			})
			runtime.failures[failedOperation] = 1
			_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
			if err == nil || !strings.Contains(err.Error(), strings.Fields(failedOperation)[0]) {
				t.Fatalf("error=%v", err)
			}
			assertFakeSwap(t, runtime, "/dev/zram0", true, 2<<30)
			if countOperation(runtime.operations, "resize-zram /dev/zram0") != 2 {
				t.Fatalf("operations=%v; old zram size was not restored", runtime.operations)
			}
		})
	}
}

func TestSwapVerificationMismatchRollsBack(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/dev/zram0", Kind: model.SwapZRAM, KernelType: "partition",
		SizeBytes: 2 << 30, Priority: 100,
	})
	runtime.mismatchProcRead = 3
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err == nil || !strings.Contains(err.Error(), "verify swap capacity") {
		t.Fatalf("error=%v", err)
	}
	assertFakeSwap(t, runtime, "/dev/zram0", true, 2<<30)
}

func TestManagedPlanRollbackReactivatesBlocksBeforeRemovingReplacement(t *testing.T) {
	runtime := newFakeSwapRuntime(
		swapinfo.Entry{Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 2 << 30, Priority: 2},
		swapinfo.Entry{Path: "/dev/block1", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 4 << 30, Priority: 1},
	)
	runtime.mismatchProcRead = 3
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err == nil || !strings.Contains(err.Error(), "verify swap capacity") {
		t.Fatalf("error=%v", err)
	}
	assertFakeSwap(t, runtime, "/dev/block0", true, 2<<30)
	assertFakeSwap(t, runtime, "/dev/block1", true, 4<<30)
	if runtime.backends[ManagedSwapPath] != nil {
		t.Fatalf("managed replacement still exists after rollback: %#v", runtime.backends[ManagedSwapPath])
	}
	assertOperations(t, runtime.operations,
		"file-state "+ManagedSwapPath,
		"resize-file "+ManagedSwapPath,
		"format "+ManagedSwapPath,
		"swapon "+ManagedSwapPath,
		"swapoff /dev/block1",
		"swapon /dev/block1",
		"swapoff "+ManagedSwapPath,
		"remove-file "+ManagedSwapPath,
	)
}

func TestManagedReplacementFailureLeavesBlocksOnline(t *testing.T) {
	runtime := newFakeSwapRuntime(
		swapinfo.Entry{Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 2 << 30, Priority: 2},
		swapinfo.Entry{Path: "/dev/block1", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 4 << 30, Priority: 1},
	)
	runtime.failures["format "+ManagedSwapPath] = 1
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err == nil || !strings.Contains(err.Error(), "format managed swapfile") {
		t.Fatalf("error=%v", err)
	}
	assertFakeSwap(t, runtime, "/dev/block0", true, 2<<30)
	assertFakeSwap(t, runtime, "/dev/block1", true, 4<<30)
	if runtime.backends[ManagedSwapPath] != nil {
		t.Fatalf("partial managed file remains: %#v", runtime.backends[ManagedSwapPath])
	}
	if countOperation(runtime.operations, "swapoff /dev/block1") != 0 {
		t.Fatalf("block was disabled before replacement succeeded: %v", runtime.operations)
	}
}

func TestRollbackKeepsReplacementWhenBlockReactivationFails(t *testing.T) {
	runtime := newFakeSwapRuntime(
		swapinfo.Entry{Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 2 << 30, Priority: 2},
		swapinfo.Entry{Path: "/dev/block1", Kind: model.SwapBlock, KernelType: "partition", SizeBytes: 4 << 30, Priority: 1},
	)
	entries, err := swapinfo.Discover(runtime)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSwapPlan(entries, 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := newRuntimeSwapExecutor(runtime, runtime).Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	runtime.failures["swapon /dev/block1"] = 1
	if err := rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "reactivate swap") {
		t.Fatalf("rollback error=%v", err)
	}
	assertFakeSwap(t, runtime, "/dev/block1", false, 4<<30)
	assertFakeSwap(t, runtime, ManagedSwapPath, true, 1<<30)
}

func TestSwapPreflightRejectsInsufficientMemoryWithoutMutation(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition",
		SizeBytes: 1 << 30, UsedBytes: 128 << 20, Priority: -2,
	})
	runtime.memAvailable = (128 << 20) + swapEvacuationReserve - 1024
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "not enough available memory") {
		t.Fatalf("error=%v", err)
	}
	if len(runtime.operations) != 0 {
		t.Fatalf("runtime operations occurred before failed preflight: %v", runtime.operations)
	}
	assertFakeSwap(t, runtime, "/dev/block0", true, 1<<30)
}

func TestSwapTopologyChangeBeforeWriteIsRejectedWithoutMutation(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/dev/zram0", Kind: model.SwapZRAM, KernelType: "partition",
		SizeBytes: 2 << 30, Priority: 100,
	})
	runtime.mismatchProcRead = 2
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 3<<30)
	if err == nil || !strings.Contains(err.Error(), "changed while planning") {
		t.Fatalf("error=%v", err)
	}
	if len(runtime.operations) != 0 {
		t.Fatalf("operations=%v", runtime.operations)
	}
	assertFakeSwap(t, runtime, "/dev/zram0", true, 2<<30)
}

func TestSwapPreflightUsesFreshUsageFromSecondDiscovery(t *testing.T) {
	runtime := newFakeSwapRuntime(swapinfo.Entry{
		Path: "/dev/block0", Kind: model.SwapBlock, KernelType: "partition",
		SizeBytes: 1 << 30, Priority: -2,
	})
	runtime.usedOnProcRead = 2
	runtime.usedOnProcBytes = 128 << 20
	runtime.memAvailable = (128 << 20) + swapEvacuationReserve - 1024
	_, err := newFakeSwapManager(runtime).ResizeTotal(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "not enough available memory") {
		t.Fatalf("error=%v", err)
	}
	if len(runtime.operations) != 0 {
		t.Fatalf("operations=%v", runtime.operations)
	}
}

func TestManagedSwapfileRollbackRestoresAndReformatsPreviousFile(t *testing.T) {
	runtime := newFakeSwapRuntime()
	runtime.addInactiveFile(ManagedSwapPath, 128<<20)
	executor := newRuntimeSwapExecutor(runtime, runtime)
	rollback, err := executor.Apply(context.Background(), SwapPlan{
		Strategy: SwapManaged, TargetBytes: 1 << 30, ManagedBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertFakeSwap(t, runtime, ManagedSwapPath, false, 128<<20)
	if countOperation(runtime.operations, "format "+ManagedSwapPath) != 2 {
		t.Fatalf("operations=%v; restored managed file was not reformatted", runtime.operations)
	}
}

func newFakeSwapManager(runtime *fakeSwapRuntime) *SwapManager {
	return NewSwapManager(runtime, newRuntimeSwapExecutor(runtime, runtime))
}

type fakeSwapBackend struct {
	entry  swapinfo.Entry
	active bool
	file   swapFileState
	zram   zramState
}

type fakeSwapRuntime struct {
	backends         map[string]*fakeSwapBackend
	order            []string
	operations       []string
	failures         map[string]int
	procReads        int
	mismatchProcRead int
	usedOnProcRead   int
	usedOnProcBytes  uint64
	memAvailable     uint64
}

func newFakeSwapRuntime(entries ...swapinfo.Entry) *fakeSwapRuntime {
	runtime := &fakeSwapRuntime{
		backends: make(map[string]*fakeSwapBackend), failures: make(map[string]int),
		memAvailable: 16 << 30,
	}
	for _, entry := range entries {
		backend := &fakeSwapBackend{entry: entry, active: true}
		switch entry.Kind {
		case model.SwapFile:
			backend.file = swapFileState{Exists: true, Size: entry.SizeBytes}
		case model.SwapZRAM:
			backend.zram = zramState{SizeBytes: entry.SizeBytes, Algorithm: "zstd", MaxStreams: "4"}
		}
		runtime.backends[entry.Path] = backend
		runtime.order = append(runtime.order, entry.Path)
	}
	return runtime
}

func (f *fakeSwapRuntime) addInactiveFile(path string, size uint64) {
	f.backends[path] = &fakeSwapBackend{
		entry: swapinfo.Entry{Path: path, Kind: model.SwapFile, KernelType: "file", SizeBytes: size, Priority: -1},
		file:  swapFileState{Exists: true, Size: size},
	}
	f.order = append(f.order, path)
}

func (f *fakeSwapRuntime) ReadFile(path string) ([]byte, error) {
	switch path {
	case swapinfo.ProcSwapsPath:
		f.procReads++
		var builder strings.Builder
		builder.WriteString("Filename Type Size Used Priority\n")
		for _, path := range f.order {
			backend := f.backends[path]
			if backend == nil || !backend.active {
				continue
			}
			size := backend.entry.SizeBytes
			used := backend.entry.UsedBytes
			if f.procReads == f.mismatchProcRead {
				size += 8 << 20
			}
			if f.procReads == f.usedOnProcRead {
				used = f.usedOnProcBytes
			}
			fmt.Fprintf(&builder, "%s %s %d %d %d\n",
				backend.entry.Path, backend.entry.KernelType, size/1024,
				used/1024, backend.entry.Priority,
			)
		}
		return []byte(builder.String()), nil
	case "/proc/meminfo":
		return []byte("MemAvailable: " + strconv.FormatUint(f.memAvailable/1024, 10) + " kB\n"), nil
	}
	name := filepath.Base(filepath.Dir(path))
	if strings.HasPrefix(path, "/sys/block/zram") && filepath.Base(path) == "disksize" {
		backend := f.backends["/dev/"+name]
		if backend != nil {
			return []byte(strconv.FormatUint(backend.zram.SizeBytes, 10)), nil
		}
	}
	return nil, os.ErrNotExist
}

func (f *fakeSwapRuntime) ReadDir(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist }
func (f *fakeSwapRuntime) Readlink(string) (string, error)       { return "", os.ErrNotExist }
func (f *fakeSwapRuntime) Glob(string) ([]string, error)         { return nil, nil }

func (f *fakeSwapRuntime) SwapOff(_ context.Context, path string) error {
	if err := f.operation("swapoff", path); err != nil {
		return err
	}
	backend := f.backends[path]
	if backend == nil || !backend.active {
		return errors.New("swap is not active")
	}
	backend.active = false
	return nil
}

func (f *fakeSwapRuntime) SwapOn(_ context.Context, path string, priority int) error {
	if err := f.operation("swapon", path); err != nil {
		return err
	}
	backend := f.backends[path]
	if backend == nil {
		return errors.New("swap backend does not exist")
	}
	backend.active = true
	backend.entry.Priority = priority
	backend.entry.UsedBytes = 0
	switch backend.entry.Kind {
	case model.SwapFile:
		backend.entry.SizeBytes = backend.file.Size
	case model.SwapZRAM:
		backend.entry.SizeBytes = backend.zram.SizeBytes
	}
	return nil
}

func (f *fakeSwapRuntime) Format(_ context.Context, path string, _ swapFormat) error {
	return f.operation("format", path)
}

func (f *fakeSwapRuntime) FileState(path string, _ bool) (swapFileState, error) {
	if err := f.operation("file-state", path); err != nil {
		return swapFileState{}, err
	}
	backend := f.backends[path]
	if backend == nil {
		return swapFileState{}, nil
	}
	return backend.file, nil
}

func (f *fakeSwapRuntime) ResizeFile(path string, size uint64, _ bool) error {
	if err := f.operation("resize-file", path); err != nil {
		return err
	}
	backend := f.backends[path]
	if backend == nil {
		backend = &fakeSwapBackend{entry: swapinfo.Entry{
			Path: path, Kind: model.SwapFile, KernelType: "file", Priority: -1,
		}}
		f.backends[path] = backend
		f.order = append(f.order, path)
	}
	if backend.active {
		return errors.New("cannot resize active swap")
	}
	backend.file = swapFileState{Exists: true, Size: size}
	backend.entry.SizeBytes = size
	return nil
}

func (f *fakeSwapRuntime) RemoveManagedFile(path string) error {
	if err := f.operation("remove-file", path); err != nil {
		return err
	}
	delete(f.backends, path)
	return nil
}

func (f *fakeSwapRuntime) ZRAMState(path string) (zramState, error) {
	if err := f.operation("zram-state", path); err != nil {
		return zramState{}, err
	}
	backend := f.backends[path]
	if backend == nil {
		return zramState{}, os.ErrNotExist
	}
	return backend.zram, nil
}

func (f *fakeSwapRuntime) ResizeZRAM(path string, settings zramState, size uint64) error {
	if err := f.operation("resize-zram", path); err != nil {
		return err
	}
	backend := f.backends[path]
	if backend == nil {
		return os.ErrNotExist
	}
	if backend.active {
		return errors.New("cannot resize active swap")
	}
	settings.SizeBytes = size
	backend.zram = settings
	backend.entry.SizeBytes = size
	return nil
}

func (f *fakeSwapRuntime) operation(name, path string) error {
	operation := name + " " + path
	f.operations = append(f.operations, operation)
	if f.failures[operation] > 0 {
		f.failures[operation]--
		return errors.New("injected " + name + " failure")
	}
	return nil
}

func assertFakeSwap(t *testing.T, runtime *fakeSwapRuntime, path string, active bool, size uint64) {
	t.Helper()
	backend := runtime.backends[path]
	if backend == nil {
		t.Fatalf("backend %s does not exist", path)
	}
	if backend.active != active || backend.entry.SizeBytes != size {
		t.Fatalf("backend %s active=%v size=%d, want active=%v size=%d",
			path, backend.active, backend.entry.SizeBytes, active, size)
	}
}

func assertOperations(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("operations:\n%s\nwant:\n%s", strings.Join(actual, "\n"), strings.Join(expected, "\n"))
	}
}

func countOperation(operations []string, requested string) int {
	count := 0
	for _, operation := range operations {
		if operation == requested {
			count++
		}
	}
	return count
}
