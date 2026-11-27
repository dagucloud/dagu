// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The isolation tests need the user ID 0, the six capabilities and the flag
// no-new-privileges, as in a root container started with
// --security-opt no-new-privileges. CI runs them in the job Test compatibility,
// with requireIsolationEnv set so that a missing condition fails them.
const testUID, testGID = 64011, 64011

const requireIsolationEnv = "DAGU_TEST_REQUIRE_ISOLATION"

const changeUserHelperEnv = "DAGU_TEST_CHANGE_USER_HELPER"

func requireIsolation(t *testing.T) {
	t.Helper()
	if err := checkIsolationSupport(); err != nil {
		if os.Getenv(requireIsolationEnv) == "1" {
			t.Fatalf("%s=1 and the process cannot isolate a step: %v", requireIsolationEnv, err)
		}
		t.Skipf("the isolation tests need a root process with no-new-privileges: %v", err)
	}
}

// testWorkDir makes a work directory of the mode 0755 below the system temp
// folder, which any user can pass, with a repository: repo/.git/hooks,
// repo/.git/config and repo/src/main.txt.
func testWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dagu-untrusted-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir); _ = os.Remove(dir + handoffSuffix) })
	require.NoError(t, os.Chmod(dir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo", ".git", "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "repo", ".git", "config"), []byte("[core]\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo", "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "repo", "src", "main.txt"), []byte("main\n"), 0o644))
	return dir
}

// beginStep calls Begin and ends the step it returns when the test ends, so
// that a failed assertion does not keep the restricted user from the next
// test.
func beginStep(t *testing.T, ctx context.Context, req Request) (*Step, error) {
	t.Helper()
	step, err := Begin(ctx, req)
	if step != nil {
		t.Cleanup(func() { _ = step.End(context.Background()) })
	}
	return step, err
}

func testRequest(t *testing.T, workDir, workingDir string) Request {
	t.Helper()
	return Request{
		StepName:    "verify",
		Config:      config.UntrustedConfig{Enabled: true, UID: testUID, GID: testGID},
		WorkDir:     workDir,
		WorkingDir:  workingDir,
		OutputLimit: 1 << 20,
		Log:         &bytes.Buffer{},
	}
}

// runAs starts the script as an untrusted process of the step and waits for
// it. It returns the stdout of the script.
func runAs(t *testing.T, ctx context.Context, dir, script string, env ...string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	process, err := StartManagedProcess(ctx, cmd)
	require.NoError(t, err)
	defer func() { _ = process.Release() }()
	// A caller such as the ACP harness reads the directory after the start.
	assert.Equal(t, dir, cmd.Dir)
	assert.Equal(t, []string{"/bin/sh", "-c", script}, cmd.Args)
	require.NoError(t, process.Wait(), "stderr: %s", stderr.String())
	return stdout.String()
}

// The helpers must lose their capabilities even when the worker preserves
// them across setuid. Signal 0 checks permission without stopping the parent.
func TestChangeToUserDropsCaps(t *testing.T) {
	if os.Getenv(changeUserHelperEnv) == "1" {
		require.NoError(t, changeToUser(testUID, testGID))
		for range 10 {
			runtime.Gosched()
			header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
			var caps [2]unix.CapUserData
			require.NoError(t, unix.Capget(&header, &caps[0]))
			require.Equal(t, [2]unix.CapUserData{}, caps)
			ambient, err := unix.PrctlRetInt(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_IS_SET, unix.CAP_KILL, 0, 0)
			require.NoError(t, err)
			require.Zero(t, ambient)
			require.ErrorIs(t, syscall.Kill(os.Getppid(), 0), syscall.EPERM)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to change user IDs and securebits")
	}
	setpriv, err := exec.LookPath("setpriv")
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(setpriv, "--no-new-privs", "--securebits=+no_setuid_fixup", //nolint:gosec // The test re-executes its own binary through setpriv.
		"--inh-caps=+kill", "--ambient-caps=+kill", executable, "-test.run=^TestChangeToUserDropsCaps$")
	cmd.Env = append(os.Environ(), changeUserHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

// Spec 103, "Isolation", rules 4, 5 and 7: the process runs as the restricted
// user with no supplementary group, no capability, no descriptor of the worker
// but 0, 1 and 2, in a session of its own, with a private HOME. It cannot read
// a file of the worker, and it can write the work directory. End stops each
// of its processes, copies the output file and removes the HOME.
func TestIsolatedStepRunsAsTheRestrictedUser(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	rootOnly := workDir + "-root-only"
	require.NoError(t, os.WriteFile(rootOnly, []byte("worker secret"), 0o400))
	t.Cleanup(func() { _ = os.Remove(rootOnly) })
	// A descriptor without close-on-exec, as a worker inherits from its
	// launcher, must not reach the process.
	leaked, err := syscall.Open(rootOnly, syscall.O_RDONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Close(leaked) })

	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	require.True(t, step.Isolated())
	outputFile := filepath.Join(t.TempDir(), "step.output")
	require.NoError(t, os.WriteFile(outputFile, nil, 0o600))
	processOutput := step.OutputFile(outputFile)
	assert.NotEqual(t, outputFile, processOutput)
	home := step.Home()

	script := strings.Join([]string{
		`echo "ids=$(id -u) $(id -g) $(id -G)"`,
		`echo "session=$(cut -d' ' -f6 /proc/$$/stat) pid=$$"`,
		`grep -E '^Cap(Prm|Eff|Amb):' /proc/$$/status | tr -s '\t' ' '`,
		// Outside a command substitution, sh holds no pipe while ls lists.
		`echo fds:; ls /proc/$$/fd; echo end`,
		`echo "home=$HOME tmp=$TMPDIR $(stat -c '%a %u' "$HOME")"`,
		`cat ` + rootOnly + ` >/dev/null 2>&1 && echo read-worker:allowed || echo read-worker:denied`,
		`touch new 2>/dev/null && echo write-work:allowed || echo write-work:denied`,
		`echo "secret=${DAGU_SECRET:-none}"`,
		`echo "answer=42" > "$DAGU_OUTPUT_FILE"`,
		`setsid sleep 1000 </dev/null >/dev/null 2>&1 &`,
	}, "\n")
	out := runAs(t, WithStep(ctx, step), workDir, script, "DAGU_SECRET=hidden", "DAGU_OUTPUT_FILE="+processOutput)
	ids := strconv.Itoa(testUID) + " " + strconv.Itoa(testGID)
	assert.Contains(t, out, "ids="+ids+" "+strconv.Itoa(testGID)+"\n", "no supplementary group")
	session := regexp.MustCompile(`session=(\d+) pid=(\d+)\n`).FindStringSubmatch(out)
	require.Len(t, session, 3, out)
	assert.Equal(t, session[2], session[1], "the process leads a new session")
	for _, set := range []string{"CapPrm", "CapEff", "CapAmb"} {
		assert.Contains(t, out, set+": 0000000000000000\n")
	}
	assert.Contains(t, out, "fds:\n0\n1\n2\nend\n")
	assert.Contains(t, out, "home="+home+" tmp="+home+" 700 "+strconv.Itoa(testUID)+"\n")
	assert.Contains(t, out, "read-worker:denied")
	assert.Contains(t, out, "write-work:allowed")
	assert.Contains(t, out, "secret=none")

	require.NoError(t, step.End(ctx))
	pids, err := userProcesses(testUID)
	require.NoError(t, err)
	assert.Empty(t, pids, "each process of the restricted user is stopped")
	assert.NoDirExists(t, home)
	output, err := os.ReadFile(outputFile)
	require.NoError(t, err)
	assert.Equal(t, "answer=42\n", string(output))
}

// A command that does not exist exits with 127, as in a shell, and says why on
// stderr.
func TestIsolatedStepCommandNotFound(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)

	missing := filepath.Join(workDir, "missing")
	cmd := exec.Command(missing)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	process, err := StartManagedProcess(WithStep(ctx, step), cmd)
	require.NoError(t, err)
	defer func() { _ = process.Release() }()
	var exitErr *exec.ExitError
	require.ErrorAs(t, process.Wait(), &exitErr)
	assert.Equal(t, 127, exitErr.ExitCode())
	assert.Contains(t, stderr.String(), "run "+missing)
}

// A working directory outside the work directory of the run fails the step.
func TestIsolatedStepPlace(t *testing.T) {
	requireIsolation(t)
	workDir := testWorkDir(t)
	_, err := beginStep(t, context.Background(), testRequest(t, workDir, "/srv"))
	require.Error(t, err)
	assert.Equal(t, `untrusted step "verify": the working directory /srv is not in the work directory of the run`, err.Error())

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(workDir, "link")))
	_, err = beginStep(t, context.Background(), testRequest(t, workDir, filepath.Join(workDir, "link", "x")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")
	assert.NoDirExists(t, filepath.Join(outside, "x"))
}

// A device node in the work directory, also in .git, fails the step: as its
// owner, or with the bit r for others, the restricted user could open the
// device. The node keeps its owner and its mode.
func TestIsolatedStepDeviceNode(t *testing.T) {
	requireIsolation(t)
	for name, dir := range map[string]string{"folder": "src", "git folder": ".git"} {
		t.Run(name, func(t *testing.T) {
			workDir := testWorkDir(t)
			node := filepath.Join(workDir, "repo", dir, "disk")
			// The device of /dev/null stands in for a disk of the host.
			require.NoError(t, unix.Mknod(node, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3)))) // #nosec G115 -- a small device number.
			_, err := beginStep(t, context.Background(), testRequest(t, workDir, workDir))
			require.Error(t, err)
			assert.Contains(t, err.Error(), node+": the work directory cannot hold a device node")
			assert.Equal(t, os.Geteuid(), ownerUID(t, node))
			assert.Equal(t, fs.FileMode(0o600), permOf(t, node))
		})
	}
}

