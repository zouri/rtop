//go:build linux

package tuner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	swapinfo "rtop/internal/swap"
	"rtop/internal/sysfs"
)

const managedSwapMarker = ManagedSwapPath + ".rtop-managed"

type linuxSwapRuntime struct {
	fs   sysfs.WritableSystemFS
	live bool
}

func NewLinuxSwapExecutor(root string, systemFS sysfs.WritableSystemFS) SwapPlanExecutor {
	return newRuntimeSwapExecutor(systemFS, &linuxSwapRuntime{
		fs: systemFS, live: filepath.Clean(root) == string(filepath.Separator),
	})
}

func (r *linuxSwapRuntime) Preflight(plan SwapPlan) error {
	if !r.live {
		return errors.New("swap capacity changes require --root=/ on the live system")
	}
	required := []string{"swapoff", "swapon"}
	if plan.Resize != nil || plan.ManagedBytes > 0 {
		required = append(required, "mkswap")
	}
	for _, name := range required {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required command %s is unavailable: %w", name, err)
		}
	}
	return nil
}

func (r *linuxSwapRuntime) SwapOff(ctx context.Context, path string) error {
	err := r.run(ctx, "swapoff", path)
	if err == nil || !r.live {
		return err
	}
	if _, active, inspectErr := r.activeSwap(path); inspectErr == nil && !active {
		// The kernel transition may have completed just before the command was
		// canceled or otherwise returned a non-zero status.
		return nil
	}
	return err
}

func (r *linuxSwapRuntime) SwapOn(ctx context.Context, path string, priority int) error {
	args := make([]string, 0, 3)
	if priority >= -1 {
		args = append(args, "-p", strconv.Itoa(priority))
	}
	args = append(args, path)
	err := r.run(ctx, "swapon", args...)
	if err == nil || !r.live {
		return err
	}
	if _, active, inspectErr := r.activeSwap(path); inspectErr == nil && active {
		return nil
	}
	return err
}

func (r *linuxSwapRuntime) activeSwap(path string) (swapinfo.Entry, bool, error) {
	entries, err := swapinfo.Discover(r.fs)
	if err != nil {
		return swapinfo.Entry{}, false, err
	}
	for _, entry := range entries {
		if entry.Path == path {
			return entry, true, nil
		}
	}
	return swapinfo.Entry{}, false, nil
}

func (r *linuxSwapRuntime) Format(ctx context.Context, path string, format swapFormat) error {
	if r.live {
		if _, active, err := r.activeSwap(path); err != nil {
			return fmt.Errorf("verify %s is inactive before mkswap: %w", path, err)
		} else if active {
			return fmt.Errorf("refuse to format active swap backend %s", path)
		}
	}
	args := make([]string, 0, 5)
	if format.UUID != "" {
		args = append(args, "-U", format.UUID)
	}
	if format.Label != "" {
		args = append(args, "-L", format.Label)
	}
	args = append(args, path)
	return r.run(ctx, "mkswap", args...)
}

func (r *linuxSwapRuntime) run(ctx context.Context, name string, args ...string) error {
	if !r.live {
		return errors.New("swap capacity changes require --root=/ on the live system")
	}
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s: %w: %s", name, err, message)
}

