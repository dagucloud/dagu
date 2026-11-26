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
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
