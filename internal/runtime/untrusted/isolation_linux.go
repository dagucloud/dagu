// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"golang.org/x/sys/unix"
)

// Spec 103, "Isolation". The invariant of this file: Dagu makes a file of the
// restricted user, or reads one, only while no process of that user exists.
// Begin stops each such process before it makes the HOME of the step, and End
// stops each such process before it copies the output file and removes the
// HOME.

// killTimeout limits the wait for the end of the processes of the restricted
// user after SIGKILL.
const killTimeout = 30 * time.Second

// pidfdOpen opens a descriptor that follows a process. A test replaces it.
var pidfdOpen = unix.PidfdOpen

// removexattr removes an extended attribute. A test replaces it.
var removexattr = unix.Removexattr

// listProcesses lists the processes of a user for killUser. A test replaces
// it.
var listProcesses = userProcesses

// gitConfig is the global git config of the restricted user. The folders that
// hold an entry .git belong to the user of the worker, and git refuses a
// repository of another owner without it.
const gitConfig = "[safe]\n\tdirectory = *\n"

var (
	// slot holds one token: the process runs one untrusted step at a time.
	slot = make(chan struct{}, 1)

	poisonMu  sync.Mutex
	poisonErr error
)

// poison makes each later step of a run with an isolated untrusted step fail.
// Dagu uses it when a process of the restricted user survives SIGKILL and when
// a git entry changed during the step.
func poison(err error) {
	poisonMu.Lock()
	defer poisonMu.Unlock()
	if poisonErr == nil {
		poisonErr = err
	}
}

func poisoned() error {
	poisonMu.Lock()
	defer poisonMu.Unlock()
	return poisonErr
}

// Poisoned returns the error that stopped the isolation of the process, or
// nil. A step of a run with an isolated untrusted step does not run after it.
func Poisoned() error {
	if err := poisoned(); err != nil {
		return fmt.Errorf("%w: %w", ErrRunAborted, err)
	}
	return nil
}

// isolation is the state of one isolated untrusted step.
type isolation struct {
	step        string
	uid, gid    uint32
	workerUID   int
	workerGID   int
	workDir     string
	lexicalWork string
	privateDir  string
	home        string
	outputCopy  string
	nodeOutput  string
	outputLimit int64
	// protected keeps each folder that got the mode 1777, with its mode.
	protected map[string]protectedDir
	// passDirs keeps each folder above the work directory that got the ACL
	// of passACL, with its mode.
	passDirs map[string]protectedDir
	// gitEntries holds each entry .git of the work directory at the handoff.
	gitEntries map[string]bool
	// gitDirs holds each folder that git opens as a repository at the handoff.
	gitDirs map[string]bool
	// gitSnapshot holds the state of each entry of gitDirs and gitEntries
	// after the handoff; seenGit marks the entries that the restore saw.
	gitSnapshot map[string]fileSig
	seenGit     map[string]bool
	// gitModes holds the mode of each git entry whose mode the handoff
	// changed, for the restore.
	gitModes map[string]uint32
	// givenLinks holds each link that the handoff gave, by its path.
	givenLinks map[string]givenLink
	// hostLock is the lock file of the restricted user on the host.
	hostLock *os.File
	// marker is the marker of the work directory that this step made, until
	// a restore makes the work directory safe.
	marker string
	// handedOff is true once the walk of the handoff that changes owners
	// started.
	handedOff bool

	watchers sync.WaitGroup
	stopping atomic.Bool
	ended    atomic.Bool
	// startMu guards starting and running. The processes of the restricted
	// user cannot be told apart by their root process, so a watcher stops them
	// only while no other root process of the step runs or is starting.
	startMu sync.Mutex
	// starting counts the processes that prepare accepted and that have
	// neither started nor failed to start.
	starting int
	// running counts the root processes that a watcher follows and that have
	// not ended.
	running int
	// roots holds the pidfd of each root process that a watcher follows and
	// whose end nobody settled yet.
	roots map[int]bool
	// endMu makes the stop of what an ended root process left come before the
	// count of the next start, so that the next process of a step never sees
	// a process that the one before it left.
	endMu sync.Mutex
	// outputMu guards nodeOutput.
	outputMu sync.Mutex
}

// stepError returns the error of a failed action of the isolation.
func stepError(step, action, path string, err error) error {
	if path == "" {
		return fmt.Errorf("untrusted step %q: %s: %w", step, action, err)
	}
	return fmt.Errorf("untrusted step %q: %s %s: %w", step, action, path, err)
}

