// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/shirou/gopsutil/v4/disk"
)

const (
	dirPerm = 0o750
	// copySuffix names the directory a copy fills before it takes the
	// destination's name, so a destination never holds part of a copy.
	copySuffix = ".migrating"
)

// mover moves state by rename, and by copy to another file system.
type mover struct {
	sameFileSystem func(a, b string) (bool, error)
	freeSpace      func(path string) (uint64, error)
	// beforeInstall, when set by tests, runs after a copy is complete and
	// before it takes the destination's name.
	beforeInstall func() error
}

// pendingCopy is a move to another file system and the bytes it copies.
type pendingCopy struct {
	move
	size uint64
}

func newMover() mover {
	return mover{sameFileSystem: fileutil.SameFileSystem, freeSpace: freeSpace}
}

// move moves mv.From to mv.To. It finishes a move that an earlier attempt
// stopped partway: a missing source was moved already, and a destination
// beside its source is a copy that was installed before the source was
// removed. The plan guarantees that no destination existed before.
func (mr mover) move(ctx context.Context, mv move) error {
	if _, err := os.Lstat(mv.From); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if _, err := os.Lstat(mv.To); err == nil {
		return fileutil.RemoveAll(mv.From)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	parent := filepath.Dir(mv.To)
	if err := fileutil.MkdirAll(parent, dirPerm); err != nil {
		return err
	}
	same, err := mr.sameFileSystem(mv.From, parent)
	if err != nil {
		return err
	}
	if same {
		return fileutil.Rename(mv.From, mv.To)
	}
	return mr.copyMove(ctx, mv)
}

// copyMove copies mv.From beside mv.To, gives the copy mv.To's name once
// it is durable, and then removes mv.From.
func (mr mover) copyMove(ctx context.Context, mv move) error {
	staging := mv.To + copySuffix
	// A killed attempt may have left part of a copy.
	if err := fileutil.RemoveAll(staging); err != nil {
		return err
	}
	if err := copyTree(ctx, mv.From, staging); err != nil {
		return err
	}
	if mr.beforeInstall != nil {
		if err := mr.beforeInstall(); err != nil {
			return err
		}
	}
	if err := fileutil.Rename(staging, mv.To); err != nil {
		return err
	}
	if err := fileutil.SyncDir(filepath.Dir(mv.To)); err != nil {
		return err
	}
	return fileutil.RemoveAll(mv.From)
}

// planCopies returns the moves that copy to another file system, with the
// bytes each copies.
func (mr mover) planCopies(moves []move) ([]pendingCopy, error) {
	var copies []pendingCopy
	for _, mv := range moves {
		if _, err := os.Lstat(mv.From); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		same, err := mr.sameFileSystem(mv.From, mv.To)
		if err != nil {
			return nil, err
		}
		if same {
			continue
		}
		size, err := treeSize(mv.From)
		if err != nil {
			return nil, err
		}
		copies = append(copies, pendingCopy{move: mv, size: size})
	}
	return copies, nil
}

// shortage returns the first file system that lacks the space of every
// copy to it, or nil when each fits.
func (mr mover) shortage(copies []pendingCopy) (*SpaceShortage, error) {
	for i, c := range copies {
		needed := c.size
		for j, other := range copies {
			if i == j {
				continue
			}
			same, err := mr.sameFileSystem(c.To, other.To)
			if err != nil {
				return nil, err
			}
			if same {
				needed += other.size
			}
		}
		free, err := mr.freeSpace(c.To)
		if err != nil {
			return nil, fmt.Errorf("check free space for %s: %w", c.To, err)
		}
		if free < needed {
			return &SpaceShortage{Path: filepath.Dir(c.To), Needed: needed, Free: free}, nil
		}
	}
	return nil, nil
}

// freeSpace returns the bytes free for an unprivileged writer on the file
// system that holds path, or would hold it once created.
func freeSpace(path string) (uint64, error) {
	existing, err := existingAncestor(path)
	if err != nil {
		return 0, err
	}
	usage, err := disk.Usage(existing)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}

func existingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		_, err := os.Stat(current)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return current, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		current = parent
	}
}

// treeSize returns the bytes of the regular files under root.
func treeSize(root string) (uint64, error) {
	var size uint64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += uint64(info.Size()) //nolint:gosec // a file size is never negative
		return nil
	})
	return size, err
}

// copyTree copies the file or directory src to dst, which must not exist.
// It keeps permissions and modification times, which some stores read, and
// syncs each file so the copy outlasts a crash.
func copyTree(ctx context.Context, src, dst string) error {
	type dirTime struct {
		path    string
		modTime time.Time
	}
	// A directory's time is set once its entries are in place, since
	// adding them changes it.
	var dirs []dirTime
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			dirs = append(dirs, dirTime{path: target, modTime: info.ModTime()})
			return fileutil.MkdirAll(target, info.Mode().Perm())
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target) //nolint:gosec // no other process writes the state while it migrates
		case entry.Type().IsRegular():
			if err := copyFile(path, target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		default:
			return fmt.Errorf("copy %s: not a regular file, directory, or symbolic link", path)
		}
	})
	if err != nil {
		return err
	}
	for _, dir := range slices.Backward(dirs) {
		if err := os.Chtimes(dir.path, dir.modTime, dir.modTime); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, perm fs.FileMode) (err error) {
	in, err := fileutil.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // dst lies in a migration destination
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
