// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Spec 092, "End": Dagu sends SIGKILL to each process of the restricted user
// and waits until no process of that user is left. Two means stop the
// processes:
//
//   - A helper that runs as the restricted user calls kill(-1, SIGKILL). The
//     kernel sends the signal to each process of the user, also to a process
//     that the list of /proc does not show, such as the threads of a process
//     whose main thread exited.
//   - A scan of each task of /proc (/proc/<pid>/task/<tid>/status) sends
//     SIGKILL to each thread group with a live task of the user, also when its
//     leader is a zombie.
//
// The wait ends when the scan finds no live task of the user. A task that is a
// zombie holds no code: the scan does not count it.

// killHelperArg is the argument that makes a Dagu binary the helper of the
// kill. The helper refuses to run as the user ID 0.
const killHelperArg = "__dagu-untrusted-kill-all__"

func init() {
	if len(os.Args) == 2 && os.Args[1] == killHelperArg {
		os.Exit(runKillHelper())
	}
}

// runKillHelper sends SIGKILL to each process of its user, but itself.
func runKillHelper() int {
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		return 3
	}
	for range 5 {
		if err := syscall.Kill(-1, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
			return 0
		}
		time.Sleep(5 * time.Millisecond)
	}
	return 0
}

// runKillHelperAs starts the helper as the restricted user and waits for it.
// It returns false when the helper cannot run, for example when the
// restricted user cannot run the binary of the process.
func runKillHelperAs(uid, gid uint32) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if err := checkRunnable(self, uid, gid); err != nil {
		return false
	}
	cmd := exec.Command(self, killHelperArg) //nolint:gosec
	cmd.Dir = "/"
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}},
		Setsid:     true,
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return true
}

// killUser stops each process of the user and waits until no live task of the
// user is left.
func killUser(uid, gid uint32) error {
	deadline := time.Now().Add(killTimeout)
	for {
		runKillHelperAs(uid, gid)
		pids, err := userProcesses(uid)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		killTaskGroups(uid)
		if time.Now().After(deadline) {
			return fmt.Errorf("%d processes of the user ID %d are left after SIGKILL", len(pids), uid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// killTaskGroups sends SIGKILL to each thread group with a live task of the
// user, by the scan of /proc only.
func killTaskGroups(uid uint32) {
	pids, err := userProcesses(uid)
	if err != nil {
		return
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for _, tid := range liveTasks(pid, uid) {
			_ = syscall.Tgkill(pid, tid, syscall.SIGKILL)
		}
	}
}

// userProcesses returns each thread group that has a live task with the user ID
// as its real, effective, saved or file system user ID. The leader of the group
// can be a zombie.
func userProcesses(uid uint32) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		if len(liveTasks(pid, uid)) > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// liveTasks returns the tasks of the thread group that belong to the user and
// are not zombies.
func liveTasks(pid int, uid uint32) []int {
	dir := "/proc/" + strconv.Itoa(pid) + "/task"
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A group that ends between the two reads has no task left.
		data, statusErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status") // #nosec G304 -- a fixed /proc path.
		if statusErr == nil && taskOfUser(string(data), uid) {
			return []int{pid}
		}
		return nil
	}
	var tids []int
	for _, entry := range entries {
		tid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(dir + "/" + entry.Name() + "/status") // #nosec G304 -- a fixed /proc path.
		if err != nil {
			continue
		}
		if taskOfUser(string(data), uid) {
			tids = append(tids, tid)
		}
	}
	return tids
}

// taskOfUser reads the text of a status file of /proc: the task belongs to the
// user and is not a zombie.
func taskOfUser(status string, uid uint32) bool {
	var state string
	var uids []string
	for line := range strings.SplitSeq(status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "State":
			state = strings.TrimSpace(value)
		case "Uid":
			uids = strings.Fields(value)
		}
	}
	if strings.HasPrefix(state, "Z") || strings.HasPrefix(state, "X") {
		return false
	}
	want := strconv.FormatUint(uint64(uid), 10)
	return slices.Contains(uids, want)
}
