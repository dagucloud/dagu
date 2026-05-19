// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package resourcelimit

import (
	"context"
	"fmt"
	"math"
	goruntime "runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	jobObjectCPURateControlInformationClass = 15

	jobObjectCPURateControlEnable  = 0x1
	jobObjectCPURateControlHardCap = 0x4
)

type windowsJobGuard struct {
	handle windows.Handle
}

type jobObjectCPURateControlInformation struct {
	ControlFlags uint32
	CPURate      uint32
}

func startNativeGuard(_ context.Context, opts Options) (nativeGuard, error) {
	if opts.Limits == nil {
		return nil, fmt.Errorf("resource limits are empty")
	}
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	if err := configureJobObject(handle, opts); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsJobGuard{handle: handle}, nil
}

func configureJobObject(handle windows.Handle, opts Options) error {
	if opts.Limits.MemoryBytes > 0 {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(opts.Limits.MemoryBytes)
		_, err := windows.SetInformationJobObject(
			handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		)
		if err != nil {
			return fmt.Errorf("set job memory limit: %w", err)
		}
	}

	if opts.Limits.CPUMillis > 0 {
		rate := cpuRateForMillis(opts.Limits.CPUMillis)
		info := jobObjectCPURateControlInformation{
			ControlFlags: jobObjectCPURateControlEnable | jobObjectCPURateControlHardCap,
			CPURate:      rate,
		}
		_, err := windows.SetInformationJobObject(
			handle,
			jobObjectCPURateControlInformationClass,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		)
		if err != nil {
			return fmt.Errorf("set job cpu limit: %w", err)
		}
	}

	return nil
}

func (g *windowsJobGuard) AssignProcess(pid int) error {
	if g == nil || g.handle == 0 || pid <= 0 {
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process for job assignment: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(g.handle, process); err != nil {
		return fmt.Errorf("assign process to job object: %w", err)
	}
	return nil
}

func (g *windowsJobGuard) Close(context.Context) error {
	if g == nil || g.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(g.handle)
	g.handle = 0
	return err
}

func (*windowsJobGuard) Enforcer() string {
	return "windows-job-object"
}

func cpuRateForMillis(millis int64) uint32 {
	totalMillis := int64(goruntime.NumCPU()) * 1000
	rate := uint32(math.Ceil(float64(millis) * 10_000 / float64(totalMillis)))
	if rate < 1 {
		return 1
	}
	if rate > 10_000 {
		return 10_000
	}
	return rate
}
