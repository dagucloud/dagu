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

// testWorkDir makes a work directory below the system temp folder, which any
// user can pass, with the folder repo. The restricted user can enter both and
// cannot write them.
func testWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dagu-untrusted-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "repo"), 0o755))
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
// a file of the worker or write the work directory. End stops each of its
// processes, copies the output file and removes the HOME.
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
	assert.Contains(t, out, "write-work:denied")
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