// isolate gives step the isolation of the restricted user.
func isolate(ctx context.Context, req Request, step *Step) (*Step, error) {
	iso, err := beginIsolation(ctx, req)
	if err != nil {
		return nil, err
	}
	step.iso = iso
	return step, nil
}

func beginIsolation(ctx context.Context, req Request) (iso *isolation, err error) {
	hostLock, err := acquireSlot(ctx, req.Config.UID, req.LockDir, req.Log)
	if err != nil {
		return nil, stepError(req.StepName, "wait for the restricted user", "", err)
	}
	iso = &isolation{
		step:        req.StepName,
		uid:         req.Config.UID,
		gid:         req.Config.GID,
		workerUID:   os.Geteuid(),
		workerGID:   os.Getegid(),
		outputLimit: req.OutputLimit,
		protected:   make(map[string]protectedDir),
		passDirs:    make(map[string]protectedDir),
		roots:       make(map[int]bool),
		gitEntries:  make(map[string]bool),
		gitDirs:     make(map[string]bool),
		gitSnapshot: make(map[string]fileSig),
		seenGit:     make(map[string]bool),
		gitModes:    make(map[string]uint32),
		givenLinks:  make(map[string]givenLink),
		hostLock:    hostLock,
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, iso.abort())
			iso = nil
		}
	}()

	// The locks can be free when ctx has ended: the step then does not run.
	if err := ctx.Err(); err != nil {
		return iso, stepError(req.StepName, "wait for the restricted user", "", err)
	}
	if err := Poisoned(); err != nil {
		return iso, stepError(req.StepName, "start", "", err)
	}
	if err := checkIsolationSupport(); err != nil {
		return iso, stepError(req.StepName, "check the process", "", err)
	}
	// A process of the restricted user that an earlier run left would watch
	// this step.
	if err := killUser(iso.uid, iso.gid); err != nil {
		poison(err)
		return iso, fmt.Errorf("%w: %w", ErrRunAborted, stepError(req.StepName, "stop the processes of the restricted user", "", err))
	}
	if err := iso.place(req.WorkDir, req.WorkingDir, req.ManagedRoot); err != nil {
		return iso, err
	}
	if err := iso.makePrivateDir(); err != nil {
		return iso, err
	}
	if err := iso.handoff(); err != nil {
		return iso, err
	}
	return iso, nil
}

// acquireSlot waits for the end of the previous untrusted step of the process,
// then for the end of an untrusted step of another Dagu process of the host
// with the same restricted user. It returns the lock file of the host.
func acquireSlot(ctx context.Context, uid uint32, lockDir string, log io.Writer) (*os.File, error) {
	waited := false
	select {
	case slot <- struct{}{}:
	default:
		writeLine(log, WaitingLine)
		waited = true
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	lock, err := lockHost(ctx, uid, lockDir, log, waited)
	if err != nil {
		<-slot
		return nil, err
	}
	return lock, nil
}

// hostLockDirs lists the folders of the lock file of the host, in order:
// /run/dagu, then the folder untrusted of the data directory.
func hostLockDirs(dataDir string) []string {
	dirs := []string{"/run/dagu"}
	if dataDir != "" {
		dirs = append(dirs, filepath.Join(dataDir, "untrusted"))
	}
	return dirs
}

// privateLockDir makes the folder, or checks the folder that exists: a folder
// of the user of the worker, not a link, with no access for the group or
// others.
func privateLockDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if uid, _ := ownerOf(info); !info.IsDir() || uid != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the folder %s must be a folder of the user of the worker with the mode 0700", dir)
	}
	return nil
}

