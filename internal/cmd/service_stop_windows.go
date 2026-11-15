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
	supervised := openSupervised()
	defer func() {
		for _, process := range supervised {
			_ = windows.CloseHandle(process)
		}
	}()
	if err := windows.SetEvent(event); err != nil {
		return fmt.Errorf("request service stop: %w", err)
	}
	return awaitExit(supervised, timeout)
}

// serviceWrapperExe is the service wrapper the installer sets up.
const serviceWrapperExe = "dagu-service.exe"

// supervisedProcesses returns the IDs of the other processes this process's
// parent started, and whether that parent is the service wrapper.
func supervisedProcesses() ([]uint32, bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, false, fmt.Errorf("list processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	wrapper, self := uint32(os.Getppid()), uint32(os.Getpid()) //nolint:gosec // process IDs fit in uint32
	var (
		entry      windows.ProcessEntry32
		supervised []uint32
		byWrapper  bool
	)
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		switch {
		case entry.ProcessID == wrapper:
			byWrapper = strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), serviceWrapperExe)
		case entry.ParentProcessID == wrapper && entry.ProcessID != self:
			supervised = append(supervised, entry.ProcessID)
		}
	}
	return supervised, byWrapper, nil
}

// openSupervised opens the processes the service wrapper supervises, when
// the wrapper runs this process, to wait for and end them.
func openSupervised() []windows.Handle {
	pids, byWrapper, err := supervisedProcesses()
	if err != nil || !byWrapper {
		return nil
	}
	var processes []windows.Handle
	for _, pid := range pids {
		if process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid); err == nil {
			processes = append(processes, process)
		}
	}
	return processes
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
	supervised, byWrapper, err := supervisedProcesses()
	if err != nil {
		return err
	}
	if !byWrapper {
		return errors.New("no Dagu process serving this data directory listens for a stop request")
	}

	var errs []error
	for _, pid := range supervised {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.FormatUint(uint64(pid), 10)) //nolint:gosec // fixed command and a process ID
		if output, killErr := kill.CombinedOutput(); killErr != nil {
			errs = append(errs, fmt.Errorf("end process %d: %w: %s", pid, killErr, output))
		}
	}
	return errors.Join(errs...)
}