func (r *linuxSwapRuntime) FileState(path string, managed bool) (swapFileState, error) {
	if !r.live {
		return swapFileState{}, errors.New("swap capacity changes require --root=/ on the live system")
	}
	if managed {
		if err := verifyManagedDirectory(); err != nil {
			return swapFileState{}, err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if managed {
			if markerInfo, markerErr := os.Lstat(managedSwapMarker); markerErr == nil {
				return swapFileState{}, fmt.Errorf(
					"refuse to create %s: ownership marker already exists without its swapfile (%s)",
					path, markerInfo.Mode(),
				)
			} else if !errors.Is(markerErr, fs.ErrNotExist) {
				return swapFileState{}, fmt.Errorf("inspect managed swapfile marker: %w", markerErr)
			}
		}
		return swapFileState{}, nil
	}
	if err != nil {
		return swapFileState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return swapFileState{}, fmt.Errorf("%s is not a regular file", path)
	}
	if managed {
		if err := verifyManagedMarker(); err != nil {
			return swapFileState{}, err
		}
	}
	format, err := readSwapFormat(path)
	if err != nil {
		return swapFileState{}, fmt.Errorf("read swapfile identity: %w", err)
	}
	return swapFileState{Exists: true, Size: uint64(info.Size()), Format: format}, nil
}

func (r *linuxSwapRuntime) ResizeFile(path string, size uint64, managed bool) error {
	if !r.live {
		return errors.New("swap capacity changes require --root=/ on the live system")
	}
	if size > uint64(^uint64(0)>>1) {
		return errors.New("swapfile size exceeds int64")
	}
	if _, active, err := r.activeSwap(path); err != nil {
		return fmt.Errorf("verify swapfile %s is inactive: %w", path, err)
	} else if active {
		return fmt.Errorf("refuse to resize active swapfile %s", path)
	}
	state, err := r.FileState(path, managed)
	if err != nil {
		return err
	}
	created := false
	if !state.Exists {
		if !managed || path != ManagedSwapPath {
			return fmt.Errorf("swapfile %s does not exist", path)
		}
		directory := filepath.Dir(path)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		directoryInfo, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
			return fmt.Errorf("managed swap directory %s is not a real directory", directory)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return err
		}
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return createErr
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			return closeErr
		}
		created = true
		if err := createManagedMarker(); err != nil {
			_ = os.Remove(path)
			return err
		}
	}
	if created {
		if err := prepareBtrfsSwapfile(path); err != nil {
			_ = r.RemoveManagedFile(path)
			return err
		}
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("open swapfile returned an invalid descriptor")
	}
	defer file.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return err
	}
	if opened.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("swapfile %s is no longer a regular file", path)
	}
	if managed {
		if err := unix.Fchmod(fd, 0o600); err != nil {
			return err
		}
	}
	if size > 0 {
		// Allocate the new range before truncating. If allocation fails, the
		// previous backing file remains intact and rollback has less to repair.
		if err := unix.Fallocate(fd, 0, 0, int64(size)); err != nil {
			return fmt.Errorf("allocate non-sparse swapfile: %w", err)
		}
	}
	if err := file.Truncate(int64(size)); err != nil {
		return err
	}
	return file.Sync()
}

