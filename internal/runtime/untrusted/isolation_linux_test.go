// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The isolation tests need the user ID 0, the six capabilities and the flag
// no-new-privileges. Run them in a root container, for example with
// NNP=1 gowt.sh <worktree> 'go test ./internal/runtime/untrusted/...'.
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

// Spec 092, "Isolation": the restricted user cannot read a file of the
// worker, cannot write .git or rename it, can write the other files, has a
// private HOME, gets no DAGU_ variable but DAGU_OUTPUT_FILE, and leaves no
// process. At the end, the work directory belongs to the worker again.
func TestIsolatedStep(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	rootOnly := filepath.Join(workDir, "..", filepath.Base(workDir)+"-root-only")
	require.NoError(t, os.WriteFile(rootOnly, []byte("worker secret"), 0o400))
	t.Cleanup(func() { _ = os.Remove(rootOnly) })

	req := testRequest(t, workDir, filepath.Join(workDir, "repo"))
	step, err := Begin(ctx, req)
	require.NoError(t, err)
	require.True(t, step.Isolated())
	stepCtx := WithStep(ctx, step)
	outputFile := filepath.Join(t.TempDir(), "step.output")
	require.NoError(t, os.WriteFile(outputFile, nil, 0o600))
	processOutput := step.OutputFile(outputFile)
	assert.NotEqual(t, outputFile, processOutput)

	script := strings.Join([]string{
		`id -u; id -g; id -G`,
		`cat ` + rootOnly + ` >/dev/null 2>&1 && echo read-root:allowed || echo read-root:denied`,
		`(echo x > .git/hooks/x) 2>/dev/null && echo write-git:allowed || echo write-git:denied`,
		`mv .git .gitx 2>/dev/null && echo move-git:allowed || echo move-git:denied`,
		`rm -rf .git 2>/dev/null; [ -d .git/hooks ] && echo remove-git:denied || echo remove-git:allowed`,
		`echo new > src/new.txt && echo write-src:allowed || echo write-src:denied`,
		`echo changed > src/main.txt && echo change-src:allowed || echo change-src:denied`,
		`echo "home=$HOME tmp=$TMPDIR"`,
		`stat -c 'home-mode=%a home-uid=%u' "$HOME"`,
		`env | grep -c '^DAGU_SECRET=' || true`,
		`echo "answer=42" > "$DAGU_OUTPUT_FILE"`,
		`setsid sleep 1000 </dev/null >/dev/null 2>&1 &`,
		`sleep 1000 </dev/null >/dev/null 2>&1 &`,
		`echo done`,
	}, "\n")
	out := runAs(t, stepCtx, filepath.Join(workDir, "repo"), script, "DAGU_SECRET=hidden", "DAGU_OUTPUT_FILE="+processOutput)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.GreaterOrEqual(t, len(lines), 13, out)
	assert.Equal(t, strconv.Itoa(testUID), lines[0])
	assert.Equal(t, strconv.Itoa(testGID), lines[1])
	assert.Equal(t, strconv.Itoa(testGID), lines[2], "no supplementary group")
	assert.Contains(t, out, "read-root:denied")
	assert.Contains(t, out, "write-git:denied")
	assert.Contains(t, out, "move-git:denied")
	assert.Contains(t, out, "remove-git:denied")
	assert.Contains(t, out, "write-src:allowed")
	assert.Contains(t, out, "change-src:allowed")
	assert.Contains(t, out, "home-mode=700 home-uid="+strconv.Itoa(testUID))
	assert.Contains(t, out, "home="+step.iso.home+" tmp="+step.iso.home)
	assert.Contains(t, out, "\n0\n", "the process has no DAGU_SECRET")
	home := step.iso.home

	require.NoError(t, step.End(ctx))

	pids, err := userProcesses(testUID)
	require.NoError(t, err)
	assert.Empty(t, pids, "each process of the restricted user is stopped")
	assert.NoFileExists(t, filepath.Join(workDir, "repo", ".git", "hooks", "x"))
	assert.DirExists(t, filepath.Join(workDir, "repo", ".git"))
	assert.NoDirExists(t, home, "the HOME of the step is removed")
	err = filepath.WalkDir(workDir, func(path string, _ fs.DirEntry, err error) error {
		require.NoError(t, err)
		assert.Equal(t, os.Geteuid(), ownerUID(t, path), path)
		return nil
	})
	require.NoError(t, err)
	info, err := os.Stat(workDir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm(), "the mode of the work directory is restored")
	data, err := os.ReadFile(filepath.Join(workDir, "repo", "src", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "changed\n", string(data))
	output, err := os.ReadFile(outputFile)
	require.NoError(t, err)
	assert.Equal(t, "answer=42\n", string(output), "the output file comes back")
}

// A new .git that the restricted user makes fails the step, and Dagu removes
// it.
func TestIsolatedStepNewGit(t *testing.T) {
	requireIsolation(t)
	ctx := context.Background()
	workDir := testWorkDir(t)
	step, err := Begin(ctx, testRequest(t, workDir, filepath.Join(workDir, "repo")))
	require.NoError(t, err)
	runAs(t, WithStep(ctx, step), filepath.Join(workDir, "repo"), `mkdir -p src/.git/hooks && echo evil > src/.git/hooks/pre-commit`)

	err = step.End(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the restricted user made the entry")
	assert.Contains(t, err.Error(), filepath.Join("src", ".git"))
	assert.NoDirExists(t, filepath.Join(workDir, "repo", "src", ".git"))
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

// The config of the tool bridge belongs to the restricted user with the mode
// 0400, and a shared folder is readable and not writable.
func TestIsolatedStepFiles(t *testing.T) {
	requireIsolation(t)
	workDir := testWorkDir(t)
	step, err := Begin(context.Background(), testRequest(t, workDir, workDir))
	require.NoError(t, err)
	defer func() { require.NoError(t, step.End(context.Background())) }()

	path, err := step.WriteFile("mcp.json", []byte(`{"mcpServers":{}}`))
	require.NoError(t, err)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o400), info.Mode().Perm())
	assert.Equal(t, testUID, ownerUID(t, path))

	shared, err := os.MkdirTemp("", "dagu-untrusted-shared-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(shared) }()
	require.NoError(t, os.WriteFile(filepath.Join(shared, "notes.txt"), []byte("notes"), 0o400))
	require.NoError(t, step.ShareReadOnly(shared))
	out := runAs(t, WithStep(context.Background(), step), workDir,
		`cat `+filepath.Join(shared, "notes.txt")+`; (echo x > `+filepath.Join(shared, "new")+`) 2>/dev/null && echo write:allowed || echo write:denied`)
	assert.Contains(t, out, "notes")
	assert.Contains(t, out, "write:denied")
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

// A handoff that fails part way leaves a snapshot of a part of the git folders.
// No process of the restricted user ran, so the restore of the abort keeps each
// git entry of the worker. It needs no root.
func TestRestoreAfterAFailedHandoffKeepsTheRepositories(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	gitDir := filepath.Join(work, "repo", ".git")
	for _, dir := range []string{"objects", "refs/heads", "hooks"} {
		require.NoError(t, os.MkdirAll(filepath.Join(gitDir, dir), 0o750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o600))
	iso := &isolation{
		step: "s", workDir: work, uid: testUID, gid: testGID,
		workerUID: os.Geteuid(), workerGID: os.Getegid(),
		protected: map[string]protectedDir{}, passDirs: map[string]protectedDir{},
		gitEntries: map[string]bool{gitDir: true}, gitDirs: map[string]bool{gitDir: true},
		gitSnapshot: map[string]fileSig{}, seenGit: map[string]bool{},
	}
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
	work := t.TempDir()
	sub := filepath.Join(work, "sub")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, ".git"), 0o750))
	require.NoError(t, os.Chmod(sub, 0o555))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o750) })
	iso := &isolation{
		step: "s", workDir: work, uid: testUID, gid: testGID,
		workerUID: os.Geteuid(), workerGID: os.Getegid(),
		protected: map[string]protectedDir{}, passDirs: map[string]protectedDir{},
		gitEntries: map[string]bool{}, gitDirs: map[string]bool{},
		gitSnapshot: map[string]fileSig{}, seenGit: map[string]bool{},
	}
	err := iso.restore(true)
	require.ErrorIs(t, err, ErrRunAborted)
}
