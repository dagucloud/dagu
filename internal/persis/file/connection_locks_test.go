// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

func TestConnectionLocks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}}
	// Two processes that share the data directory.
	first := persisfile.NewServerStores(cfg).ConnectionLocks()
	second := persisfile.NewServerStores(cfg).ConnectionLocks()
	work := connection.Key{Workspace: "ops", Name: "work"}

	lock, err := first.TryLock(ctx, work)
	require.NoError(t, err)
	require.NoError(t, lock.Held())

	_, err = second.TryLock(ctx, work)
	require.ErrorIs(t, err, connection.ErrLocked)
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	assert.ErrorIs(t, second.WaitUnlocked(short, work), context.DeadlineExceeded)

	// Another connection's lock is separate.
	other, err := second.TryLock(ctx, connection.Key{Workspace: "Ops", Name: "work"})
	require.NoError(t, err)
	other.Unlock()

	released := make(chan error, 1)
	go func() { released <- second.WaitUnlocked(ctx, work) }()
	lock.Unlock()
	require.NoError(t, <-released)

	lock, err = second.TryLock(ctx, work)
	require.NoError(t, err)
	lock.Unlock()
}

// A catalog's lock is shared across processes, and is neither its
// connection's token lock nor another service's catalog lock.
func TestConnectionCatalogLocks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}}
	first := persisfile.NewServerStores(cfg)
	second := persisfile.NewServerStores(cfg)
	work := connection.Key{Workspace: "ops", Name: "work"}

	lock, err := first.ConnectionCatalogLocks().TryLock(ctx, work, "gmail")
	require.NoError(t, err)
	_, err = second.ConnectionCatalogLocks().TryLock(ctx, work, "gmail")
	require.ErrorIs(t, err, connection.ErrLocked)

	token, err := second.ConnectionLocks().TryLock(ctx, work)
	require.NoError(t, err)
	token.Unlock()
	calendar, err := second.ConnectionCatalogLocks().TryLock(ctx, work, "calendar")
	require.NoError(t, err)
	calendar.Unlock()

	released := make(chan error, 1)
	go func() { released <- second.ConnectionCatalogLocks().WaitUnlocked(ctx, work, "gmail") }()
	lock.Unlock()
	require.NoError(t, <-released)
}
