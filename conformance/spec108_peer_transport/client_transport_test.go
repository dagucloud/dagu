// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
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

// listedWorker is how GET /api/v1/workers lists a worker.
type listedWorker struct {
	ID              string `json:"id"`
	Connection      string `json:"connection"`
	PlaintextReason string `json:"plaintextReason"`
}

// waitForWorker waits until the server of s lists the worker id without
// an error, which it does only when it reaches the coordinator, and returns
// the worker as listed.
func (s startAll) waitForWorker(t *testing.T, id string) listedWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), harness.WaitTimeout(t))
	defer cancel()
	var last string
	for ctx.Err() == nil {
		worker, err := s.lists(ctx, id)
		if worker != nil {
			return *worker
		}
		last = fmt.Sprint(err)
		select {
		case <-s.process.Done():
			t.Fatalf("start-all exited: %s", s.process.FailureOutput())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("the server did not list worker %s: %s", id, last)
	return listedWorker{}
}

// lists returns the worker id when the server lists it without an error.
func (s startAll) lists(ctx context.Context, id string) (*listedWorker, error) {
	var body struct {
		Workers []listedWorker `json:"workers"`
		Errors  []string       `json:"errors"`
	}
	if err := s.get(ctx, "/api/v1/workers", &body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		return nil, errors.New(strings.Join(body.Errors, "; "))
	}
	for _, worker := range body.Workers {
		if worker.ID == id {
			return &worker, nil
		}
	}
	return nil, fmt.Errorf("%d other workers", len(body.Workers))
}

// get decodes the JSON answer of the server of s to GET path into body.
func (s startAll) get(ctx context.Context, path string, body any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.server+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(body); err != nil {
		return fmt.Errorf("status %d: %w", resp.StatusCode, err)
	}
	return nil
}

// h2cCallers returns the h2c callers of the coordinators that the server of
// s lists, as "<host> <reason>".
func (s startAll) h2cCallers(t *testing.T) []string {
	t.Helper()
	var body struct {
		Coordinators []struct {
			H2CCallers []struct {
				Host   string `json:"host"`
				Reason string `json:"reason"`
			} `json:"h2cCallers"`
		} `json:"coordinators"`
	}
	require.NoError(t, s.get(t.Context(), "/api/v1/services/coordinator", &body))
	var callers []string
	for _, coordinator := range body.Coordinators {
		for _, caller := range coordinator.H2CCallers {
			callers = append(callers, caller.Host+" "+caller.Reason)
		}
	}
	return callers
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

	worker := services.startSharedWorker(t, dagu, "tls-worker", services.coordinator)
	waitForOutput(t, worker, builtinFromDataDir)
	assert.Equal(t, listedWorker{ID: "tls-worker", Connection: "tls"}, services.waitForWorker(t, "tls-worker"))

	plaintext := services.startSharedWorker(t, dagu, "h2c-worker", services.coordinator, "--peer.insecure")
	waitForOutput(t, plaintext, h2cInsecure,
		"The coordinator requires TLS. Upgrade this process, or remove peer.insecure: true.")
}

// A worker on h2c, as every worker before Spec 108 is, is still served,
// and the Workers list says why it uses h2c (Spec 108 §Versions, §Workers
// List).
func TestH2CWorkerServed(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu)
	worker := dagu.StartWithEnv(nil, "worker", "--worker.id=h2c-worker", "--worker.health-port=0",
		"--worker.coordinators="+services.coordinator, "--peer.insecure")
	waitForOutput(t, worker, h2cInsecure)
	assert.Equal(t, listedWorker{ID: "h2c-worker", Connection: "plaintext", PlaintextReason: "peer.insecure"},
		services.waitForWorker(t, "h2c-worker"))
	assert.Empty(t, services.h2cCallers(t), "workers' calls are on the Workers page only")
}

// A process that predates Spec 108 sends no h2c reason: its heartbeat reads
// as an older version on the Workers list, and its other calls are listed
// as h2c callers (Spec 108 §Two Transports on One Port, §h2c Callers).
func TestOlderH2CProcess(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu)
	client := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, services.coordinator, insecure.NewCredentials()))

	_, err := client.Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{WorkerId: "old-worker"}, grpc.WaitForReady(true))
	require.NoError(t, err)
	assert.Equal(t, listedWorker{ID: "old-worker", Connection: "plaintext", PlaintextReason: "older version"},
		services.waitForWorker(t, "old-worker"))

	_, err = client.GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"127.0.0.1 older version"}, services.h2cCallers(t))
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
	assert.Equal(t, listedWorker{ID: "proxied-worker", Connection: "plaintext", PlaintextReason: "tls failed on the network path"},
		services.waitForWorker(t, "proxied-worker"))
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
