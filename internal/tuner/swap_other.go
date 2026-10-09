//go:build !linux

package tuner

import (
	"context"
	"errors"

	"rtop/internal/sysfs"
)

type unsupportedSwapExecutor struct{}

// NewLinuxSwapExecutor keeps the command layer buildable for fixture and help
// use on non-Linux development hosts. Actual capacity changes remain Linux-only.
func NewLinuxSwapExecutor(string, sysfs.WritableSystemFS) SwapPlanExecutor {
	return unsupportedSwapExecutor{}
}

func (unsupportedSwapExecutor) Apply(context.Context, SwapPlan) (func(context.Context) error, error) {
	return nil, errors.New("swap capacity changes are supported only on Linux")
}
