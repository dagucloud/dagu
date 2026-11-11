// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Spec 092, "Files". The handoff gives the work directory to the restricted
// user, except the folders that git opens as a repository (the git folders)
// and the entries named .git. These stay with the worker, readable and not
// writable by the restricted user. The work directory and each folder on the
// way to a git folder or to a .git keep the owner of the worker with the mode
// 1777, so that the restricted user cannot rename or replace them.
//
// The restore gives the work directory back to the worker. It fails the step
// for what an agent could leave for a later trusted step:
//
//   - a new .git, or a new folder that git opens as a repository: Dagu removes
//     the .git, or the files HEAD, commondir and gitdir of the folder;
//   - a change in a git folder: the run stops, because Dagu cannot undo it;
//   - a link of the restricted user to a path outside the work directory or
//     into a git folder, and a file of the restricted user with more than one
//     link: Dagu removes it.

// protectedDir is a folder whose mode the isolation changed. The descriptor
// keeps the folder for the restore of its mode.
type protectedDir struct {
	fd   int
	mode uint32
}

// gitMarkers are the files that make a folder a git folder.
var gitMarkers = []string{"HEAD", "commondir", "gitdir"}

// isGitFolder reports whether git opens the folder fd as a repository: it has
// HEAD, objects and refs, or a file commondir or gitdir.
func isGitFolder(fd int) bool {
	if hasChild(fd, "commondir", 0) || hasChild(fd, "gitdir", unix.S_IFREG) {
		return true
	}
	return hasChild(fd, "HEAD", 0) && hasChild(fd, "objects", unix.S_IFDIR) && hasChild(fd, "refs", unix.S_IFDIR)
}

// insideWork reports whether path is the work directory or a path below it.
func (iso *isolation) insideWork(path string) bool {
	return within(iso.workDir, path) || within(iso.lexicalWork, path)
}

// insideGit reports whether path is a git folder, a path in one, or an entry
// .git of the handoff.
func (iso *isolation) insideGit(path string) bool {
	for dir := range iso.gitDirs {
		if within(dir, path) {
			return true
		}
	}
	return iso.gitEntries[path]
}

// resolveGitPointer returns the git folder that the file of path names with
// the line "<key>: <path>" (a .git file) or with its whole text (commondir).
func resolveGitPointer(path string, prefix string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- a file of the work directory, read as data.
	if err != nil {
		return "", err
	}
	text := string(bytes.TrimSpace(data))
	if prefix != "" {
		after, ok := strings.CutPrefix(text, prefix)
		if !ok {
			return "", fmt.Errorf("the file has no line %q", strings.TrimSpace(prefix))
		}
		text = strings.TrimSpace(after)
	}
	if text == "" {
		return "", errors.New("the file names no folder")
	}
	if !filepath.IsAbs(text) {
		text = filepath.Join(filepath.Dir(path), text)
	}
	return filepath.EvalSymlinks(filepath.Clean(text))
}

// discoverGit finds the entries .git and the git folders of the work
// directory. A git folder that a .git names must be in the work directory.
func (iso *isolation) discoverGit() error {
	addDir := func(dir, from string) error {
		if !iso.insideWork(dir) {
			return fmt.Errorf("untrusted step %q: %s names the git folder %s, which is not in the work directory of the run", iso.step, from, dir)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return stepError(iso.step, "read the git folder", dir, err)
		}
		if !info.IsDir() {
			return stepError(iso.step, "read the git folder", dir, errors.New("not a folder"))
		}
		iso.gitDirs[dir] = true
		common := filepath.Join(dir, "commondir")
		if _, err := os.Lstat(common); err == nil {
			target, err := resolveGitPointer(common, "")
			if err != nil {
				return stepError(iso.step, "read", common, err)
			}
			if !iso.insideWork(target) {
				return fmt.Errorf("untrusted step %q: %s names the git folder %s, which is not in the work directory of the run", iso.step, common, target)
			}
			iso.gitDirs[target] = true
		}
		return nil
	}
	return walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
		if e.path == iso.workDir {
			return false, nil
		}
		if e.name == ".git" {
			iso.gitEntries[e.path] = true
			switch {
			case e.isDir():
				return true, addDir(e.path, e.path)
			case e.isRegular():
				target, err := resolveGitPointer(e.path, "gitdir:")
				if err != nil {
					return true, stepError(iso.step, "read", e.path, err)
				}
				return true, addDir(target, e.path)
			case e.isLink():
				target, err := filepath.EvalSymlinks(e.path)
				if err != nil {
					return true, stepError(iso.step, "resolve", e.path, err)
				}
				return true, addDir(target, e.path)
			}
			return true, nil
		}
		if e.isDir() && isGitFolder(e.fd) {
			return true, addDir(e.path, e.path)
		}
		return false, nil
	})
}

