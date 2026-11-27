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
	// depth counts the folders between the root of the walk and the entry.
	depth int
	st    unix.Stat_t
}

func (e *fsEntry) isDir() bool     { return e.st.Mode&unix.S_IFMT == unix.S_IFDIR }
func (e *fsEntry) isLink() bool    { return e.st.Mode&unix.S_IFMT == unix.S_IFLNK }
func (e *fsEntry) isRegular() bool { return e.st.Mode&unix.S_IFMT == unix.S_IFREG }
func (e *fsEntry) isSocket() bool  { return e.st.Mode&unix.S_IFMT == unix.S_IFSOCK }
func (e *fsEntry) uid() int        { return int(e.st.Uid) }
func (e *fsEntry) gid() int        { return int(e.st.Gid) }

// isDevice reports whether the entry is a character or a block device node.
func (e *fsEntry) isDevice() bool {
	return e.st.Mode&unix.S_IFMT == unix.S_IFCHR || e.st.Mode&unix.S_IFMT == unix.S_IFBLK
}

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

// maxWalkDepth bounds the folders that a walk or a removal enters below its
// root. Each level holds a descriptor and a path, so a deeper tree that the
// restricted user made could exhaust the descriptors or the memory of the
// worker.
const maxWalkDepth = 256

// errTooDeep is the error of a folder deeper than maxWalkDepth.
var errTooDeep = fmt.Errorf("the tree is deeper than %d folders", maxWalkDepth)

// walkEntry walks the tree of an entry of a walk. The depth of each entry
// counts from the root of that walk.
func walkEntry(entry *fsEntry, visit visitFunc) error {
	skip, err := visit(entry)
	if err != nil || skip || !entry.isDir() {
		return err
	}
	if entry.depth >= maxWalkDepth {
		return &fs.PathError{Op: "walk", Path: entry.path, Err: errTooDeep}
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
		child.depth = entry.depth + 1
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

// readLinkFD returns the target of the link entry.
func readLinkFD(entry *fsEntry) (string, error) {
	buf := make([]byte, unix.PathMax)
	n, err := unix.Readlinkat(entry.fd, "", buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

// aclNames are the extended attributes that hold the POSIX ACLs of an entry.
var aclNames = []string{"system.posix_acl_access", "system.posix_acl_default"}

// hasACL reports whether the entry has an access ACL, which can let the
// restricted user write it whatever its mode. An error that does not show the
// lack of one counts as one. A link has none.
func hasACL(entry *fsEntry) bool {
	if entry.isLink() {
		return false
	}
	_, err := unix.Getxattr("/proc/self/fd/"+strconv.Itoa(entry.fd), aclNames[0], nil)
	return !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.EOPNOTSUPP)
}

// removeACLs removes the POSIX ACLs of the entry of the descriptor. The entry
// is not a link. The change goes through /proc/self/fd, as chmodFD does.
func removeACLs(entry *fsEntry) error {
	for _, name := range aclNames {
		err := unix.Removexattr("/proc/self/fd/"+strconv.Itoa(entry.fd), name)
		if err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.EOPNOTSUPP) {
			return &fs.PathError{Op: "removexattr", Path: entry.path, Err: err}
		}
	}
	return nil
}

// removeAt removes the entry name of the folder dirfd and its content. A link
// is removed, not followed.
func removeAt(dirfd int, dirPath, name string) error {
	return removeDepth(dirfd, dirPath, name, 0)
}

func removeDepth(dirfd int, dirPath, name string, depth int) error {
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
	if depth >= maxWalkDepth {
		return &fs.PathError{Op: "remove", Path: child.path, Err: errTooDeep}
	}
	names, err := readNames(child.fd, child.path)
	if err != nil {
		return err
	}
	for _, sub := range names {
		if err := removeDepth(child.fd, child.path, sub, depth+1); err != nil {
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
	uid   uint32
	gid   uint32
	size  int64
	mtime int64
	ctime int64
	nlink uint64
}

func sigOf(st *unix.Stat_t) fileSig {
	return fileSig{
		ino:   st.Ino,
		mode:  st.Mode,
		uid:   st.Uid,
		gid:   st.Gid,
		size:  st.Size,
		mtime: st.Mtim.Nano(),
		ctime: st.Ctim.Nano(),
		nlink: uint64(st.Nlink), // #nosec G115 -- a link count is not negative.
	}
}

// sameSig reports whether an entry kept the state before. For a file that had
// more than one link, as git clone --local makes them, it compares no link
// count and no time: another path of the file, such as one of another clone,
// changes them, as git does when it refreshes a pack. The restricted user
// cannot write such a file (protectGitEntry).
func sameSig(before, after fileSig) bool {
	if before.mode&unix.S_IFMT != unix.S_IFDIR && before.nlink > 1 {
		after.nlink, after.mtime, after.ctime = before.nlink, before.mtime, before.ctime
	}
	return before == after
}

// describeSig tells how two states differ, for an error.
func describeSig(before, after fileSig) string {
	switch {
	case before.ino != after.ino:
		return "it is a new file"
	case before.mode != after.mode:
		return fmt.Sprintf("its mode is %o, not %o", after.mode, before.mode)
	case before.uid != after.uid || before.gid != after.gid:
		return fmt.Sprintf("its owner is %d:%d, not %d:%d", after.uid, after.gid, before.uid, before.gid)
	default:
		return "its content changed"
	}
}