func (r *linuxSwapRuntime) RemoveManagedFile(path string) error {
	if path != ManagedSwapPath {
		return fmt.Errorf("refuse to remove unmanaged swapfile %s", path)
	}
	if err := verifyManagedDirectory(); err != nil {
		return err
	}
	if _, active, err := r.activeSwap(path); err != nil {
		return fmt.Errorf("verify managed swapfile is inactive: %w", err)
	} else if active {
		return errors.New("refuse to remove active managed swapfile")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to remove %s: not a regular managed swapfile", path)
		}
		if markerErr := verifyManagedMarker(); markerErr != nil {
			return markerErr
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var errs []error
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	if err := os.Remove(managedSwapMarker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *linuxSwapRuntime) ZRAMState(path string) (zramState, error) {
	name, err := zramName(path)
	if err != nil {
		return zramState{}, err
	}
	base := filepath.Join("/sys/block", name)
	size, err := r.readUint(filepath.Join(base, "disksize"))
	if err != nil {
		return zramState{}, err
	}
	state := zramState{SizeBytes: size}
	if r.live {
		format, err := readSwapFormat(path)
		if err != nil {
			return zramState{}, fmt.Errorf("read zram swap identity: %w", err)
		}
		state.Format = format
	}
	if data, readErr := r.fs.ReadFile(filepath.Join(base, "comp_algorithm")); readErr == nil {
		state.Algorithm = selectedAlgorithm(string(data))
	}
	if data, readErr := r.fs.ReadFile(filepath.Join(base, "max_comp_streams")); readErr == nil {
		state.MaxStreams = strings.TrimSpace(string(data))
	}
	if data, readErr := r.fs.ReadFile(filepath.Join(base, "mem_limit")); readErr == nil {
		state.MemoryLimit = strings.TrimSpace(string(data))
	}
	return state, nil
}

func (r *linuxSwapRuntime) ResizeZRAM(path string, settings zramState, size uint64) error {
	if _, active, err := r.activeSwap(path); err != nil {
		return fmt.Errorf("verify zram %s is inactive: %w", path, err)
	} else if active {
		return fmt.Errorf("refuse to resize active zram %s", path)
	}
	name, err := zramName(path)
	if err != nil {
		return err
	}
	base := filepath.Join("/sys/block", name)
	if err := r.fs.WriteFile(filepath.Join(base, "reset"), []byte("1")); err != nil {
		return err
	}
	if settings.Algorithm != "" {
		if err := r.fs.WriteFile(filepath.Join(base, "comp_algorithm"), []byte(settings.Algorithm)); err != nil {
			return err
		}
	}
	if settings.MaxStreams != "" {
		if err := r.fs.WriteFile(filepath.Join(base, "max_comp_streams"), []byte(settings.MaxStreams)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if settings.MemoryLimit != "" && settings.MemoryLimit != "0" {
		if err := r.fs.WriteFile(filepath.Join(base, "mem_limit"), []byte(settings.MemoryLimit)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return r.fs.WriteFile(filepath.Join(base, "disksize"), []byte(strconv.FormatUint(size, 10)))
}

func (r *linuxSwapRuntime) readUint(path string) (uint64, error) {
	data, err := r.fs.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}

func selectedAlgorithm(value string) string {
	for _, field := range strings.Fields(value) {
		if strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
			return strings.TrimSuffix(strings.TrimPrefix(field, "["), "]")
		}
	}
	return ""
}

func verifyManagedMarker() error {
	info, err := os.Lstat(managedSwapMarker)
	if err != nil {
		return fmt.Errorf("refuse to modify %s without its rtop ownership marker: %w", ManagedSwapPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to modify %s: ownership marker is not a regular file", ManagedSwapPath)
	}
	data, err := os.ReadFile(managedSwapMarker)
	if err != nil {
		return fmt.Errorf("refuse to modify %s without its rtop ownership marker: %w", ManagedSwapPath, err)
	}
	if strings.TrimSpace(string(data)) != "rtop managed swapfile v1" {
		return fmt.Errorf("refuse to modify %s: invalid rtop ownership marker", ManagedSwapPath)
	}
	return nil
}

func verifyManagedDirectory() error {
	directory := filepath.Dir(ManagedSwapPath)
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect managed swap directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed swap directory %s is not a real directory", directory)
	}
	return nil
}

func createManagedMarker() error {
	file, err := os.OpenFile(managedSwapMarker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create managed swapfile marker: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(managedSwapMarker)
		}
	}()
	if _, err := file.WriteString("rtop managed swapfile v1\n"); err != nil {
		return fmt.Errorf("write managed swapfile marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync managed swapfile marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close managed swapfile marker: %w", err)
	}
	remove = false
	return nil
}

func prepareBtrfsSwapfile(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &stat); err != nil {
		return err
	}
	if uint64(stat.Type) != uint64(unix.BTRFS_SUPER_MAGIC) {
		return nil
	}
	command := exec.Command("chattr", "+C", path)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare Btrfs NOCOW swapfile with chattr +C: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func readSwapFormat(path string) (swapFormat, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return swapFormat{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return swapFormat{}, errors.New("open Swap header returned an invalid descriptor")
	}
	defer file.Close()

	const maximumHeaderPage = 64 << 10
	data := make([]byte, maximumHeaderPage)
	read, err := file.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return swapFormat{}, err
	}
	data = data[:read]
	valid := false
	for _, pageSize := range []int{4 << 10, 8 << 10, 16 << 10, 32 << 10, 64 << 10} {
		if len(data) >= pageSize && bytes.Equal(data[pageSize-10:pageSize], []byte("SWAPSPACE2")) {
			valid = true
			break
		}
	}
	if !valid || len(data) < 1068 {
		return swapFormat{}, nil
	}

	uuidBytes := data[1036:1052]
	format := swapFormat{}
	if !allZero(uuidBytes) {
		format.UUID = fmt.Sprintf("%x-%x-%x-%x-%x",
			uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
	}
	labelBytes := data[1052:1068]
	if end := bytes.IndexByte(labelBytes, 0); end >= 0 {
		labelBytes = labelBytes[:end]
	}
	format.Label = string(labelBytes)
	return format, nil
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
