// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Proofs of the adversarial review of Spec 103, "Isolation". Each test states
// the property, and fails while the property is broken. They need root, as the
// isolation tests of isolation_linux_test.go do.

// resetPoison clears the poison of the package at the end of the test, so that
// the next isolation test can run.
func resetPoison(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		poisonMu.Lock()
		poisonErr = nil
		poisonMu.Unlock()
	})
}

// killAllAs stops each process of the restricted user with kill(-1) as that
// user, which the kernel applies to the whole task list at once. The kill
// helper starts as root, so a process of the user that the test left cannot
// stop the start.
func killAllAs(t *testing.T) {
	t.Helper()
	for range 100 {
		_ = runKillHelperAs(testUID, testGID)
		pids, err := userProcesses(testUID)
		if err == nil && len(pids) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("killAllAs: processes of the restricted user are left")
}

// hopSource is a loop that forks; the parent exits at once. The loop ends by
// itself after 45 s.
const hopSource = `#include <time.h>
#include <unistd.h>
int main(void) {
	time_t end = time(0) + 45;
	for (;;) {
		if (time(0) > end) _exit(0);
		pid_t p = fork();
		if (p > 0) _exit(0);
	}
}
`

// hopCommand returns the shell command of the loop: a small C program when
// gcc is present, else perl.
func hopCommand(t *testing.T) string {
	t.Helper()
	if gcc, err := exec.LookPath("gcc"); err == nil {
		dir, err := os.MkdirTemp("", "dagu-review-hop-")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		require.NoError(t, os.Chmod(dir, 0o755))
		src := filepath.Join(dir, "hop.c")
		require.NoError(t, os.WriteFile(src, []byte(hopSource), 0o644))
		bin := filepath.Join(dir, "hop")
		out, err := exec.Command(gcc, "-O2", "-static", "-o", bin, src).CombinedOutput()
		if err != nil {
			out, err = exec.Command(gcc, "-O2", "-o", bin, src).CombinedOutput()
		}
		require.NoError(t, err, string(out))
		t.Log("hop: a C program")
		return bin
	}
	perl, err := exec.LookPath("perl")
	if err != nil {
		t.Skip("needs gcc or perl")
	}
	t.Log("hop: perl")
	return perl + ` -MPOSIX -e '$t=time+45; while(time<$t){ my $p=fork(); POSIX::_exit(0) if defined($p) && $p; }'`
}

// zombieLeaderSource exits the main thread and keeps a second thread alive.
// The thread group leader is then a zombie (State Z in /proc/<pid>/status),
// and the live thread has no entry in the list of /proc. The thread writes a
// count to the file of its argument each 100 ms for 60 s.
const zombieLeaderSource = `#include <pthread.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>
static const char *path;
static void *watch(void *arg) {
	(void)arg;
	for (int i = 0; i < 600; i++) {
		FILE *f = fopen(path, "w");
		if (f) { fprintf(f, "%d\n", i); fclose(f); }
		usleep(100000);
	}
	return 0;
}
int main(int argc, char **argv) {
	if (argc < 2) return 2;
	path = argv[1];
	pthread_t t;
	pthread_create(&t, 0, watch, 0);
	usleep(200000);
	syscall(SYS_exit, 0);
	return 0;
}
`

// Property (Spec 103, "Isolation", rule 7): Dagu stops each process of the restricted user,
// and waits until no process of that user is left. The attack: a process whose
// main thread exits keeps its other threads. Its leader is a zombie and its
// threads are not in the list of /proc, so a scan that skips Z processes or
// reads only that list misses them, and the thread of the agent keeps running
// to watch the later steps.
func TestReviewZombieLeaderSurvivesEnd(t *testing.T) {
	requireIsolation(t)
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("needs gcc")
	}
	resetPoison(t)
	t.Cleanup(func() { killAllAs(t) })
	dir, err := os.MkdirTemp("", "dagu-review-zombie-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0o755))
	src := filepath.Join(dir, "zombie.c")
	require.NoError(t, os.WriteFile(src, []byte(zombieLeaderSource), 0o644))
	bin := filepath.Join(dir, "zombie")
	out, err := exec.Command(gcc, "-O2", "-pthread", "-o", bin, src).CombinedOutput()
	require.NoError(t, err, string(out))

	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := beginStep(t, ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
	require.NoError(t, err)
	// The agent writes its count in /tmp, which any user can write.
	marker := filepath.Join(os.TempDir(), "dagu-review-zombie-"+strconv.Itoa(os.Getpid()))
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	runAs(t, WithStep(ctx, step), filepath.Join(workDir, "repo"), bin+" "+marker+" </dev/null >/dev/null 2>&1 &\nsleep 1")
	// A count that changes shows that the thread runs before End, so an equal
	// count below means a stopped thread. A read between the truncation and
	// the write of the file is empty.
	var seen string
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(marker)
		count := strings.TrimSpace(string(data))
		if count == "" || seen == "" {
			seen = cmp.Or(seen, count)
			return false
		}
		return count != seen
	}, 5*time.Second, 20*time.Millisecond, "the thread of the agent does not run")
	require.NoError(t, step.End(ctx))

	first, err := os.ReadFile(marker)
	require.NoError(t, err)
	time.Sleep(time.Second)
	second, err := os.ReadFile(marker)
	require.NoError(t, err)
	t.Logf("count of the thread of the agent after End: %q, 1 s later: %q", strings.TrimSpace(string(first)), strings.TrimSpace(string(second)))
	assert.Equal(t, string(first), string(second), "a thread of the restricted user runs after End")
}

