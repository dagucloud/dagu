// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	// activityCacheSize bounds the DAGs whose activity lists keep.
	activityCacheSize = 1000
	// activityCacheMaxAge bounds how long lists reuse a kept activity.
	activityCacheMaxAge = 30 * time.Second
	// serviceActivityMaxAge bounds how long lists reuse the activity of a
	// service DAG while its run is in progress and its up time grows.
	serviceActivityMaxAge = 10 * time.Second
	// activityFollowGrace is how long after the last read of the run events a
	// kept activity still waits for them. The events are read each second.
	activityFollowGrace = 10 * time.Second
)

// listedDAGActivity gives the activity of a DAG of a list over the window that
// ends at now. While the run events are followed, a kept activity lasts until
// an event tells that the runs of its DAG changed, so the latest run of the
// DAG is read only when its activity is not kept.
func (a *API) listedDAGActivity(ctx context.Context, dag *ir.DAG, now time.Time, window time.Duration) (*api.DAGListActivity, error) {
	if activity, ok := a.activities.getFollowed(activityKeyOf(dag, window), now); ok {
		return withRunsSince(activity, now.Add(-window)), nil
	}
	return a.cachedDAGActivity(ctx, dag, a.latestRunStatus(ctx, dag), now, window)
}

// cachedDAGActivity gives the activity of a DAG over the window that ends at
// now.
// It reuses the activity that an earlier list read while the runs of the DAG
// did not change, so that a list reads the runs only of the DAGs whose runs
// changed. The lists of several tabs ask for a changed DAG at once, and one
// read serves them all.
func (a *API) cachedDAGActivity(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus, now time.Time, window time.Duration) (*api.DAGListActivity, error) {
	since := now.Add(-window)
	key := activityKeyOf(dag, window)
	if activity, ok := a.activities.get(key, latest, now); ok {
		return withRunsSince(activity, since), nil
	}
	flight := fmt.Sprintf("%s\x00%d\x00%t\x00%v", key.dag, key.window, key.service, latestRunKeyOf(latest))
	shared, err, _ := a.activityReads.Do(flight, func() (any, error) {
		// One caller that goes away does not fail the others.
		activity, err := a.dagActivity(context.WithoutCancel(ctx), dag, latest, since, window)
		if err == nil {
			a.activities.put(key, latest, now, activity)
		}
		return activity, err
	})
	activity, _ := shared.(*api.DAGListActivity)
	return activity, err
}

func activityKeyOf(dag *ir.DAG, window time.Duration) activityKey {
	return activityKey{dag: dag.Name, window: window, service: dag.RestartPolicy.Enabled()}
}

// activityCache keeps the activity of each listed DAG with the latest run
// that it was read for, and reuses it for at most one bucket of its window.
// While the run events are followed, the activity of a DAG lasts until an
// event tells that the runs of the DAG changed. Otherwise the activity of a
// plain DAG lasts at most activityCacheMaxAge, which bounds the changes that
// the latest run does not show, such as the retry of an older run. A service
// DAG starts a run each few seconds, so its activity does not wait for its
// latest run to stay the same, and lasts at most serviceActivityMaxAge.
type activityCache struct {
	once    sync.Once
	entries *lru.Cache[activityKey, keptActivity]
	// changedAt holds when the runs of each DAG last changed, so that an
	// activity whose read began before that change and ended after it is not
	// reused.
	changedAt *lru.Cache[string, time.Time]
	// followedAt is when the run events were last read, in Unix nanoseconds,
	// or 0 before the first read.
	followedAt atomic.Int64
}

type activityKey struct {
	dag     string
	window  time.Duration
	service bool
}

type keptActivity struct {
	latest   latestRunKey
	readAt   time.Time
	activity *api.DAGListActivity
}

// latestRunKey changes with each change of the latest run of a DAG.
type latestRunKey struct {
	dagRunID   string
	attemptID  string
	status     ir.Status
	startedAt  string
	finishedAt string
}

func latestRunKeyOf(status ir.DAGRunStatus) latestRunKey {
	return latestRunKey{
		dagRunID:   status.DAGRunID,
		attemptID:  status.AttemptID,
		status:     status.Status,
		startedAt:  status.StartedAt,
		finishedAt: status.FinishedAt,
	}
}

func (c *activityCache) store() *lru.Cache[activityKey, keptActivity] {
	c.once.Do(func() {
		c.entries, _ = lru.New[activityKey, keptActivity](activityCacheSize)
		c.changedAt, _ = lru.New[string, time.Time](activityCacheSize)
	})
	return c.entries
}

func (c *activityCache) changes() *lru.Cache[string, time.Time] {
	c.store()
	return c.changedAt
}