// A socket in the work directory keeps the worker as owner and its mode: as
// its owner, the restricted user could set the bit w and connect to the
// program that listens, such as a daemon of root.
func TestIsolatedStepKeepsSockets(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	sock := filepath.Join(workDir, "repo", "src", "d.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	require.NoError(t, os.Chmod(sock, 0o660))
	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)

	out := runAs(t, WithStep(ctx, step), workDir, `chmod 666 repo/src/d.sock 2>/dev/null && echo chmod:allowed || echo chmod:denied`)
	assert.Equal(t, "chmod:denied\n", out)
	require.NoError(t, step.End(ctx))
	assert.Equal(t, os.Geteuid(), ownerUID(t, sock))
	assert.Equal(t, fs.FileMode(0o660), permOf(t, sock))
}

// Untrusted steps run one at a time. The second step waits with a line in its
// log.
func TestIsolatedStepsOneAtATime(t *testing.T) {
	requireIsolation(t)
	workDir := testWorkDir(t)
	first, err := beginStep(t, context.Background(), testRequest(t, workDir, workDir))
	require.NoError(t, err)

	req := testRequest(t, workDir, workDir)
	log := &bytes.Buffer{}
	req.Log = log
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = beginStep(t, ctx, req)
	require.Error(t, err)
	assert.Contains(t, log.String(), WaitingLine)

	require.NoError(t, first.End(context.Background()))
	second, err := beginStep(t, context.Background(), testRequest(t, workDir, workDir))
	require.NoError(t, err)
	require.NoError(t, second.End(context.Background()))
}