// lockHost takes the lock file of the restricted user in a folder that only
// the user of the worker can reach. The file belongs to the user of the
// worker; a file of another owner or a link stops the step.
func lockHost(ctx context.Context, uid uint32, dataDir string, log io.Writer, waited bool) (*os.File, error) {
	var dir string
	var dirErrs []error
	for _, candidate := range hostLockDirs(dataDir) {
		if err := privateLockDir(candidate); err != nil {
			dirErrs = append(dirErrs, fmt.Errorf("%s: %w", candidate, err))
			continue
		}
		dir = candidate
		break
	}
	if dir == "" {
		return nil, fmt.Errorf("no folder for the lock file of the restricted user: %w", errors.Join(dirErrs...))
	}
	path := filepath.Join(dir, "untrusted-"+strconv.FormatUint(uint64(uid), 10)+".lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- a fixed name in a folder of the worker.
	if err != nil {
		return nil, fmt.Errorf("open the lock file %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("read the lock file %s: %w", path, err)
	}
	if uid, _ := ownerOf(info); !info.Mode().IsRegular() || uid != os.Geteuid() {
		_ = file.Close()
		return nil, fmt.Errorf("the lock file %s does not belong to the user of the worker", path)
	}
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) // #nosec G115 -- a file descriptor fits in int.
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			_ = file.Close()
			return nil, fmt.Errorf("lock the file %s: %w", path, err)
		}
		if !waited {
			writeLine(log, WaitingLine)
			waited = true
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (iso *isolation) releaseSlot() {
	if iso.hostLock != nil {
		_ = unix.Flock(int(iso.hostLock.Fd()), unix.LOCK_UN) // #nosec G115 -- a file descriptor fits in int.
		_ = iso.hostLock.Close()
		iso.hostLock = nil
	}
	<-slot
}

// abort undoes a Begin that failed. No process of the restricted user started.
func (iso *isolation) abort() error {
	var restoreErr error
	if iso.handedOff {
		restoreErr = iso.restore(false)
	} else {
		restoreErr = iso.restoreModes()
	}
	errs := []error{iso.unmarkUnlessAborted(restoreErr)}
	if iso.privateDir != "" {
		if removeErr := os.RemoveAll(iso.privateDir); removeErr != nil {
			errs = append(errs, stepError(iso.step, "remove the folder", iso.privateDir, removeErr))
		}
	}
	iso.releaseSlot()
	return errors.Join(errs...)
}

// place checks that the working directory of the step is in the work
// directory of the run, and makes the folders that are absent. No folder on the
// way can be a symbolic link.
func (iso *isolation) place(workDir, workingDir, managedRoot string) error {
	if strings.TrimSpace(workDir) == "" {
		return fmt.Errorf("untrusted step %q: the run has no work directory", iso.step)
	}
	lexical, err := filepath.Abs(workDir)
	if err != nil {
		return stepError(iso.step, "resolve the work directory", workDir, err)
	}
	real, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return stepError(iso.step, "resolve the work directory", lexical, err)
	}
	info, err := os.Lstat(real)
	if err != nil {
		return stepError(iso.step, "read the work directory", real, err)
	}
	if !info.IsDir() {
		return stepError(iso.step, "read the work directory", real, errors.New("not a folder"))
	}
	if err := iso.openPath(real, managedRoot); err != nil {
		return stepError(iso.step, "reach the work directory", real, err)
	}
	iso.workDir = real
	iso.lexicalWork = lexical

	rel, ok := iso.relativeToWork(workingDir)
	if !ok {
		return fmt.Errorf("untrusted step %q: the working directory %s is not in the work directory of the run", iso.step, workingDir)
	}
	current := real
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(current, 0o750); err != nil {
				return stepError(iso.step, "make the working directory", current, err)
			}
			continue
		}
		if err != nil {
			return stepError(iso.step, "read the working directory", current, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("untrusted step %q: the working directory %s has the symbolic link %s", iso.step, workingDir, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("untrusted step %q: the working directory %s has the file %s, which is not a folder", iso.step, workingDir, current)
		}
	}
	return nil
}

// checkTraversable checks that each folder above path lets any user pass
// (the bit x for others), so that the restricted user can reach path. Dagu
// does not change the mode of such a folder.
func checkTraversable(path string) error {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o001 == 0 {
			return fmt.Errorf("the folder %s has the mode %04o: the restricted user cannot pass it", dir, info.Mode().Perm())
		}
		if parent := filepath.Dir(dir); parent == dir {
			return nil
		}
	}
}

// openPath lets the restricted user pass each folder above path. A folder of
// the user of the worker at managedRoot (paths.dag_run_work_dir) or below it
// gets an ACL entry that lets only the restricted user pass it, until the end
// of the step. Any other folder that the restricted user cannot pass stops the
// step.
func (iso *isolation) openPath(path, managedRoot string) error {
	managed := ""
	if managedRoot != "" {
		if resolved, err := filepath.EvalSymlinks(managedRoot); err == nil {
			managed = resolved
		}
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if err := iso.passDir(dir, managed != "" && within(managed, dir)); err != nil {
			return err
		}
		if parent := filepath.Dir(dir); parent == dir {
			return nil
		}
	}
}

