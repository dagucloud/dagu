// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSelfUpgrader returns an available upgrader from v1.0.0 to latest whose
// install runs install and whose restart closes the returned channel.
func testSelfUpgrader(t *testing.T, latest string, install func(func(downloaded, total int64)) error) (*selfUpgrader, <-chan struct{}) {
	t.Helper()
	restarted := make(chan struct{})
	u := &selfUpgrader{
		ctx:       t.Context(),
		available: true,
		current:   "1.0.0",
		restart:   func() { close(restarted) },
		fetch: func(context.Context) (*upgrade.ReleaseInfo, error) {
			return &upgrade.ReleaseInfo{
				Release:       &upgrade.Release{TagName: latest},
				TargetVersion: semver.MustParse(latest),
			}, nil
		},
		install: func(_ context.Context, _ *upgrade.ReleaseInfo, onProgress func(downloaded, total int64)) error {
			return install(onProgress)
		},
		status: api.SelfUpgradeStatus{State: api.SelfUpgradeStateIdle},
	}
	return u, restarted
}

// waitForState waits until the upgrader reaches state.
func waitForState(t *testing.T, u *selfUpgrader, state api.SelfUpgradeState) api.SelfUpgradeStatus {
	t.Helper()
	require.Eventually(t, func() bool { return u.Status().State == state }, 5*time.Second, 10*time.Millisecond)
	return u.Status()
}

func TestSelfUpgraderInstallsThenRestarts(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	u, restarted := testSelfUpgrader(t, "v9.9.9", func(onProgress func(downloaded, total int64)) error {
		onProgress(50, 100)
		<-release
		onProgress(100, 100)
		return nil
	})

	require.NoError(t, u.Start())
	require.Eventually(t, func() bool {
		status := u.Status()
		return status.DownloadedBytes != nil && *status.DownloadedBytes == 50
	}, 5*time.Second, 10*time.Millisecond)
	status := u.Status()
	assert.Equal(t, api.SelfUpgradeStateDownloading, status.State)
	require.NotNil(t, status.TargetVersion)
	assert.Equal(t, "v9.9.9", *status.TargetVersion)
	assert.ErrorIs(t, u.Start(), apiv1.ErrSelfUpgradeRunning)

	close(release)
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the upgrader did not restart Dagu")
	}
	assert.Equal(t, api.SelfUpgradeStateRestarting, u.Status().State)
}

// A failed upgrade leaves the binary alone and can be retried.
func TestSelfUpgraderReportsFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	u, restarted := testSelfUpgrader(t, "v9.9.9", func(func(downloaded, total int64)) error {
		attempts++
		return errors.New("checksum mismatch")
	})

	require.NoError(t, u.Start())
	status := waitForState(t, u, api.SelfUpgradeStateFailed)
	require.NotNil(t, status.Error)
	assert.Contains(t, *status.Error, "checksum mismatch")

	require.NoError(t, u.Start(), "a failed upgrade can be retried")
	waitForState(t, u, api.SelfUpgradeStateFailed)
	assert.Equal(t, 2, attempts)
	select {
	case <-restarted:
		t.Fatal("a failed upgrade restarted Dagu")
	default:
	}
}

func TestSelfUpgraderRefusesAnOlderRelease(t *testing.T) {
	t.Parallel()

	installed := false
	u, _ := testSelfUpgrader(t, "v1.0.0", func(func(downloaded, total int64)) error {
		installed = true
		return nil
	})

	require.NoError(t, u.Start())
	status := waitForState(t, u, api.SelfUpgradeStateFailed)
	require.NotNil(t, status.Error)
	assert.Contains(t, *status.Error, "already the latest release")
	assert.False(t, installed)
}

func TestSelfUpgraderUnavailable(t *testing.T) {
	t.Parallel()

	u, _ := testSelfUpgrader(t, "v9.9.9", func(func(downloaded, total int64)) error { return nil })
	u.available = false

	assert.ErrorIs(t, u.Start(), apiv1.ErrSelfUpgradeUnavailable)
	assert.Equal(t, api.SelfUpgradeStatus{State: api.SelfUpgradeStateIdle}, u.Status())
}

// Serving ends with a restart request, so the service starts the new binary.
func TestServeEndsOnRestartRequest(t *testing.T) {
	t.Parallel()

	requested := make(chan struct{})
	close(requested)
	srv := &Server{config: &config.Config{}, restartRequested: requested}
	err := srv.setupGracefulShutdown(signalctx.WithOSSignalsDisabled(t.Context()), make(chan error))
	assert.ErrorIs(t, err, errRestartRequested)
}
