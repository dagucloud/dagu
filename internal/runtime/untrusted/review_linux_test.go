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
	"syscall"
	"testing"
	"time"

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
