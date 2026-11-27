// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

const (
	builtinFromDataDir = "Peer transport: built-in TLS (from the coordinator's data directory)"
	// tlsRecord is the first byte of a TLS connection.
	tlsRecord = 0x16
)

// startAll is a start-all process and the addresses of its server and
// coordinator.
type startAll struct {
	process     *harness.Process
	server      string
	coordinator string
	// home is the Dagu home, and so the data directory, it shares with the
	// processes that get it.
	home string
}

// startStartAll starts start-all with env and no peer setting, and returns
// once its server and coordinator serve.
func startStartAll(t *testing.T, dagu *harness.Runner, env ...string) startAll {
	t.Helper()
	s := startAll{
		server:      fmt.Sprintf("127.0.0.1:%d", harness.FreePort(t)),
		coordinator: fmt.Sprintf("127.0.0.1:%d", harness.FreePort(t)),
		home:        dagu.ProjectPath("home"),
	}
	_, serverPort, _ := net.SplitHostPort(s.server)
	_, coordinatorPort, _ := net.SplitHostPort(s.coordinator)
	s.process = dagu.StartWithEnv(append(env, "DAGU_HOME="+s.home),
		"start-all",
		"--host=127.0.0.1",
		"--port="+serverPort,
		"--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		"--coordinator.port="+coordinatorPort,
	)
	waitForOutput(t, s.process, "Starting to serve on coordinator service", "Server is starting")
	return s
}

// startSharedWorker starts a worker with id that dials coordinator and
// shares the data directory of s.
func (s startAll) startSharedWorker(t *testing.T, dagu *harness.Runner, id, coordinator string, args ...string) *harness.Process {
	t.Helper()
	return dagu.StartWithEnv([]string{"DAGU_HOME=" + s.home}, append([]string{
		"worker",
		"--worker.id=" + id,
		"--worker.health-port=0",
		"--worker.coordinators=" + coordinator,
	}, args...)...)
}

// waitForWorker waits until the server of s lists the worker id without
// an error, which it does only when it reaches the coordinator.
func (s startAll) waitForWorker(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	var last string
	for time.Now().Before(deadline) {
		listed, err := s.lists(id)
		if listed {
			return
		}
		last = fmt.Sprint(err)
		select {
		case <-s.process.Done():
			t.Fatalf("start-all exited: %s", s.process.FailureOutput())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("the server did not list worker %s: %s", id, last)
}

// lists reports whether the server lists the worker id without an error.
func (s startAll) lists(id string) (bool, error) {
	resp, err := http.Get("http://" + s.server + "/api/v1/workers")
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Workers []struct {
			ID string `json:"id"`
		} `json:"workers"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("status %d: %w", resp.StatusCode, err)
	}
	if len(body.Errors) > 0 {
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, strings.Join(body.Errors, "; "))
	}
	for _, worker := range body.Workers {
		if worker.ID == id {
			return true, nil
		}
	}
	return false, fmt.Errorf("status %d: %d other workers", resp.StatusCode, len(body.Workers))
}

// After the upgrade, start-all with no setting serves built-in TLS, and its
// server and a worker that shares its data directory use it: both keep
// working under coordinator.require_tls, which refuses a worker on h2c
// (Spec 108 §start-all After the Upgrade, §Require TLS).
func TestStartAllBuiltinTLS(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu, "DAGU_COORDINATOR_REQUIRE_TLS=true")
	output := waitForOutput(t, services.process,
		"Coordinator transport: built-in TLS only (from coordinator.require_tls)", builtinFromDataDir)
	assert.Equal(t, 1, strings.Count(output, "Peer transport:"), output)
	assert.FileExists(t, filepath.Join(services.home, "data", "coordinator", "peer-ca.crt"))

	worker := services.startSharedWorker(t, dagu, "tls-worker", services.coordinator)
	waitForOutput(t, worker, builtinFromDataDir)
	services.waitForWorker(t, "tls-worker")

	plaintext := services.startSharedWorker(t, dagu, "h2c-worker", services.coordinator, "--peer.insecure")
	waitForOutput(t, plaintext, h2cInsecure,
		"The coordinator requires TLS. Upgrade this process, or remove peer.insecure: true.")
}

// A worker on h2c, as every worker before Spec 108 is, is still served
// (Spec 108 §Versions).
func TestH2CWorkerServed(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu)
	worker := dagu.StartWithEnv(nil, "worker", "--worker.id=h2c-worker", "--worker.health-port=0",
		"--worker.coordinators="+services.coordinator, "--peer.insecure")
	waitForOutput(t, worker, h2cInsecure)
	services.waitForWorker(t, "h2c-worker")
}

// A worker whose path to the coordinator passes a proxy that expects
// plaintext gRPC falls back to h2c and keeps working (Spec 108 §Plaintext
// Proxy).
func TestPlaintextProxyFallback(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu)
	proxy := plaintextProxy(t, services.coordinator)

	worker := services.startSharedWorker(t, dagu, "proxied-worker", proxy)
	waitForOutput(t, worker, builtinFromDataDir,
		"Built-in TLS failed on the network path; using h2c for 10 minutes",
		"Peer transport to "+proxy+": h2c (from tls failed on the network path)")
	services.waitForWorker(t, "proxied-worker")
}

// plaintextProxy forwards connections to target, and closes one that starts
// with a TLS record, as a proxy that expects plaintext gRPC does. It returns
// its address.
func plaintextProxy(t *testing.T, target string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go forwardPlaintext(conn, target)
		}
	}()
	return listener.Addr().String()
}

func forwardPlaintext(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	first := make([]byte, 1)
	if _, err := io.ReadFull(conn, first); err != nil || first[0] == tlsRecord {
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	if _, err := upstream.Write(first); err != nil {
		return
	}
	go func() { _, _ = io.Copy(upstream, conn) }()
	_, _ = io.Copy(conn, upstream)
}