// protectedPaths returns the work directory and each folder on the way to a
// git folder or to an entry .git.
func (iso *isolation) protectedPaths() map[string]bool {
	protected := map[string]bool{iso.workDir: true}
	mark := func(path string) {
		for dir := filepath.Dir(path); within(iso.workDir, dir); dir = filepath.Dir(dir) {
			protected[dir] = true
			if dir == iso.workDir {
				break
			}
		}
	}
	for dir := range iso.gitDirs {
		mark(dir)
	}
	for entry := range iso.gitEntries {
		mark(entry)
	}
	return protected
}

// handoff gives the work directory to the restricted user.
func (iso *isolation) handoff() error {
	if err := iso.discoverGit(); err != nil {
		return err
	}
	protected := iso.protectedPaths()
	return walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
		switch {
		case iso.gitDirs[e.path]:
			return true, iso.protectGitTree(e)
		case iso.gitEntries[e.path]:
			return true, iso.protectGitEntry(e)
		case protected[e.path]:
			return false, iso.protectDir(e)
		default:
			if err := chownFD(e, int(iso.uid), int(iso.gid)); err != nil {
				return false, stepError(iso.step, "change the owner of", e.path, err)
			}
			return false, nil
		}
	})
}

// protectDir keeps the folder for the user of the worker with the mode 1777.
func (iso *isolation) protectDir(e *fsEntry) error {
	if !e.isDir() {
		return stepError(iso.step, "read", e.path, errors.New("not a folder"))
	}
	if e.uid() != iso.workerUID || e.gid() == int(iso.gid) {
		if err := chownFD(e, iso.workerUID, iso.workerGID); err != nil {
			return stepError(iso.step, "change the owner of", e.path, err)
		}
	}
	if _, done := iso.protected[e.path]; !done {
		fd, err := unix.Dup(e.fd)
		if err != nil {
			return stepError(iso.step, "keep the folder", e.path, err)
		}
		iso.protected[e.path] = protectedDir{fd: fd, mode: e.perm()}
	}
	if err := chmodFD(e, 0o1777); err != nil {
		return stepError(iso.step, "change the mode of", e.path, err)
	}
	return nil
}

// protectGitEntry keeps one entry for the user of the worker, readable and not
// writable by the restricted user, and records its state.
func (iso *isolation) protectGitEntry(e *fsEntry) error {
	if e.uid() != iso.workerUID || e.gid() == int(iso.gid) {
		if err := chownFD(e, iso.workerUID, iso.workerGID); err != nil {
			return stepError(iso.step, "change the owner of", e.path, err)
		}
	}
	if !e.isLink() {
		perm := e.perm()&^0o002 | 0o004
		if e.isDir() {
			perm |= 0o001
		}
		if perm != e.perm() {
			if err := chmodFD(e, perm); err != nil {
				return stepError(iso.step, "change the mode of", e.path, err)
			}
		}
	}
	if err := unix.Fstat(e.fd, &e.st); err != nil {
		return stepError(iso.step, "read", e.path, err)
	}
	iso.gitSnapshot[e.path] = sigOf(&e.st)
	return nil
}

// protectGitTree protects a git folder and its content.
func (iso *isolation) protectGitTree(root *fsEntry) error {
	return walkEntry(root, func(e *fsEntry) (bool, error) {
		return false, iso.protectGitEntry(e)
	})
}

// restore gives the work directory back to the user of the worker. It returns
// an error that wraps ErrRunAborted when a git folder changed. With verifyGit
// false, it checks no git entry: a handoff that failed has a part of the
// snapshot only, and no process of the restricted user ran, so each entry that
// the snapshot lacks is an entry of the worker, not a new one.
func (iso *isolation) restore(verifyGit bool) error {
	var errs []error
	fail := func(err error) { errs = append(errs, err) }
	if iso.workDir != "" {
		walkErr := walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
			switch {
			case !verifyGit && (iso.gitDirs[e.path] || iso.gitEntries[e.path] || e.name == ".git"):
				return true, nil
			case iso.gitDirs[e.path]:
				iso.checkGitTree(e, fail)
				return true, nil
			case iso.gitEntries[e.path]:
				iso.checkGitEntry(e, fail)
				return true, nil
			case e.name == ".git" && e.path != iso.workDir:
				fail(iso.removeEntry(e, "the restricted user made the entry %s"))
				return true, nil
			}
			if verifyGit && e.isDir() && e.path != iso.workDir && isGitFolder(e.fd) {
				for _, marker := range gitMarkers {
					if err := removeAt(e.fd, e.path, marker); err != nil {
						fail(fmt.Errorf("%w: %w", ErrRunAborted, stepError(iso.step, "remove", filepath.Join(e.path, marker), err)))
					}
				}
				fail(fmt.Errorf("untrusted step %q: the restricted user made the git folder %s; Dagu removed its files %s",
					iso.step, e.path, strings.Join(gitMarkers, ", ")))
			}
			if e.uid() != int(iso.uid) && e.gid() != int(iso.gid) {
				return false, nil
			}
			if e.isLink() {
				if target, bad := iso.badLink(e); bad {
					fail(iso.removeEntry(e, "the restricted user made the link %s to "+target))
					return true, nil
				}
			}
			if e.isRegular() && e.st.Nlink > 1 {
				fail(iso.removeEntry(e, "the restricted user made the file %s with more than one link"))
				return true, nil
			}
			if err := chownFD(e, iso.workerUID, iso.workerGID); err != nil {
				fail(stepError(iso.step, "change the owner of", e.path, err))
				return false, nil
			}
			if e.isRegular() && e.perm()&0o6000 != 0 {
				if err := chmodFD(e, e.perm()&0o777); err != nil {
					fail(stepError(iso.step, "change the mode of", e.path, err))
				}
			}
			return false, nil
		})
		if walkErr != nil {
			// A part of the tree was not checked: no trusted step may run on it.
			fail(fmt.Errorf("%w: %w", ErrRunAborted, stepError(iso.step, "read the work directory", iso.workDir, walkErr)))
		}
	}
	fail(iso.restoreModes())
	return errors.Join(errs...)
}