// passDir checks that the restricted user can pass the folder dir, by its
// mode and its access ACL (canPass). A folder that it cannot pass gets the
// access ACL of passACL when managed is true, the folder belongs to the user
// of the worker and it has no access ACL. The bit x for others would
// let every user of the host reach the work directory, whose mode is 1777
// during the step. The check and the change go through one descriptor, which
// the isolation keeps for the restore, so a folder that replaces dir cannot
// get the entry.
func (iso *isolation) passDir(dir string, managed bool) error {
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return &fs.PathError{Op: "stat", Path: dir, Err: err}
	}
	fdPath := "/proc/self/fd/" + strconv.Itoa(fd)
	mode := st.Mode & 0o7777
	acl, aclErr := readAccessACL(fdPath)
	noACL := errors.Is(aclErr, unix.ENODATA) || errors.Is(aclErr, unix.EOPNOTSUPP)
	if (aclErr == nil || noACL) && canPass(&st, acl, iso.uid, iso.gid) {
		_ = unix.Close(fd)
		return nil
	}
	if !managed || int(st.Uid) != iso.workerUID {
		_ = unix.Close(fd)
		return fmt.Errorf("the folder %s has the mode %04o: the restricted user cannot pass it", dir, mode&0o777)
	}
	// A link that replaced a folder on the way since the work directory was
	// resolved would lead the descriptor to another folder.
	if real, err := os.Readlink(fdPath); err != nil || real != dir {
		_ = unix.Close(fd)
		return fmt.Errorf("the folder %s changed while Dagu opened it", dir)
	}
	if !noACL {
		_ = unix.Close(fd)
		return fmt.Errorf("the folder %s has an access ACL that does not let the restricted user pass it", dir)
	}
	if err := unix.Setxattr(fdPath, aclNames[0], passACL(mode, iso.uid), 0); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("let the restricted user pass the folder %s: %w", dir, &fs.PathError{Op: "setxattr", Path: dir, Err: err})
	}
	iso.passDirs[dir] = protectedDir{fd: fd, mode: mode}
	return nil
}

// restorePassDirs gives each folder above the work directory its mode back,
// and then removes the access ACL that the folder got. The mode comes first:
// with the ACL, it sets the mask, which the group bits keep once the ACL is
// gone. An ACL or a mode that stays lets a user pass the folder, so either
// error wraps ErrRunAborted.
func (iso *isolation) restorePassDirs() error {
	var errs []error
	for path, dir := range iso.passDirs {
		if err := chmodFDPath(dir.fd, path, dir.mode); err != nil {
			errs = append(errs, fmt.Errorf("%w: %w", ErrRunAborted, stepError(iso.step, "change the mode of", path, err)))
		} else if err := removexattr("/proc/self/fd/"+strconv.Itoa(dir.fd), aclNames[0]); err != nil && !errors.Is(err, unix.ENODATA) {
			errs = append(errs, fmt.Errorf("%w: %w", ErrRunAborted, stepError(iso.step, "remove the ACL of", path, err)))
		}
		_ = unix.Close(dir.fd)
		delete(iso.passDirs, path)
	}
	return errors.Join(errs...)
}

// The tags and the version of the xattr format of a POSIX ACL.
const (
	aclVersion     = 2
	aclUserObj     = 0x01
	aclUser        = 0x02
	aclGroupObj    = 0x04
	aclGroup       = 0x08
	aclMask        = 0x10
	aclOther       = 0x20
	aclUndefinedID = ^uint32(0)
	// aclEntrySize is the size of an entry: tag, permissions and ID.
	aclEntrySize = 8
)

// readAccessACL returns the access ACL of the entry at path, in the xattr
// format. An entry without one gives ENODATA.
func readAccessACL(path string) ([]byte, error) {
	size, err := unix.Getxattr(path, aclNames[0], nil)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size)
	n, err := unix.Getxattr(path, aclNames[0], buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// canPass reports whether the user uid, whose only group is gid, can pass the
// folder of st with the access ACL acl (nil without one), as the kernel checks
// it (acl(5), "Access check algorithm"): the entry of the owner, else the
// entry of the user, else the entries of its group, else the bits of others.
func canPass(st *unix.Stat_t, acl []byte, uid, gid uint32) bool {
	if st.Uid == uid {
		return st.Mode&0o100 != 0
	}
	user, group := -1, -1
	groupObj, mask := int(st.Mode>>3&7), 7
	if acl != nil {
		if len(acl) < 4 || binary.LittleEndian.Uint32(acl) != aclVersion {
			return false
		}
		for entry := acl[4:]; len(entry) >= aclEntrySize; entry = entry[aclEntrySize:] {
			perm := int(binary.LittleEndian.Uint16(entry[2:]))
			id := binary.LittleEndian.Uint32(entry[4:])
			switch binary.LittleEndian.Uint16(entry) {
			case aclUser:
				if id == uid {
					user = perm
				}
			case aclGroupObj:
				groupObj = perm
			case aclGroup:
				if id == gid {
					group = perm
				}
			case aclMask:
				mask = perm
			}
		}
	}
	switch {
	case user >= 0:
		return user&mask&1 != 0
	case st.Gid == gid || group >= 0:
		return (st.Gid == gid && groupObj&mask&1 != 0) || (group >= 0 && group&mask&1 != 0)
	}
	return st.Mode&0o001 != 0
}

// passACL returns the access ACL that keeps the permissions of the owner, the
// group and others of mode, and lets the user uid pass the folder and nothing
// more. The mask adds the bit x to the permissions of the group, which the
// entry of the group keeps.
func passACL(mode, uid uint32) []byte {
	group := mode >> 3 & 7
	acl := binary.LittleEndian.AppendUint32(nil, aclVersion)
	for _, entry := range [][3]uint32{
		{aclUserObj, mode >> 6 & 7, aclUndefinedID},
		{aclUser, 1, uid},
		{aclGroupObj, group, aclUndefinedID},
		{aclMask, group | 1, aclUndefinedID},
		{aclOther, mode & 7, aclUndefinedID},
	} {
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[0])) // #nosec G115 -- small tags.
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[1])) // #nosec G115 -- small modes.
		acl = binary.LittleEndian.AppendUint32(acl, entry[2])
	}
	return acl
}

