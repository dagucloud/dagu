// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Spec 103, "Isolation", rules 3 and 8. The handoff gives the work directory to
// the restricted user, except the folders that git opens as a repository (the
// git folders), the entries named .git, the sockets and the files with more
// than one link that keepsLinkedFile keeps. These stay with the worker,
// readable and not writable by the restricted user, until the restore gives
// them their modes back. The work directory and each folder on the way to a git folder or
// to a .git keep the owner of the worker with the mode 1777, so that the
// restricted user cannot rename or replace them.
//
// The restore gives the work directory back to the worker. It fails the step
// for what an agent could leave for a later trusted step:
//
//   - a new .git, or a new folder that git opens as a repository, the work
//     directory included: Dagu removes the .git, or the files HEAD, commondir
//     and gitdir of the folder;
//   - a change in a git folder: the run stops, because Dagu cannot undo it;
//   - a link of the restricted user to a path outside the work directory or
//     into a git folder, by the text of its target or by the path that the
//     kernel reaches through it, and a file of the restricted user with more
//     than one link: Dagu removes it. A link that the handoff gave and that
//     keeps its path and its target is one of the worker and stays.
//
// Each removal fails the step. So a step that succeeds kept each link as the
// restore checked it.

// protectedDir is a folder whose mode the isolation changed. The descriptor
// keeps the folder for the restore of its mode.
type protectedDir struct {
	fd   int
	mode uint32
}

// givenLink is a link that the handoff gave, at its path.
type givenLink struct {
	ino    uint64
	target string
}

// gitMarkers are the files that make a folder a git folder.
var gitMarkers = []string{"HEAD", "commondir", "gitdir"}

// isGitFolder reports whether git opens the folder fd as a repository: it has
// HEAD, objects and refs, or a file commondir or gitdir. git follows a link
// at objects and refs, so an entry of any type counts there.
func isGitFolder(fd int) bool {
	if hasChild(fd, "commondir", 0) || hasChild(fd, "gitdir", unix.S_IFREG) {
		return true
	}
	return hasChild(fd, "HEAD", 0) && hasChild(fd, "objects", 0) && hasChild(fd, "refs", 0)
}

// within reports whether path is root or a path below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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
	data, err := readGitPointer(path)
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
		// Not filepath.Join, which would fold a ".." before EvalSymlinks
		// follows the link in front of it, as git does.
		text = filepath.Dir(path) + "/" + text
	}
	return filepath.EvalSymlinks(text)
}

// readGitPointer reads the regular file of path. It does not wait at a FIFO,
// which would hold Begin until a writer opens it.
func readGitPointer(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) // #nosec G304 -- a file of the work directory, read as data.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return io.ReadAll(file)
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
			// A work directory that git opens as a repository stays with the
			// worker as a whole.
			if isGitFolder(e.fd) {
				return true, addDir(e.path, e.path)
			}
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

// handoff gives the work directory to the restricted user. It first makes the
// marker of the work directory, which stays until a restore makes the work
// directory safe, so that no step of the run uses the work directory after a
// stop of the process in between (CheckWorkDir).
func (iso *isolation) handoff() error {
	if err := iso.markHandoff(); err != nil {
		return err
	}
	if err := iso.discoverGit(); err != nil {
		return err
	}
	protected := iso.protectedPaths()
	iso.handedOff = true
	return walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
		switch {
		case iso.gitDirs[e.path]:
			return true, iso.protectGitTree(e)
		case iso.gitEntries[e.path]:
			return true, iso.protectGitEntry(e)
		case protected[e.path]:
			return false, iso.protectDir(e)
		default:
			if err := iso.checkNoDevice(e); err != nil {
				return false, err
			}
			// A connect needs only the bit w on a socket, which its owner can
			// set: the restricted user could drive the program that listens,
			// such as a daemon of root.
			if e.isSocket() {
				return false, nil
			}
			// A change of owner acts on the inode, so it would also give the
			// other paths of an entry with more than one link: a file, a link
			// or a FIFO.
			if !e.isDir() && e.st.Nlink > 1 {
				if iso.keepsLinkedFile(e) {
					return false, nil
				}
				return false, stepError(iso.step, "give", e.path, errors.New("the file has more than one link"))
			}
			if e.isLink() {
				if err := iso.recordLink(e); err != nil {
					return false, err
				}
			}
			if err := chownFD(e, int(iso.uid), int(iso.gid)); err != nil {
				return false, stepError(iso.step, "change the owner of", e.path, err)
			}
			return false, nil
		}
	})
}

