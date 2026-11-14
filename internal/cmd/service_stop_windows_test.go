// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmd

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// service-stop reaches the start-all process serving the same data
// directory, and only that one.
func TestServiceStopRequest(t *testing.T) {
	served := t.TempDir()
	requests, err := serviceStopRequests(served)
	require.NoError(t, err)

	other := t.TempDir()
	otherRequests, err := serviceStopRequests(other)
	require.NoError(t, err)

	require.NoError(t, requestServiceStop(served))
	select {
	case sig := <-requests:
		require.Equal(t, os.Interrupt, sig)
	case <-time.After(10 * time.Second):
		t.Fatal("the stop request did not arrive")
	}
	select {
	case <-otherRequests:
		t.Fatal("the stop request reached another data directory")
	case <-time.After(100 * time.Millisecond):
	}

	// A repeated request during shutdown still arrives.
	require.NoError(t, requestServiceStop(served))
	select {
	case <-requests:
	case <-time.After(10 * time.Second):
		t.Fatal("the repeated stop request did not arrive")
	}
}

// Without a listener, service-stop ends processes only when the service
// wrapper started it; run from a terminal it must not touch its siblings.
func TestServiceStopWithoutListenerOutsideTheService(t *testing.T) {
	err := requestServiceStop(t.TempDir())
	require.ErrorContains(t, err, "listens for a stop request")
}
