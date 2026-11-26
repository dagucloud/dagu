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

	"golang.org/x/sys/unix"
)

// Spec 103, "Isolation", rule 7: Dagu sends SIGKILL to each process of the restricted user
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
// kill. Dagu starts the helper as root with the user ID and the group ID as the
// next two arguments. A child that runs as the restricted user before its
// execve could be stopped there by a process of that user, and Go would wait
// for that execve forever; so the helper changes to the user itself, after its
// execve. The change also makes it non-dumpable.
const killHelperArg = "__dagu-untrusted-kill-all__"

func init() {
	if len(os.Args) == 4 && os.Args[1] == killHelperArg {
		os.Exit(runKillHelper(os.Args[2], os.Args[3]))
	}
}

// runKillHelper changes to the user and the group of its arguments, with no
// supplementary group, and sends SIGKILL to each process of that user, but
// itself. It refuses to send it as the user ID 0.
func runKillHelper(uidArg, gidArg string) int {
	uid, uidErr := strconv.Atoi(uidArg)
	gid, gidErr := strconv.Atoi(gidArg)
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		return 2
	}
	if changeToUser(uid, gid) != nil {
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

// runKillHelperAs runs the helper for the restricted user and waits for it.
func runKillHelperAs(uid, gid uint32) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the binary of the kill helper: %w", err)
	}
	cmd := exec.Command(self, killHelperArg, strconv.FormatUint(uint64(uid), 10), strconv.FormatUint(uint64(gid), 10)) //nolint:gosec
	cmd.Dir = "/"
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the kill helper: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("kill helper: %w", err)
		}
		return nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return errors.New("the kill helper did not end in 5 s")
	}
}

// killUser stops each process of the user and waits until no live task of the
// user is left. A round counts only when the kill helper ran: the /proc scan
// alone can miss a process that forks in a loop.
func killUser(uid, gid uint32) error {
	deadline := time.Now().Add(killTimeout)
	for {
		helperErr := runKillHelperAs(uid, gid)
		pids, err := userProcesses(uid)
		if err != nil {
			return err
		}
		if len(pids) == 0 && helperErr == nil {
			return nil
		}
		killTaskGroups(uid)
		if time.Now().After(deadline) {
			if len(pids) == 0 {
				return fmt.Errorf("the kill helper of the user ID %d failed: %w", uid, helperErr)
			}
			return errors.Join(fmt.Errorf("%d processes of the user ID %d are left after SIGKILL", len(pids), uid), helperErr)
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
		killTaskGroup(pid, uid)
	}
}

// killTaskGroup sends SIGKILL to the thread group pid only while it has a live
// task of the user. The PID can be reused since the scan: the check runs after
// the pidfd is open, so a PID reused before the open fails the check, and one
// reused after it gets no signal. Without pidfd (Linux before 5.3), it sends
// nothing, and only the kill helper, which has no right on other users, stops
// the processes.
func killTaskGroup(pid int, uid uint32) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return
	}
	defer func() { _ = unix.Close(fd) }()
	if len(liveTasks(pid, uid)) == 0 {
		return
	}
	_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
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