// keepsLinkedFile reports whether a file with more than one link stays with its
// owner, as one that uv, pnpm or bun links from a package cache. The restricted
// user cannot write it, change its mode or link it (fs.protected_hardlinks).
// A file of the restricted group, which the restore takes as one of the
// restricted user, one that others can write, one with an ACL and one with a
// set-ID bit do not stay.
func (iso *isolation) keepsLinkedFile(e *fsEntry) bool {
	return e.isRegular() && e.uid() != int(iso.uid) && e.gid() != int(iso.gid) && e.perm()&0o6002 == 0 && !hasACL(e)
}

// recordLink keeps the link that the handoff gives, so that the restore checks
// only the links that the restricted user made or moved.
func (iso *isolation) recordLink(e *fsEntry) error {
	target, err := readLinkFD(e)
	if err != nil {
		return stepError(iso.step, "read the link", e.path, err)
	}
	iso.givenLinks[e.path] = givenLink{ino: e.st.Ino, target: target}
	return nil
}

// isGivenLink reports whether the link is the one that the handoff gave at its
// path. The restricted user can remove or rename a link and cannot change its
// target, so such a link is one of the worker, such as a link of a Python
// virtual environment to the interpreter of the host.
func (iso *isolation) isGivenLink(e *fsEntry) bool {
	given, ok := iso.givenLinks[e.path]
	if !ok || given.ino != e.st.Ino {
		return false
	}
	target, err := readLinkFD(e)
	return err == nil && target == given.target
}

// markHandoff makes the marker of the work directory. A marker that exists is
// the mark of a work directory that no restore made safe: the step fails and
// the run stops.
func (iso *isolation) markHandoff() error {
	marker, err := handoffMarker(iso.lexicalWork)
	if err != nil {
		return stepError(iso.step, "resolve the work directory", iso.lexicalWork, err)
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0o600) // #nosec G304 -- a fixed name next to the work directory.
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("untrusted step %q: %w", iso.step, notGivenBack(iso.lexicalWork, marker))
	}
	if err != nil {
		return stepError(iso.step, "make the file", marker, err)
	}
	iso.marker = marker
	if err := file.Close(); err != nil {
		return stepError(iso.step, "make the file", marker, err)
	}
	return nil
}

