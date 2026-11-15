// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// A command launched outside the service regards the chosen process as its
// parent, so the tree of this process, which the service wrapper ends on a
// stop, does not hold it. This process still gives the command its input,
// takes its output, and reads how it exited. The test stands in a ping
// process for the wrapper, so it does not run in parallel with launches
// that must keep this process as their parent.
func TestLaunchOutsideServiceParent(t *testing.T) {
	stand := exec.Command("ping", "-n", "60", "127.0.0.1")
	// The service wrapper has no console to share with the process it runs.
	stand.SysProcAttr = cmdutil.DetachAttr(false)
	require.NoError(t, stand.Start())
	t.Cleanup(func() {
		_ = stand.Process.Kill()
		_ = stand.Wait()
	})
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE, false, uint32(stand.Process.Pid)) //nolint:gosec // process IDs fit in uint32
	require.NoError(t, err)
	serviceParent.Store(uintptr(parent))
	t.Cleanup(func() {
		serviceParent.Store(0)
		_ = windows.CloseHandle(parent)
	})

	output, err := os.Create(filepath.Join(t.TempDir(), "output"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = output.Close() })
	started, err := StartProcess(t.Context(), CmdSpec{
		Executable: "powershell",
		Args:       []string{"-NoProfile", "-NonInteractive", "-Command", "[Console]::Out.Write([Console]::In.ReadToEnd()); Start-Sleep -Seconds 2; exit 3"},
		Params:     "the params",
		Stdout:     output,
	})
	require.NoError(t, err)

	assert.Equal(t, stand.Process.Pid, parentPID(t, started.PID), "the parent the command regards")
	err = <-started.Done
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "the exit of the command: %v", err)
	assert.Equal(t, 3, exitErr.ExitCode())
	written, err := os.ReadFile(output.Name())
	require.NoError(t, err)
	assert.Equal(t, "the params", string(written), "the input given and the output taken")
}

// parentPID returns the parent ID the process with pid records.
func parentPID(t *testing.T, pid int) int {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // process IDs fit in uint32
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(process) }()
	var info windows.PROCESS_BASIC_INFORMATION
	err = windows.NtQueryInformationProcess(process, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil) //nolint:gosec // info is the buffer ProcessBasicInformation fills
	require.NoError(t, err)
	return int(info.InheritedFromUniqueProcessId) //nolint:gosec // process IDs fit in int
}
