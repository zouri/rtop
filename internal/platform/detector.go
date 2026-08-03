package platform

import (
	"runtime"
	"sort"
	"strings"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type Detector struct {
	fs sysfs.SystemFS
}

func NewDetector(systemFS sysfs.SystemFS) *Detector {
	return &Detector{fs: systemFS}
}

func (d *Detector) Detect() (model.SystemInfo, []model.Warning) {
	info := model.SystemInfo{Arch: runtime.GOARCH}
	var warnings []model.Warning

	if data, err := d.fs.ReadFile("/proc/sys/kernel/hostname"); err == nil {
		info.Hostname = cleanText(data)
	} else {
		warnings = append(warnings, warning("hostname", err))
	}
	if data, err := d.fs.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info.Kernel = cleanText(data)
	} else {
		warnings = append(warnings, warning("kernel", err))
	}
	if data, err := d.fs.ReadFile("/etc/os-release"); err == nil {
		values := parseOSRelease(string(data))
		info.OS = values["PRETTY_NAME"]
		if info.OS == "" {
			info.OS = strings.TrimSpace(values["NAME"] + " " + values["VERSION"])
		}
	} else {
		warnings = append(warnings, warning("os-release", err))
	}
	if data, err := d.fs.ReadFile("/proc/device-tree/model"); err == nil {
		info.Board = cleanText(data)
	}
	if data, err := d.fs.ReadFile("/proc/device-tree/compatible"); err == nil {
		info.Compatible = splitCompatible(data)
		info.SoC = detectSoC(info.Compatible)
	}

	return info, warnings
}

func parseOSRelease(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = strings.Trim(value, "\"")
			value = strings.ReplaceAll(value, "\\\"", "\"")
			value = strings.ReplaceAll(value, "\\\\", "\\")
		}
		values[strings.TrimSpace(key)] = value
	}
	return values
}

func splitCompatible(data []byte) []string {
	fields := strings.FieldsFunc(string(data), func(r rune) bool {
		return r == 0 || r == '\n'
	})
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func detectSoC(compatible []string) string {
	var candidates []string
	for _, item := range compatible {
		for _, part := range strings.FieldsFunc(strings.ToLower(item), func(r rune) bool {
			return r == ',' || r == '-' || r == '_'
		}) {
			if isRockchipSoC(part) {
				candidates = append(candidates, strings.ToUpper(part))
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return candidates[0]
}

func isRockchipSoC(value string) bool {
	if !strings.HasPrefix(value, "rk") || len(value) < 6 {
		return false
	}
	digits := 0
	for index, r := range value[2:] {
		if r >= '0' && r <= '9' {
			if index == digits {
				digits++
			}
			continue
		}
		if digits < 4 || r < 'a' || r > 'z' {
			return false
		}
	}
	return digits >= 4
}

func cleanText(data []byte) string {
	return strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
}

func warning(field string, err error) model.Warning {
	return model.Warning{Component: "platform", Message: field + ": " + err.Error()}
}
