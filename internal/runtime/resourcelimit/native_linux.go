// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package resourcelimit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

const cgroupRoot = "/sys/fs/cgroup"

type cgroupGuard struct {
	path string
}

func startNativeGuard(_ context.Context, opts Options) (nativeGuard, error) {
	if opts.Limits == nil {
		return nil, fmt.Errorf("resource limits are empty")
	}
	parent, err := currentCgroupDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(parent, "cgroup.controllers")); err != nil {
		return nil, fmt.Errorf("cgroup v2 is unavailable: %w", err)
	}

	path := filepath.Join(parent, cgroupName(opts))
	created := false
	if err := os.Mkdir(path, 0o750); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create cgroup: %w", err)
		}
	} else {
		created = true
	}
	keep := false
	defer func() {
		if created && !keep {
			_ = os.Remove(path)
		}
	}()

	if err := writeCgroupLimits(path, opts.Limits); err != nil {
		return nil, err
	}

	keep = true
	return &cgroupGuard{path: path}, nil
}

func (g *cgroupGuard) AssignProcess(pid int) error {
	if g == nil || g.path == "" || pid <= 0 {
		return nil
	}
	return os.WriteFile(filepath.Join(g.path, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o600)
}

func (g *cgroupGuard) Close(context.Context) error {
	if g == nil || g.path == "" {
		return nil
	}
	return os.Remove(g.path)
}

func (*cgroupGuard) Enforcer() string {
	return "cgroup-v2"
}

func writeCgroupLimits(path string, limits *core.ResourceLimits) error {
	if limits.CPUMillis > 0 {
		period := int64(100_000)
		quota := limits.CPUMillis * period / 1000
		if quota <= 0 {
			quota = 1
		}
		if err := os.WriteFile(filepath.Join(path, "cpu.max"), fmt.Appendf(nil, "%d %d", quota, period), 0o600); err != nil {
			return fmt.Errorf("write cpu.max: %w", err)
		}
	}
	if limits.MemoryBytes > 0 {
		if err := os.WriteFile(filepath.Join(path, "memory.max"), []byte(strconv.FormatInt(limits.MemoryBytes, 10)), 0o600); err != nil {
			return fmt.Errorf("write memory.max: %w", err)
		}
	}
	return nil
}

func currentCgroupDir() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read current cgroup: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			rel := strings.TrimPrefix(filepath.Clean("/"+parts[2]), "/")
			return filepath.Join(cgroupRoot, rel), nil
		}
	}
	return "", fmt.Errorf("cgroup v2 entry not found")
}

func cgroupName(opts Options) string {
	return "dagu-" + sanitizeCgroupName(opts.DAGName) + "-" +
		sanitizeCgroupName(opts.DAGRunID) + "-" +
		strconv.Itoa(os.Getpid()) + "-" +
		strconv.FormatInt(time.Now().UnixNano(), 36)
}

func sanitizeCgroupName(value string) string {
	if value == "" {
		return "run"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return "run"
	}
	return trimmed
}
