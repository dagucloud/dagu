// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// workbookPathLock admits one write transaction at a time on one file.
// refs counts the holder and the waiters, so an idle entry can be dropped.
type workbookPathLock struct {
	token chan struct{}
	refs  int
}

// workbookPathLocks serializes write transactions on each file within this
// process, so concurrent writers do not save over each other's changes.
// Other processes are not coordinated.
var workbookPathLocks = struct {
	sync.Mutex
	entries map[string]*workbookPathLock
}{
	entries: make(map[string]*workbookPathLock),
}

// acquireWorkbookPathLocks holds the files that paths name until the returned
// release runs. Paths naming one file count once, and files are taken in a
// fixed order so two callers never deadlock. Empty paths are ignored. When ctx
// ends first, nothing stays held. log, when set, receives one line for each
// file that another write holds.
func acquireWorkbookPathLocks(ctx context.Context, log func(string), paths ...string) (func(), error) {
	keys := make(map[string]string, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		keys[workbookPathLockKey(path)] = path
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)

	releases := make([]func(), 0, len(ordered))
	for _, key := range ordered {
		release, err := acquireWorkbookPathLockKey(ctx, key, keys[key], log)
		if err != nil {
			for _, release := range slices.Backward(releases) {
				release()
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	return func() {
		for _, release := range slices.Backward(releases) {
			release()
		}
	}, nil
}

func acquireWorkbookPathLockKey(ctx context.Context, key, path string, log func(string)) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workbookPathLocks.Lock()
	entry := workbookPathLocks.entries[key]
	if entry == nil {
		entry = &workbookPathLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		workbookPathLocks.entries[key] = entry
	}
	entry.refs++
	workbookPathLocks.Unlock()

	select {
	case <-entry.token:
	default:
		if log != nil {
			log(fmt.Sprintf("%s is being written by another step; waiting for it to finish", Base(path)))
		}
		select {
		case <-ctx.Done():
			releaseWorkbookPathLock(key, entry, false)
			return nil, ctx.Err()
		case <-entry.token:
		}
	}
	return func() { releaseWorkbookPathLock(key, entry, true) }, nil
}

func releaseWorkbookPathLock(key string, entry *workbookPathLock, held bool) {
	if held {
		entry.token <- struct{}{}
	}
	workbookPathLocks.Lock()
	entry.refs--
	if entry.refs == 0 && workbookPathLocks.entries[key] == entry {
		delete(workbookPathLocks.entries, key)
	}
	workbookPathLocks.Unlock()
}

// workbookPathLockKey names the file a path reaches: symbolic links are
// resolved and, where the filesystem ignores case, case is folded.
func workbookPathLockKey(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := fileutil.ResolveExistingAncestor(path); err == nil {
		path = resolved
	}
	if fileutil.IsCaseInsensitiveFS(path) {
		path = strings.ToLower(path)
	}
	return path
}
