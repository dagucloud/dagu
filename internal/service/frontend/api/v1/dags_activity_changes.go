// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	// runChangeRetention is how long the change feed keeps a change: three
	// times the shortest gap between two refetches of its topic, so that a
	// list that reads each refetch does not find the feed short.
	runChangeRetention = 3 * time.Minute
	// runChangeLimit bounds the DAG files that the change feed keeps, and so
	// the size of one refetch: about 180 KB. Beyond it the feed drops changes
	// and every list reads all its rows again, so it holds the DAGs that a
	// schedule starts together at the top of an hour.
	runChangeLimit = 2000
)

// DAGRunsChanged is told, after each read of the DAG-run events, the
// snapshots of the root runs whose state changed in them, possibly none. It
// drops the activity kept for their DAGs, then lists their DAG files in the
// change feed, so that a list that reads the activity after the feed names a
// file reads the new activity.
func (a *API) DAGRunsChanged(changes []*eventstore.DAGRunStatusSnapshot) {
	a.activities.changed(changedDAGNames(changes), time.Now())
	a.runChanges.record(changes)
}

// ResetDAGRunChanges makes the change feed complete only from now, so that
// the lists read all their activity again. The server calls it when its
// stream of app events resets and may have missed changes.
func (a *API) ResetDAGRunChanges() {
	a.runChanges.reset()
}

// GetDAGRunChanges returns the change feed as the caller sees it.
func (a *API) GetDAGRunChanges(ctx context.Context, _ api.GetDAGRunChangesRequestObject) (api.GetDAGRunChangesResponseObject, error) {
	return api.GetDAGRunChanges200JSONResponse(a.dagRunChanges(ctx)), nil
}

// GetDAGRunChangesData returns the change feed for SSE. The topic parser
// checks the identifier.
func (a *API) GetDAGRunChangesData(ctx context.Context, _ string) (any, error) {
	return a.dagRunChanges(ctx), nil
}

func (a *API) dagRunChanges(ctx context.Context) api.DAGRunChanges {
	return a.runChanges.read(a.workspaceFilterForContext(ctx))
}

// changedDAGNames returns the names of the DAGs of changes, each once.
func changedDAGNames(changes []*eventstore.DAGRunStatusSnapshot) []string {
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		if !slices.Contains(names, change.Name) {
			names = append(names, change.Name)
		}
	}
	return names
}

// runChanges keeps the DAG files whose root runs changed lately, the latest
// change of each, oldest first. It holds every change after from, which is
// zero before the first read of the run events. The run events are read from
// a cursor, so a pause in their reads delays changes and loses none.
type runChanges struct {
	mu      sync.Mutex
	entries []runChange
	from    time.Time
	// now is the clock; nil means time.Now.
	now func() time.Time
}

type runChange struct {
	fileName string
	labels   ir.Labels
	at       time.Time
}

func (c *runChanges) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// record lists the DAG files of changes as changed now. The time is taken
// under the lock, after the caller dropped the kept activity of the DAGs, so
// that a read of the activity that may miss a change began before the change
// time that the feed tells. The first record starts the feed.
func (c *runChanges) record(changes []*eventstore.DAGRunStatusSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := c.clock()
	if c.from.IsZero() {
		c.from = at
	}
	for _, change := range changes {
		fileName := fileutil.TrimYAMLFileExtension(change.DAGFile)
		if fileName == "" {
			fileName = change.Name
		}
		c.entries = slices.DeleteFunc(c.entries, func(e runChange) bool { return e.fileName == fileName })
		c.entries = append(c.entries, runChange{fileName: fileName, labels: ir.NewLabels(change.Labels), at: at})
	}
	c.trim(at)
}

// trim drops the changes older than the retention or beyond the limit, oldest
// first, and moves from to the latest dropped change.
func (c *runChanges) trim(now time.Time) {
	drop := 0
	for drop < len(c.entries) &&
		(now.Sub(c.entries[drop].at) > runChangeRetention || len(c.entries)-drop > runChangeLimit) {
		if c.entries[drop].at.After(c.from) {
			c.from = c.entries[drop].at
		}
		drop++
	}
	c.entries = slices.Delete(c.entries, 0, drop)
}

// reset makes the feed complete from now only.
func (c *runChanges) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.from = c.clock()
}

// read returns the changes after from that filter lets the caller see, the
// newest first. Before the first read of the run events, the feed is complete
// from now only.
func (c *runChanges) read(filter *workspace.WorkspaceFilter) api.DAGRunChanges {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	c.trim(now)
	readAt := now.UTC().Truncate(time.Millisecond)
	changes := []api.DAGRunChange{}
	if c.from.IsZero() {
		return api.DAGRunChanges{Since: readAt, ReadAt: readAt, Changes: changes}
	}
	for _, e := range slices.Backward(c.entries) {
		if !e.at.After(c.from) {
			break
		}
		if !filter.MatchesLabels(e.labels) {
			continue
		}
		changes = append(changes, api.DAGRunChange{FileName: e.fileName, ChangedAt: ceilMillis(e.at)})
	}
	return api.DAGRunChanges{Since: ceilMillis(c.from), ReadAt: readAt, Changes: changes}
}

// ceilMillis rounds t up to the millisecond, in UTC. A change after a read
// then keeps a later time than the read, which is rounded down, in a client
// that keeps milliseconds only.
func ceilMillis(t time.Time) time.Time {
	t = t.UTC()
	if truncated := t.Truncate(time.Millisecond); truncated.Before(t) {
		return truncated.Add(time.Millisecond)
	}
	return t
}
