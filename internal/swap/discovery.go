// Package swap discovers and classifies the kernel's active swap backends.
package swap

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

const ProcSwapsPath = "/proc/swaps"

type Entry struct {
	Path       string
	Kind       model.SwapKind
	KernelType string
	SizeBytes  uint64
	UsedBytes  uint64
	Priority   int
}

func Discover(systemFS sysfs.SystemFS) ([]Entry, error) {
	data, err := systemFS.ReadFile(ProcSwapsPath)
	if err != nil {
		return nil, err
	}
	entries, err := Parse(data)
	if err != nil {
		return nil, err
	}
	for index := range entries {
		entries[index].Kind = classify(systemFS, entries[index])
	}
	return entries, nil
}

func Parse(data []byte) ([]Entry, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return []Entry{}, nil
	}
	header := strings.Fields(lines[0])
	if len(header) < 5 || header[0] != "Filename" || header[1] != "Type" {
		return nil, fmt.Errorf("%s: invalid header", ProcSwapsPath)
	}
	entries := make([]Entry, 0, len(lines)-1)
	for lineNumber, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf("%s line %d: expected 5 fields", ProcSwapsPath, lineNumber+2)
		}
		sizeKiB, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil || sizeKiB > ^uint64(0)/1024 {
			return nil, fmt.Errorf("%s line %d: invalid size %q", ProcSwapsPath, lineNumber+2, fields[2])
		}
		usedKiB, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil || usedKiB > sizeKiB {
			return nil, fmt.Errorf("%s line %d: invalid used size %q", ProcSwapsPath, lineNumber+2, fields[3])
		}
		priority, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("%s line %d: invalid priority %q", ProcSwapsPath, lineNumber+2, fields[4])
		}
		entries = append(entries, Entry{
			Path: decodeProcPath(fields[0]), KernelType: fields[1],
			SizeBytes: sizeKiB * 1024, UsedBytes: usedKiB * 1024, Priority: priority,
		})
	}
	return entries, nil
}

func ToModel(entries []Entry, timestamp time.Time) []model.SwapDevice {
	devices := make([]model.SwapDevice, 0, len(entries))
	for _, entry := range entries {
		devices = append(devices, model.SwapDevice{
			Path: entry.Path, Kind: entry.Kind, KernelType: entry.KernelType,
			Size:     model.AvailableMetric(float64(entry.SizeBytes)/(1024*1024), "MiB", ProcSwapsPath, timestamp),
			Used:     model.AvailableMetric(float64(entry.UsedBytes)/(1024*1024), "MiB", ProcSwapsPath, timestamp),
			Priority: entry.Priority,
		})
	}
	return devices
}

func TotalBytes(entries []Entry) (uint64, error) {
	var total uint64
	for _, entry := range entries {
		if entry.SizeBytes > ^uint64(0)-total {
			return 0, errors.New("active swap capacity overflows uint64")
		}
		total += entry.SizeBytes
	}
	return total, nil
}

func classify(systemFS sysfs.SystemFS, entry Entry) model.SwapKind {
	if entry.KernelType == "file" {
		return model.SwapFile
	}
	name := filepath.Base(entry.Path)
	if strings.HasPrefix(name, "zram") {
		if _, err := systemFS.ReadFile(filepath.Join("/sys/block", name, "disksize")); err == nil {
			return model.SwapZRAM
		}
	}
	return model.SwapBlock
}

func decodeProcPath(value string) string {
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); {
		if index+4 <= len(value) && value[index] == '\\' {
			var replacement byte
			switch value[index+1 : index+4] {
			case "040":
				replacement = ' '
			case "011":
				replacement = '\t'
			case "012":
				replacement = '\n'
			case "134":
				replacement = '\\'
			}
			if replacement != 0 {
				decoded.WriteByte(replacement)
				index += 4
				continue
			}
		}
		decoded.WriteByte(value[index])
		index++
	}
	return decoded.String()
}
