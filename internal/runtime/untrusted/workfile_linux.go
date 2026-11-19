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
	"strings"

	"golang.org/x/sys/unix"
)

// OpenWorkFile opens a file that Dagu reads or writes for a step, such as a
// redirect of stdout or a file of stdin, in a run with an untrusted step. When
// path is in the work directory of the run, no part of it below the work
// directory can be a symbolic link: an untrusted step could have made the link
// to a file of the worker. Outside the work directory, it opens path as
// os.OpenFile does.
func OpenWorkFile(workDir, path string, flags int, perm fs.FileMode) (*os.File, error) {
	rel, base, ok := workRelative(workDir, path)
	if !ok {
		return os.OpenFile(path, flags, perm) // #nosec G304 -- the path comes from the workflow.
	}
	dir, err := os.Open(base) // #nosec G304 -- the work directory of the run.
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	how := unix.OpenHow{
		// O_NONBLOCK: an open of a FIFO that an untrusted step made does not wait.
		Flags:   uint64(flags) | unix.O_CLOEXEC | unix.O_NONBLOCK, // #nosec G115 -- open flags are not negative.
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_BENEATH,
	}
	// openat2 refuses a mode for an open that makes no file, where os.OpenFile
	// ignores it.
	if flags&unix.O_CREAT != 0 || flags&unix.O_TMPFILE == unix.O_TMPFILE {
		how.Mode = uint64(perm.Perm())
	}
	fd, err := unix.Openat2(int(dir.Fd()), rel, &how) // #nosec G115 -- a file descriptor fits in int.
	if errors.Is(err, unix.ENOSYS) {
		return openWorkFileWalk(base, rel, flags, perm)
	}
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EXDEV) {
			return nil, symlinkError(path)
		}
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return regularWorkFile(fd, path)
}

func symlinkError(path string) error {
	return fmt.Errorf("open %s: the path has a symbolic link below the work directory of the run, which an untrusted step could have made", path)
}

// regularWorkFile keeps the file of fd when it is a regular file, and clears the
// flag O_NONBLOCK of the open. A FIFO or a device in the work directory can come
// from an untrusted step: a read or a write of it could block the step for ever.
func regularWorkFile(fd int, path string) (*os.File, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %s: not a regular file; an untrusted step could have made it", path)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openWorkFileWalk is OpenWorkFile for a kernel without openat2. It opens each
// folder of the path relative to the descriptor of the folder before it, with
// O_NOFOLLOW, and the last part relative to the last folder. So no part can
// become a link between a check and the open.
func openWorkFileWalk(base, rel string, flags int, perm fs.FileMode) (*os.File, error) {
	path := filepath.Join(base, rel)
	dirfd, err := unix.Open(base, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: base, Err: err}
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(dirfd, part, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			link := hasChild(dirfd, part, unix.S_IFLNK)
			_ = unix.Close(dirfd)
			if link {
				return nil, symlinkError(path)
			}
			return nil, &fs.PathError{Op: "open", Path: path, Err: err}
		}
		_ = unix.Close(dirfd)
		dirfd = next
	}
	defer func() { _ = unix.Close(dirfd) }()
	fd, err := unix.Openat(dirfd, parts[len(parts)-1], flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, uint32(perm.Perm()))
	if errors.Is(err, unix.ELOOP) {
		return nil, symlinkError(path)
	}
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return regularWorkFile(fd, path)
}

// workRelative returns path relative to the work directory, and the work
// directory to open. ok is false when path is not in the work directory.
func workRelative(workDir, path string) (rel, base string, ok bool) {
	if workDir == "" || !filepath.IsAbs(path) {
		return "", "", false
	}
	path = filepath.Clean(path)
	candidates := []string{filepath.Clean(workDir)}
	if real, err := filepath.EvalSymlinks(workDir); err == nil {
		candidates = append(candidates, real)
	}
	for _, candidate := range candidates {
		r, err := filepath.Rel(candidate, path)
		if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
			continue
		}
		return r, candidate, true
	}
	return "", "", false
}
