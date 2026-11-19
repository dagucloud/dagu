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
	"syscall"

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
		Mode:    uint64(perm.Perm()),
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_BENEATH,
	}
	fd, err := unix.Openat2(int(dir.Fd()), rel, &how) // #nosec G115 -- a file descriptor fits in int.
	if errors.Is(err, unix.ENOSYS) {
		return openWorkFileWalk(base, rel, flags, perm)
	}
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EXDEV) {
			return nil, fmt.Errorf("open %s: the path has a symbolic link below the work directory of the run, which an untrusted step could have made", path)
		}
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return regularWorkFile(fd, path)
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

// openWorkFileWalk is OpenWorkFile for a kernel without openat2: it checks
// each part of the path, then opens the last part with O_NOFOLLOW.
func openWorkFileWalk(base, rel string, flags int, perm fs.FileMode) (*os.File, error) {
	current := base
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			break
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("open %s: the path has the symbolic link %s below the work directory of the run", filepath.Join(base, rel), current)
		}
	}
	path := filepath.Join(base, rel)
	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm) // #nosec G304 -- checked above.
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("open %s: not a regular file; an untrusted step could have made it", path)
	}
	if err == nil {
		err = unix.SetNonblock(int(file.Fd()), false) // #nosec G115 -- a file descriptor fits in int.
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
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
