// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"golang.org/x/sys/unix"
)

// The walks of the handoff and of the restore act on file descriptors. Each
// entry is opened with O_PATH and O_NOFOLLOW relative to the descriptor of its
// folder, and each change of owner or mode goes through the descriptor of the
// entry. So no change follows a path that a process could swap for a link.

// fsEntry is one entry of a walk.
type fsEntry struct {
	// path is the path of the entry, for the errors and the checks of place.
	path string
	name string
	// parent is the descriptor of the folder of the entry, or -1 for the root.
	parent int
	// fd is a descriptor of the entry with O_PATH and O_NOFOLLOW.
	fd int
	st unix.Stat_t
}

func (e *fsEntry) isDir() bool     { return e.st.Mode&unix.S_IFMT == unix.S_IFDIR }
func (e *fsEntry) isLink() bool    { return e.st.Mode&unix.S_IFMT == unix.S_IFLNK }
func (e *fsEntry) isRegular() bool { return e.st.Mode&unix.S_IFMT == unix.S_IFREG }
func (e *fsEntry) uid() int        { return int(e.st.Uid) }
func (e *fsEntry) gid() int        { return int(e.st.Gid) }

// perm returns the permission bits and the bits set-user-ID, set-group-ID and
// sticky of the entry, in the format of chmod.
func (e *fsEntry) perm() uint32 { return e.st.Mode & 0o7777 }

// visitFunc is called for each entry. It returns skip to stay out of a folder.
type visitFunc func(e *fsEntry) (skip bool, err error)

// walkFD calls visit for root and for each entry below it, in the order of the
// names. The descriptors are valid only during the call of visit.
func walkFD(root string, visit visitFunc) error {
	fd, err := unix.Open(root, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return &fs.PathError{Op: "open", Path: root, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	entry := &fsEntry{path: root, name: filepath.Base(root), parent: -1, fd: fd}
	if err := unix.Fstat(fd, &entry.st); err != nil {
		return &fs.PathError{Op: "stat", Path: root, Err: err}
	}
	return walkEntry(entry, visit)
}

func walkEntry(entry *fsEntry, visit visitFunc) error {
	skip, err := visit(entry)
	if err != nil || skip || !entry.isDir() {
		return err
	}
	names, err := readNames(entry.fd, entry.path)
	if err != nil {
		return err
	}
	for _, name := range names {
		child, err := openChild(entry.fd, entry.path, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = walkEntry(child, visit)
		_ = unix.Close(child.fd)
		if err != nil {
			return err
		}
	}
	return nil
}

// openChild opens the entry name of the folder dirfd with O_PATH and
// O_NOFOLLOW. The caller closes the descriptor.
func openChild(dirfd int, dirPath, name string) (*fsEntry, error) {
	path := filepath.Join(dirPath, name)
	fd, err := unix.Openat(dirfd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	entry := &fsEntry{path: path, name: name, parent: dirfd, fd: fd}
	if err := unix.Fstat(fd, &entry.st); err != nil {
		_ = unix.Close(fd)
		return nil, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	return entry, nil
}

// readNames returns the names of the folder that the O_PATH descriptor fd
// names, sorted.
func readNames(fd int, path string) ([]string, error) {
	dirfd, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	dir := os.NewFile(uintptr(dirfd), path)
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	sort.Strings(names)
	return names, nil
}

// hasChild reports whether the folder fd has the entry name, of the type mode
// (unix.S_IFDIR, unix.S_IFREG) when mode is not 0. A link is not followed.
func hasChild(fd int, name string, mode uint32) bool {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return mode == 0 || st.Mode&unix.S_IFMT == mode
}

// chownFD changes the owner of the entry of the descriptor. A link keeps its
// target.
func chownFD(entry *fsEntry, uid, gid int) error {
	if err := unix.Fchownat(entry.fd, "", uid, gid, unix.AT_EMPTY_PATH); err != nil {
		return &fs.PathError{Op: "chown", Path: entry.path, Err: err}
	}
	return unix.Fstat(entry.fd, &entry.st)
}

// chmodFD changes the mode of the entry of the descriptor. The entry is not a
// link. The change goes through /proc/self/fd, which names the inode of the
// descriptor, not a path.
func chmodFD(entry *fsEntry, mode uint32) error {
	return chmodFDPath(entry.fd, entry.path, mode)
}

func chmodFDPath(fd int, path string, mode uint32) error {
	if err := unix.Fchmodat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), mode, 0); err != nil {
		return &fs.PathError{Op: "chmod", Path: path, Err: err}
	}
	return nil
}

// removeAt removes the entry name of the folder dirfd and its content. A link
// is removed, not followed.
func removeAt(dirfd int, dirPath, name string) error {
	child, err := openChild(dirfd, dirPath, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(child.fd) }()
	if !child.isDir() {
		if err := unix.Unlinkat(dirfd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return &fs.PathError{Op: "remove", Path: child.path, Err: err}
		}
		return nil
	}
	names, err := readNames(child.fd, child.path)
	if err != nil {
		return err
	}
	for _, sub := range names {
		if err := removeAt(child.fd, child.path, sub); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "remove", Path: child.path, Err: err}
	}
	return nil
}

// fileSig identifies the state of an entry: Dagu compares it at the restore
// with the state at the handoff.
type fileSig struct {
	ino   uint64
	mode  uint32
	size  int64
	mtime int64
	ctime int64
	nlink uint64
}

func sigOf(st *unix.Stat_t) fileSig {
	return fileSig{
		ino:   st.Ino,
		mode:  st.Mode,
		size:  st.Size,
		mtime: st.Mtim.Nano(),
		ctime: st.Ctim.Nano(),
		nlink: uint64(st.Nlink), // #nosec G115 -- a link count is not negative.
	}
}

// describeSig tells how two states differ, for an error.
func describeSig(before, after fileSig) string {
	switch {
	case before.ino != after.ino:
		return "it is a new file"
	case before.mode != after.mode:
		return fmt.Sprintf("its mode is %o, not %o", after.mode, before.mode)
	default:
		return "its content changed"
	}
}
