// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import "github.com/dagucloud/dagu/internal/cmn/value"

func testEnvScope(entries map[string]string) *value.EnvScope {
	scope := value.NewEnvScope(nil, false)
	for key, val := range entries {
		scope = scope.WithEntry(key, val, value.EnvSourceDAGEnv)
	}
	return scope
}
