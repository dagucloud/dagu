// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRoot writes files under a temporary root, by path relative to it.
func fakeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return root
}

func TestCgroupReadsLimitsAndUsage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files map[string]string
		want  cgroupUsage
	}{
		{
			// A container with its own cgroup namespace sees its cgroup at
			// the mount's root.
			name: "v2 in a container",
			files: map[string]string{
				"proc/self/cgroup":                 "0::/\n",
				"sys/fs/cgroup/cgroup.controllers": "cpu memory\n",
				"sys/fs/cgroup/memory.max":         "536870912\n",
				"sys/fs/cgroup/memory.current":     "300000000\n",
				"sys/fs/cgroup/memory.stat":        "anon 200000000\ninactive_file 100000000\n",
				"sys/fs/cgroup/cpu.max":            "150000 100000\n",
				"sys/fs/cgroup/cpu.stat":           "usage_usec 2500000\nuser_usec 2000000\n",
			},
			want: cgroupUsage{MemoryLimit: 536870912, MemoryUsed: 200000000, CPULimit: 1.5, CPUTime: 2500 * time.Millisecond},
		},
		{
			name: "v2 under a path",
			files: map[string]string{
				"proc/self/cgroup":                              "0::/kubepods/pod1/c1\n",
				"sys/fs/cgroup/cgroup.controllers":              "cpu memory\n",
				"sys/fs/cgroup/kubepods/pod1/c1/memory.max":     "1073741824\n",
				"sys/fs/cgroup/kubepods/pod1/c1/memory.current": "1000\n",
				"sys/fs/cgroup/kubepods/pod1/c1/cpu.max":        "max 100000\n",
			},
			want: cgroupUsage{MemoryLimit: 1073741824, MemoryUsed: 1000},
		},
		{
			name: "v1 with joined cpu and cpuacct",
			files: map[string]string{
				"proc/self/cgroup": "5:memory:/docker/abc\n4:cpu,cpuacct:/docker/abc\n",
				"sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes":  "268435456\n",
				"sys/fs/cgroup/memory/docker/abc/memory.usage_in_bytes":  "150000000\n",
				"sys/fs/cgroup/memory/docker/abc/memory.stat":            "total_inactive_file 50000000\n",
				"sys/fs/cgroup/cpu,cpuacct/docker/abc/cpu.cfs_quota_us":  "200000\n",
				"sys/fs/cgroup/cpu,cpuacct/docker/abc/cpu.cfs_period_us": "100000\n",
				"sys/fs/cgroup/cpu,cpuacct/docker/abc/cpuacct.usage":     "3000000000\n",
			},
			want: cgroupUsage{MemoryLimit: 268435456, MemoryUsed: 100000000, CPULimit: 2, CPUTime: 3 * time.Second},
		},
		{
			// A cgroup may enable the cpu controller without memory, so
			// each controller's files are found on their own.
			name: "v2 with only a CPU quota under a path",
			files: map[string]string{
				"proc/self/cgroup":                 "0::/app\n",
				"sys/fs/cgroup/cgroup.controllers": "cpu memory\n",
				"sys/fs/cgroup/app/cpu.max":        "50000 100000\n",
				"sys/fs/cgroup/app/cpu.stat":       "usage_usec 1000000\n",
				"sys/fs/cgroup/cpu.stat":           "usage_usec 9000000\n",
			},
			want: cgroupUsage{CPULimit: 0.5, CPUTime: time.Second},
		},
		{
			// A limit whose usage cannot be read is no reading of 0.
			name: "v2 with unreadable usage",
			files: map[string]string{
				"proc/self/cgroup":                 "0::/\n",
				"sys/fs/cgroup/cgroup.controllers": "memory\n",
				"sys/fs/cgroup/memory.max":         "536870912\n",
			},
			want: cgroupUsage{MemoryLimit: 536870912, MemoryUnread: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := cgroup{root: fakeRoot(t, tc.files)}.read()
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Without a limit, as on a host or in a container started without one,
// the machine's own figures stand.
func TestCgroupWithoutLimitsReadsNone(t *testing.T) {
	t.Parallel()

	for name, files := range map[string]map[string]string{
		"v2 unlimited": {
			"proc/self/cgroup":                    "0::/user.slice\n",
			"sys/fs/cgroup/cgroup.controllers":    "cpu memory\n",
			"sys/fs/cgroup/user.slice/memory.max": "max\n",
			"sys/fs/cgroup/user.slice/cpu.max":    "max 100000\n",
		},
		"v1 unlimited": {
			"proc/self/cgroup":                            "5:memory:/\n4:cpu,cpuacct:/\n",
			"sys/fs/cgroup/memory/memory.limit_in_bytes":  "9223372036854771712\n",
			"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_quota_us":  "-1\n",
			"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_period_us": "100000\n",
		},
		"no cgroup": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, ok := cgroup{root: fakeRoot(t, files)}.read()
			assert.False(t, ok)
		})
	}
}
