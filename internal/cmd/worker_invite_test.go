// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	testInvite        = "dagu1.pinpinpinpinpinpinpinpinpinpinpinpinpinpinp.secretsecretsecretsecretsecretsecretsecr"
	testInviteAddress = "192.168.1.20:50055"
)

// testInviteExpiry is the expiry the fake coordinator reports.
var testInviteExpiry = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)

// inviteCoordinator is a coordinator that reports join readiness and
// creates invites, recording what it was asked.
type inviteCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	readiness    *coordinatorv1.GetJoinReadinessResponse
	readinessErr error
	createErr    error

	mu      sync.Mutex
	created []*coordinatorv1.CreateInviteRequest
}

func (c *inviteCoordinator) GetJoinReadiness(context.Context, *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	return c.readiness, c.readinessErr
}

func (c *inviteCoordinator) CreateInvite(_ context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created = append(c.created, req)
	if c.createErr != nil {
		return nil, c.createErr
	}
	kind := coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE
	var expiresAt int64
	if req.Reusable {
		kind = coordinatorv1.InviteKind_INVITE_KIND_REUSABLE
	}
	if !req.Reusable || req.ExpiresInSeconds > 0 {
		expiresAt = testInviteExpiry.Unix()
	}
	return &coordinatorv1.CreateInviteResponse{
		Invite: testInvite,
		Record: &coordinatorv1.InviteRecord{Id: "invite-1", Kind: kind, ExpiresAt: expiresAt, Address: req.Address},
	}, nil
}

func (c *inviteCoordinator) createRequests() []*coordinatorv1.CreateInviteRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.created
}

func readyReport() *coordinatorv1.GetJoinReadinessResponse {
	return &coordinatorv1.GetJoinReadinessResponse{
		ListenHost:  "0.0.0.0",
		Port:        50055,
		Environment: coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST,
		BuiltinTls:  true,
		Ready:       true,
	}
}

func loopbackReport(change func(*coordinatorv1.GetJoinReadinessResponse)) *coordinatorv1.GetJoinReadinessResponse {
	report := readyReport()
	report.ListenHost = "127.0.0.1"
	report.LoopbackOnly = true
	report.Ready = false
	report.ConfigFile = "/etc/dagu/config.yaml"
	report.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT}
	if change != nil {
		change(report)
	}
	return report
}

func noBuiltinTLSReport(reason coordinatorv1.BuiltinTLSReason) *coordinatorv1.GetJoinReadinessResponse {
	report := readyReport()
	report.BuiltinTls = false
	report.BuiltinTlsReason = reason
	report.Ready = false
	return report
}

func TestWorkerInvite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want *coordinatorv1.CreateInviteRequest
		kind string
	}{
		{
			name: "SingleUse",
			want: &coordinatorv1.CreateInviteRequest{Address: testInviteAddress},
			kind: "single use, expires 2026-11-28T10:00:00Z",
		},
		{
			name: "Reusable",
			args: []string{"--reusable"},
			want: &coordinatorv1.CreateInviteRequest{Address: testInviteAddress, Reusable: true},
			kind: "reusable, no expiry",
		},
		{
			// A partial second still counts, so the invite lasts at least
			// as long as asked.
			name: "Expires",
			args: []string{"--reusable", "--expires", "90m500ms"},
			want: &coordinatorv1.CreateInviteRequest{Address: testInviteAddress, Reusable: true, ExpiresInSeconds: 5401},
			kind: "reusable, expires 2026-11-28T10:00:00Z",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport()}
			out, err := runWorkerInvite(t, startInviteCoordinators(t, fake), append([]string{"--address", testInviteAddress}, tc.args...)...)

			require.NoError(t, err)
			assert.Equal(t, "Invite ("+tc.kind+"):\n  "+testInvite+"\n"+
				"Run on the worker host:\n  dagu worker --worker.coordinators="+testInviteAddress+" --worker.invite="+testInvite+"\n", out)
			requests := fake.createRequests()
			require.Len(t, requests, 1)
			assert.Equal(t, tc.want.Address, requests[0].Address)
			assert.Equal(t, tc.want.Reusable, requests[0].Reusable)
			assert.Equal(t, tc.want.ExpiresInSeconds, requests[0].ExpiresInSeconds)
			assert.Empty(t, requests[0].CreatedBy)
		})
	}
}

