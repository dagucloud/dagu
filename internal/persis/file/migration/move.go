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
)

const (
	dirPerm = 0o750
	// copySuffix names the directory a copy fills before it takes the
	// destination's name, so a destination never holds part of a copy. A
	// store writes "%" only to escape a byte, never before "m", so no state
	// has a name with this suffix; checkPlan refuses a plan where one would.
	copySuffix = "%migrating"
	// allocationUnit is the block size of common file systems. A copy takes
	// at least one per file and per directory, whatever their length.
	allocationUnit = 4096
)

// mover moves state by rename, and by copy to another file system.
type mover struct {
	sameFileSystem func(a, b string) (bool, error)
	freeSpace      func(path string) (uint64, error)
	// beforeInstall, when set by tests, runs after a copy is complete and
	// before it takes the destination's name.
	beforeInstall func() error
}

// pendingCopy is a move to another file system and the space it needs.
type pendingCopy struct {
	move
	size uint64
}

// installRecord is the journal's record of a move's copy. The copy is
// recorded before it takes the destination's name, so a resumed move tells
// its own copy from state that the migration did not make.
type installRecord struct {
	// recorded reports that an earlier attempt recorded the copy.
	recorded bool
	// record records the copy.
	record func() error
}

// errForeignDestination reports a destination beside its source that the
// journal records no copy for.
var errForeignDestination = errors.New("the destination exists, and the journal records no copy there")

func newMover() mover {
	return mover{sameFileSystem: fileutil.SameFileSystem, freeSpace: fileutil.FreeSpace}
}

// move moves mv.From to mv.To. It finishes a move that an earlier attempt
// stopped partway: a missing source was moved already, and a destination
// beside its source is that attempt's copy when install says the journal
// recorded it. Any other destination beside its source is an error, and the
// source stays. held is called before each step that changes state, and an
// error from it stops the move.
func (mr mover) move(ctx context.Context, mv move, install installRecord, held func() error) error {
	if _, err := os.Lstat(mv.From); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	found, err := exists(mv.To)
	if err != nil {
		return err
	}
	if found {
		if !install.recorded {
			return fmt.Errorf("%s: %w", mv.To, errForeignDestination)
		}
		if err := held(); err != nil {
			return err
		}
		return fileutil.RemoveAll(mv.From)
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
		if err := held(); err != nil {
			return err
		}
		return fileutil.Rename(mv.From, mv.To)
	}
	return mr.copyMove(ctx, mv, install.record, held)
}

// copyMove copies mv.From beside mv.To, records the copy once it is
// durable, gives it mv.To's name, and then removes mv.From. A copy that
// fills its file system is discarded, so its space is free again while the
// caller waits.
func (mr mover) copyMove(ctx context.Context, mv move, record func() error, held func() error) error {
	staging := mv.To + copySuffix
	if err := discardStaging(staging, held); err != nil {
		return err
	}
	err := copyTree(ctx, mv.From, staging, held)
	if err == nil && mr.beforeInstall != nil {
		err = mr.beforeInstall()
	}
	if isNoSpace(err) {
		return errors.Join(err, discardStaging(staging, held))
	}
	if err != nil {
		return err
	}
	if err := held(); err != nil {
		return err
	}
	if err := record(); err != nil {
		return err
	}
	if err := fileutil.Rename(staging, mv.To); err != nil {
		return err
	}
	if err := fileutil.SyncDir(filepath.Dir(mv.To)); err != nil {
		return err
	}
	if err := held(); err != nil {
		return err
	}
	return fileutil.RemoveAll(mv.From)
}

// discardStaging removes the part of a copy that a killed attempt left.
func discardStaging(staging string, held func() error) error {
	found, err := exists(staging)
	if err != nil || !found {
		return err
	}
	if err := held(); err != nil {
		return err
	}
	return fileutil.RemoveAll(staging)
}

// pendingCopies returns the moves that still copy to another file system,
// with the space each needs: its estimate, or its floor when larger. It
// discards the part of a copy that a killed attempt left, so its space
// counts as free.
func (mr mover) pendingCopies(moves []move, floors map[string]uint64, held func() error) ([]pendingCopy, error) {
	var copies []pendingCopy
	for _, mv := range moves {
		if _, err := os.Lstat(mv.From); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// A destination that exists needs no room: the move removes the
		// source of an installed copy, or refuses.
		found, err := exists(mv.To)
		if err != nil {
			return nil, err
		}
		if found {
			continue
		}
		same, err := mr.sameFileSystem(mv.From, mv.To)
		if err != nil {
			return nil, err
		}
		if same {
			continue
		}
		if err := discardStaging(mv.To+copySuffix, held); err != nil {
			return nil, err
		}
		size, err := copySize(mv.From)
		if err != nil {
			return nil, err
		}
		copies = append(copies, pendingCopy{move: mv, size: max(size, floors[mv.To])})
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

// exists reports whether an entry, a dangling symbolic link included, is at
// path.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// copySize estimates the space a copy of root takes on any common file
// system: each file rounded up to whole allocation units, one unit for each
// directory and symbolic link, and a sixteenth more for the metadata the file
// system keeps.
func copySize(root string) (uint64, error) {
	var size uint64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			size += allocationUnit
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		length := uint64(info.Size()) //nolint:gosec // a file size is never negative
		size += max(1, (length+allocationUnit-1)/allocationUnit) * allocationUnit
		return nil
	})
	return size + size/16, err
}

// copyTree copies the file or directory src to dst, which must not exist.
// It keeps permissions and modification times, which some stores read, and
// syncs each file so the copy outlasts a crash.
func copyTree(ctx context.Context, src, dst string, held func() error) error {
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
		if err := held(); err != nil {
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
