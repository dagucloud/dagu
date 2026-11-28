// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// The worker serves its health check while its enrollment is in flight,
// so a liveness probe sees it alive while it waits for a coordinator, and
// no other process can take the port and leave the worker unable to start
// after its single-use invite is used up. Its readiness waits until it has
// enrolled and reached the coordinator, so a rollout does not count it
// before it can take runs.
func TestWorkerInviteHoldsHealthPort(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	coord := startInviteCoordinator(t, dagu)
	invite := coord.createInvite(t, false).Invite
	proxy, enrollment, calls := gatedProxy(t, coord.addr)
	port := harness.FreePort(t)

	worker := dagu.StartWithEnv(inviteWorkerEnv(dagu, "host-a", invite),
		append(inviteWorkerArgs("worker-a", proxy), fmt.Sprintf("--worker.health-port=%d", port))...)
	// Only enrollment dials the coordinator before the worker holds a
	// certificate, so the first connection at the proxy is enrollment. It
	// waits there until released.
	waitForOutput(t, worker, builtinFromInvite)
	waitForGate(t, worker, enrollment, "the worker did not start enrolling")
	if other, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
		_ = other.Close()
		t.Fatal("another process could bind the health check port while enrollment was in flight")
	}
	expectStatus(t, port, "/health", http.StatusOK)
	assert.Equal(t, http.StatusServiceUnavailable, getStatus(t, port, "/ready"), "not ready while enrollment waits")

	// Once enrolled, the worker's calls, its first heartbeat among them,
	// wait at the proxy too, so readiness still waits.
	enrollment.release()
	waitForOutput(t, worker, workerEnrolled)
	waitForGate(t, worker, calls, "the worker did not call the coordinator after enrolling")
	expectStatus(t, port, "/health", http.StatusOK)
	assert.Equal(t, http.StatusServiceUnavailable, getStatus(t, port, "/ready"), "not ready before a heartbeat reaches the coordinator")

	calls.release()
	expectStatus(t, port, "/ready", http.StatusOK)
	expectStatus(t, port, "/health", http.StatusOK)
}

// proxyGate holds connections until released, and says when it holds the
// first.
type proxyGate struct {
	accepted   chan struct{}
	acceptOnce sync.Once
	open       chan struct{}
	openOnce   sync.Once
}

func newProxyGate() *proxyGate {
	return &proxyGate{accepted: make(chan struct{}), open: make(chan struct{})}
}

func (g *proxyGate) hold() {
	g.acceptOnce.Do(func() { close(g.accepted) })
	<-g.open
}

func (g *proxyGate) release() {
	g.openOnce.Do(func() { close(g.open) })
}

// gatedProxy forwards connections to target. The first waits at first, and
// every later one at rest, until each gate is released.
func gatedProxy(t *testing.T, target string) (addr string, first, rest *proxyGate) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	first, rest = newProxyGate(), newProxyGate()
	t.Cleanup(func() {
		first.release()
		rest.release()
		_ = listener.Close()
	})
	go func() {
		for n := 0; ; n++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			gate := rest
			if n == 0 {
				gate = first
			}
			go func() {
				gate.hold()
				forward(conn, target)
			}()
		}
	}()
	return listener.Addr().String(), first, rest
}

// waitForGate waits until gate holds a connection of process.
func waitForGate(t *testing.T, process *harness.Process, gate *proxyGate, failure string) {
	t.Helper()
	select {
	case <-gate.accepted:
	case <-process.Done():
		t.Fatalf("the worker exited: %s", process.FailureOutput())
	case <-time.After(harness.WaitTimeout(t)):
		t.Fatal(failure)
	}
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

// expectStatus waits until path on port answers want, within the test's
// wait budget, each request included.
func expectStatus(t *testing.T, port int, path string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), harness.WaitTimeout(t))
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		got := 0
		if err == nil {
			_ = resp.Body.Close()
			if got = resp.StatusCode; got == want {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s on port %d did not answer %d (last %d): %v", path, port, want, got, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// getStatus returns what path on port answers now.
func getStatus(t *testing.T, port int, path string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), harness.WaitTimeout(t))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode
}
