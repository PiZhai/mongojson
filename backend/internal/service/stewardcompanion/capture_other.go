//go:build !windows

package stewardcompanion

import (
	"context"
	"fmt"
)

type unsupportedActivitySampler struct{}

func NewNativeActivitySampler() ActivitySampler { return unsupportedActivitySampler{} }

func (unsupportedActivitySampler) Sample(context.Context) (ActivitySnapshot, error) {
	return ActivitySnapshot{}, fmt.Errorf("native activity sampling is only available on Windows")
}