// Begin with an ended context fails before it checks the process or stops a
// process of the restricted user, also when both locks are free.
func TestBeginWithEndedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	workDir := testWorkDir(t)
	req := testRequest(t, workDir, workDir)
	req.LockDir = t.TempDir()
	step, err := beginStep(t, ctx, req)
	assert.Nil(t, step)
	require.ErrorIs(t, err, context.Canceled)
}

// taskOfUser reads the four user IDs of a status, and skips a zombie task.
func TestTaskOfUser(t *testing.T) {
	t.Parallel()
	status := "Name:\tsleep\nState:\tS (sleeping)\nUid:\t0\t64011\t0\t0\n"
	assert.True(t, taskOfUser(status, 64011))
	assert.False(t, taskOfUser(status, 64012))
	assert.False(t, taskOfUser(strings.Replace(status, "S (sleeping)", "Z (zombie)", 1), 64011))
}

// killTaskGroup kills a process only while it has a task of the user, so a PID
// that a process of another user took since the scan is spared. Each process
// then gets SIGTERM: one that SIGKILL already reached ends with SIGKILL, as the
// kernel drops a signal to a dying process.
func TestKillTaskGroup(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		uid  uint32
		want syscall.Signal
	}{
		"owner":      {uid: uint32(os.Getuid()), want: syscall.SIGKILL},
		"other user": {uid: testUID, want: syscall.SIGTERM},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sleep := exec.Command("/bin/sleep", "60")
			require.NoError(t, sleep.Start())
			killTaskGroup(sleep.Process.Pid, tc.uid)
			require.NoError(t, sleep.Process.Signal(syscall.SIGTERM))
			var exitErr *exec.ExitError
			require.ErrorAs(t, sleep.Wait(), &exitErr)
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			assert.Equal(t, tc.want, status.Signal())
		})
	}
}

// The parser of /proc/self/status reads the effective user ID, the effective
// capabilities and NoNewPrivs.
func TestParseStatus(t *testing.T) {
	t.Parallel()
	priv, err := parseStatus("Uid:\t1000\t0\t0\t0\nCapEff:\t00000000000000eb\nNoNewPrivs:\t1\n")
	require.NoError(t, err)
	assert.Equal(t, 0, priv.euid)
	assert.True(t, priv.noNewPrivs)
	assert.NoError(t, priv.checkChange())

	priv.capEff &^= 1 << 3
	assert.EqualError(t, priv.checkChange(), "the effective capability CAP_FOWNER is absent")
	priv.euid = 1000
	assert.EqualError(t, priv.checkChange(), "the effective user ID is 1000, not 0")

	_, err = parseStatus("Uid:\t0\t0\t0\t0\nCapEff:\t0\n")
	assert.ErrorContains(t, err, "NoNewPrivs")
}

// Spec 103, "Isolation": a ptrace_scope of 0, or no Yama, would let the
// restricted user trace the kill helper.
func TestCheckPtraceScope(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1\n", "2\n", "3\n"} {
		assert.NoError(t, checkPtraceScope(value), value)
	}
	for _, value := range []string{"0\n", "", "x\n"} {
		assert.ErrorContains(t, checkPtraceScope(value), "the restricted user could trace the kill helper", value)
	}
}

func TestProcHidePID(t *testing.T) {
	t.Parallel()
	line := "22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw,hidepid=invisible\n"
	option, hidden := procHidePID(line, []int{0})
	assert.True(t, hidden)
	assert.Equal(t, "hidepid=invisible", option)
	_, hidden = procHidePID(strings.Replace(line, ",hidepid=invisible", "", 1), []int{0})
	assert.False(t, hidden)
	// The option gid of a group of the worker keeps each process visible to it.
	_, hidden = procHidePID(strings.Replace(line, "hidepid=invisible", "hidepid=invisible,gid=0", 1), []int{0})
	assert.False(t, hidden)
	_, hidden = procHidePID(strings.Replace(line, "hidepid=invisible", "hidepid=invisible,gid=5", 1), []int{0})
	assert.True(t, hidden)
}

