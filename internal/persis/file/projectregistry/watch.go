// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// pollInterval is how often Watch checks the file. Shared storage such as
// NFS sends no notice of another host's write.
const pollInterval = 5 * time.Second

// Watch calls onChange with every registered project when it starts, or once
// projects.json can be read, and again each time the file changes, until ctx
// ends. It checks the file every 5 seconds at the cost of one stat, and reads
// it only when it changed.
func (r *Registry) Watch(ctx context.Context, onChange func([]project.Project)) {
	ticks, stop := r.pollTicks()
	defer stop()

	w := newWatcher(r, onChange)
	w.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			w.check(ctx)
		}
	}
}

func newPollTicker() (<-chan time.Time, func()) {
	ticker := time.NewTicker(pollInterval)
	return ticker.C, ticker.Stop
}

// watcher reports the registry each time its content changes.
type watcher struct {
	registry *Registry
	onChange func([]project.Project)
	// stamp is the stat of the file when it was last read.
	stamp fileStamp
	// settling asks the next check to read the file again although its stat
	// is unchanged. A write within the file system's timestamp resolution of
	// the one last read can leave the stat as it was.
	settling bool
	// reported is the content last passed to onChange, valid once
	// hasReported is set.
	reported    []byte
	hasReported bool
}

// fileStamp is what a stat of the file shows of its content. The zero value
// stands for a missing file.
type fileStamp struct {
	modTime int64
	size    int64
}

// unreadStamp matches no stat, so the first check reads the file.
var unreadStamp = fileStamp{size: -1}

func newWatcher(r *Registry, onChange func([]project.Project)) *watcher {
	return &watcher{registry: r, onChange: onChange, stamp: unreadStamp}
}

// check reads the file when its stat changed since the last read, or when
// that read is settling, and reports the content when it differs from what
// was last reported. Failures are logged. A stat or read that failed is
// tried again on the next check, and a file that cannot be decoded once it
// changes.
func (w *watcher) check(ctx context.Context) {
	stamp, err := w.registry.stat()
	if err != nil {
		logger.Warn(ctx, "Failed to check the project registry", tag.File(w.registry.path), tag.Error(err))
		return
	}
	changed := stamp != w.stamp
	if !changed && !w.settling {
		return
	}
	data, err := w.registry.readFile()
	if err != nil {
		logger.Warn(ctx, "Failed to read the project registry", tag.Error(err))
		return
	}
	w.stamp = stamp
	w.settling = changed
	if w.hasReported && bytes.Equal(data, w.reported) {
		return
	}
	entries, err := decode(data)
	if err != nil {
		logger.Warn(ctx, "Failed to read the project registry", tag.Error(err))
		return
	}
	w.reported, w.hasReported = data, true
	w.onChange(projectsOf(entries))
}

func (r *Registry) stat() (fileStamp, error) {
	info, err := fileutil.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{modTime: info.ModTime().UnixNano(), size: info.Size()}, nil
}
