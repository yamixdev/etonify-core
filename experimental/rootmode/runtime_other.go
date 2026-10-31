//go:build !linux

package rootmode

import (
	"context"
	"io"
)

func Run(context.Context, RunOptions, io.Reader, io.Writer) error { return Reason("android_required") }
func Cleanup(RunOptions) error                                    { return Reason("android_required") }
