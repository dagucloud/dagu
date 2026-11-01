// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// changeMarkerSuffix ends the name of every change marker file.
const changeMarkerSuffix = ".changed"

// staleMarkerAge is how old a marker file must be before readers ignore it and
// a bump removes it, so files left behind by decommissioned hosts or pods
// neither count nor pile up.
const staleMarkerAge = 7 * 24 * time.Hour

// unknownHostID names the marker file of a host whose name could not be read.
const unknownHostID = "unknown"

// localHostName is the name of this host, or "" when it is unknown.
var localHostName = sync.OnceValue(func() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(host)
})

// ChangeMarkerPath returns this host's change marker for the DAG directory
// baseDir, kept in indexDir next to the markers of other hosts. It returns ""
// when indexDir is empty, which disables the marker.
func ChangeMarkerPath(indexDir, baseDir string) string {
	return changeMarkerPathForHost(indexDir, baseDir, localHostName())
}

func changeMarkerPathForHost(indexDir, baseDir, host string) string {
	if indexDir == "" {
		return ""
	}
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(baseDir)))
	return filepath.Join(indexDir, hex.EncodeToString(sum[:8])+"."+hostID(host)+changeMarkerSuffix)
}

// hostID names a host in a marker file name.
func hostID(host string) string {
	if host == "" {
		return unknownHostID
	}
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:8])
}

// ChangeMarker signals DAG definition changes made through the Dagu store to
// readers on other hosts, whose file watches miss writes made over a shared
// network volume. It is a hint that something may have changed, never the
// source of truth.
//
// Hosts see each other's signals only when they share the data directory that
// holds the markers and mount the DAG directory at the same absolute path.
// Changes made outside the store, such as git sync or direct file edits, are
// not signalled. Hosts that share a host name, or whose name is unknown, share
// one marker.
type ChangeMarker struct {
	path string
}

// NewChangeMarker returns a marker backed by path, as returned by
// ChangeMarkerPath. An empty path disables it.
func NewChangeMarker(path string) *ChangeMarker {
	return &ChangeMarker{path: path}
}

// Bump records that DAG definitions changed and removes stale markers.
// Failures are logged and ignored.
func (m *ChangeMarker) Bump(ctx context.Context) {
	if m == nil || m.path == "" {
		return
	}
	token, err := newChangeToken()
	if err != nil {
		logger.Warn(ctx, "Failed to create DAG change marker value", tag.Error(err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0750); err != nil {
		logger.Warn(ctx, "Failed to update DAG change marker", tag.File(m.path), tag.Error(err))
		return
	}
	if err := fileutil.WriteFileAtomic(m.path, []byte(token), defaultPerm); err != nil {
		logger.Warn(ctx, "Failed to update DAG change marker", tag.File(m.path), tag.Error(err))
		return
	}
	removeStaleMarkers(ctx, filepath.Dir(m.path))
}

// removeStaleMarkers deletes the marker files in dir that every reader already
// ignores. A marker its host rewrites between the age check and the removal
// loses that one signal, which the readers' periodic resync covers.
func removeStaleMarkers(ctx context.Context, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), changeMarkerSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) <= staleMarkerAge {
			continue
		}
		// Not fileutil.Remove: its Windows retries would stall the DAG write
		// that triggered the bump.
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.Debug(ctx, "Failed to remove stale DAG change marker", tag.File(path), tag.Error(err))
		}
	}
}

// markerVersions maps marker file names to the file info seen last.
type markerVersions map[string]fs.FileInfo

// poll lists the marker directory and returns the versions to remember and
// whether the marker of any other host is new or changed since last. This
// host's own marker is recorded but never counts, unless the host is unknown.
// Removed markers are dropped without counting, markers not modified for
// staleMarkerAge are ignored, and an unreadable directory counts as unchanged.
//
// Only the directory is listed; the markers are never opened. On NFS, open
// and stat can serve attributes cached for up to a minute, while a directory
// listing with READDIRPLUS (the Linux default) refreshes them.
func (m *ChangeMarker) poll(ctx context.Context, last markerVersions) (markerVersions, bool) {
	if m == nil || m.path == "" {
		return last, false
	}
	entries, err := os.ReadDir(filepath.Dir(m.path))
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Debug(ctx, "Failed to list DAG change marker directory", tag.File(m.path), tag.Error(err))
		}
		return last, false
	}

	own := filepath.Base(m.path)
	prefix, _, _ := strings.Cut(own, ".")
	prefix += "."
	ignoreOwn := !strings.HasSuffix(own, "."+unknownHostID+changeMarkerSuffix)

	next := make(markerVersions)
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		if len(name) <= len(prefix)+len(changeMarkerSuffix) ||
			!strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, changeMarkerSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) > staleMarkerAge {
			continue
		}
		next[name] = info
		if name == own && ignoreOwn {
			continue
		}
		if prev, ok := last[name]; !ok || !sameMarkerVersion(prev, info) {
			changed = true
		}
	}
	return next, changed
}

// sameMarkerVersion reports whether two infos describe the same version of a
// marker file: an atomic rewrite replaces the file, so its identity or time
// changes.
func sameMarkerVersion(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}

func newChangeToken() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(random[:])), nil
}