// Spec 103, "Isolation", rule 3: the restricted user cannot write .git,
// rename it or remove it, and can write the other files. At the end, the work
// directory belongs to the worker again, with its mode, and no longer has the
// marker of the handoff.
func TestIsolatedStep(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	step, err := beginStep(t, ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)

	out := runAs(t, WithStep(ctx, step), repo, strings.Join([]string{
		`(echo x > .git/hooks/x) 2>/dev/null && echo write-git:allowed || echo write-git:denied`,
		`mv .git .gitx 2>/dev/null && echo move-git:allowed || echo move-git:denied`,
		`rm -rf .git 2>/dev/null; [ -d .git/hooks ] && echo remove-git:denied || echo remove-git:allowed`,
		`echo new > src/new.txt && echo write-src:allowed || echo write-src:denied`,
		`echo changed > src/main.txt && echo change-src:allowed || echo change-src:denied`,
	}, "\n"))
	assert.Equal(t, "write-git:denied\nmove-git:denied\nremove-git:denied\nwrite-src:allowed\nchange-src:allowed\n", out)

	require.NoError(t, step.End(ctx))
	assert.NoError(t, CheckWorkDir(workDir))
	assert.NoFileExists(t, filepath.Join(repo, ".git", "hooks", "x"))
	err = filepath.WalkDir(workDir, func(path string, _ fs.DirEntry, err error) error {
		require.NoError(t, err)
		assert.Equal(t, os.Geteuid(), ownerUID(t, path), path)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o755), permOf(t, workDir))
	assert.Equal(t, fs.FileMode(0o755), permOf(t, filepath.Join(repo, ".git")))
	data, err := os.ReadFile(filepath.Join(repo, "src", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "changed\n", string(data))
}

// A new .git that the restricted user makes fails the step, and Dagu removes
// it.
func TestIsolatedStepNewGit(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := beginStep(t, ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
	require.NoError(t, err)
	runAs(t, WithStep(ctx, step), filepath.Join(workDir, "repo"), `mkdir -p src/.git/hooks && echo evil > src/.git/hooks/pre-commit`)

	err = step.End(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the restricted user made the entry")
	assert.Contains(t, err.Error(), filepath.Join("src", ".git"))
	assert.NoDirExists(t, filepath.Join(workDir, "repo", "src", ".git"))
}

// A file with more than one link in the group of the restricted user fails the
// step: a change of owner would also give its path outside the work directory.
// The failed Begin gives back what it handed over and removes nothing, since
// no process of the restricted user ran.
func TestIsolatedStepHardLink(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	src := filepath.Join(workDir, "repo", "src")
	outside := filepath.Join(workDir, "..", filepath.Base(workDir)+"-store")
	require.NoError(t, os.WriteFile(outside, []byte("store\n"), 0o644))
	t.Cleanup(func() { _ = os.Remove(outside) })
	// The handoff walks in name order: the link to outside goes first.
	tool := filepath.Join(src, "a-tool")
	require.NoError(t, os.Symlink(outside, tool))
	linked := filepath.Join(src, "z-linked.txt")
	require.NoError(t, os.Link(outside, linked))
	// The group of the restricted user does not make the file its own.
	require.NoError(t, os.Chown(linked, -1, testGID))

	step, err := Begin(ctx, testRequest(t, workDir, workDir))
	if err == nil {
		_ = step.End(ctx)
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the file has more than one link")
	assert.Contains(t, err.Error(), linked)
	assert.Equal(t, os.Geteuid(), ownerUID(t, outside), "the other path keeps its owner")
	for _, path := range []string{tool, linked, filepath.Join(src, "main.txt")} {
		_, err := os.Lstat(path)
		require.NoError(t, err, "a failed Begin removes nothing")
		assert.Equal(t, os.Geteuid(), ownerUID(t, path), path)
	}
	assert.NoError(t, CheckWorkDir(workDir), "a failed Begin removes its marker")
}

// A file of the worker with more than one link, as uv, pnpm or bun link a
// package of a cache of the worker into the work directory, stays with the
// worker: the restricted user can read it, and cannot write it or link it.
// Such a file that others can write fails the step.
func TestIsolatedStepKeepsLinkedFilesOfTheWorker(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	cache := filepath.Join(workDir, "..", filepath.Base(workDir)+"-cache")
	require.NoError(t, os.WriteFile(cache, []byte("pkg\n"), 0o644))
	t.Cleanup(func() { _ = os.Remove(cache) })
	pkg := filepath.Join(workDir, "pkg.py")
	require.NoError(t, os.Link(cache, pkg))

	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	out := runAs(t, WithStep(ctx, step), workDir, strings.Join([]string{
		`cat pkg.py`,
		`(echo x > pkg.py) 2>/dev/null && echo write:allowed || echo write:denied`,
		`ln pkg.py pkg2.py 2>/dev/null && echo link:allowed || echo link:denied`,
	}, "\n"))
	assert.Equal(t, "pkg\nwrite:denied\nlink:denied\n", out)
	require.NoError(t, step.End(ctx))
	assert.Equal(t, os.Geteuid(), ownerUID(t, cache))
	assert.Equal(t, os.Geteuid(), ownerUID(t, pkg))

	require.NoError(t, os.Chmod(cache, 0o646))
	_, err = beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the file has more than one link")
}

// A file that the step links from its HOME, as uv links a package of its cache
// into a virtual environment, stays after End: the HOME is gone before the
// checks, so the file has one link left.
func TestIsolatedStepKeepsLinksFromHome(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	runAs(t, WithStep(ctx, step), workDir, `mkdir "$HOME/cache" && echo pkg > "$HOME/cache/pkg.py" && ln "$HOME/cache/pkg.py" pkg.py`)

	require.NoError(t, step.End(ctx))
	pkg := filepath.Join(workDir, "pkg.py")
	data, err := os.ReadFile(pkg)
	require.NoError(t, err)
	assert.Equal(t, "pkg\n", string(data))
	assert.Equal(t, os.Geteuid(), ownerUID(t, pkg))
}

// A link or a FIFO of the restricted user with a second path outside the work
// directory fails the step, and Dagu removes it. A change of owner would also
// give the second path to the worker: a link of root in a folder of mode 1777
// of root is one that fs.protected_symlinks lets root follow. The second path
// keeps the restricted user as owner.
func TestIsolatedStepLinkedEntry(t *testing.T) {
	requireIsolation(t)
	for name, mk := range map[string]string{
		"link": `ln -s ../../x a/b/e`,
		"FIFO": `mkfifo a/b/e`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			workDir := testWorkDir(t)
			// A folder such as /var/tmp on the file system of the work directory.
			shared, err := os.MkdirTemp("", "dagu-untrusted-shared-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(shared) })
			require.NoError(t, os.Chmod(shared, 0o1777))
			step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
			require.NoError(t, err)
			runAs(t, WithStep(ctx, step), workDir, "mkdir -p a/b && "+mk+" && ln -P a/b/e "+shared+"/e")

			entry := filepath.Join(workDir, "a", "b", "e")
			err = step.End(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "made the file "+entry+" with more than one link")
			_, err = os.Lstat(entry)
			assert.ErrorIs(t, err, fs.ErrNotExist, "Dagu removes the entry")
			assert.Equal(t, testUID, ownerUID(t, filepath.Join(shared, "e")))
		})
	}
}

// A git object with more than one link, as git clone --local makes them, can
// get a path outside the work directory or new times during the step, as at
// the clone of another run or a refresh of the pack by git there. End finds no
// change in git. A change of its owner still stops the run.
func TestIsolatedStepLinkedGitObject(t *testing.T) {
	requireIsolation(t)
	resetPoison(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	mirror := filepath.Join(workDir, "..", filepath.Base(workDir)+"-mirror")
	require.NoError(t, os.WriteFile(mirror, []byte("pack\n"), 0o444))
	clone := mirror + "-clone"
	t.Cleanup(func() { _ = os.Remove(mirror); _ = os.Remove(clone) })
	object := filepath.Join(workDir, "repo", ".git", "objects", "pack")
	require.NoError(t, os.MkdirAll(filepath.Dir(object), 0o755))
	require.NoError(t, os.Link(mirror, object))

	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	require.NoError(t, os.Link(mirror, clone))
	hourAgo := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(clone, hourAgo, hourAgo))
	require.NoError(t, step.End(ctx))
	assert.NoError(t, Poisoned())

	step, err = beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	require.NoError(t, os.Chown(clone, testUID, -1))
	err = step.End(ctx)
	require.ErrorIs(t, err, ErrRunAborted)
	assert.ErrorContains(t, err, "the git entry "+object+" changed: its owner is")
}

// A file with more than one link whose ACL lets the restricted user write it
// fails the step, in a folder and in a git folder: the handoff keeps such a
// file with the worker only when the restricted user cannot write it.
func TestIsolatedStepLinkedFileWithACL(t *testing.T) {
	requireIsolation(t)
	for name, rel := range map[string]string{"folder": "pkg.py", "git folder": "repo/.git/objects/pack"} {
		t.Run(name, func(t *testing.T) {
			workDir := testWorkDir(t)
			cache := filepath.Join(workDir, "..", filepath.Base(workDir)+"-cache")
			require.NoError(t, os.WriteFile(cache, []byte("pkg\n"), 0o444))
			t.Cleanup(func() { _ = os.Remove(cache) })
			setUserACL(t, cache, aclNames[0])
			path := filepath.Join(workDir, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.Link(cache, path))

			_, err := beginStep(t, context.Background(), testRequest(t, workDir, workDir))
			require.Error(t, err)
			assert.ErrorContains(t, err, "the file has more than one link")
		})
	}
}

// A tree too deep to check stops the run and keeps the work directory marked.
// The restricted user can make one, so it leaves the other runs of the process
// alone.
func TestIsolatedStepDeepTree(t *testing.T) {
	requireIsolation(t)
	resetPoison(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := beginStep(t, ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	runAs(t, WithStep(ctx, step), workDir, "mkdir -p a/"+strings.Repeat("d/", maxWalkDepth))

	err = step.End(ctx)
	require.ErrorIs(t, err, ErrRunAborted)
	assert.ErrorContains(t, err, "deeper than")
	assert.NoError(t, Poisoned())
	assert.ErrorIs(t, CheckWorkDir(workDir), ErrRunAborted, "the work directory stays marked")
}

// A handoff that fails part way leaves a snapshot of a part of the git folders.
// No process of the restricted user ran, so the restore of the abort keeps each
// git entry of the worker. It needs no root.
func TestRestoreAfterAFailedHandoffKeepsTheRepositories(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	gitDir := filepath.Join(iso.workDir, "repo", ".git")
	for _, dir := range []string{"objects", "refs/heads", "hooks"} {
		require.NoError(t, os.MkdirAll(filepath.Join(gitDir, dir), 0o750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o600))
	iso.gitEntries[gitDir], iso.gitDirs[gitDir] = true, true
	require.NoError(t, iso.restore(false))
	for _, name := range []string{"HEAD", "config", "objects", "refs/heads", "hooks"} {
		_, err := os.Lstat(filepath.Join(gitDir, name))
		assert.NoError(t, err, name)
	}
}

// An entry that the restore must remove and cannot remove stays in the work
// directory, so the run stops: no trusted step runs on it. The test needs a
// user other than root, which removes the entry anyway.
func TestRestoreThatCannotRemoveAnEntryAbortsTheRun(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root removes an entry of a folder of mode 0555")
	}
	iso := selfIsolation(t, t.TempDir())
	sub := filepath.Join(iso.workDir, "sub")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, ".git"), 0o750))
	require.NoError(t, os.Chmod(sub, 0o555))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o750) })
	err := iso.restore(true)
	require.ErrorIs(t, err, ErrRunAborted)
}

