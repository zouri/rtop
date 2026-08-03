package sysfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rtop/internal/model"
)

// SystemFS provides the subset of filesystem operations used by detectors and
// collectors. Paths passed to it are always absolute paths in the target Linux
// system.
type SystemFS interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]os.DirEntry, error)
	Readlink(path string) (string, error)
	Glob(pattern string) ([]string, error)
}

// RootFS maps absolute system paths below Root. Root "/" accesses the live
// system, while a testdata directory provides a virtual procfs/sysfs tree.
type RootFS struct {
	root string
}

func New(root string) (*RootFS, error) {
	if root == "" {
		root = "/"
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &RootFS{root: filepath.Clean(absolute)}, nil
}

func MustNew(root string) *RootFS {
	systemFS, err := New(root)
	if err != nil {
		panic(err)
	}
	return systemFS
}

func (f *RootFS) Root() string {
	return f.root
}

func (f *RootFS) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(f.resolve(path))
}

func (f *RootFS) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(f.resolve(path))
}

func (f *RootFS) Readlink(path string) (string, error) {
	return os.Readlink(f.resolve(path))
}

func (f *RootFS) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(f.resolve(pattern))
	if err != nil {
		return nil, err
	}
	logical := make([]string, 0, len(matches))
	for _, match := range matches {
		if f.root == string(filepath.Separator) {
			logical = append(logical, filepath.Clean(match))
			continue
		}
		relative, err := filepath.Rel(f.root, match)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		logical = append(logical, string(filepath.Separator)+relative)
	}
	return logical, nil
}

func (f *RootFS) resolve(path string) string {
	cleaned := filepath.Clean(string(filepath.Separator) + path)
	if f.root == string(filepath.Separator) {
		return cleaned
	}
	return filepath.Join(f.root, strings.TrimPrefix(cleaned, string(filepath.Separator)))
}

// StatusForError keeps unavailable data distinct from a genuine zero.
func StatusForError(err error) model.MetricStatus {
	switch {
	case err == nil:
		return model.Available
	case errors.Is(err, fs.ErrPermission):
		return model.PermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		return model.Unsupported
	default:
		return model.Unavailable
	}
}
