// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import (
	"context"
	"time"
)

// WatchLedger is what the scheduler remembers of one DAG's watched folder:
// the folder and pattern it was read with, and the files it has seen there.
type WatchLedger struct {
	Path    string              `json:"path"`
	Pattern string              `json:"pattern,omitempty"`
	Files   map[string]FileMark `json:"files,omitempty"`
}

// FileMark tells one version of a file apart from another.
type FileMark struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Same reports whether two marks are of the same version of a file. Times
// are compared as instants, since a mark read back from the store carries
// another location.
func (m FileMark) Same(o FileMark) bool {
	return m.Size == o.Size && m.ModTime.Equal(o.ModTime)
}

// WatchStore keeps the ledgers of watched folders, by DAG name.
type WatchStore interface {
	LoadWatch(ctx context.Context) (map[string]WatchLedger, error)
	SaveWatch(ctx context.Context, ledgers map[string]WatchLedger) error
}
