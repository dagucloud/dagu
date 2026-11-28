// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package worker

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refusal as revoked closes the worker's admission at once, before the
// refusal is recorded and the termination message written, and Close waits
// for the recording before it releases the lock (Spec 108 §Removed Worker).
func TestMembershipRevokeSignalsFirst(t *testing.T) {
	ca := newTestCA(t)
	dataDir := t.TempDir()
	// Writing a FIFO blocks until it is read, which holds the recording.
	log := filepath.Join(t.TempDir(), "termination-log")
	require.NoError(t, syscall.Mkfifo(log, 0o600))
	fake := enrolling(ca)
	fake.kubernetes, fake.terminationLog = true, log
	m, err := open(t, workerConfig(dataDir, ca.invite(t)), fake)
	require.NoError(t, err)
	require.NoError(t, connect(t.Context(), t, m))

	go fake.onRemoved()
	select {
	case <-m.revoked:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker kept admitting runs while the removal was recorded")
	}
	select {
	case <-m.recorded:
		t.Fatal("recorded before the termination message was written")
	default:
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()

	data, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, removedKubernetesLine, string(data))
	require.NoError(t, <-closed)
	<-m.recorded
}