// selfIsolation returns an isolation whose restricted user is the user of the
// test, so that the handoff and the restore run without root.
func selfIsolation(t *testing.T, work string) *isolation {
	t.Helper()
	real, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)
	return &isolation{
		step: "s", workDir: real, lexicalWork: work,
		uid: uint32(os.Getuid()), gid: uint32(os.Getgid()), // #nosec G115 -- IDs fit in uint32.
		workerUID: os.Geteuid(), workerGID: os.Getegid(),
		protected:  map[string]protectedDir{},
		gitEntries: map[string]bool{}, gitDirs: map[string]bool{},
		gitSnapshot: map[string]fileSig{}, seenGit: map[string]bool{}, gitModes: map[string]uint32{},
		givenLinks: map[string]givenLink{},
	}
}

// The handoff marks the work directory until a restore makes it safe. A mark
// that a stopped process left fails the next handoff and each step of the run,
// and that handoff gives nothing away. It needs no root.
func TestHandoffMarksTheWorkDirectory(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	iso := selfIsolation(t, work)
	require.NoError(t, iso.handoff())
	assert.ErrorIs(t, CheckWorkDir(work), ErrRunAborted)

	// The process of iso stops before the restore.
	next := selfIsolation(t, work)
	err := next.handoff()
	require.ErrorIs(t, err, ErrRunAborted)
	assert.ErrorContains(t, err, "not given back safely")
	assert.False(t, next.handedOff, "the next step gives nothing away")

	require.NoError(t, iso.unmarkUnlessAborted(iso.restore(true)))
	assert.NoError(t, CheckWorkDir(work))
	require.NoError(t, next.handoff())
	require.NoError(t, next.unmarkUnlessAborted(next.restore(true)))
}

