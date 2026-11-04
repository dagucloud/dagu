// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cgroupUnlimited is the least memory limit cgroup v1 reports when none is
// set; it rounds the largest int64 down to a page.
const cgroupUnlimited = 1 << 62

// cgroup reads the memory and CPU limits of the cgroup the process runs in,
// as a container's are, and what the process's cgroup uses of them.
type cgroup struct {
	// root is the directory /proc and /sys are under: "/" outside tests.
	root string
}

// cgroupUsage is what the process's cgroup may use and uses. A limit of
// zero means none is set.
type cgroupUsage struct {
	// MemoryLimit is the memory limit in bytes, and MemoryUsed the memory
	// in use without the inactive file cache the kernel can reclaim;
	// MemoryUnread is set when the limit was read but the usage was not.
	MemoryLimit  uint64
	MemoryUsed   uint64
	MemoryUnread bool
	// CPULimit is the CPU quota in CPUs, and CPUTime the CPU time the
	// cgroup has used since it started.
	CPULimit float64
	CPUTime  time.Duration
}

// read returns the cgroup's limits and usage, and false when the process
// runs in no cgroup this reads, as outside Linux.
func (c cgroup) read() (cgroupUsage, bool) {
	paths, err := c.ownPaths()
	if err != nil {
		return cgroupUsage{}, false
	}
	if c.exists("sys/fs/cgroup/cgroup.controllers") {
		return c.readV2(paths[""])
	}
	return c.readV1(paths)
}

// ownPaths returns the cgroup path of the process per v1 controller, and
// the v2 path under "".
func (c cgroup) ownPaths() (map[string]string, error) {
	f, err := os.Open(filepath.Join(c.root, "proc/self/cgroup"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	paths := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			paths[""] = parts[2]
			continue
		}
		for controller := range strings.SplitSeq(parts[1], ",") {
			paths[controller] = parts[2]
		}
	}
	return paths, scanner.Err()
}

// readV2 reads memory and CPU each from the directory that has its
// controller's files, since a cgroup may enable one without the other.
func (c cgroup) readV2(path string) (cgroupUsage, bool) {
	var u cgroupUsage
	memory := c.dir("sys/fs/cgroup", path, "memory.max")
	if limit, ok := c.limit(memory, "memory.max"); ok {
		u.MemoryLimit = limit
		u.MemoryUsed, ok = c.workingSet(memory, "memory.current", "inactive_file")
		u.MemoryUnread = !ok
	}
	cpu := c.dir("sys/fs/cgroup", path, "cpu.max")
	if fields := c.fields(cpu, "cpu.max"); len(fields) == 2 {
		u.CPULimit = quota(fields[0], fields[1])
	}
	if usec, ok := c.stat(cpu, "cpu.stat", "usage_usec"); ok {
		u.CPUTime = time.Duration(usec) * time.Microsecond //nolint:gosec // CPU microseconds fit an int64 for millennia
	}
	return u, u.MemoryLimit > 0 || u.CPULimit > 0
}

func (c cgroup) readV1(paths map[string]string) (cgroupUsage, bool) {
	var u cgroupUsage
	if path, ok := paths["memory"]; ok {
		dir := c.dir("sys/fs/cgroup/memory", path, "memory.limit_in_bytes")
		if limit, ok := c.limit(dir, "memory.limit_in_bytes"); ok {
			u.MemoryLimit = limit
			u.MemoryUsed, ok = c.workingSet(dir, "memory.usage_in_bytes", "total_inactive_file")
			u.MemoryUnread = !ok
		}
	}
	if path, ok := paths["cpu"]; ok {
		dir := c.dir(c.v1Mount("cpu"), path, "cpu.cfs_quota_us")
		if quotaUs, periodUs := c.fields(dir, "cpu.cfs_quota_us"), c.fields(dir, "cpu.cfs_period_us"); len(quotaUs) == 1 && len(periodUs) == 1 {
			u.CPULimit = quota(quotaUs[0], periodUs[0])
		}
	}
	if path, ok := paths["cpuacct"]; ok {
		dir := c.dir(c.v1Mount("cpuacct"), path, "cpuacct.usage")
		if fields := c.fields(dir, "cpuacct.usage"); len(fields) == 1 {
			if ns, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				u.CPUTime = time.Duration(ns) //nolint:gosec // CPU nanoseconds fit an int64 for centuries
			}
		}
	}
	return u, u.MemoryLimit > 0 || u.CPULimit > 0
}

// v1Mount is where a v1 controller is mounted: on its own, or joined with
// its sibling as cpu,cpuacct.
func (c cgroup) v1Mount(controller string) string {
	if dir := "sys/fs/cgroup/" + controller; c.exists(dir) {
		return dir
	}
	return "sys/fs/cgroup/cpu,cpuacct"
}

// dir is the process's cgroup directory under mount, or the mount itself
// when the cgroup namespace puts the process's cgroup at its root, as in a
// container.
func (c cgroup) dir(mount, path, probe string) string {
	if dir := filepath.Join(mount, path); c.exists(filepath.Join(dir, probe)) {
		return dir
	}
	return mount
}

// limit reads a memory limit file, and false when no limit is set.
func (c cgroup) limit(dir, name string) (uint64, bool) {
	fields := c.fields(dir, name)
	if len(fields) != 1 || fields[0] == "max" {
		return 0, false
	}
	limit, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || limit == 0 || limit >= cgroupUnlimited {
		return 0, false
	}
	return limit, true
}

// workingSet is the memory in use without the inactive file cache, as the
// kernel counts toward the limit before it reclaims, and false when the
// usage cannot be read.
func (c cgroup) workingSet(dir, usageFile, inactiveKey string) (uint64, bool) {
	fields := c.fields(dir, usageFile)
	if len(fields) != 1 {
		return 0, false
	}
	used, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	if inactive, ok := c.stat(dir, "memory.stat", inactiveKey); ok && inactive < used {
		used -= inactive
	}
	return used, true
}

// stat reads one key of a flat keyed file such as memory.stat.
func (c cgroup) stat(dir, name, key string) (uint64, bool) {
	data, err := os.ReadFile(filepath.Join(c.root, dir, name)) //nolint:gosec // a cgroup file under the configured root
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// fields reads a one-line file's fields, and none when it cannot.
func (c cgroup) fields(dir, name string) []string {
	data, err := os.ReadFile(filepath.Join(c.root, dir, name)) //nolint:gosec // a cgroup file under the configured root
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

func (c cgroup) exists(name string) bool {
	_, err := os.Stat(filepath.Join(c.root, name))
	return err == nil
}

// quota is a CPU quota and period in CPUs, and zero when no quota is set.
func quota(quotaUs, periodUs string) float64 {
	q, err := strconv.ParseFloat(quotaUs, 64)
	if err != nil || q <= 0 {
		return 0
	}
	p, err := strconv.ParseFloat(periodUs, 64)
	if err != nil || p <= 0 {
		return 0
	}
	return q / p
}