// within reports whether path is root or a path below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// relativeToWork returns the path of dir relative to the work directory. ok is
// false when dir is not in the work directory.
func (iso *isolation) relativeToWork(dir string) (string, bool) {
	if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	dir = filepath.Clean(dir)
	for _, base := range []string{iso.workDir, iso.lexicalWork} {
		rel, err := filepath.Rel(base, dir)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		return rel, true
	}
	return "", false
}

// makePrivateDir makes the folder of the step outside the work directory. It
// belongs to the user of the worker with the mode 0711, so that the restricted
// user can reach its HOME and its output file and cannot rename them.
func (iso *isolation) makePrivateDir() error {
	dir, err := os.MkdirTemp("", "dagu-untrusted-")
	if err != nil {
		return stepError(iso.step, "make the folder of the step", os.TempDir(), err)
	}
	iso.privateDir = dir
	if err := os.Chmod(dir, 0o711); err != nil { //nolint:gosec // The mode 0711 is the design: the restricted user passes the folder and cannot list or write it.
		return stepError(iso.step, "change the mode of", dir, err)
	}
	if err := checkTraversable(dir); err != nil {
		return stepError(iso.step, "reach the folder of the step", dir, err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return stepError(iso.step, "make the home folder", home, err)
	}
	if err := os.Lchown(home, int(iso.uid), int(iso.gid)); err != nil {
		return stepError(iso.step, "change the owner of", home, err)
	}
	iso.home = home
	if err := iso.writeRestrictedFile(filepath.Join(home, ".gitconfig"), []byte(gitConfig), 0o600); err != nil {
		return err
	}
	output := filepath.Join(dir, "output")
	if err := iso.writeRestrictedFile(output, nil, 0o600); err != nil {
		return err
	}
	iso.outputCopy = output
	return nil
}

// setOutputTarget records the file that the runtime reads, and returns the
// file that the process writes.
func (iso *isolation) setOutputTarget(target string) string {
	iso.outputMu.Lock()
	defer iso.outputMu.Unlock()
	iso.nodeOutput = target
	return iso.outputCopy
}

// writeRestrictedFile makes a new file that belongs to the restricted user.
func (iso *isolation) writeRestrictedFile(path string, data []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- the path is in the folder of the step that Dagu made.
	if err != nil {
		return stepError(iso.step, "make the file", path, err)
	}
	_, writeErr := file.Write(data)
	// The owner and the mode change through the descriptor: a link that
	// replaces the path cannot redirect them.
	chownErr := file.Chown(int(iso.uid), int(iso.gid))
	chmodErr := file.Chmod(mode)
	closeErr := file.Close()
	if err := errors.Join(writeErr, chownErr, chmodErr, closeErr); err != nil {
		return stepError(iso.step, "write the file", path, err)
	}
	return nil
}

// prepare applies the isolation to cmd. The caller calls release once the
// start of cmd returns.
func (iso *isolation) prepare(cmd *exec.Cmd, env []string) (release func(), err error) {
	if iso.ended.Load() {
		return nil, fmt.Errorf("untrusted step %q: the step ended", iso.step)
	}
	if len(cmd.ExtraFiles) > 0 {
		return nil, fmt.Errorf("untrusted step %q: the process cannot get extra files", iso.step)
	}
	if err := closeOnExec(); err != nil {
		return nil, stepError(iso.step, "keep the descriptors of the worker from the process", "", err)
	}
	dir := cmd.Dir
	if dir != "" && !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, stepError(iso.step, "resolve the working directory", dir, err)
		}
		dir = abs
	}
	if _, ok := iso.relativeToWork(dir); !ok {
		return nil, fmt.Errorf("untrusted step %q: the working directory %s is not in the work directory of the run", iso.step, cmd.Dir)
	}
	env = setEnv(env, "HOME", iso.home)
	env = setEnv(env, "TMPDIR", iso.home)
	iso.outputMu.Lock()
	hasTarget := iso.nodeOutput != ""
	iso.outputMu.Unlock()
	if hasTarget {
		env = setEnv(env, runenv.EnvKeyDAGUOutputFile, iso.outputCopy)
	}
	path, args, cmdDir := cmd.Path, cmd.Args, cmd.Dir
	envFile, err := iso.viaStartHelper(cmd, dir, env)
	if err != nil {
		return nil, stepError(iso.step, "prepare the start of the process", "", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The child stays root until the start helper changes the user.
	cmd.SysProcAttr.Credential = nil
	cmd.SysProcAttr.AmbientCaps = nil
	// A new session: a new process group and no controlling terminal.
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setpgid = false
	cmd.SysProcAttr.Setctty = false
	cmd.SysProcAttr.Noctty = false
	cmd.SysProcAttr.Pgid = 0

	release = func() {
		_ = envFile.Close()
		// The caller sees its own command after the start, not the helper's.
		cmd.Path, cmd.Args, cmd.Dir, cmd.Env, cmd.ExtraFiles = path, args, cmdDir, env, nil
	}

	iso.endMu.Lock()
	defer iso.endMu.Unlock()
	// The root process before this one may have ended before its watcher saw
	// it end. With no root process left, each process of the restricted user
	// is one that an ended root process left, and it must be gone before the
	// next start.
	iso.settleEnded()
	if running, starting := iso.otherProcesses(); !running && !starting {
		if err := iso.killLeftovers(); err != nil {
			release()
			return nil, stepError(iso.step, "stop the processes that the step left", "", err)
		}
	}
	iso.startMu.Lock()
	iso.starting++
	iso.startMu.Unlock()
	return release, nil
}

// closeOnExec marks each descriptor of the process from 3 up close-on-exec.
// Go opens its own descriptors so, but a descriptor that the worker inherited
// from its launcher would reach the process of the step.
func closeOnExec() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err == nil && flags&unix.FD_CLOEXEC == 0 {
			_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC)
		}
		// A descriptor closed since the listing, such as the one of the
		// listing itself, needs nothing.
		if err != nil && !errors.Is(err, unix.EBADF) {
			return fmt.Errorf("descriptor %d: %w", fd, err)
		}
	}
	return nil
}

