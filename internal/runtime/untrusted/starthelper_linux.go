// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// startHelperArg is the argument that makes a Dagu binary the start helper of
// a process of an isolated step. Dagu starts the helper as root, so no process
// of the restricted user can stop the child before its execve, where Go waits
// with the whole runtime of the worker blocked. The helper changes to the user
// itself, after its execve, then enters the working directory and runs the
// command. The arguments that follow are the user ID, the group ID, the
// working directory, the path of the command and its argv.
const startHelperArg = "__dagu-untrusted-start__"

// startHelperEnvFD is the descriptor from which the helper reads the variables
// of the command, each one ended by a NUL byte. The variables do not go
// through the environment of the helper, which runs as root, or through its
// arguments, which any user can read.
const startHelperEnvFD = 3

func init() {
	if len(os.Args) >= 7 && os.Args[1] == startHelperArg {
		os.Exit(runStartHelper(os.Args[2:]))
	}
}

// runStartHelper changes to the user and the group of its arguments, with no
// supplementary group, and replaces itself with the command. It returns only
// when that fails, with the exit code of a shell: 127 for a command that does
// not exist, else 126.
func runStartHelper(args []string) int {
	uid, uidErr := strconv.Atoi(args[0])
	gid, gidErr := strconv.Atoi(args[1])
	dir, path, argv := args[2], args[3], args[4:]
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		return helperFailed("read the user", errors.New("invalid user or group ID"))
	}
	env, err := readHelperEnv()
	if err != nil {
		return helperFailed("read the variables", err)
	}
	if err := closeOnExec(); err != nil {
		return helperFailed("close the descriptors", err)
	}
	if err := changeToUser(uid, gid); err != nil {
		return helperFailed("change to the restricted user", err)
	}
	if err := syscall.Chdir(dir); err != nil {
		return helperFailed("enter "+dir, err)
	}
	err = syscall.Exec(path, argv, env) // #nosec G204 -- the command of the step.
	code := helperFailed("run "+path, err)
	if errors.Is(err, syscall.ENOENT) {
		code = 127
	}
	return code
}

// changeToUser changes the process to the user and the group, with no
// supplementary group, and checks the IDs after the change.
func changeToUser(uid, gid int) error {
	if err := syscall.Setgroups([]int{}); err != nil {
		return err
	}
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	if err := syscall.Setuid(uid); err != nil {
		return err
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		return errors.New("the IDs did not change")
	}
	return nil
}

// readHelperEnv reads the variables of the command and closes their
// descriptor.
func readHelperEnv() ([]string, error) {
	file := os.NewFile(startHelperEnvFD, "variables")
	if file == nil {
		return nil, errors.New("no descriptor of the variables")
	}
	data, err := io.ReadAll(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	env := []string{}
	for entry := range strings.SplitSeq(string(data), "\x00") {
		if entry != "" {
			env = append(env, entry)
		}
	}
	return env, nil
}

func helperFailed(action string, err error) int {
	_, _ = fmt.Fprintf(os.Stderr, "dagu: untrusted step: %s: %v\n", action, err)
	return 126
}

// viaStartHelper makes cmd start the start helper as root with the user of the
// isolation, in the session of cmd. The returned file holds the variables of
// the command; the caller closes it once the start returns.
func (iso *isolation) viaStartHelper(cmd *exec.Cmd, dir string, env []string) (*os.File, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the binary of the start helper: %w", err)
	}
	envFile, err := helperEnvFile(env)
	if err != nil {
		return nil, fmt.Errorf("pass the variables to the start helper: %w", err)
	}
	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	cmd.Args = append([]string{
		self, startHelperArg,
		strconv.FormatUint(uint64(iso.uid), 10), strconv.FormatUint(uint64(iso.gid), 10),
		dir, cmd.Path,
	}, argv...)
	cmd.Path = self
	cmd.Dir = "/"
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{envFile}
	return envFile, nil
}

// helperEnvFile returns a file in memory with the variables, read from its
// start.
func helperEnvFile(env []string) (*os.File, error) {
	fd, err := unix.MemfdCreate("dagu-untrusted-env", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "dagu-untrusted-env")
	var data strings.Builder
	for _, entry := range env {
		data.WriteString(entry)
		data.WriteByte(0)
	}
	if _, err := io.WriteString(file, data.String()); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
