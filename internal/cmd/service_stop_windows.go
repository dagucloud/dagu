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

// requestServiceStop asks the process serving dataDir to shut down. A
// process that does not listen, as a binary from before this request
// existed, is ended with its process tree instead, as the service wrapper
// would, so the service stop cannot hang.
func requestServiceStop(dataDir string) error {
	name, err := windows.UTF16PtrFromString(serviceStopEventName(dataDir))
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err == nil {
		defer func() { _ = windows.CloseHandle(event) }()
		return windows.SetEvent(event)
	}
	if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("open service stop event: %w", err)
	}
	return endSupervisedTrees()
}

// serviceWrapperExe is the service wrapper the installer sets up.
const serviceWrapperExe = "dagu-service.exe"

// endSupervisedTrees ends the process trees of the other processes the
// service wrapper started, when the wrapper is this process's parent.
func endSupervisedTrees() error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
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