// started watches the root process of the step. When it ends and no other
// root process of the step runs, Dagu stops each process of the restricted
// user, so that a process that it left cannot keep the pipes of the step open.
// It does not stop the processes while another root process runs. A process
// that no watcher can follow is stopped, with what it left, and started
// returns the error: the start fails.
func (iso *isolation) started(pid int) error {
	if pid <= 0 {
		iso.abandon()
		return nil
	}
	// The process is not reaped yet: its caller has not called Wait, so the
	// PID is still its own.
	fd, err := pidfdOpen(pid, 0)
	if err != nil {
		// The process leads its own process group (Setsid), whose ID no other
		// group takes while the process is not reaped: the signal reaches the
		// process and what it started, so no child keeps its pipes open.
		_ = unix.Kill(-pid, unix.SIGKILL)
		// A watcher that waits for this start under endMu goes on at once.
		iso.abandon()
		iso.endMu.Lock()
		defer iso.endMu.Unlock()
		_ = iso.killLeftovers()
		return stepError(iso.step, "watch the process", "", err)
	}
	// One step moves the process from starting to running, so that no check
	// in between sees neither.
	iso.startMu.Lock()
	iso.starting--
	iso.running++
	iso.roots[fd] = true
	iso.startMu.Unlock()
	iso.watchers.Go(func() {
		defer func() { _ = unix.Close(fd) }()
		ended := false
		for !ended && !iso.stopping.Load() {
			n, err := pollEnded(fd, 200)
			if err != nil && !errors.Is(err, unix.EINTR) {
				break
			}
			ended = n
		}
		iso.endMu.Lock()
		defer iso.endMu.Unlock()
		iso.startMu.Lock()
		settled := iso.roots[fd]
		if settled {
			delete(iso.roots, fd)
			iso.running--
		}
		iso.startMu.Unlock()
		// A process whose end prepare settled had its leftovers stopped there.
		// An error leaves them to the next start, which checks again, or to
		// End.
		if settled && ended {
			_ = iso.killLeftovers()
		}
	})
	return nil
}