// abortBegin undoes the Begin of iso, which failed: abort gives back the slot
// that Begin took.
func abortBegin(iso *isolation) error {
	slot <- struct{}{}
	return iso.abort()
}

// A handoff that fails before it changes an owner, as at a git folder outside
// the work directory, changes nothing at the abort: a file of the worker keeps
// its set-user-ID bit. It needs no root.
func TestAbortBeforeTheHandoffChangesNothing(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	tool := filepath.Join(iso.workDir, "tool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Chmod(tool, 0o755|fs.ModeSetuid))
	repo := filepath.Join(iso.workDir, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: "+t.TempDir()+"\n"), 0o644))

	require.ErrorContains(t, iso.handoff(), "which is not in the work directory")
	require.NoError(t, abortBegin(iso))
	info, err := os.Lstat(tool)
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSetuid, info.Mode()&fs.ModeSetuid)
	assert.NoError(t, CheckWorkDir(iso.lexicalWork), "the abort removes the marker")
}

// An abort whose restore stops the run, as at a tree too deep to check, keeps
// the work directory marked and leaves the other runs of the process alone.
// It needs no root.
func TestAbortThatStopsTheRunKeepsTheProcess(t *testing.T) {
	resetPoison(t)
	iso := selfIsolation(t, t.TempDir())
	require.NoError(t, iso.handoff())
	require.NoError(t, os.MkdirAll(filepath.Join(iso.workDir, "a", strings.Repeat("d/", maxWalkDepth)), 0o750))

	require.ErrorIs(t, abortBegin(iso), ErrRunAborted)
	assert.NoError(t, Poisoned())
	assert.ErrorIs(t, CheckWorkDir(iso.lexicalWork), ErrRunAborted, "the work directory stays marked")
}

// A process that the worker starts during the step, such as a step of another
// run, gets no descriptor of the work directory that the handoff keeps. It
// needs no root.
func TestHandoffKeepsNoDescriptorForChildren(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	require.NoError(t, iso.handoff())
	t.Cleanup(func() { _ = iso.unmarkUnlessAborted(iso.restore(true)) })

	// The descriptor of the listing is gone once readlink runs.
	out, err := exec.Command("/bin/sh", "-c", `for fd in /proc/$$/fd/*; do readlink "$fd" || true; done`).Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), iso.workDir)
}

// A FIFO named commondir fails the handoff instead of holding it until a writer
// opens the FIFO. It needs no root.
func TestHandoffRefusesAFIFOGitPointer(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	dir := filepath.Join(iso.workDir, "sub")
	require.NoError(t, os.Mkdir(dir, 0o755))
	common := filepath.Join(dir, "commondir")
	require.NoError(t, unix.Mkfifo(common, 0o600))

	err := iso.handoff()
	require.Error(t, err)
	assert.ErrorContains(t, err, common+": not a regular file")
	require.NoError(t, iso.unmarkUnlessAborted(iso.restore(false)))
}

// git follows the link in a .git file before a ".." after it: with link ->
// <outside>/a/b, the text link/../x names <outside>/a/x, not the folder x next
// to link. The handoff resolves the text in the same order, so it fails for
// the git folder outside the work directory. It needs no root.
func TestHandoffResolvesTheGitFileAsGit(t *testing.T) {
	t.Parallel()
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "a", "b"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "a", "x"), 0o755))
	iso := selfIsolation(t, t.TempDir())
	repo := filepath.Join(iso.workDir, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "x"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(outside, "a", "b"), filepath.Join(repo, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: link/../x\n"), 0o644))

	err = iso.handoff()
	require.Error(t, err)
	assert.ErrorContains(t, err, "names the git folder "+filepath.Join(outside, "a", "x")+", which is not in the work directory")
	require.NoError(t, iso.unmarkUnlessAborted(iso.restore(false)))
}

// A file of a git folder with more than one link fails the step when the
// handoff must change its mode: the change would also open its path outside
// the work directory to the restricted user. It needs no root.
func TestHandoffRefusesALinkedGitFile(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	gitDir := filepath.Join(iso.workDir, "repo", ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	outside := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(outside, []byte("token"), 0o600))
	require.NoError(t, os.Link(outside, filepath.Join(gitDir, "config")))

	err := iso.handoff()
	require.Error(t, err)
	assert.ErrorContains(t, err, "the file has more than one link")
	assert.Equal(t, fs.FileMode(0o600), permOf(t, outside))
	require.NoError(t, iso.unmarkUnlessAborted(iso.restore(false)))
}

// setUserACL gives the entry the ACLs names with the entries user::rwx,
// user:64011:rwx, group::r-x, mask::rwx and other::r-x. It skips the test on a
// file system that takes no ACL.
func setUserACL(t *testing.T, path string, names ...string) {
	t.Helper()
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range [][3]uint32{{0x01, 7, ^uint32(0)}, {0x02, 7, testUID}, {0x04, 5, ^uint32(0)}, {0x10, 7, ^uint32(0)}, {0x20, 5, ^uint32(0)}} {
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[0])) // #nosec G115 -- small tags.
		acl = binary.LittleEndian.AppendUint16(acl, uint16(entry[1])) // #nosec G115 -- small modes.
		acl = binary.LittleEndian.AppendUint32(acl, entry[2])
	}
	for _, name := range names {
		if err := unix.Setxattr(path, name, acl, 0); err != nil {
			t.Skipf("the file system takes no ACL: %v", err)
		}
	}
}

