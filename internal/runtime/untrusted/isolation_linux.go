// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"context"
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
// Dagu uses it when a process of the restricted user survives SIGKILL.
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
	workDir     string
	lexicalWork string
	privateDir  string
	home        string
	outputCopy  string
	nodeOutput  string
	outputLimit int64
	// hostLock is the lock file of the restricted user on the host.
	hostLock *os.File

	ended atomic.Bool
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
		outputLimit: req.OutputLimit,
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
	if err := iso.place(req.WorkDir, req.WorkingDir); err != nil {
		return iso, err
	}
	if err := iso.makePrivateDir(); err != nil {
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
	var err error
	if iso.privateDir != "" {
		if removeErr := os.RemoveAll(iso.privateDir); removeErr != nil {
			err = stepError(iso.step, "remove the folder", iso.privateDir, removeErr)
		}
	}
	iso.releaseSlot()
	return err
}

// place checks that the working directory of the step is in the work
// directory of the run, and makes the folders that are absent. No folder on the
// way can be a symbolic link.
func (iso *isolation) place(workDir, workingDir string) error {
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
	if err := checkTraversable(real); err != nil {
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

// prepare applies the isolation to cmd.
func (iso *isolation) prepare(cmd *exec.Cmd, env []string) error {
	if iso.ended.Load() {
		return fmt.Errorf("untrusted step %q: the step ended", iso.step)
	}
	if len(cmd.ExtraFiles) > 0 {
		return fmt.Errorf("untrusted step %q: the process cannot get extra files", iso.step)
	}
	if err := closeOnExec(); err != nil {
		return stepError(iso.step, "keep the descriptors of the worker from the process", "", err)
	}
	dir := cmd.Dir
	if dir != "" && !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return stepError(iso.step, "resolve the working directory", dir, err)
		}
		dir = abs
	}
	if _, ok := iso.relativeToWork(dir); !ok {
		return fmt.Errorf("untrusted step %q: the working directory %s is not in the work directory of the run", iso.step, cmd.Dir)
	}
	env = setEnv(env, "HOME", iso.home)
	env = setEnv(env, "TMPDIR", iso.home)
	iso.outputMu.Lock()
	hasTarget := iso.nodeOutput != ""
	iso.outputMu.Unlock()
	if hasTarget {
		env = setEnv(env, runenv.EnvKeyDAGUOutputFile, iso.outputCopy)
	}
	cmd.Env = env
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: iso.uid, Gid: iso.gid, Groups: []uint32{}}
	cmd.SysProcAttr.AmbientCaps = nil
	// A new session: a new process group and no controlling terminal.
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setpgid = false
	cmd.SysProcAttr.Setctty = false
	cmd.SysProcAttr.Noctty = false
	cmd.SysProcAttr.Pgid = 0
	return nil
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

// startTimeout limits the start of a process of the step. The child runs as
// the restricted user from its setuid to its execve, and Go waits for that
// execve with the fork lock of the worker held: a process of the user that
// stops the child there would block each later start of the worker.
const startTimeout = 5 * time.Second

// watchStart sends SIGKILL to each task of the restricted user, the stopped
// child included, while a start of a process of the step takes more than
// startTimeout. It uses the /proc scan only: the kill helper would need a
// start too. The returned function ends the watch.
func (iso *isolation) watchStart() (stop func()) {
	done := make(chan struct{})
	go func() {
		timer := time.NewTimer(startTimeout)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
			}
			killTaskGroups(iso.uid)
			timer.Reset(10 * time.Millisecond)
		}
	}()
	return func() { close(done) }
}

// started and abandon have nothing to do: end stops each process of the
// restricted user.
func (*isolation) started(int) {}

func (*isolation) abandon() {}

// end runs the actions of the end of the step (Spec 103, "Isolation", rule 7).
// An error that wraps ErrRunAborted stops the run.
func (iso *isolation) end(_ context.Context) error {
	iso.ended.Store(true)
	defer iso.releaseSlot()

	if killErr := killUser(iso.uid, iso.gid); killErr != nil {
		// A live process of the restricted user could watch the later steps.
		err := stepError(iso.step, "stop the processes of the restricted user", "", killErr)
		poison(err)
		return fmt.Errorf("%w: %w", ErrRunAborted, err)
	}

	errs := []error{iso.copyOutput()}
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

func ownerOf(info fs.FileInfo) (uid, gid int) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}
