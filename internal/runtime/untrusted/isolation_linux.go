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

// Spec 092, "Isolation". The invariant of this file: Dagu changes the owner of
// a file only while no process of the restricted user exists. Begin stops each
// such process before it gives the work directory to the restricted user, and
// End stops each such process before it gives the work directory back. The
// walks also act on descriptors (fswalk_linux.go), so a path that changes
// under a walk cannot redirect it.

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
// Dagu uses it when a process of the restricted user survives SIGKILL, or when
// a git folder changed during an untrusted step.
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
	// passDirs keeps each folder above the work directory that got the bit x
	// for others, with its mode.
	passDirs map[string]protectedDir
	// gitEntries holds each entry .git of the work directory at the handoff.
	gitEntries map[string]bool
	// gitDirs holds each folder that git opens as a repository at the handoff.
	gitDirs map[string]bool
	// gitSnapshot holds the state of each entry of gitDirs and gitEntries
	// after the handoff; seenGit marks the entries that the restore saw.
	gitSnapshot map[string]fileSig
	seenGit     map[string]bool
	// hostLock is the lock file of the restricted user on the host.
	hostLock *os.File
	// handedOff is true once the walk of the handoff started.
	handedOff bool

	watchers sync.WaitGroup
	stopping atomic.Bool
	ended    atomic.Bool
	// generation counts the processes of the step. The watcher of a process
	// stops when the next process of the step starts.
	generation atomic.Int64
	// files numbers the files of the attempt, so that two attempts of the step
	// get two files.
	files atomic.Int64
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
		gitEntries:  make(map[string]bool),
		gitDirs:     make(map[string]bool),
		gitSnapshot: make(map[string]fileSig),
		seenGit:     make(map[string]bool),
		hostLock:    hostLock,
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, iso.abort())
			iso = nil
		}
	}()

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
	iso.handedOff = true
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
	var errs []error
	if iso.handedOff {
		errs = append(errs, iso.restore(false))
	} else {
		errs = append(errs, iso.restoreModes())
	}
	if iso.privateDir != "" {
		if err := os.RemoveAll(iso.privateDir); err != nil {
			errs = append(errs, stepError(iso.step, "remove the folder", iso.privateDir, err))
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
// a run below managedRoot (paths.dag_run_work_dir) that belongs to the user of
// the worker gets the bit x for others until the end of the step. Any other
// folder that the restricted user cannot pass stops the step.
func (iso *isolation) openPath(path, managedRoot string) error {
	managed := ""
	if managedRoot != "" {
		if resolved, err := filepath.EvalSymlinks(managedRoot); err == nil {
			managed = resolved
		}
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o001 == 0 {
			uid, _ := ownerOf(info)
			if managed == "" || !within(managed, dir) || uid != iso.workerUID {
				return fmt.Errorf("the folder %s has the mode %04o: the restricted user cannot pass it", dir, info.Mode().Perm())
			}
			if err := iso.passDir(dir); err != nil {
				return err
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			return nil
		}
	}
}

// passDir gives the bit x for others to the folder, through a descriptor that
// the isolation keeps for the restore of its mode.
func (iso *isolation) passDir(dir string) error {
	if _, done := iso.passDirs[dir]; done {
		return nil
	}
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return &fs.PathError{Op: "stat", Path: dir, Err: err}
	}
	mode := st.Mode & 0o7777
	iso.passDirs[dir] = protectedDir{fd: fd, mode: mode}
	return chmodFDPath(fd, dir, mode|0o001)
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
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return stepError(iso.step, "write the file", path, err)
	}
	if err := os.Lchown(path, int(iso.uid), int(iso.gid)); err != nil {
		return stepError(iso.step, "change the owner of", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return stepError(iso.step, "change the mode of", path, err)
	}
	return nil
}

// writeOwnedFile writes a file of the attempt in the folder of the step.
func (iso *isolation) writeOwnedFile(name string, data []byte) (string, error) {
	if iso.ended.Load() {
		return "", fmt.Errorf("untrusted step %q: the step ended", iso.step)
	}
	path := filepath.Join(iso.privateDir, strconv.FormatInt(iso.files.Add(1), 10)+"-"+name)
	if err := iso.writeRestrictedFile(path, data, 0o400); err != nil {
		return "", err
	}
	return path, nil
}

// prepare applies the isolation to cmd.
func (iso *isolation) prepare(cmd *exec.Cmd, env []string) error {
	if iso.ended.Load() {
		return fmt.Errorf("untrusted step %q: the step ended", iso.step)
	}
	if len(cmd.ExtraFiles) > 0 {
		return fmt.Errorf("untrusted step %q: the process cannot get extra files", iso.step)
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
	iso.generation.Add(1)
	return nil
}

// started watches the root process of the step. When it ends, Dagu stops each
// process of the restricted user, so that a process that it left cannot keep
// the pipes of the step open. The watch stops when the next process of the
// step starts: it does not stop that process.
func (iso *isolation) started(pid int) {
	if pid <= 0 {
		return
	}
	generation := iso.generation.Load()
	// The process is not reaped yet: its caller has not called Wait.
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return
	}
	iso.watchers.Go(func() {
		defer func() { _ = unix.Close(fd) }()
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} // #nosec G115 -- a file descriptor fits in int32.
		for !iso.stopping.Load() {
			n, err := unix.Poll(fds, 200)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return
			}
			if n > 0 {
				iso.killLeftovers(generation)
				return
			}
		}
	})
}