// Property (Spec 103, "Isolation", rule 7): Dagu stops each process of the restricted user,
// and waits until no process of that user is left. The attack: a loop that
// forks and lets its parent exit at once changes its PID while killUser scans
// /proc, so that a scan could see no process. 200 idle processes of root make
// the scan longer, as on a busy worker. The test passes: the scan still caught
// the loop in each round, so this attempt did not break the property.
func TestReviewForkHopIsKilled(t *testing.T) {
	requireIsolation(t)
	hop := hopCommand(t)
	resetPoison(t)
	t.Cleanup(func() { killAllAs(t) })
	ctx := context.Background()
	for range 200 {
		idle := exec.Command("/bin/sleep", "120")
		require.NoError(t, idle.Start())
		t.Cleanup(func() { _ = idle.Process.Kill(); _ = idle.Wait() })
	}

	const rounds = 10
	escaped := 0
	for round := range rounds {
		workDir := testWorkDir(t)
		step, err := beginStep(t, ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
		require.NoError(t, err)
		// The script waits 1 s so that the loop runs before End.
		out := runAs(t, WithStep(ctx, step), filepath.Join(workDir, "repo"),
			"cut -d' ' -f5 /proc/loadavg\n"+hop+" </dev/null >/dev/null 2>&1 &\nsleep 1\ncut -d' ' -f5 /proc/loadavg")
		t.Logf("round %d: last PID before and after 1 s of the loop: %s", round, strings.Join(strings.Fields(out), " -> "))
		endErr := step.End(ctx)
		after, _ := userProcesses(testUID)
		// A process that is still there 500 ms later is a live loop, not a
		// process that the last SIGKILL is ending.
		time.Sleep(500 * time.Millisecond)
		later, _ := userProcesses(testUID)
		t.Logf("round %d: End error: %v; processes of the restricted user after End: %d, 500 ms later: %d",
			round, endErr, len(after), len(later))
		if endErr == nil && len(later) > 0 {
			escaped++
		}
		killAllAs(t)
	}
	assert.Zero(t, escaped, "a process of the restricted user is alive after a successful End")
}

// stopperSource waits 500 ms, so that the shell that starts it can end, then
// sends SIGSTOP to each process of its user from four threads for 45 s, and
// makes the file of its argument once they run. kill(-1) spares the thread
// group of the caller.
const stopperSource = `#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <time.h>
#include <unistd.h>
static void *stop(void *arg) {
	(void)arg;
	time_t end = time(0) + 45;
	while (time(0) < end) kill(-1, SIGSTOP);
	return 0;
}
int main(int argc, char **argv) {
	if (argc < 2) return 2;
	usleep(500000);
	pthread_t t[4];
	for (int i = 0; i < 4; i++) pthread_create(&t[i], 0, stop, 0);
	FILE *f = fopen(argv[1], "w");
	if (f) fclose(f);
	for (int i = 0; i < 4; i++) pthread_join(t[i], 0);
	return 0;
}
`

// Property (Spec 103, "Isolation", rule 7): End and each start of a process of
// the step return. The attack: a process of the restricted user sends SIGSTOP
// to each process of that user. Go waits for the execve of each child it
// starts, with the runtime of the worker blocked at its next garbage
// collection; a child that ran as that user before its execve could be
// stopped there and block the worker forever.
func TestReviewStopperBlocksNoStart(t *testing.T) {
	requireIsolation(t)
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("needs gcc")
	}
	resetPoison(t)
	t.Cleanup(func() { killAllAs(t) })
	dir, err := os.MkdirTemp("", "dagu-review-stopper-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0o755))
	src := filepath.Join(dir, "stopper.c")
	require.NoError(t, os.WriteFile(src, []byte(stopperSource), 0o644))
	bin := filepath.Join(dir, "stopper")
	out, err := exec.Command(gcc, "-O2", "-pthread", "-o", bin, src).CombinedOutput()
	require.NoError(t, err, string(out))

	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	step, err := beginStep(t, ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)
	stepCtx := WithStep(ctx, step)
	// The stopper makes its marker in /tmp, which any user can write.
	marker := filepath.Join(os.TempDir(), "dagu-review-stopper-"+strconv.Itoa(os.Getpid()))
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	runAs(t, stepCtx, repo, bin+" "+marker+" </dev/null >/dev/null 2>&1 &")
	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond, "the stopper does not run")

	// Up to 200 starts in 3 s, while the stopper runs. A start takes
	// milliseconds; one that takes 10 s is blocked.
	var processes []*cmdutil.ManagedProcess
	slow := 0
	for begin, n := time.Now(), 0; n < 200 && time.Since(begin) < 3*time.Second; n++ {
		started := make(chan *cmdutil.ManagedProcess, 1)
		startedAt := time.Now()
		go func() {
			cmd := exec.Command("/bin/true")
			cmd.Dir = repo
			process, _ := StartManagedProcess(stepCtx, cmd)
			started <- process
		}()
		select {
		case process := <-started:
			if process != nil {
				processes = append(processes, process)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a start of a process of the step is blocked")
		}
		if time.Since(startedAt) > time.Second {
			slow++
		}
	}
	t.Logf("%d processes started, %d starts took more than 1 s", len(processes), slow)

	ended := make(chan error, 1)
	go func() { ended <- step.End(ctx) }()
	select {
	case err := <-ended:
		require.NoError(t, err)
	case <-time.After(killTimeout + 10*time.Second):
		t.Fatal("End is blocked")
	}
	for _, process := range processes {
		_ = process.Wait()
		_ = process.Release()
	}
}

// Property (Spec 103, "Protections for later trusted steps", .git): the
// restricted user cannot write .git. The attack: a write of the worker in the
// work directory during the step, such as a trusted step that the gate of the
// run failed to hold back. The agent replaces a folder that it owns with a link
// to .git/hooks, so the write lands in .git. The restore finds the change,
// removes the link and stops the run, and no later isolated step of the
// process runs.
func TestReviewConcurrentTrustedWriteReachesGit(t *testing.T) {
	requireIsolation(t)
	resetPoison(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	step, err := Begin(ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)
	runAs(t, WithStep(ctx, step), repo, `rm -rf src && ln -s .git/hooks src`)

	// The trusted step of the other branch: it writes its file src/pre-commit.
	_ = os.WriteFile(filepath.Join(repo, "src", "pre-commit"), []byte("#!/bin/sh\necho build\n"), 0o755) //nolint:gosec
	endErr := step.End(ctx)
	t.Logf("End: %v", endErr)
	assert.NoFileExists(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), "a write of a trusted step made a hook in .git")
	// The change of a git folder stops the run.
	assert.ErrorIs(t, endErr, ErrRunAborted)
	assert.ErrorIs(t, Poisoned(), ErrRunAborted, "the process runs no later isolated step")
	assert.ErrorIs(t, CheckWorkDir(workDir), ErrRunAborted, "no process runs a later step of the run")
	assert.NoFileExists(t, filepath.Join(repo, "src"), "the link of the agent into .git is removed")
}

// Property (Spec 103, "Protections for later trusted steps", .git): the agent
// cannot make a later trusted step run its code through git. The attack: a
// folder with HEAD, objects and refs is a bare repository for git, and git
// finds it before the .git of a parent folder. Once the folder belongs to the
// worker again, the owner check of git (safe.directory) passes, and a trusted
// git command in a folder below it runs core.fsmonitor as root. The restore
// treats such a folder as a new git folder.
func TestReviewBareRepositoryRunsAsRoot(t *testing.T) {
	requireIsolation(t)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git")
	}
	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	// A real repository, as the step git.checkout makes it.
	require.NoError(t, os.RemoveAll(filepath.Join(repo, ".git")))
	initCmd := exec.Command(gitBin, "init", "-q", repo)
	initCmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
	out, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "services", "api"), 0o755))

	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	step, err := Begin(ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)
	agent := strings.Join([]string{
		`cd services`,
		`mkdir objects refs`,
		`echo 'ref: refs/heads/main' > HEAD`,
		`printf '[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = %s\n\tfsmonitor = "id -u > ` + marker + `; false"\n' "$PWD/api" > config`,
	}, "\n")
	runAs(t, WithStep(ctx, step), repo, agent)
	endErr := step.End(ctx)
	require.Error(t, endErr, "the restore sees the bare repository")
	assert.Contains(t, endErr.Error(), "made the git folder "+filepath.Join(repo, "services"))
	assert.NoFileExists(t, filepath.Join(repo, "services", "HEAD"))

	// A later trusted step: git status in a folder of the repository, as root.
	status := exec.Command(gitBin, "status", "--porcelain")
	status.Dir = filepath.Join(repo, "services", "api")
	status.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
	statusOut, statusErr := status.CombinedOutput()
	t.Logf("trusted git status: err=%v output=%q", statusErr, statusOut)
	data, readErr := os.ReadFile(marker)
	if readErr == nil {
		t.Logf("core.fsmonitor of the agent ran as uid %s", strings.TrimSpace(string(data)))
	}
	assert.Error(t, readErr, "a trusted git command must not run a command of the agent")
}

