// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
)

// errRestartRequested ends serving once the binary is upgraded, so the
// service that runs Dagu restarts it with the new binary.
var errRestartRequested = errors.New("exiting so the service restarts Dagu with the upgraded binary")

// selfUpgrader upgrades the running binary to the latest release from the web
// UI, then has restart end the server, so the service the installer set up
// starts the new binary. Runs in progress keep running across that restart.
type selfUpgrader struct {
	ctx       context.Context
	available bool
	current   string // Version of the running Dagu
	restart   func()
	fetch     func(context.Context) (*upgrade.ReleaseInfo, error)
	install   func(context.Context, *upgrade.ReleaseInfo, func(downloaded, total int64)) error

	mu     sync.Mutex
	status api.SelfUpgradeStatus
}

func newSelfUpgrader(ctx context.Context, cfg *config.Config, store upgrade.CacheStore, restart func()) *selfUpgrader {
	return &selfUpgrader{
		ctx:       ctx,
		available: canUpgradeFromWeb(ctx, cfg),
		current:   config.Version,
		restart:   restart,
		fetch: func(ctx context.Context) (*upgrade.ReleaseInfo, error) {
			return upgrade.FetchReleaseInfo(ctx, upgrade.Options{})
		},
		install: func(ctx context.Context, info *upgrade.ReleaseInfo, onProgress func(downloaded, total int64)) error {
			_, err := upgrade.UpgradeWithReleaseInfo(ctx, upgrade.Options{OnProgress: onProgress}, info, store)
			return err
		},
		status: api.SelfUpgradeStatus{State: api.SelfUpgradeStateIdle},
	}
}

// canUpgradeFromWeb reports whether this Dagu can replace its own binary
// and has a service that restarts it afterwards.
func canUpgradeFromWeb(ctx context.Context, cfg *config.Config) bool {
	if !cfg.Server.AllowWebUpgrade {
		return false
	}
	if _, err := upgrade.ParseVersion(config.Version); err != nil {
		return false
	}
	if ok, _ := upgrade.CanSelfUpgrade(); !ok {
		return false
	}
	if upgrade.DetectService() == nil {
		return false
	}
	execPath, err := upgrade.GetExecutablePath()
	if err == nil {
		err = upgrade.CheckWritePermission(execPath)
	}
	if err != nil {
		logger.Info(ctx, "Upgrading from the web UI is unavailable", tag.Error(err))
		return false
	}
	return true
}

// Status implements apiv1.SelfUpgrader.
func (u *selfUpgrader) Status() api.SelfUpgradeStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	status := u.status
	status.Available = u.available
	return status
}

// Start implements apiv1.SelfUpgrader.
func (u *selfUpgrader) Start() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.available {
		return apiv1.ErrSelfUpgradeUnavailable
	}
	switch u.status.State {
	case api.SelfUpgradeStateDownloading, api.SelfUpgradeStateInstalling, api.SelfUpgradeStateRestarting:
		return apiv1.ErrSelfUpgradeRunning
	case api.SelfUpgradeStateIdle, api.SelfUpgradeStateFailed:
	}
	u.status = api.SelfUpgradeStatus{State: api.SelfUpgradeStateDownloading}
	go u.run()
	return nil
}

func (u *selfUpgrader) run() {
	info, err := u.fetch(u.ctx)
	if err == nil {
		err = newerRelease(u.current, info)
	}
	if err != nil {
		u.fail(err)
		return
	}
	u.update(func(status *api.SelfUpgradeStatus) {
		status.TargetVersion = &info.Release.TagName
	})
	logger.Info(u.ctx, "Upgrading Dagu from the web UI",
		tag.Version(u.current),
		slog.String("target-version", info.Release.TagName),
	)
	err = u.install(u.ctx, info, func(downloaded, total int64) {
		u.update(func(status *api.SelfUpgradeStatus) {
			status.DownloadedBytes, status.TotalBytes = &downloaded, &total
			if total > 0 && downloaded >= total {
				status.State = api.SelfUpgradeStateInstalling
			}
		})
	})
	if err != nil {
		u.fail(err)
		return
	}
	u.update(func(status *api.SelfUpgradeStatus) {
		status.State = api.SelfUpgradeStateRestarting
	})
	u.restart()
}

// newerRelease checks that info names a release newer than version.
func newerRelease(version string, info *upgrade.ReleaseInfo) error {
	current, err := upgrade.ParseVersion(version)
	if err != nil {
		return err
	}
	if !upgrade.IsNewer(current, info.TargetVersion) {
		return fmt.Errorf("version %s is already the latest release", version)
	}
	return nil
}

func (u *selfUpgrader) update(change func(*api.SelfUpgradeStatus)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	change(&u.status)
}

func (u *selfUpgrader) fail(err error) {
	logger.Error(u.ctx, "Upgrading from the web UI failed", tag.Error(err))
	message := err.Error()
	u.update(func(status *api.SelfUpgradeStatus) {
		status.State = api.SelfUpgradeStateFailed
		status.Error = &message
	})
}