// An ACL that the restricted user set on its own folder, such as a default ACL
// that would let it write a .git that a later trusted step makes there, does
// not survive the restore. It needs no root.
func TestRestoreRemovesACLs(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	require.NoError(t, iso.handoff())
	dir := filepath.Join(iso.workDir, "out")
	require.NoError(t, os.Mkdir(dir, 0o755))
	setUserACL(t, dir, aclNames...)

	require.NoError(t, iso.unmarkUnlessAborted(iso.restore(true)))
	for _, name := range aclNames {
		_, err := unix.Getxattr(dir, name, nil)
		assert.ErrorIs(t, err, unix.ENODATA, name)
	}
}

// A tree deeper than maxWalkDepth that the restricted user made, also in a new
// .git that the restore must remove, stops the run instead of holding a
// descriptor and a path for each level of the walk. It needs no root.
func TestRestoreRefusesADeepTree(t *testing.T) {
	t.Parallel()
	for name, parent := range map[string]string{"folder": "deep", "new .git": ".git"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			iso := selfIsolation(t, t.TempDir())
			require.NoError(t, iso.handoff())
			deep := filepath.Join(iso.workDir, "a", parent, strings.Repeat("d/", maxWalkDepth))
			require.NoError(t, os.MkdirAll(deep, 0o750))

			err := iso.unmarkUnlessAborted(iso.restore(true))
			require.ErrorIs(t, err, ErrRunAborted)
			assert.ErrorContains(t, err, "deeper than")
			assert.ErrorIs(t, CheckWorkDir(iso.lexicalWork), ErrRunAborted, "the work directory stays marked")
		})
	}
}

// The depth of a walk of a git folder counts from the work directory, so a git
// folder below the work directory cannot hold maxWalkDepth more folders. It
// needs no root.
func TestHandoffRefusesADeepGitFolder(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	// repo and .git are 2 folders deep, so the last d is past maxWalkDepth.
	require.NoError(t, os.MkdirAll(filepath.Join(iso.workDir, "repo", ".git", strings.Repeat("d/", maxWalkDepth-1)), 0o750))

	err := iso.handoff()
	require.Error(t, err)
	assert.ErrorContains(t, err, "deeper than")
	_ = iso.unmarkUnlessAborted(iso.restore(false))
}

func permOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}

// A chain of links can leave the work directory while the text of each target
// stays in it: l1 -> ., l2 -> <work>/l1/.., l3 -> l2/.., up to the root, then
// out -> lN/<outside>. The restore follows the links as the kernel does, so a
// later trusted write through out cannot reach the folder outside. It needs no
// root.
func TestRestoreFollowsLinkChains(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	work := iso.workDir
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, iso.handoff())

	require.NoError(t, os.Symlink(".", filepath.Join(work, "l1")))
	// Not filepath.Join, which would clean the text to the work directory.
	require.NoError(t, os.Symlink(work+"/l1/..", filepath.Join(work, "l2")))
	last := "l2"
	for i := 3; i <= strings.Count(work, "/")+1; i++ {
		name := "l" + strconv.Itoa(i)
		require.NoError(t, os.Symlink(last+"/..", filepath.Join(work, name)))
		last = name
	}
	out := filepath.Join(work, "out")
	require.NoError(t, os.Symlink(last+outside, out))
	reached, err := filepath.EvalSymlinks(out)
	require.NoError(t, err)
	require.Equal(t, outside, reached, "the chain leaves the work directory")

	var bad bool
	require.NoError(t, walkFD(work, func(e *fsEntry) (bool, error) {
		if e.name == "out" {
			_, bad = iso.badLink(e)
		}
		return false, nil
	}))
	assert.True(t, bad, "the link out leads outside the work directory")

	err = iso.restore(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "made the link "+filepath.Join(work, "l2"))
	_ = os.WriteFile(filepath.Join(out, "x"), []byte("x"), 0o600)
	assert.NoFileExists(t, filepath.Join(outside, "x"), "a trusted write through out stays in the work directory")
}

