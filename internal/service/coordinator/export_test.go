// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import "github.com/dagucloud/dagu/internal/core/exec"

// RuntimeDispatcherConfigForTest exposes the coordinator client config for
// external package tests.
func RuntimeDispatcherConfigForTest(dispatcher exec.Dispatcher) (*Config, bool) {
	client, ok := dispatcher.(*clientImpl)
	if !ok {
		return nil, false
	}
	return client.config, true
}