// get returns the activity kept for key when it is still recent at now, was
// read after the last change of the runs of its DAG and, for a plain DAG, for
// the same latest run.
func (c *activityCache) get(key activityKey, latest ir.DAGRunStatus, now time.Time) (*api.DAGListActivity, bool) {
	kept, ok := c.store().Get(key)
	if !ok {
		return nil, false
	}
	if age := now.Sub(kept.readAt); age < 0 || age >= c.maxAge(key, now) {
		return nil, false
	}
	if c.changedSince(key.dag, kept.readAt) {
		return nil, false
	}
	if key.service {
		return kept.activity, true
	}
	if kept.latest != latestRunKeyOf(latest) {
		return nil, false
	}
	if !c.follows(now) && changesUnseen(latest, kept.activity) {
		return nil, false
	}
	return kept.activity, true
}

// getFollowed returns the activity kept for key while the run events are
// followed, when it is still recent at now and no event told of a change of
// the runs of its DAG since it was read. It needs no latest run, as the
// events tell of every change.
func (c *activityCache) getFollowed(key activityKey, now time.Time) (*api.DAGListActivity, bool) {
	if !c.follows(now) {
		return nil, false
	}
	kept, ok := c.store().Get(key)
	if !ok {
		return nil, false
	}
	if age := now.Sub(kept.readAt); age < 0 || age >= c.maxAge(key, now) {
		return nil, false
	}
	if c.changedSince(key.dag, kept.readAt) {
		return nil, false
	}
	return kept.activity, true
}

// maxAge is how long lists reuse the activity of key at now: one bucket of
// its window, and at most serviceActivityMaxAge for a service DAG, or
// activityCacheMaxAge for a plain DAG while the run events are not followed.
func (c *activityCache) maxAge(key activityKey, now time.Time) time.Duration {
	bucket := key.window / activityBucketLimit
	switch {
	case key.service:
		return min(serviceActivityMaxAge, bucket)
	case c.follows(now):
		return bucket
	default:
		return min(activityCacheMaxAge, bucket)
	}
}

// put keeps an activity read for the latest run at now.
func (c *activityCache) put(key activityKey, latest ir.DAGRunStatus, now time.Time, activity *api.DAGListActivity) {
	c.store().Add(key, keptActivity{latest: latestRunKeyOf(latest), readAt: now, activity: activity})
}

// changed records that the run events were read at, and that the runs of the
// DAGs of names changed then. It drops the activity kept for those DAGs. The
// time of the change keeps out an activity whose read began before it.
func (c *activityCache) changed(names []string, at time.Time) {
	c.followedAt.Store(at.UnixNano())
	if len(names) == 0 {
		return
	}
	changedAt := c.changes()
	changedDAGs := make(map[string]struct{}, len(names))
	for _, name := range names {
		changedAt.Add(name, at)
		changedDAGs[name] = struct{}{}
	}
	entries := c.store()
	for _, key := range entries.Keys() {
		if _, ok := changedDAGs[key.dag]; ok {
			entries.Remove(key)
		}
	}
}

// follows reports whether the run events were read recently enough at now
// for a kept activity to wait for them.
func (c *activityCache) follows(now time.Time) bool {
	followed := c.followedAt.Load()
	return followed != 0 && now.Sub(time.Unix(0, followed)) < activityFollowGrace
}

// changedSince reports whether the runs of the DAG changed at or after readAt.
func (c *activityCache) changedSince(dag string, readAt time.Time) bool {
	at, ok := c.changes().Peek(dag)
	return ok && !readAt.After(at)
}

// changesUnseen reports whether the activity of a plain DAG can change while
// its latest run stays the same: when an older run is in progress, or when the
// latest run is queued, since the runs queued before it can start.
func changesUnseen(latest ir.DAGRunStatus, activity *api.DAGListActivity) bool {
	if latest.Status == ir.Queued {
		return true
	}
	return slices.ContainsFunc(activity.Runs, func(run api.DAGListActivityRun) bool {
		return run.DagRunId != latest.DAGRunID && ir.Status(run.Status).IsActive()
	})
}

// withRunsSince leaves out the runs that ended and started before since, as
// the window moved on since the activity was read. It does not change the
// kept activity.
func withRunsSince(activity *api.DAGListActivity, since time.Time) *api.DAGListActivity {
	before := func(run api.DAGListActivityRun) bool {
		return runTime(run.StartedAt).Before(since) && !ir.Status(run.Status).IsActive()
	}
	if !slices.ContainsFunc(activity.Runs, before) {
		return activity
	}
	moved := *activity
	moved.Runs = slices.DeleteFunc(slices.Clone(activity.Runs), before)
	return &moved
}