// pollEnded reports whether the process of the pidfd fd ended, waiting for at
// most timeout milliseconds.
func pollEnded(fd, timeout int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} // #nosec G115 -- a file descriptor fits in int32.
	n, err := unix.Poll(fds, timeout)
	return n > 0, err
}

// settleEnded counts as ended each root process in roots that ended. The
// caller holds endMu.
func (iso *isolation) settleEnded() {
	iso.startMu.Lock()
	defer iso.startMu.Unlock()
	for fd := range iso.roots {
		if ended, _ := pollEnded(fd, 0); ended {
			delete(iso.roots, fd)
			iso.running--
		}
	}
}

// abandon settles a process that failed to start.
func (iso *isolation) abandon() {
	iso.startMu.Lock()
	defer iso.startMu.Unlock()
	iso.starting--
}

// otherProcesses reports whether a root process of the step runs, and whether
// one is starting.
func (iso *isolation) otherProcesses() (running, starting bool) {
	iso.startMu.Lock()
	defer iso.startMu.Unlock()
	return iso.running > 0, iso.starting > 0
}

// killLeftovers stops each process of the restricted user with killUser once
// no root process of the step runs or is starting, and returns nil once none
// is left. It returns nil at once while a root process runs, whose watcher
// stops them at its own end, and waits while one is starting. The caller
// holds endMu, which keeps a new root process from starting during the kill.
// It returns an error when the step ends first, when a start does not settle
// within killTimeout, or when killUser fails.
func (iso *isolation) killLeftovers() error {
	deadline := time.Now().Add(killTimeout)
	for {
		if iso.stopping.Load() {
			return errors.New("the step ended")
		}
		running, starting := iso.otherProcesses()
		switch {
		case running:
			return nil
		case !starting:
			// The kill(-1) of the helper also reaches a process that a fork
			// made after a scan of /proc read the list.
			return killUser(iso.uid, iso.gid)
		case !time.Now().Before(deadline):
			return errors.New("a root process of the step is still starting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// end runs the actions of the end of the step (Spec 103, "Isolation", rule 7).
// An error that wraps ErrRunAborted stops the run.
func (iso *isolation) end(_ context.Context) error {
	iso.ended.Store(true)
	defer iso.releaseSlot()

	iso.stopping.Store(true)
	iso.watchers.Wait()
	if killErr := killUser(iso.uid, iso.gid); killErr != nil {
		// A live process of the restricted user could watch the later steps.
		err := stepError(iso.step, "stop the processes of the restricted user", "", killErr)
		poison(err)
		iso.closeProtected()
		// Without the ACL entry, a process that survived cannot reach the
		// work directory by its path.
		return fmt.Errorf("%w: %w", ErrRunAborted, errors.Join(err, iso.restorePassDirs()))
	}

	// The HOME goes before the checks: a file that the step linked from it, such
	// as one of a package cache, then has one link and stays.
	var homeErr error
	if err := os.RemoveAll(iso.home); err != nil {
		homeErr = stepError(iso.step, "remove the home folder", iso.home, err)
	}
	restoreErr := iso.restore(true)
	if errors.Is(restoreErr, errGitChanged) {
		// Spec 103, "Isolation". Another error that stops the run keeps the
		// marker, which stops only the runs on this work directory.
		poison(restoreErr)
	}
	errs := []error{homeErr, iso.unmarkUnlessAborted(restoreErr), iso.copyOutput()}
	if err := os.RemoveAll(iso.privateDir); err != nil {
		errs = append(errs, stepError(iso.step, "remove the folder", iso.privateDir, err))
	}
	return errors.Join(errs...)
}

// copyOutput copies the output file that the restricted user wrote to the file
// that the runtime reads. The copy reads the file that Dagu made, not a link.
func (iso *isolation) copyOutput() error {
	iso.outputMu.Lock()
	target := iso.nodeOutput
	iso.outputMu.Unlock()
	if iso.outputCopy == "" || target == "" {
		return nil
	}
	file, err := os.OpenFile(iso.outputCopy, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 -- the path is in the folder of the step that Dagu made.
	if err != nil {
		return stepError(iso.step, "read the output file", iso.outputCopy, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return stepError(iso.step, "read the output file", iso.outputCopy, err)
	}
	if !info.Mode().IsRegular() {
		return stepError(iso.step, "read the output file", iso.outputCopy, errors.New("not a regular file"))
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		return stepError(iso.step, "read the output file", iso.outputCopy, fmt.Errorf("the file has %d links", stat.Nlink))
	}
	limit := iso.outputLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return stepError(iso.step, "read the output file", iso.outputCopy, err)
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0) // #nosec G304 -- the runtime made the file for the attempt.
	if err != nil {
		return stepError(iso.step, "write the output file", target, err)
	}
	_, writeErr := out.Write(data)
	if err := errors.Join(writeErr, out.Close()); err != nil {
		return stepError(iso.step, "write the output file", target, err)
	}
	return nil
}

// shareReadOnly lets the restricted user read and run the regular file at path
// and not write it. The file keeps the worker as owner and gets the group of
// the restricted user with the mode 0550, so that no other user reads it. The
// changes go through a descriptor, so a link that replaces the path cannot
// redirect them.
func (iso *isolation) shareReadOnly(path string) error {
	if err := checkTraversable(path); err != nil {
		return stepError(iso.step, "share", path, err)
	}
	err := walkFD(path, func(e *fsEntry) (bool, error) {
		if !e.isRegular() || e.st.Nlink > 1 {
			return true, errors.New("not a regular file with one link")
		}
		if err := chownFD(e, iso.workerUID, int(iso.gid)); err != nil {
			return true, err
		}
		// The mode sets the mask of an access ACL, which a default ACL of the
		// folder can give the file: a named entry would let another user read it.
		if err := removeACLs(e); err != nil {
			return true, err
		}
		return true, chmodFD(e, 0o550)
	})
	if err != nil {
		return stepError(iso.step, "share", path, err)
	}
	return nil
}

func ownerOf(info fs.FileInfo) (uid, gid int) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}

// writableBy reports whether the user uid or the group gid can write the
// folder path or put another folder in its place: it can write the folder, it
// can make the folder or a missing folder on the way, or it can rename a
// folder or a link on the way. Each link that the path leads through counts,
// and so do each entry that a ".." leaves and the folder that the last link
// leads to. A sticky folder lets it make an entry and not rename one of
// another owner. A folder or a link of the user counts as writable whatever
// its mode: the owner can change the mode or replace the link. A folder with
// an access ACL, a folder that cannot be read, and a path that resolveLinks
// cannot resolve count as writable.
func writableBy(path string, uid, gid uint32) bool {
	path, err := absolute(path)
	if err != nil {
		return true
	}
	writable := false
	real, err := followLinks(path, func(passed string) {
		writable = writable || writableOnTheWay(passed, uid, gid)
	})
	return err != nil || writable || writableOnTheWay(real, uid, gid)
}

// inWorkDir reports whether the path reaches the work directory workDir or an
// entry below it, or leads through a link or an entry that a ".." leaves
// there. The restricted user owns these entries during an untrusted step, and
// the restore keeps what it wrote in them. A path that cannot be resolved
// counts as in it. An empty workDir has no entry.
func inWorkDir(path, workDir string) bool {
	if workDir == "" {
		return false
	}
	path, err := absolute(path)
	if err != nil {
		return true
	}
	if workDir, err = absolute(workDir); err != nil {
		return true
	}
	work, err := resolveLinks(workDir)
	if err != nil {
		return true
	}
	in := false
	real, err := followLinks(path, func(passed string) {
		in = in || within(work, passed)
	})
	return err != nil || in || within(work, real)
}

// absolute returns path, below the working directory of the process when it is
// relative. The path stays uncleaned: a ".." after a link leads where the link
// leads, not where the text leads.
func absolute(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + "/" + path, nil
}

// writableOnTheWay checks path and each folder above it for writableBy. Only
// path itself can be a link.
func writableOnTheWay(path string, uid, gid uint32) bool {
	missing := false
	for dir := path; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = true
		case err != nil:
			return true
		default:
			owner, group := ownerOf(info)
			mode := info.Mode()
			canWrite := mode&fs.ModeSymlink == 0 &&
				((group == int(gid) && mode&0o020 != 0) || mode&0o002 != 0 || dirHasACL(dir))
			if owner == int(uid) || (canWrite && (dir == path || missing || mode&fs.ModeSticky == 0)) {
				return true
			}
			missing = false
		}
		if parent := filepath.Dir(dir); parent == dir {
			return false
		}
	}
}

// dirHasACL reports whether the folder at path has an access ACL, which can
// let the restricted user write it whatever its mode. An error that does not
// show the lack of one counts as one.
func dirHasACL(path string) bool {
	_, err := unix.Getxattr(path, aclNames[0], nil)
	return !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.EOPNOTSUPP)
}
