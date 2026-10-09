// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/stretchr/testify/require"
)

func TestStopLocalAgentSessionCleanupHonorsShutdownContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- stopLocalAgentSessionCleanup(ctx, func() {}, done, nil)
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		close(done)
		<-result
		t.Fatal("agent session cleanup did not honor the shutdown context")
	}
}

func TestStartCloudReport(t *testing.T) {
	newContext := func(report bool) *Context {
		return &Context{
			Context:        t.Context(),
			Config:         &config.Config{Cloud: config.CloudConfig{Report: report}},
			LicenseManager: license.NewTestManager(),
		}
	}

	t.Run("on", func(t *testing.T) {
		reporter := startCloudReport(newContext(true))
		require.NotNil(t, reporter)
		reporter.Stop()
	})

	t.Run("off", func(t *testing.T) {
		require.Nil(t, startCloudReport(newContext(false)))
	})
}