// A link that a trusted step made before the handoff, such as one of a Python
// virtual environment to the interpreter of the host, stays after the restore.
// The restricted user can move such a link: at its new path, the restore
// checks it as a link of the restricted user. It needs no root.
func TestRestoreKeepsTheLinksOfTheWorker(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	outside := t.TempDir()
	bin := filepath.Join(iso.workDir, ".venv", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	python := filepath.Join(bin, "python3")
	require.NoError(t, os.Symlink(filepath.Join(outside, "python3"), python))
	tool := filepath.Join(bin, "tool")
	require.NoError(t, os.Symlink(filepath.Join(outside, "tool"), tool))
	require.NoError(t, iso.handoff())

	moved := filepath.Join(iso.workDir, "results.txt")
	require.NoError(t, os.Rename(tool, moved))

	err := iso.restore(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "made the link "+moved)
	assert.NotContains(t, err.Error(), python)
	_, err = os.Lstat(python)
	assert.NoError(t, err, "the link of the worker stays")
	_, err = os.Lstat(moved)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the restore removes the moved link")
}

// An entry that does not exist in the path of a link can become a folder later.
// The kernel then resolves the ".." after it through the links that exist:
// with d -> .., the link l -> missing/../d/../secret reaches the folder above the
// work directory once missing exists. The restore resolves l as if missing were
// a folder, so l counts as leaving the work directory. It needs no root.
func TestRestoreFollowsLinksPastMissingEntries(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	secret := filepath.Join(base, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(base, "work"), 0o755))
	iso := selfIsolation(t, filepath.Join(base, "work"))
	require.NoError(t, iso.handoff())

	dir := filepath.Join(iso.workDir, "a")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Symlink("..", filepath.Join(dir, "d")))
	link := filepath.Join(dir, "l")
	require.NoError(t, os.Symlink("missing/../d/../secret", link))
	missing := filepath.Join(dir, "missing")
	require.NoError(t, os.Mkdir(missing, 0o755))
	reached, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)
	require.Equal(t, secret, reached, "once missing exists, the link leaves the work directory")
	require.NoError(t, os.Remove(missing))

	bad := map[string]bool{}
	require.NoError(t, walkFD(iso.workDir, func(e *fsEntry) (bool, error) {
		if e.isLink() {
			_, bad[e.name] = iso.badLink(e)
		}
		return false, nil
	}))
	assert.Equal(t, map[string]bool{"d": false, "l": true}, bad)

	err = iso.restore(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "made the link "+link)
	_, err = os.Lstat(link)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the restore removes the link")
}

// A run can name its work directory through a link, such as alias -> real. A
// link whose target reaches a git folder through that alias still points into
// the git folder: the restore follows the alias to the real path, which is the
// path of the git folders, and removes the link. It needs no root.
func TestRestoreFollowsTheAliasOfTheWorkDirectory(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	real := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "repo", ".git"), 0o750))
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(real, alias))
	iso := selfIsolation(t, alias)
	require.NoError(t, iso.handoff())

	link := filepath.Join(real, "config")
	require.NoError(t, os.Symlink(filepath.Join(alias, "repo", ".git", "config"), link))

	err = iso.restore(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "made the link "+link)
	_, err = os.Lstat(link)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the restore removes the link")
}

// The restricted user chooses the target of a link, so the error quotes it: a
// verb such as %d stays text and a newline cannot start a line of the log. It
// needs no root.
func TestRestoreQuotesTheLinkTarget(t *testing.T) {
	t.Parallel()
	iso := selfIsolation(t, t.TempDir())
	require.NoError(t, iso.handoff())

	target := filepath.Join(t.TempDir(), "a%d\nb")
	link := filepath.Join(iso.workDir, "out")
	require.NoError(t, os.Symlink(target, link))

	err := iso.restore(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "made the link "+link+" to "+strconv.Quote(target))
}

// git follows a link at objects and refs, so a folder with HEAD, refs and a
// link objects is a git folder. The work directory itself can become one. The
// restore finds both and removes HEAD. It needs no root.
func TestRestoreFindsNewGitFolders(t *testing.T) {
	t.Parallel()
	for name, folder := range map[string]func(work string) string{
		"link objects":   func(work string) string { return filepath.Join(work, "services") },
		"work directory": func(work string) string { return work },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			iso := selfIsolation(t, t.TempDir())
			dir := folder(iso.workDir)
			require.NoError(t, os.MkdirAll(dir, 0o750))
			require.NoError(t, iso.handoff())

			require.NoError(t, os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "refs"), 0o750))
			require.NoError(t, os.Symlink("refs", filepath.Join(dir, "objects")))

			err := iso.restore(true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "made the git folder "+dir+";")
			assert.NoFileExists(t, filepath.Join(dir, "HEAD"))
		})
	}
}

// A git folder stays with the worker during the step, readable by the
// restricted user, and gets its modes back at the end. A work directory that is
// a git folder stays with the worker as a whole. It needs no root.
func TestRestoreGivesGitModesBack(t *testing.T) {
	t.Parallel()
	for name, folder := range map[string]func(work string) string{
		"entry .git":     func(work string) string { return filepath.Join(work, "repo", ".git") },
		"work directory": func(work string) string { return work },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			iso := selfIsolation(t, t.TempDir())
			dir := folder(iso.workDir)
			for _, sub := range []string{"objects", "refs"} {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o700))
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
			config := filepath.Join(dir, "config")
			require.NoError(t, os.WriteFile(config, []byte("[core]\n"), 0o600))

			require.NoError(t, iso.handoff())
			assert.True(t, iso.gitDirs[dir])
			assert.Equal(t, fs.FileMode(0o604), permOf(t, config), "the restricted user reads the config")
			require.NoError(t, iso.restore(true))
			assert.Equal(t, fs.FileMode(0o600), permOf(t, config))
			assert.Equal(t, fs.FileMode(0o700), permOf(t, filepath.Join(dir, "objects")))
		})
	}
}

func ownerUID(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	uid, _ := ownerOf(info)
	return uid
}
