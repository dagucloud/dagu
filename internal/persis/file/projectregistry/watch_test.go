// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

func TestWatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ticks := make(chan time.Time)
	reg := New(dir)
	reg.pollTicks = func() (<-chan time.Time, func()) { return ticks, func() {} }
	writer := New(dir)

	ctx, cancel := context.WithCancel(t.Context())
	reports := make(chan []project.Project, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.Watch(ctx, func(projects []project.Project) { reports <- projects })
	}()

	require.Empty(t, receive(t, reports), "the registry is reported when the watch starts")

	created, err := writer.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	ticks <- time.Time{}
	require.Equal(t, []project.Project{created}, receive(t, reports))

	cancel()
	receive(t, done)
}

func TestCheckReportsChanges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	writer := New(dir)
	var reports [][]project.Project
	w := newWatcher(New(dir), func(projects []project.Project) { reports = append(reports, projects) })

	w.check(ctx)
	w.check(ctx)
	w.check(ctx)
	require.Len(t, reports, 1, "an unchanged registry is reported once")
	require.Empty(t, reports[0])

	created, err := writer.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	w.check(ctx)
	w.check(ctx)
	require.Len(t, reports, 2)
	require.Equal(t, []project.Project{created}, reports[1])

	marked, err := writer.SetDeleting(ctx, created.ID, true)
	require.NoError(t, err)
	w.check(ctx)
	require.Len(t, reports, 3)
	require.Equal(t, []project.Project{marked}, reports[2])
}

// A second write can leave the file's size and modification time as the
// first left them, as on a file system that keeps whole seconds. The check
// after a change reads the file again, so it still sees the second write.
func TestCheckSeesWriteThatKeepsTheStat(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	writer := New(dir)
	var reports [][]project.Project
	w := newWatcher(New(dir), func(projects []project.Project) { reports = append(reports, projects) })

	created, err := writer.Create(ctx, project.CreateInput{Name: "weather", DisplayName: "aa"})
	require.NoError(t, err)
	w.check(ctx)
	require.Len(t, reports, 1)

	info, err := os.Stat(writer.path)
	require.NoError(t, err)
	renamed, err := writer.SetDisplayName(ctx, created.ID, "bb")
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(writer.path, info.ModTime(), info.ModTime()))
	stamp, err := writer.stat()
	require.NoError(t, err)
	require.Equal(t, fileStamp{modTime: info.ModTime().UnixNano(), size: info.Size()}, stamp)

	w.check(ctx)
	require.Len(t, reports, 2)
	require.Equal(t, []project.Project{renamed}, reports[1])
}

func TestCheckWaitsForReadableFile(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := New(dir)
	var reports [][]project.Project
	w := newWatcher(reg, func(projects []project.Project) { reports = append(reports, projects) })

	require.NoError(t, os.WriteFile(reg.path, []byte(`{"schema_version": 1, "projects": [`), 0o600))
	w.check(ctx)
	w.check(ctx)
	require.Empty(t, reports, "a file that cannot be decoded is not reported")

	require.NoError(t, os.WriteFile(reg.path, []byte(`{"schema_version": 1, "projects": []}`), 0o600))
	w.check(ctx)
	require.Len(t, reports, 1)
	require.Empty(t, reports[0])
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the watch")
		var zero T
		return zero
	}
}