// unmarkUnlessAborted removes the marker of the work directory after a restore
// whose error does not stop the run, and returns that error.
func (iso *isolation) unmarkUnlessAborted(restoreErr error) error {
	if iso.marker == "" || errors.Is(restoreErr, ErrRunAborted) {
		return restoreErr
	}
	if err := os.Remove(iso.marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(restoreErr, stepError(iso.step, "remove the file", iso.marker, err))
	}
	iso.marker = ""
	return restoreErr
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
		// A process that the worker starts during the step does not inherit it.
		fd, err := unix.FcntlInt(uintptr(e.fd), unix.F_DUPFD_CLOEXEC, 0)
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

// checkNoDevice fails for a device node: as its owner, or with the bit r for
// others, the restricted user could open the device, such as a disk of the
// host.
func (iso *isolation) checkNoDevice(e *fsEntry) error {
	if e.isDevice() {
		return stepError(iso.step, "give", e.path, errors.New("the work directory cannot hold a device node"))
	}
	return nil
}

// protectGitEntry keeps one entry for the user of the worker, readable and not
// writable by the restricted user, and records its state.
func (iso *isolation) protectGitEntry(e *fsEntry) error {
	if err := iso.checkNoDevice(e); err != nil {
		return err
	}
	chown := e.uid() != iso.workerUID || e.gid() == int(iso.gid)
	perm := e.perm()
	if !e.isLink() {
		perm = perm&^0o002 | 0o004
		if e.isDir() {
			perm |= 0o001
		}
	}
	// A change acts on the file, so it would also reach the other paths of a
	// file with more than one link, as git clone --local makes them. An ACL
	// could let the restricted user write such a file, whose times the
	// restore does not compare (sameSig).
	if !e.isDir() && e.st.Nlink > 1 && (chown || perm != e.perm() || hasACL(e)) {
		return stepError(iso.step, "protect", e.path, errors.New("the file has more than one link"))
	}
	if chown {
		if err := chownFD(e, iso.workerUID, iso.workerGID); err != nil {
			return stepError(iso.step, "change the owner of", e.path, err)
		}
	}
	if !e.isLink() {
		if perm != e.perm() {
			iso.gitModes[e.path] = e.perm()
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
// an error that wraps ErrRunAborted when a git entry changed or is gone, or
// when it could not read a part of the work directory or of a git folder. With
// verifyGit false, it checks no git entry: a handoff that failed has a part of
// the snapshot only, and no process of the restricted user ran, so each entry
// that the snapshot lacks is an entry of the worker, not a new one.
func (iso *isolation) restore(verifyGit bool) error {
	var errs []error
	fail := func(err error) { errs = append(errs, err) }
	if iso.workDir != "" {
		walkErr := walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
			switch {
			case !verifyGit && (iso.gitDirs[e.path] || iso.gitEntries[e.path] || e.name == ".git"):
				return true, iso.restoreGitModes(e, fail)
			case iso.gitDirs[e.path]:
				return true, iso.checkGitTree(e, fail)
			case iso.gitEntries[e.path]:
				iso.checkGitEntry(e, fail)
				return true, nil
			case e.name == ".git" && e.path != iso.workDir:
				fail(iso.removeEntry(e, "the restricted user made the entry %s"))
				return true, nil
			}
			// A work directory that was a git folder at the handoff is in gitDirs.
			if verifyGit && e.isDir() && isGitFolder(e.fd) {
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
			// After a failed handoff, every entry is the worker's and the restore
			// removes none. The handoff gives no file with more than one link,
			// so such a file was never given: only its group matched.
			if !verifyGit && !e.isDir() && e.st.Nlink > 1 {
				return false, nil
			}
			if verifyGit && e.isLink() && !iso.isGivenLink(e) {
				if target, bad := iso.badLink(e); bad {
					fail(iso.removeEntry(e, "the restricted user made the link %s to %q", target))
					return true, nil
				}
			}
			// A link of root at another path in a folder of mode 1777 of root
			// is one that fs.protected_symlinks lets root follow.
			if verifyGit && !e.isDir() && e.st.Nlink > 1 {
				fail(iso.removeEntry(e, "the restricted user made the file %s with more than one link"))
				return true, nil
			}
			if err := chownFD(e, iso.workerUID, iso.workerGID); err != nil {
				fail(stepError(iso.step, "change the owner of", e.path, err))
				return false, nil
			}
			// The owner of an entry can set its ACLs: one that names the
			// restricted user would let it write the entry, and a default ACL
			// would pass to a .git that a later trusted step makes there.
			if verifyGit && !e.isLink() {
				if err := removeACLs(e); err != nil {
					fail(stepError(iso.step, "remove the ACLs of", e.path, err))
				}
			}
			if e.isRegular() && e.perm()&0o6000 != 0 {
				if err := chmodFD(e, e.perm()&0o777); err != nil {
					fail(stepError(iso.step, "change the mode of", e.path, err))
				}
			}
			return false, nil
		})
		switch {
		case walkErr != nil:
			// A part of the tree was not checked: no trusted step may run on it.
			// A git entry in that part is not gone.
			fail(fmt.Errorf("%w: %w", ErrRunAborted, stepError(iso.step, "read the work directory", iso.workDir, walkErr)))
		case verifyGit:
			fail(iso.checkGitRemoved())
		}
	}
	fail(iso.restoreModes())
	return errors.Join(errs...)
}

// badLink reports whether the link of the restricted user points outside the
// work directory or into a git folder. It checks the text of the target and
// the path that the kernel reaches through the link: a chain of links can
// leave the work directory while the text of each target stays in it. It
// returns the target.
func (iso *isolation) badLink(e *fsEntry) (string, bool) {
	target, err := readLinkFD(e)
	if err != nil {
		return "(unreadable)", true
	}
	lexical := target
	if !filepath.IsAbs(lexical) {
		lexical = filepath.Join(filepath.Dir(e.path), lexical)
	}
	resolved, err := resolveLinks(e.path)
	if err != nil {
		return target, true
	}
	for _, path := range []string{filepath.Clean(lexical), resolved} {
		if !iso.insideWork(path) || iso.insideGit(path) {
			return target, true
		}
	}
	return target, false
}

// maxLinks is the number of links that Linux follows in one lookup.
const maxLinks = 40

// resolveLinks returns the path that the kernel reaches through the absolute
// path, as filepath.EvalSymlinks does. An entry that does not exist, or that
// sits below a file, counts as a folder, as one made there later would be: a
// ".." after it comes back to the entries that exist, and their links are
// followed as the kernel will follow them then. No process of the restricted
// user runs during the restore, so the links cannot change under it.
func resolveLinks(path string) (string, error) {
	resolved := "/"
	rest := path
	for links := 0; ; {
		var part string
		part, rest, _ = strings.Cut(strings.TrimLeft(rest, "/"), "/")
		switch part {
		case "":
			return resolved, nil
		case ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, part)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
			resolved = next
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			resolved = next
			continue
		}
		if links++; links > maxLinks {
			return "", &fs.PathError{Op: "resolve", Path: path, Err: unix.ELOOP}
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = "/"
		}
		rest = target + "/" + rest
	}
}

// removeEntry removes the entry and returns the error of the step. format has
// a %s for the path, then a verb for each of args.
func (iso *isolation) removeEntry(e *fsEntry, format string, args ...any) error {
	msg := fmt.Sprintf("untrusted step %q: "+format, append([]any{iso.step, e.path}, args...)...)
	if e.parent < 0 {
		return fmt.Errorf("%s; Dagu cannot remove it: %w", msg, ErrRunAborted)
	}
	if err := removeAt(e.parent, filepath.Dir(e.path), e.name); err != nil {
		// The entry stays in the work directory: no trusted step may run on it.
		return fmt.Errorf("%s; Dagu cannot remove it: %w: %w", msg, err, ErrRunAborted)
	}
	return errors.New(msg + "; Dagu removed it")
}

// errGitChanged stops the run as ErrRunAborted does and also poisons the
// process: the restricted user cannot change a git entry, so a change shows a
// write that the isolation did not keep out.
var errGitChanged = fmt.Errorf("%w", ErrRunAborted)

// checkGitEntry compares an entry .git or an entry of a git folder with its
// state at the handoff.
func (iso *isolation) checkGitEntry(e *fsEntry, fail func(error)) {
	before, known := iso.gitSnapshot[e.path]
	if !known {
		fail(iso.removeEntry(e, "the entry %s is new in a git folder"))
		return
	}
	iso.seenGit[e.path] = true
	if after := sigOf(&e.st); !sameSig(before, after) {
		fail(fmt.Errorf("untrusted step %q: the git entry %s changed: %s: %w",
			iso.step, e.path, describeSig(before, after), errGitChanged))
		return
	}
	iso.restoreGitMode(e, fail)
}

// restoreGitMode gives a git entry the mode that it had before the handoff.
func (iso *isolation) restoreGitMode(e *fsEntry, fail func(error)) {
	mode, changed := iso.gitModes[e.path]
	if !changed {
		return
	}
	if err := chmodFD(e, mode); err != nil {
		fail(stepError(iso.step, "change the mode of", e.path, err))
	}
}

// restoreGitModes gives each entry of the tree at root the mode that it had
// before the handoff, without a check of the entries. It returns the error of
// the walk.
func (iso *isolation) restoreGitModes(root *fsEntry, fail func(error)) error {
	if len(iso.gitModes) == 0 {
		return nil
	}
	return walkEntry(root, func(e *fsEntry) (bool, error) {
		iso.restoreGitMode(e, fail)
		return false, nil
	})
}

// checkGitTree compares a git folder and its content with their state at the
// handoff. It returns the error of the walk.
func (iso *isolation) checkGitTree(root *fsEntry, fail func(error)) error {
	return walkEntry(root, func(e *fsEntry) (bool, error) {
		if _, known := iso.gitSnapshot[e.path]; !known {
			fail(iso.removeEntry(e, "the entry %s is new in a git folder"))
			return true, nil
		}
		iso.checkGitEntry(e, fail)
		return false, nil
	})
}

// checkGitRemoved fails for an entry of a git folder that the restore did not
// see.
func (iso *isolation) checkGitRemoved() error {
	var errs []error
	for path := range iso.gitSnapshot {
		if !iso.seenGit[path] {
			errs = append(errs, fmt.Errorf("untrusted step %q: the git entry %s is gone: %w", iso.step, path, errGitChanged))
		}
	}
	return errors.Join(errs...)
}

// restoreModes gives each folder whose mode the isolation changed its mode
// back, through the descriptor that the isolation kept.
func (iso *isolation) restoreModes() error {
	var errs []error
	for path, dir := range iso.protected {
		if err := chmodFDPath(dir.fd, path, dir.mode); err != nil {
			errs = append(errs, stepError(iso.step, "change the mode of", path, err))
		}
		_ = unix.Close(dir.fd)
		delete(iso.protected, path)
	}
	return errors.Join(errs...)
}

// closeProtected closes the descriptors that the isolation kept for the
// restore of the folder modes, and leaves the modes as they are.
func (iso *isolation) closeProtected() {
	for path, dir := range iso.protected {
		_ = unix.Close(dir.fd)
		delete(iso.protected, path)
	}
}
