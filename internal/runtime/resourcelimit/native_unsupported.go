// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !windows

package resourcelimit

import (
	"context"
	"fmt"
	"runtime"
)

func startNativeGuard(context.Context, Options) (nativeGuard, error) {
	return nil, fmt.Errorf("native resource limits are unsupported on %s", runtime.GOOS)
}