// Property (Spec 103, "Protections for later trusted steps", .git): each .git
// entry and its content stay with the worker. The attack: a .git file (git
// worktree, --separate-git-dir) names a git folder that has another name, and
// the agent writes a hook in that folder for a later trusted git commit.
func TestReviewGitFileTargetIsNotProtected(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	require.NoError(t, os.RemoveAll(filepath.Join(repo, ".git")))
	gitDir := filepath.Join(workDir, "repo.gitdir")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../repo.gitdir\n"), 0o644))

	step, err := Begin(ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)
	out := runAs(t, WithStep(ctx, step), repo,
		`(printf '#!/bin/sh\nid -u > /tmp/hook\n' > ../repo.gitdir/hooks/pre-commit && chmod 755 ../repo.gitdir/hooks/pre-commit) 2>/dev/null && echo hook:allowed || echo hook:denied`)
	endErr := step.End(ctx)
	t.Logf("agent: %s; End: %v", strings.TrimSpace(out), endErr)
	assert.Contains(t, out, "hook:denied", "the agent cannot write the git folder of the repository")
	if endErr == nil {
		assert.NoFileExists(t, filepath.Join(gitDir, "hooks", "pre-commit"), "a hook of the agent is left for a trusted git commit")
	}
}

// Property (Spec 103, "Isolation", rule 8): the restricted user cannot read the
// files of the worker. The attack: during the step, fs.protected_symlinks stops
// root at a link of the agent in a folder with the mode 1777. If the restore
// gave the link to the worker, a later trusted step that reads the file of the
// agent as data would read the file of the worker, for example its key. The
// restore removes the link and fails the step.
func TestReviewSymlinkReadAsRootAfterRestore(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	repo := filepath.Join(workDir, "repo")
	key, err := os.CreateTemp("", "dagu-review-worker-key-")
	require.NoError(t, err)
	_, _ = key.WriteString("WORKER-PRIVATE-KEY")
	require.NoError(t, key.Close())
	require.NoError(t, os.Chmod(key.Name(), 0o400))
	t.Cleanup(func() { _ = os.Remove(key.Name()) })

	step, err := Begin(ctx, testRequest(t, workDir, repo))
	require.NoError(t, err)
	out := runAs(t, WithStep(ctx, step), repo,
		`cat `+key.Name()+` >/dev/null 2>&1 && echo read:allowed || echo read:denied; ln -s `+key.Name()+` summary.txt && echo link:made`)
	assert.Contains(t, out, "read:denied")
	assert.Contains(t, out, "link:made")
	_, duringErr := os.ReadFile(filepath.Join(repo, "summary.txt"))
	t.Logf("root reads the link during the step: %v", duringErr)
	endErr := step.End(ctx)
	require.Error(t, endErr, "a link of the agent out of the work directory fails the step")
	assert.Contains(t, endErr.Error(), "made the link "+filepath.Join(repo, "summary.txt"))

	_, err = os.Lstat(filepath.Join(repo, "summary.txt"))
	assert.ErrorIs(t, err, os.ErrNotExist, "Dagu removes the link")
	// A later trusted step: cat summary.txt, as root.
	data, err := os.ReadFile(filepath.Join(repo, "summary.txt"))
	if err == nil {
		t.Logf("the trusted read got %q", data)
	}
	assert.NotEqual(t, "WORKER-PRIVATE-KEY", string(data), "a trusted read of a file of the agent must not reach a file of the worker")
}
