// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The isolation tests need the user ID 0, the six capabilities and the flag
// no-new-privileges, as in a root container started with
// --security-opt no-new-privileges. CI runs them in the job Test compatibility.
const testUID, testGID = 64011, 64011

func requireIsolation(t *testing.T) {
	t.Helper()
	if err := checkIsolationSupport(); err != nil {
		t.Skipf("the isolation tests need a root process with no-new-privileges: %v", err)
	}
}

// testWorkDir makes a work directory below the system temp folder, which any
// user can pass, with a repository: repo/.git/hooks and repo/src/main.txt.
func testWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dagu-untrusted-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo", ".git", "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "repo", ".git", "config"), []byte("[core]\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo", "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "repo", "src", "main.txt"), []byte("main\n"), 0o644))
	return dir
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
	require.NoError(t, process.Wait(), "stderr: %s", stderr.String())
	return stdout.String()
}

func ownerUID(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	uid, _ := ownerOf(info)
	return uid
}

// A working directory outside the work directory of the run fails the step.
func TestIsolatedStepPlace(t *testing.T) {
	requireIsolation(t)
	workDir := testWorkDir(t)
	_, err := Begin(context.Background(), testRequest(t, workDir, "/srv"))
	require.Error(t, err)
	assert.Equal(t, `untrusted step "verify": the working directory /srv is not in the work directory of the run`, err.Error())

	require.NoError(t, os.Symlink("/etc", filepath.Join(workDir, "link")))
	_, err = Begin(context.Background(), testRequest(t, workDir, filepath.Join(workDir, "link", "x")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")
	assert.NoDirExists(t, "/etc/x")
	assert.Equal(t, os.Geteuid(), ownerUID(t, filepath.Join(workDir, "repo", "src")), "a failed Begin gives nothing")
}

// Untrusted steps run one at a time. The second step waits with a line in its
// log.
func TestIsolatedStepsOneAtATime(t *testing.T) {
	requireIsolation(t)
	workDir := testWorkDir(t)
	first, err := Begin(context.Background(), testRequest(t, workDir, workDir))
	require.NoError(t, err)

	req := testRequest(t, workDir, workDir)
	log := &bytes.Buffer{}
	req.Log = log
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = Begin(ctx, req)
	require.Error(t, err)
	assert.Contains(t, log.String(), WaitingLine)

	require.NoError(t, first.End(context.Background()))
	second, err := Begin(context.Background(), testRequest(t, workDir, workDir))
	require.NoError(t, err)
	require.NoError(t, second.End(context.Background()))
}

// The end of a process of the step does not stop another process of the step
// that runs, whichever of the two was prepared first, and also when no watcher
// can follow the other process. Both are prepared before either starts, and
// the process that ends starts first.
func TestIsolatedStepKeepsAnotherProcess(t *testing.T) {
	t.Run("prepared first", func(t *testing.T) { testKeepsAnotherProcess(t, true, false) })
	t.Run("prepared last", func(t *testing.T) { testKeepsAnotherProcess(t, false, false) })
	t.Run("unwatched", func(t *testing.T) { testKeepsAnotherProcess(t, true, true) })
}

func testKeepsAnotherProcess(t *testing.T, endingPreparedFirst, stayingUnwatched bool) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := Begin(ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	t.Cleanup(func() { _ = step.End(ctx) })

	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
		cmd.Dir = workDir
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		return cmd
	}
	ending, staying := command("/bin/true"), command("/bin/sleep", "1000")
	if endingPreparedFirst {
		require.NoError(t, step.prepare(ending))
		require.NoError(t, step.prepare(staying))
	} else {
		require.NoError(t, step.prepare(staying))
		require.NoError(t, step.prepare(ending))
	}
	start := func(cmd *exec.Cmd) *cmdutil.ManagedProcess {
		process, err := cmdutil.StartManagedProcess(cmd)
		require.NoError(t, err)
		t.Cleanup(func() { _ = process.Release() })
		step.iso.started(process.PID())
		return process
	}
	endingProcess := start(ending)
	if stayingUnwatched {
		pidfdOpen = func(int, int) (int, error) { return -1, unix.EMFILE }
		t.Cleanup(func() { pidfdOpen = unix.PidfdOpen })
	}
	stayingProcess := start(staying)

	require.NoError(t, endingProcess.Wait())
	ended := make(chan struct{})
	go func() {
		_ = stayingProcess.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		t.Fatal("the end of one process stopped the other one")
	case <-time.After(time.Second):
	}
}

// A process that fails to start does not keep the watcher of the process
// before it from stopping what that process left: the leftover holds the
// stdout pipe, so Wait returns only once the watcher stops it. The subtests
// fail the second start before and after the first process ends.
func TestIsolatedStepFailedStartKeepsTheCleanup(t *testing.T) {
	t.Run("before", func(t *testing.T) { testFailedStartKeepsTheCleanup(t, false) })
	t.Run("after", func(t *testing.T) { testFailedStartKeepsTheCleanup(t, true) })
}

func testFailedStartKeepsTheCleanup(t *testing.T, failAfterExit bool) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := Begin(ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	t.Cleanup(func() { _ = step.End(ctx) })
	stepCtx := WithStep(ctx, step)

	script := "sleep 1000 & sleep 1"
	if failAfterExit {
		script = "sleep 1000 &"
	}
	first := exec.Command("/bin/sh", "-c", script) //nolint:gosec
	first.Dir = workDir
	first.Env = []string{"PATH=/usr/bin:/bin"}
	var stdout bytes.Buffer
	first.Stdout = &stdout
	failing := exec.Command(filepath.Join(workDir, "no-such-binary")) //nolint:gosec
	failing.Dir = workDir

	if failAfterExit {
		// The failing start is prepared before the first process starts, so
		// the first process ends while the other one is still starting. The
		// start fails only after the watcher saw the end.
		require.NoError(t, step.prepare(failing))
		firstProcess, err := StartManagedProcess(stepCtx, first)
		require.NoError(t, err)
		defer func() { _ = firstProcess.Release() }()
		waitEnded(t, firstProcess.PID())
		time.Sleep(time.Second)
		step.iso.abandon()
		requireWaitReturns(t, firstProcess)
		return
	}
	firstProcess, err := StartManagedProcess(stepCtx, first)
	require.NoError(t, err)
	defer func() { _ = firstProcess.Release() }()
	_, err = StartManagedProcess(stepCtx, failing)
	require.Error(t, err)
	requireWaitReturns(t, firstProcess)
}

// waitEnded waits until the process pid has ended. Its parent has not reaped
// it yet, so it stays a zombie.
func waitEnded(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		// The state follows the command name, which is in parentheses.
		i := bytes.LastIndexByte(data, ')')
		return i >= 0 && i+2 < len(data) && data[i+2] == 'Z'
	}, 10*time.Second, 10*time.Millisecond)
}

// requireWaitReturns fails when Wait of the process does not return soon,
// as when a process it left still holds its pipe.
func requireWaitReturns(t *testing.T, process *cmdutil.ManagedProcess) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()
	select {
	case err := <-waited:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the process left by the first one kept its pipe open")
	}
}

// taskOfUser reads the four user IDs of a status, and skips a zombie task.
func TestTaskOfUser(t *testing.T) {
	t.Parallel()
	status := "Name:\tsleep\nState:\tS (sleeping)\nUid:\t0\t64011\t0\t0\n"
	assert.True(t, taskOfUser(status, 64011))
	assert.False(t, taskOfUser(status, 64012))
	assert.False(t, taskOfUser(strings.Replace(status, "S (sleeping)", "Z (zombie)", 1), 64011))
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
