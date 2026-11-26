// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func changeOf(name, file string, labels ...string) *eventstore.DAGRunStatusSnapshot {
	return &eventstore.DAGRunStatusSnapshot{Name: name, DAGFile: file, Labels: labels}
}

// feedAt returns a feed whose clock reads the time that the returned function
// sets.
func feedAt(start time.Time) (*runChanges, func(time.Time)) {
	now := start
	feed := &runChanges{now: func() time.Time { return now }}
	return feed, func(t time.Time) { now = t }
}

// startedFeed returns a feed that holds every change after feedStart, as after
// the first read of the run events.
func startedFeed() (*runChanges, func(time.Time)) {
	feed, setNow := feedAt(feedStart)
	feed.record(nil)
	return feed, setNow
}

func changedFiles(changes openapiv1.DAGRunChanges) []string {
	files := make([]string, 0, len(changes.Changes))
	for _, change := range changes.Changes {
		files = append(files, change.FileName)
	}
	return files
}

var feedStart = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// Each DAG file shows once, with its latest change, the newest first. A file
// is named by its stem, and by the name of the DAG when the event has no file.
func TestRunChangesListsEachFileOnceNewestFirst(t *testing.T) {
	t.Parallel()
	feed, setNow := startedFeed()
	setNow(feedStart.Add(time.Millisecond))
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("a", "a.yaml"), changeOf("b", "")})
	setNow(feedStart.Add(time.Second))
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("c", "c"), changeOf("a", "a.yaml")})

	changes := feed.read(nil)
	assert.Equal(t, []string{"a", "c", "b"}, changedFiles(changes))
	assert.True(t, changes.Changes[0].ChangedAt.Equal(feedStart.Add(time.Second)))
	assert.True(t, changes.Since.Equal(feedStart), "complete from the first read of the events")
}

// A caller sees the changes of the runs of the workspaces it can see, and of
// runs in no workspace.
func TestRunChangesFiltersByWorkspace(t *testing.T) {
	t.Parallel()
	feed, setNow := startedFeed()
	setNow(feedStart.Add(time.Millisecond))
	feed.record([]*eventstore.DAGRunStatusSnapshot{
		changeOf("ops", "ops", "workspace=ops"),
		changeOf("dev", "dev", "workspace=dev"),
		changeOf("plain", "plain"),
	})

	ops := &workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}, IncludeUnlabelled: true}
	assert.Equal(t, []string{"plain", "ops"}, changedFiles(feed.read(ops)))
	assert.Equal(t, []string{"plain", "dev", "ops"}, changedFiles(feed.read(nil)))
}

// A change older than the retention leaves the feed, and the feed is then
// complete only from that change.
func TestRunChangesDropsOldChanges(t *testing.T) {
	t.Parallel()
	feed, setNow := startedFeed()
	old := feedStart.Add(time.Second)
	setNow(old)
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("old", "old")})
	setNow(feedStart.Add(time.Minute))
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("new", "new")})

	setNow(old.Add(runChangeRetention + time.Second))
	changes := feed.read(nil)
	assert.Equal(t, []string{"new"}, changedFiles(changes))
	assert.True(t, changes.Since.Equal(old), "since %s", changes.Since)
}

func TestRunChangesKeepsAtMostTheLimit(t *testing.T) {
	t.Parallel()
	feed, setNow := startedFeed()
	for i := range runChangeLimit + 1 {
		setNow(feedStart.Add(time.Duration(i+1) * time.Millisecond))
		feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("d", fmt.Sprintf("d%03d", i))})
	}

	changes := feed.read(nil)
	require.Len(t, changes.Changes, runChangeLimit)
	assert.Equal(t, fmt.Sprintf("d%03d", runChangeLimit), changes.Changes[0].FileName)
	assert.Equal(t, "d001", changes.Changes[runChangeLimit-1].FileName)
	assert.True(t, changes.Since.Equal(feedStart.Add(time.Millisecond)), "since %s", changes.Since)
}

// Before the first read of the run events, the feed is complete from now only,
// so a list reads all its rows. A pause in the reads delays changes and loses
// none, so it does not move since.
func TestRunChangesCompleteFromTheFirstRead(t *testing.T) {
	t.Parallel()
	feed, setNow := feedAt(feedStart.Add(500 * time.Microsecond))
	changes := feed.read(nil)
	assert.True(t, changes.Since.Equal(changes.ReadAt), "before the first read")
	assert.Empty(t, changes.Changes)

	setNow(feedStart)
	feed.record(nil)
	paused := feedStart.Add(time.Minute)
	setNow(paused)
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("b", "b")})
	changes = feed.read(nil)
	assert.True(t, changes.Since.Equal(feedStart), "since %s", changes.Since)
	assert.Equal(t, []string{"b"}, changedFiles(changes), "the change read after the pause")
}

// A change and a read at the same instant keep the change after the read, in a
// client that keeps milliseconds only.
func TestRunChangesRoundTimes(t *testing.T) {
	t.Parallel()
	feed, setNow := startedFeed()
	setNow(feedStart.Add(500 * time.Microsecond))
	feed.record([]*eventstore.DAGRunStatusSnapshot{changeOf("a", "a")})

	changes := feed.read(nil)
	require.Len(t, changes.Changes, 1)
	assert.True(t, changes.Changes[0].ChangedAt.After(changes.ReadAt))
	assert.True(t, changes.ReadAt.Equal(feedStart))
}

// A change drops the kept activity of its DAG before the feed records it, so a
// read that lands between the two cannot serve, and keep, the old activity,
// and a read that began before the change has an earlier time than the change.
func TestDAGRunsChangedDropsActivityFirst(t *testing.T) {
	t.Parallel()
	var a API
	key := activityKey{dag: "d", window: 24 * time.Hour}
	latest := ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Succeeded}
	a.DAGRunsChanged(nil)
	testutil.WaitForClockTick(t)
	readAt := time.Now()
	a.activities.put(key, latest, readAt, &openapiv1.DAGListActivity{})
	// The feed reads its clock under its lock, as it records the change.
	keptAtRecord := true
	a.runChanges.now = func() time.Time {
		_, keptAtRecord = a.activities.get(key, latest, time.Now())
		return time.Now()
	}

	a.DAGRunsChanged([]*eventstore.DAGRunStatusSnapshot{changeOf("d", "d.yaml")})

	assert.False(t, keptAtRecord, "the activity was still kept when the feed recorded the change")
	changes := a.runChanges.read(nil)
	require.Equal(t, []string{"d"}, changedFiles(changes))
	assert.False(t, changes.Changes[0].ChangedAt.Before(readAt.Truncate(time.Millisecond)))
}