// killLeftovers stops the processes of the restricted user while no next
// process of the step started. The check of the count comes after each scan:
// a process that the scan sees started after the count changed.
func (iso *isolation) killLeftovers(generation int64) {
	deadline := time.Now().Add(killTimeout)
	for !iso.stopping.Load() && time.Now().Before(deadline) {
		pids, err := userProcesses(iso.uid)
		if err != nil || len(pids) == 0 {
			return
		}
		if iso.generation.Load() != generation {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// end runs the actions of the end of the step (Spec 092, "End"). An error that
// wraps ErrRunAborted stops the run.
func (iso *isolation) end(_ context.Context) error {
	iso.ended.Store(true)
	defer iso.releaseSlot()

	iso.stopping.Store(true)
	iso.watchers.Wait()
	if killErr := killUser(iso.uid, iso.gid); killErr != nil {
		// A live process of the restricted user could watch the later steps.
		err := stepError(iso.step, "stop the processes of the restricted user", "", killErr)
		poison(err)
		return fmt.Errorf("%w: %w", ErrRunAborted, err)
	}

	var errs []error
	errs = append(errs, iso.restore(true))
	errs = append(errs, iso.checkGitRemoved())
	errs = append(errs, iso.copyOutput())
	if err := os.RemoveAll(iso.privateDir); err != nil {
		errs = append(errs, stepError(iso.step, "remove the folder", iso.privateDir, err))
	}
	err := errors.Join(errs...)
	if errors.Is(err, ErrRunAborted) {
		poison(err)
	}
	return err
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

// shareReadOnly lets the restricted user read the tree at path and not write
// it. Each folder gets the mode 0555 and each file the mode 0444; the owner
// stays the user of the worker. A link keeps its mode. The walk acts on
// descriptors, so a path that changes under it cannot redirect a change of mode.
func shareReadOnly(step, path string) error {
	var chmodErr error
	err := walkFD(path, func(e *fsEntry) (bool, error) {
		if e.isLink() {
			return true, nil
		}
		mode := uint32(0o444)
		if e.isDir() {
			mode = 0o555
		}
		if err := chmodFD(e, mode); err != nil {
			chmodErr = stepError(step, "change the mode of", e.path, err)
			return false, chmodErr
		}
		return false, nil
	})
	if chmodErr != nil {
		return chmodErr
	}
	if err != nil {
		return stepError(step, "read", path, err)
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
// folder path. A folder that does not exist counts as writable: the user could
// make it.
func writableBy(path string, uid, gid uint32) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	owner, group := ownerOf(info)
	mode := info.Mode().Perm()
	return (owner == int(uid) && mode&0o200 != 0) || (group == int(gid) && mode&0o020 != 0) || mode&0o002 != 0
}