// A coordinator that is not ready gets the change to make, as the Add
// worker dialog shows it, and no coordinator creates an invite.
func TestWorkerInviteNotReady(t *testing.T) {
	t.Parallel()

	const restart = "  Each change needs a restart of the coordinator.\n"
	for _, tc := range []struct {
		name      string
		readiness *coordinatorv1.GetJoinReadinessResponse
		err       error
		want      string
	}{
		{
			name:      "ConfigFile",
			readiness: loopbackReport(nil),
			want: "  It listens on 127.0.0.1, so only its own machine can connect. Add this to /etc/dagu/config.yaml:\n" +
				"    coordinator:\n      host: 0.0.0.0\n" + restart,
		},
		{
			name: "Env",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV, Name: "DAGU_COORDINATOR_HOST"}
			}),
			want: "connect. Set DAGU_COORDINATOR_HOST=0.0.0.0.\n" + restart,
		},
		{
			name: "Flag",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG, Name: "--coordinator.host"}
			}),
			want: "connect. Start it with --coordinator.host=0.0.0.0.\n" + restart,
		},
		{
			name: "Container",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.Environment = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER
			}),
			want: "connect. Recreate its container with -p 50055:50055 -e DAGU_COORDINATOR_HOST=0.0.0.0.\n" + restart,
		},
		{
			name: "Kubernetes",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.Environment = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
			}),
			want: "connect. Set coordinator.host to 0.0.0.0 through the chart's values.\n" + restart,
		},
		{
			name:      "OwnCertificates",
			readiness: noBuiltinTLSReport(coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES),
			want:      "  Invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file.\n" + restart,
		},
		{
			name:      "CAUnavailable",
			readiness: noBuiltinTLSReport(coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE),
			want:      "  Invites need the built-in CA: this coordinator could not create or read its CA; see its start warning.\n" + restart,
		},
		{
			name: "OlderCoordinator",
			err:  status.Error(codes.Unimplemented, "unknown method GetJoinReadiness"),
			want: " predates worker invites: upgrade it to create invites\n",
		},
		{
			name: "Unreachable",
			err:  status.Error(codes.Unavailable, "connection refused"),
			want: " did not report whether it accepts workers: connection refused\n",
		},
		{
			name: "NoPeerToken",
			err:  status.Error(codes.Unauthenticated, "the coordinator requires a peer token"),
			want: " did not report whether it accepts workers: the coordinator requires a peer token\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: tc.readiness, readinessErr: tc.err}
			out, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", testInviteAddress)

			require.EqualError(t, err, "no invite created: 1 of 1 coordinators cannot accept workers from other hosts")
			assert.Contains(t, out, tc.want)
			assert.Empty(t, fake.createRequests())
		})
	}
}

// One coordinator that is not ready keeps the others from creating an
// invite, since the worker may reach any of them.
func TestWorkerInviteOneOfTwoNotReady(t *testing.T) {
	t.Parallel()

	ready := &inviteCoordinator{readiness: readyReport()}
	loopback := &inviteCoordinator{readiness: loopbackReport(nil)}
	out, err := runWorkerInvite(t, startInviteCoordinators(t, ready, loopback), "--address", testInviteAddress)

	require.EqualError(t, err, "no invite created: 1 of 2 coordinators cannot accept workers from other hosts")
	assert.Contains(t, out, "It listens on 127.0.0.1")
	assert.Empty(t, ready.createRequests())
	assert.Empty(t, loopback.createRequests())
}

// A refused create reaches the user with the coordinator's message.
func TestWorkerInviteRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "FailedPrecondition", err: status.Error(codes.FailedPrecondition, "invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file")},
		{name: "InvalidArgument", err: status.Error(codes.InvalidArgument, "address must be host:port")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport(), createErr: tc.err}
			out, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", testInviteAddress)

			require.EqualError(t, err, "failed to create the invite: "+status.Convert(tc.err).Message())
			assert.Empty(t, out)
		})
	}
}

func TestWorkerInviteFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "NoAddress", wantErr: `required flag(s) "address" not set`},
		{name: "NoPort", args: []string{"--address", "192.168.1.20"}, wantErr: "--address must be host:port, such as 192.168.1.20:50055"},
		{name: "BadPort", args: []string{"--address", "192.168.1.20:70000"}, wantErr: "--address must be host:port, such as 192.168.1.20:50055"},
		{name: "BadExpiry", args: []string{"--address", testInviteAddress, "--expires", "1day"}, wantErr: "--expires must be a positive duration, such as 24h"},
		{name: "NegativeExpiry", args: []string{"--address", testInviteAddress, "--expires", "-1h"}, wantErr: "--expires must be a positive duration, such as 24h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport()}
			_, err := runWorkerInvite(t, startInviteCoordinators(t, fake), tc.args...)

			require.EqualError(t, err, tc.wantErr)
			assert.Empty(t, fake.createRequests())
		})
	}
}

// The invite reaches standard output only, not the log, even at the debug
// level. The test replaces os.Stderr, where the logger writes, so it does not
// run in parallel.
func TestWorkerInviteNotLogged(t *testing.T) {
	t.Setenv("DEBUG", "1")
	home := startInviteCoordinators(t, &inviteCoordinator{readiness: readyReport()})
	logFile, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	require.NoError(t, err)
	stderr := os.Stderr
	os.Stderr = logFile
	t.Cleanup(func() { os.Stderr = stderr })

	out, err := runWorkerInvite(t, home, "--address", testInviteAddress)
	require.NoError(t, err)
	require.NoError(t, logFile.Close())

	logged, err := os.ReadFile(logFile.Name())
	require.NoError(t, err)
	assert.Contains(t, out, testInvite)
	assert.NotContains(t, string(logged), testInvite)
}

// runWorkerInvite runs dagu worker invite with home as DAGU_HOME and returns
// its standard output.
func runWorkerInvite(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	command := cmd.CmdWorker()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs(append([]string{"invite", "--dagu-home", home, "--config", filepath.Join(home, "config.yaml")}, args...))
	err := command.ExecuteContext(t.Context())
	return out.String(), err
}

// startInviteCoordinators serves each coordinator on h2c, lists them in the
// service registry of a new DAGU_HOME, and returns that home.
func startInviteCoordinators(t *testing.T, coordinators ...*inviteCoordinator) string {
	t.Helper()
	home := checkHome(t)
	for _, c := range coordinators {
		server := grpc.NewServer()
		coordinatorv1.RegisterCoordinatorServiceServer(server, c)
		healthServer := health.NewServer()
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
		grpc_health_v1.RegisterHealthServer(server, healthServer)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		registerCoordinator(t, home, listener.Addr().String())
	}
	return home
}
