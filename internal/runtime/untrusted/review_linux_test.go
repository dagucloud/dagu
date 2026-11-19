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
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Proofs of the adversarial review of Spec 092, "Isolation". Each test states
// the property, and fails while the property is broken. Run them in a root
// container: NNP=1 gowt.sh <worktree> 'go test -run TestReview ./internal/runtime/untrusted/'.

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
// user, which the kernel applies to the whole task list at once.
func killAllAs(t *testing.T) {
	t.Helper()
	for range 100 {
		cmd := exec.Command("/bin/sh", "-c", "kill -9 -1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: testUID, Gid: testGID, Groups: []uint32{}}}
		_ = cmd.Run()
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

// Property (Spec 092, "End"): Dagu stops each process of the restricted user,
// and waits until no process of that user is left. killUser skips each process
// whose status is Z, and reads only the entries of the list of /proc. A process
// whose main thread exits keeps its other threads: its leader is a zombie, and
// its threads are not in the list. End returns nil and the thread of the agent
// keeps running, so it watches the later steps.
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
	step, err := Begin(ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
	require.NoError(t, err)
	// The agent writes its count in /tmp, which any user can write.
	marker := filepath.Join(os.TempDir(), "dagu-review-zombie-"+strconv.Itoa(os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(marker) })
	runAs(t, WithStep(ctx, step), filepath.Join(workDir, "repo"), bin+" "+marker+" </dev/null >/dev/null 2>&1 &\nsleep 1")
	require.NoError(t, step.End(ctx))

	first, _ := os.ReadFile(marker)
	time.Sleep(time.Second)
	second, _ := os.ReadFile(marker)
	t.Logf("count of the thread of the agent after End: %q, 1 s later: %q", strings.TrimSpace(string(first)), strings.TrimSpace(string(second)))
	assert.Equal(t, string(first), string(second), "a thread of the restricted user runs after End")

	// The fix candidate: kill(-1, SIGKILL) as the restricted user reaches the
	// thread group of a zombie leader.
	killAllAs(t)
	time.Sleep(300 * time.Millisecond)
	third, _ := os.ReadFile(marker)
	time.Sleep(300 * time.Millisecond)
	fourth, _ := os.ReadFile(marker)
	t.Logf("after kill(-1) as the restricted user: %q, 300 ms later: %q", strings.TrimSpace(string(third)), strings.TrimSpace(string(fourth)))
}

// Property (Spec 092, "End"): Dagu stops each process of the restricted user,
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
		step, err := Begin(ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
		require.NoError(t, err)
		// The script waits 1 s so that the loop runs before the watcher.
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

// Property (Spec 092, threat ".git"): the restricted user cannot write .git. A
// trusted step that runs at the same time as the untrusted step (another
// branch of a graph) writes in the work directory as root. The agent replaces
// a folder that it owns with a link to .git/hooks, so the trusted write makes a
// hook. The restore does not check the content of a .git that it knows.
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
	assert.NoFileExists(t, filepath.Join(repo, "src"), "the link of the agent into .git is removed")
}

// Property (Spec 092, threat ".git"): the agent cannot make a later trusted step
// run its code through git. A folder with HEAD, objects and refs is a bare
// repository for git, and git finds it before the .git of a parent folder. The
// restore checks only entries named .git, and gives the folder to the worker,
// so the check of the owner of git (safe.directory) passes. A trusted git
// command in a folder below it runs core.fsmonitor as root.
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

// Property (Spec 092, threat ".git"): each .git entry and its content stay with
// the worker. A .git file (git worktree, --separate-git-dir) names a git
// folder that has another name; the handoff gives that folder to the agent, and
// the restore accepts the hook that the agent writes in it.
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

// Property (Spec 092, threat model): the restricted user cannot read the files
// of the worker. During the step, fs.protected_symlinks stops root at a link of
// the agent in a folder with the mode 1777. The restore gives the link to the
// worker, so a later trusted step that reads the file of the agent as data
// reads the file of the worker, for example its key.
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

// Property (Spec 092, "Process"): each process of the step runs. A step with
// two processes (run with a list of commands, or a harness attempt after
// another) starts the second process when the first ends. When the first
// process leaves a child, the watcher of the first process loops in killUser
// and also kills the second process.
func TestReviewWatcherKillsNextProcess(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := Begin(ctx, testRequest(t, workDir, workDir))
	require.NoError(t, err)
	defer func() { require.NoError(t, step.End(ctx)) }()
	stepCtx := WithStep(ctx, step)

	killed := 0
	const rounds = 5
	for range rounds {
		// The first command starts a server in the background, as "npm start &".
		runAs(t, stepCtx, workDir, `sleep 1000 &`)
		cmd := exec.Command("/bin/sh", "-c", "sleep 0.3; echo second") //nolint:gosec
		cmd.Dir = workDir
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		process, err := StartManagedProcess(stepCtx, cmd)
		require.NoError(t, err)
		waitErr := process.Wait()
		_ = process.Release()
		if waitErr != nil {
			killed++
			t.Logf("second process: %v", waitErr)
		}
	}
	assert.Zero(t, killed, "the watcher of the first process killed the second process in "+strconv.Itoa(killed)+" of "+strconv.Itoa(rounds)+" rounds")
}
