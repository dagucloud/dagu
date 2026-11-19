// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/stretchr/testify/assert"
)

// A server that ends for a web upgrade exits as its service needs to start it
// again; any other end keeps its error.
func TestRestartExit(t *testing.T) {
	t.Parallel()

	restart := fmt.Errorf("server failed: %w", frontend.ErrRestartRequested)
	if runtime.GOOS == "windows" {
		assert.ErrorIs(t, restartExit(restart), frontend.ErrRestartRequested, "the Windows service wrapper restarts only a failure")
	} else {
		assert.NoError(t, restartExit(restart), "systemd and launchd restart a clean exit")
	}

	failure := errors.New("port in use")
	assert.ErrorIs(t, restartExit(failure), failure)
	assert.NoError(t, restartExit(nil))
}
