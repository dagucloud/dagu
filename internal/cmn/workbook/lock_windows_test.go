// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package workbook

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdExclusive opens a file the way Excel does, with no sharing, so a
// rename over it fails with a sharing violation.
func holdExclusive(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	return func() { _ = syscall.CloseHandle(handle) }
}

func TestSharingViolationBecomesLockedError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)

	release := holdExclusive(t, path)
	defer release()

	_, err = Write(context.Background(), path, orders(), WriteOptions{Header: true})
	var locked *LockedError
	require.ErrorAs(t, err, &locked)
	assert.Equal(t, "held.xlsx is open in another program; close it and retry", err.Error())
	assert.True(t, isSharingViolation(errorSharingViolation))
	assert.False(t, isSharingViolation(errors.New("other")))
}

func TestWaitForUnlockSucceedsOnceReleased(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)

	release := holdExclusive(t, path)
	released := false
	var logs []string
	opts := WriteOptions{Header: true, Lock: LockOptions{
		WaitFor: 10 * time.Second,
		Log:     func(msg string) { logs = append(logs, msg) },
		sleep: func(context.Context, time.Duration) error {
			if !released {
				release()
				released = true
			}
			return nil
		},
	}}
	result, err := Append(context.Background(), path, Table{Columns: orders().Columns, Rows: [][]any{{"INV-3", 1, nil, nil, nil}}}, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.RowsAppended)
	require.NotEmpty(t, logs)
	assert.Contains(t, logs[0], "held.xlsx is open in another program; retrying in 2s")
}
