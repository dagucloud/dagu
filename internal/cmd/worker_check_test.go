// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// dagu worker --check prints the start line and what each coordinator
// showed, and fails when one does not pass.
func TestWorkerCheck(t *testing.T) {
	t.Parallel()

	reachable := startCheckCoordinator(t)
	unreachable := unusedAddress(t)
	for _, tc := range []struct {
		name         string
		coordinators string
		want         []string
		wantErr      string
	}{
		{
			name:         "Passes",
			coordinators: reachable,
			want: []string{
				"Peer transport: h2c (from no TLS source)\nCoordinator " + reachable + "\n  Reachable: yes\n",
				"  Coordinator answered: serving, and it accepted the call\n",
				"Every coordinator passed the check\n",
			},
		},
		{
			name:         "Unreachable",
			coordinators: reachable + "," + unreachable,
			want: []string{
				"Coordinator " + reachable + "\n  Reachable: yes\n",
				"  Cannot reach coordinator at " + unreachable + "\n",
			},
			wantErr: "1 of 2 coordinators did not pass the check",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := runWorkerCheck(t, "--worker.coordinators="+tc.coordinators)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.wantErr)
			}
			for _, want := range tc.want {
				assert.Contains(t, out, want)
			}
		})
	}
}

// Without worker.coordinators and without a coordinator in the registry,
// the check fails as the worker does.
func TestWorkerCheckWithoutCoordinators(t *testing.T) {
	t.Parallel()

	out, err := runWorkerCheck(t)

	require.ErrorContains(t, err, "worker.coordinators is required")
	assert.Empty(t, out)
}

func runWorkerCheck(t *testing.T, args ...string) (string, error) {
	t.Helper()
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("# empty\n"), 0o600))

	command := cmd.CmdWorker()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs(append([]string{"--dagu-home", home, "--config", configPath, "--check"}, args...))
	err := command.ExecuteContext(t.Context())
	return out.String(), err
}

// startCheckCoordinator starts a coordinator on h2c that serves the health
// service and the worker list, and returns its address.
func startCheckCoordinator(t *testing.T) string {
	t.Helper()
	server := grpc.NewServer()
	coordinatorv1.RegisterCoordinatorServiceServer(server, coordinator.NewHandler(coordinator.HandlerConfig{}))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}