// badLink reports whether the link of the restricted user points outside the
// work directory or into a git folder. It returns the target.
func (iso *isolation) badLink(e *fsEntry) (string, bool) {
	buf := make([]byte, unix.PathMax)
	n, err := unix.Readlinkat(e.fd, "", buf)
	if err != nil {
		return "(unreadable)", true
	}
	target := string(buf[:n])
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(e.path), resolved)
	}
	resolved = filepath.Clean(resolved)
	if !iso.insideWork(resolved) || iso.insideGit(resolved) {
		return target, true
	}
	return target, false
}

// removeEntry removes the entry and returns the error of the step. format has
// one %s for the path.
func (iso *isolation) removeEntry(e *fsEntry, format string) error {
	msg := fmt.Sprintf("untrusted step %q: "+format, iso.step, e.path)
	if e.parent < 0 {
		return fmt.Errorf("%s; Dagu cannot remove it: %w", msg, ErrRunAborted)
	}
	if err := removeAt(e.parent, filepath.Dir(e.path), e.name); err != nil {
		// The entry stays in the work directory: no trusted step may run on it.
		return fmt.Errorf("%s; Dagu cannot remove it: %w: %w", msg, err, ErrRunAborted)
	}
	return errors.New(msg + "; Dagu removed it")
}

// checkGitEntry compares an entry .git or an entry of a git folder with its
// state at the handoff.
func (iso *isolation) checkGitEntry(e *fsEntry, fail func(error)) {
	before, known := iso.gitSnapshot[e.path]
	if !known {
		fail(iso.removeEntry(e, "the entry %s is new in a git folder"))
		return
	}
	iso.seenGit[e.path] = true
	if after := sigOf(&e.st); after != before {
		fail(fmt.Errorf("untrusted step %q: the git entry %s changed: %s: %w",
			iso.step, e.path, describeSig(before, after), ErrRunAborted))
	}
}

// checkGitTree compares a git folder and its content with their state at the
// handoff.
func (iso *isolation) checkGitTree(root *fsEntry, fail func(error)) {
	err := walkEntry(root, func(e *fsEntry) (bool, error) {
		if _, known := iso.gitSnapshot[e.path]; !known {
			fail(iso.removeEntry(e, "the entry %s is new in a git folder"))
			return true, nil
		}
		iso.checkGitEntry(e, fail)
		return false, nil
	})
	if err != nil {
		fail(stepError(iso.step, "read the git folder", root.path, err))
	}
}

// checkGitRemoved fails for an entry of a git folder that the restore did not
// see.
func (iso *isolation) checkGitRemoved() error {
	var errs []error
	for path := range iso.gitSnapshot {
		if !iso.seenGit[path] {
			errs = append(errs, fmt.Errorf("untrusted step %q: the git entry %s is gone: %w", iso.step, path, ErrRunAborted))
		}
	}
	return errors.Join(errs...)
}

// restoreModes gives each folder whose mode the isolation changed its mode
// back, through the descriptor that the isolation kept.
func (iso *isolation) restoreModes() error {
	var errs []error
	for _, dirs := range []map[string]protectedDir{iso.protected, iso.passDirs} {
		for path, dir := range dirs {
			if err := chmodFDPath(dir.fd, path, dir.mode); err != nil {
				errs = append(errs, stepError(iso.step, "change the mode of", path, err))
			}
			_ = unix.Close(dir.fd)
			delete(dirs, path)
		}
	}
	return errors.Join(errs...)
}
