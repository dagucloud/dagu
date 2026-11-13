// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// processPrivileges reads /proc/self/status.
func processPrivileges() (privileges, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return privileges{}, fmt.Errorf("read /proc/self/status: %w", err)
	}
	return parseStatus(string(data))
}

// parseStatus reads the lines Uid, CapEff and NoNewPrivs of the text of
// /proc/<pid>/status. A line that is absent or invalid is an error.
func parseStatus(status string) (privileges, error) {
	var priv privileges
	var haveUID, haveCaps, haveNNP bool
	for line := range strings.SplitSeq(status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Uid":
			fields := strings.Fields(value)
			if len(fields) < 2 {
				return privileges{}, fmt.Errorf("/proc/self/status: invalid line Uid %q", value)
			}
			euid, err := strconv.Atoi(fields[1])
			if err != nil {
				return privileges{}, fmt.Errorf("/proc/self/status: invalid line Uid %q", value)
			}
			priv.euid, haveUID = euid, true
		case "CapEff":
			caps, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return privileges{}, fmt.Errorf("/proc/self/status: invalid line CapEff %q", value)
			}
			priv.capEff, haveCaps = caps, true
		case "NoNewPrivs":
			switch value {
			case "0":
				priv.noNewPrivs = false
			case "1":
				priv.noNewPrivs = true
			default:
				return privileges{}, fmt.Errorf("/proc/self/status: invalid line NoNewPrivs %q", value)
			}
			haveNNP = true
		}
	}
	switch {
	case !haveUID:
		return privileges{}, errors.New("/proc/self/status has no line Uid")
	case !haveCaps:
		return privileges{}, errors.New("/proc/self/status has no line CapEff")
	case !haveNNP:
		return privileges{}, errors.New("/proc/self/status has no line NoNewPrivs")
	}
	return priv, nil
}

// checkIsolationSupport checks the process before an isolated untrusted step:
// the checks 7 and 8 of an agent worker, and the protections of the kernel that
// the isolation needs. A hard link of the restricted user to a file that it
// cannot write is forbidden (fs.protected_hardlinks), and Dagu can see each
// process of the restricted user (no hidepid on /proc).
func checkIsolationSupport() error {
	priv, err := processPrivileges()
	if err != nil {
		return err
	}
	if err := priv.checkChange(); err != nil {
		return fmt.Errorf("%w: the change to the restricted user needs the user ID 0 and the capabilities %s", err, capabilityNames())
	}
	if !priv.noNewPrivs {
		return errors.New("NoNewPrivs is 0 in /proc/self/status, not 1: the restricted user needs the flag no-new-privileges")
	}
	data, err := os.ReadFile("/proc/sys/fs/protected_hardlinks")
	if err != nil {
		return fmt.Errorf("read /proc/sys/fs/protected_hardlinks: %w", err)
	}
	if value := strings.TrimSpace(string(data)); value != "1" {
		return fmt.Errorf("fs.protected_hardlinks is %q, not \"1\": the restricted user could link a file of the worker", value)
	}
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	if option, hidden := procHidePID(string(mountinfo), workerGroups()); hidden {
		return fmt.Errorf("/proc has the option %s: Dagu cannot see each process of the restricted user", option)
	}
	return nil
}

// workerGroups returns the effective group and the groups of the process.
func workerGroups() []int {
	groups, _ := os.Getgroups()
	return append(groups, os.Getegid())
}

// CheckExecutable reports whether the restricted user of cfg can run the
// regular file at path: each folder above it lets the user pass, and the mode
// of the file lets the user run it. Spec 092, "Start", check 3, can use it for
// the binary of an agent worker. The error names the path.
func CheckExecutable(path string, cfg config.UntrustedConfig) error {
	if !cfg.Enabled {
		return errors.New(`key "untrusted.uid" is not set`)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("binary %s is not a regular file", path)
	}
	return checkRunnable(path, cfg.UID, cfg.GID)
}

// checkRunnable reports whether the user uid of the group gid can run the
// regular file at path.
func checkRunnable(path string, uid, gid uint32) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("binary %s is not a regular file", path)
	}
	if err := checkTraversable(path); err != nil {
		return fmt.Errorf("binary %s: %w", path, err)
	}
	owner, group := ownerOf(info)
	perm := info.Mode().Perm()
	var runnable bool
	switch {
	case owner == int(uid):
		runnable = perm&0o100 != 0
	case group == int(gid):
		runnable = perm&0o010 != 0
	default:
		runnable = perm&0o001 != 0
	}
	if !runnable {
		return fmt.Errorf("binary %s has the mode %04o: the restricted user %d cannot run it", path, perm, uid)
	}
	return nil
}

// procHidePID returns the option hidepid of the mount /proc when it hides
// processes from the worker. With the option gid of a group of the worker
// (groups), the worker sees each process, and the mount is accepted.
func procHidePID(mountinfo string, groups []int) (string, bool) {
	for line := range strings.SplitSeq(mountinfo, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(before)
		if len(fields) < 5 || fields[4] != "/proc" {
			continue
		}
		tail := strings.Fields(after)
		if len(tail) < 3 || tail[0] != "proc" {
			continue
		}
		options := tail[2]
		if len(fields) > 5 {
			options += "," + fields[5]
		}
		hide, exempt := "", false
		for option := range strings.SplitSeq(options, ",") {
			name, value, _ := strings.Cut(option, "=")
			switch name {
			case "hidepid":
				if value != "0" && value != "off" {
					hide = option
				}
			case "gid":
				if id, err := strconv.Atoi(value); err == nil && slices.Contains(groups, id) {
					exempt = true
				}
			}
		}
		if hide != "" && !exempt {
			return hide, true
		}
	}
	return "", false
}
