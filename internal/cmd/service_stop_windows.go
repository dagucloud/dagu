// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// serviceStopRequests delivers a request each time service-stop asks the
// process serving dataDir to shut down. The event lives as long as the
// process, so a repeated request during shutdown still reaches it.
func serviceStopRequests(dataDir string) (<-chan os.Signal, error) {
	name, err := windows.UTF16PtrFromString(serviceStopEventName(dataDir))
	if err != nil {
		return nil, err
	}
	event, err := windows.CreateEvent(nil, 0, 0, name)
	if event == 0 {
		return nil, fmt.Errorf("create service stop event: %w", err)
	}
	requests := make(chan os.Signal, 1)
	go func() {
		for {
			if _, err := windows.WaitForSingleObject(event, windows.INFINITE); err != nil {
				return
			}
			select {
			case requests <- os.Interrupt:
			default:
			}
		}
	}()
	return requests, nil
}

// requestServiceStop asks the process serving dataDir to shut down. Run by
// the service wrapper, it then waits up to timeout for the process the
// wrapper supervises to exit and ends that process alone if it has not, so
// runs in progress keep running and the service stop cannot hang, as the
// wrapper waits for that exit without a bound of its own. A process that
// does not listen, as a binary from before this request existed, is ended
// with its process tree instead, as the service wrapper would.
func requestServiceStop(dataDir string, timeout time.Duration) error {
	name, err := windows.UTF16PtrFromString(serviceStopEventName(dataDir))
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return endSupervisedTrees()
	}
	if err != nil {
		return fmt.Errorf("open service stop event: %w", err)
	}
	defer func() { _ = windows.CloseHandle(event) }()

	// Opened before the request, so the ID of a process that exits at once
	// cannot name another process by the time it is waited on.
	supervised, _, openErr := supervisedProcesses(windows.SYNCHRONIZE | windows.PROCESS_TERMINATE)
	defer closeHandles(supervised)
	if err := windows.SetEvent(event); err != nil {
		return fmt.Errorf("request service stop: %w", err)
	}
	return errors.Join(openErr, awaitExit(supervised, timeout))
}

// serviceWrapperExe is the service wrapper the installer sets up.
const serviceWrapperExe = "dagu-service.exe"

// supervisedProcesses opens, with access, the other processes this
// process's parent started when that parent is the service wrapper, and
// reports whether it is. Windows keeps the parent ID of a process whose
// parent exited, and may give it to the wrapper, so a process older than
// the wrapper is not one it started. The caller closes what is returned.
func supervisedProcesses(access uint32) ([]windows.Handle, bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, false, fmt.Errorf("list processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	wrapper, self := uint32(os.Getppid()), uint32(os.Getpid()) //nolint:gosec // process IDs fit in uint32
	var (
		entry     windows.ProcessEntry32
		children  []uint32
		byWrapper bool
	)
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		switch {
		case entry.ProcessID == wrapper:
			byWrapper = strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), serviceWrapperExe)
		case entry.ParentProcessID == wrapper && entry.ProcessID != self:
			children = append(children, entry.ProcessID)
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, byWrapper, fmt.Errorf("list processes: %w", err)
	}
	if !byWrapper {
		return nil, false, nil
	}

	wrapperStarted, err := processStartTime(wrapper)
	if err != nil {
		return nil, true, fmt.Errorf("read the service wrapper's start time: %w", err)
	}
	var (
		processes []windows.Handle
		errs      []error
	)
	for _, pid := range children {
		process, err := windows.OpenProcess(access|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue // exited since the snapshot
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("open process %d: %w", pid, err))
			continue
		}
		// The ID may have passed to another process since the snapshot, so
		// the opened process itself must be the wrapper's child.
		supervised, err := startedBy(process, wrapper, wrapperStarted)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect process %d: %w", pid, err))
		}
		if !supervised {
			_ = windows.CloseHandle(process)
			continue
		}
		processes = append(processes, process)
	}
	return processes, true, errors.Join(errs...)
}

// processStartTime returns when the process with pid started, in
// nanoseconds since 1601.
func processStartTime(pid uint32) (int64, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	return startTime(process)
}

// startedBy reports whether process is a child of the process with
// parentPID that started at parentStarted.
func startedBy(process windows.Handle, parentPID uint32, parentStarted int64) (bool, error) {
	var info windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(process, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil { //nolint:gosec // info is the buffer ProcessBasicInformation fills
		return false, err
	}
	if info.InheritedFromUniqueProcessId != uintptr(parentPID) {
		return false, nil
	}
	started, err := startTime(process)
	if err != nil {
		return false, err
	}
	return started >= parentStarted, nil
}

// startTime returns when process started, in nanoseconds since 1601.
func startTime(process windows.Handle) (int64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return created.Nanoseconds(), nil
}

// closeHandles closes each of handles.
func closeHandles(handles []windows.Handle) {
	for _, handle := range handles {
		_ = windows.CloseHandle(handle)
	}
}

// awaitExit waits up to timeout for the processes to exit and ends each one
// still running then. Windows ends no child of an ended process, so the runs
// such a process started keep running.
func awaitExit(processes []windows.Handle, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var errs []error
	for _, process := range processes {
		wait := max(time.Until(deadline), 0)
		result, err := windows.WaitForSingleObject(process, uint32(wait.Milliseconds())) //nolint:gosec // the timeout fits in uint32 milliseconds
		if err != nil {
			errs = append(errs, fmt.Errorf("wait for process to exit: %w", err))
			continue
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		fmt.Fprintf(os.Stderr, "Dagu did not shut down within %s, so it is ended; runs in progress keep running\n", timeout)
		if err := windows.TerminateProcess(process, 1); err != nil {
			errs = append(errs, fmt.Errorf("end process: %w", err))
		}
	}
	return errors.Join(errs...)
}

// endSupervisedTrees ends the process trees of the other processes the
// service wrapper started, when the wrapper is this process's parent.
func endSupervisedTrees() error {
	supervised, byWrapper, err := supervisedProcesses(0)
	defer closeHandles(supervised)
	if !byWrapper {
		if err != nil {
			return err
		}
		return errors.New("no Dagu process serving this data directory listens for a stop request")
	}

	errs := []error{err}
	for _, process := range supervised {
		pid, idErr := windows.GetProcessId(process)
		if idErr != nil {
			errs = append(errs, fmt.Errorf("read process ID: %w", idErr))
			continue
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.FormatUint(uint64(pid), 10)) //nolint:gosec // fixed command and a process ID
		if output, killErr := kill.CombinedOutput(); killErr != nil {
			errs = append(errs, fmt.Errorf("end process %d: %w: %s", pid, killErr, output))
		}
	}
	return errors.Join(errs...)
}
