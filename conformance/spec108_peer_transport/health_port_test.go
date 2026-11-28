// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// The worker holds its health check port while its enrollment is in
// flight, so no other process can take the port and leave the worker
// unable to start after its single-use invite is used up. It then serves
// its health check on that port.
func TestWorkerInviteHoldsHealthPort(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	invite := coord.createInvite(t, false).Invite
	proxy, release := gatedProxy(t, coord.addr)
	port := harness.FreePort(t)

	worker := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", invite),
		append(inviteWorkerArgs("worker-a", proxy), fmt.Sprintf("--worker.health-port=%d", port))...)
	// The start line comes right before enrollment, which waits at the
	// proxy until release.
	waitForOutput(t, worker, builtinFromInvite)
	if other, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
		_ = other.Close()
		t.Fatal("another process could bind the health check port while enrollment was in flight")
	}

	release()
	waitForOutput(t, worker, workerEnrolled)
	expectHealthy(t, port)
}

// gatedProxy forwards connections to target once release is called, and
// holds them until then.
func gatedProxy(t *testing.T, target string) (addr string, release func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(func() {
		release()
		_ = listener.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				<-gate
				forward(conn, target)
			}()
		}
	}()
	return listener.Addr().String(), release
}

// forward copies conn to target and back until either side closes.
func forward(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	go func() { _, _ = io.Copy(upstream, conn) }()
	_, _ = io.Copy(conn, upstream)
}

// expectHealthy waits until the health check on port answers 200.
func expectHealthy(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.After(harness.WaitTimeout(t))
	for {
		resp, err := http.Get(url) //nolint:gosec // A loopback URL the test builds.
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("the health check on port %d did not answer: %v", port, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
