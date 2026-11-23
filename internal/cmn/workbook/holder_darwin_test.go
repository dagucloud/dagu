// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package workbook

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is a controlled process-name fixture, not an Excel integration test.
func TestMacOSWorkbookHolderProcess(t *testing.T) {
	path := os.Getenv("DAGU_TEST_WORKBOOK_HOLDER")
	if path == "" {
		return
	}
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func holdMacOSWorkbook(t *testing.T, path, name string) func() {
	t.Helper()
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is unavailable")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(self)
	require.NoError(t, err)
	executable := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(executable, data, 0o700))
	cmd := exec.Command(executable, "-test.run=^TestMacOSWorkbookHolderProcess$")
	cmd.Env = append(os.Environ(), "DAGU_TEST_WORKBOOK_HOLDER="+path)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = input.Close()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		})
	}
	t.Cleanup(release)
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(output).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		require.Equal(t, "ready\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("workbook holder did not start")
	}
	return release
}

func TestMacOSExcelProcessBlocksWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held 空.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
	release := holdMacOSWorkbook(t, path, "Microsoft Excel")
	// Confirm the actual OS probe sees the intended fixture before asserting
	// Dagu's behavior. No owner file is needed for this detection.
	output, err := exec.Command("lsof", "+c", "0", "-Fc", "--", path).Output()
	require.NoError(t, err)
	require.Contains(t, string(output), "cMicrosoft Excel\n")
	_, err = Write(context.Background(), path, orders(), WriteOptions{Header: true})
	var locked *LockedError
	require.ErrorAs(t, err, &locked)
	link := filepath.Join(t.TempDir(), "linked.xlsx")
	require.NoError(t, os.Symlink(path, link))
	_, err = checkLockFile(link)
	assert.ErrorAs(t, err, &locked)
	release()
	_, err = Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
}

func TestMacOSOtherProcessAndLeftoverOwnerDoNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
	holdMacOSWorkbook(t, path, "Workbook reader")
	require.NoError(t, os.WriteFile(lockFilePath(path), []byte("owner"), 0o600))
	result, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "may be open")
}

func TestMacOSExcelProbeOutput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		output string
		err    error
		held   bool
	}{
		{name: "Excel", output: "p123\ncMicrosoft Excel\n", held: true},
		{name: "other holder", output: "p123\ncTextEdit\n"},
		{name: "prefix is not Excel", output: "p123\ncMicrosoft Excel Helper\n"},
		{name: "several holders", output: "p1\ncTextEdit\np2\ncMicrosoft Excel\n", held: true},
		{name: "empty output"},
		{name: "failed probe", output: "cMicrosoft Excel\n", err: errors.New("probe failed")},
		{name: "timed out", output: "cMicrosoft Excel\n", err: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.held, excelProcessHoldsWorkbook([]byte(tt.output), tt.err))
		})
	}
}
