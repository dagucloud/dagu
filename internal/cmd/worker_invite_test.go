// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	testInvite        = "dagu1.pinpinpinpinpinpinpinpinpinpinpinpinpinpinp.secretsecretsecretsecretsecretsecretsecr"
	testInviteAddress = "192.168.1.20:50055"
	// testInviteID is the ID the fake coordinator gives its invite, a UUID
	// as coordinators assign.
	testInviteID = "7c2a9f31-5b0e-4d6a-9a1e-2f3b4c5d6e7f"
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
	deleteErr    error
	// onReadiness runs before join readiness answers, when set.
	onReadiness func()

	mu      sync.Mutex
	created []*coordinatorv1.CreateInviteRequest
	deleted []string
}

func (c *inviteCoordinator) GetJoinReadiness(context.Context, *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	if c.onReadiness != nil {
		c.onReadiness()
	}
	return c.readiness, c.readinessErr
}

func (c *inviteCoordinator) DeleteInvite(_ context.Context, req *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, req.Id)
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	return &coordinatorv1.DeleteInviteResponse{}, nil
}

// ListInvites lists the one invite the coordinator creates until it is
// deleted.
func (c *inviteCoordinator) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp := &coordinatorv1.ListInvitesResponse{}
	if len(c.created) > 0 && (len(c.deleted) == 0 || c.deleteErr != nil) {
		resp.Invites = append(resp.Invites, &coordinatorv1.InviteRecord{Id: testInviteID})
	}
	return resp, nil
}

func (c *inviteCoordinator) deletedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleted
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
		Record: &coordinatorv1.InviteRecord{Id: testInviteID, Kind: kind, ExpiresAt: expiresAt, Address: req.Address},
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
		name    string
		address string
		args    []string
		want    *coordinatorv1.CreateInviteRequest
		kind    string
		// command is the printed --worker.coordinators value.
		command string
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
		{
			// The longest duration rounds up to the coordinator's limit
			// without overflowing.
			name: "LongestExpiry",
			args: []string{"--reusable", "--expires", "2562047h47m16s"},
			want: &coordinatorv1.CreateInviteRequest{Address: testInviteAddress, Reusable: true, ExpiresInSeconds: math.MaxInt64 / int64(time.Second)},
			kind: "reusable, expires 2026-11-28T10:00:00Z",
		},
		{
			// A shell reads brackets as a pattern, so the address is quoted.
			name:    "IPv6",
			address: "[2001:db8::1]:50055",
			want:    &coordinatorv1.CreateInviteRequest{Address: "[2001:db8::1]:50055"},
			kind:    "single use, expires 2026-11-28T10:00:00Z",
			command: "'[2001:db8::1]:50055'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := cmp.Or(tc.address, testInviteAddress)
			fake := &inviteCoordinator{readiness: readyReport()}
			out, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), append([]string{"--address", address}, tc.args...)...)

			require.NoError(t, err)
			assert.Equal(t, "Invite ("+tc.kind+"):\n  "+testInvite+"\n"+
				"Run on the worker host:\n  dagu worker --worker.coordinators="+cmp.Or(tc.command, address)+" --worker.invite="+testInvite+"\n", out)
			requests := fake.createRequests()
			require.Len(t, requests, 1)
			assert.Equal(t, tc.want.Address, requests[0].Address)
			assert.Equal(t, tc.want.Reusable, requests[0].Reusable)
			assert.Equal(t, tc.want.ExpiresInSeconds, requests[0].ExpiresInSeconds)
			assert.Empty(t, requests[0].CreatedBy)
		})
	}
}

// The printed command runs in a POSIX shell as printed: the shell passes
// the address and the invite to dagu unchanged.
func TestWorkerInviteCommandInShell(t *testing.T) {
	t.Parallel()
	// Windows file names cannot hold the colon of the pattern's match.
	if _, err := exec.LookPath("sh"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell and file system")
	}

	const address = "[2001:db8::1]:50055"
	fake := &inviteCoordinator{readiness: readyReport()}
	out, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", address)
	require.NoError(t, err)
	_, command, ok := strings.Cut(out, "Run on the worker host:\n  ")
	require.True(t, ok, out)

	// printf stands in for dagu, so the shell's words are what dagu would
	// get. An unquoted address is a pattern that matches the file created
	// here, so the shell would pass the file's name instead.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "--worker.coordinators=2:50055"), nil, 0o600))
	shell := exec.Command("sh", "-c", `dagu() { printf '%s\n' "$@"; }; `+command)
	shell.Dir = dir
	words, err := shell.Output()
	require.NoError(t, err)
	assert.Equal(t, "worker\n--worker.coordinators="+address+"\n--worker.invite="+testInvite+"\n", string(words))
}

// A coordinator that is not ready gets the change to make on standard
// error, as the Add worker dialog shows it, and no coordinator creates an
// invite.
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
			// The file may hold a coordinator block already, such as one
			// that sets the port, so the change names no block to append.
			name:      "ConfigFile",
			readiness: loopbackReport(nil),
			want: "  It listens on 127.0.0.1, so only its own machine can connect. " +
				"In /etc/dagu/config.yaml, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.\n" + restart,
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
			want: "connect. Set DAGU_COORDINATOR_HOST=0.0.0.0 on the coordinator's workload, then restart it. " +
				"With the Helm chart, deploymentMode: distributed sets it.\n" + restart,
		},
		{
			// A flag wins over the container's -e, so the flag changes and
			// the port still needs publishing.
			name: "ContainerFlag",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.Environment = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER
				r.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG, Name: "--coordinator.host"}
			}),
			want: "connect. Start it with --coordinator.host=0.0.0.0. Recreate its container with -p 50055:50055.\n" + restart,
		},
		{
			// A variable the pod sets wins over the chart's default.
			name: "KubernetesEnv",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.Environment = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
				r.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV, Name: "DAGU_COORDINATOR_HOST"}
			}),
			want: "connect. Set DAGU_COORDINATOR_HOST=0.0.0.0.\n" + restart,
		},
		{
			// admin.yaml set the value over config.yaml, so it is the file
			// to change.
			name: "AdminFile",
			readiness: loopbackReport(func(r *coordinatorv1.GetJoinReadinessResponse) {
				r.HostSource = &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE, Name: "/etc/dagu/admin.yaml"}
			}),
			want: "connect. In /etc/dagu/admin.yaml, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.\n" + restart,
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
			want: " did not report whether it accepts workers: it predates worker invites: upgrade it to create invites\n",
		},
		{
			name: "Unreachable",
			err:  status.Error(codes.Unavailable, "connection refused"),
			want: " did not report whether it accepts workers: it could not be reached or did not answer: run dagu worker --check on this host to see why\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: tc.readiness, readinessErr: tc.err}
			out, errOut, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", testInviteAddress)

			require.EqualError(t, err, "no invite created: 1 of 1 coordinators cannot accept workers from other hosts")
			assert.Contains(t, errOut, tc.want)
			assert.Empty(t, out)
			assert.Empty(t, fake.createRequests())
		})
	}
}

// A coordinator that requires the peer token refuses a host without it,
// and the CLI passes on the coordinator's remedy.
func TestWorkerInviteNoPeerToken(t *testing.T) {
	t.Parallel()

	fake := &inviteCoordinator{readiness: readyReport()}
	auth := coordinator.NewPeerTokenAuth("coordinator-token", true, true)
	home := checkHome(t)
	registerCoordinator(t, home, serveInviteCoordinator(t, fake, grpc.ChainUnaryInterceptor(auth.UnaryInterceptor())))

	_, errOut, err := runWorkerInvite(t, home, "--address", testInviteAddress)

	require.Error(t, err)
	assert.Contains(t, errOut, " did not report whether it accepts workers: "+
		"the coordinator refused this host's peer token, or its lack of one: set the same peer.token on this host\n")
	assert.NotContains(t, errOut, "data directory")
	assert.Empty(t, fake.createRequests())
}

// A coordinator the registry lists while join readiness is read gets no
// create, even one that comes first in the order the client tries. The test
// replaces the command's registry, so it does not run in parallel.
func TestWorkerInviteRegistryChange(t *testing.T) {
	first := &inviteCoordinator{readiness: readyReport()}
	second := &inviteCoordinator{readiness: readyReport()}
	addrs := []string{serveInviteCoordinator(t, first), serveInviteCoordinator(t, second)}
	checked, late := first, second
	// The late coordinator comes first both in the order of addresses and
	// in the registry's list, so a client that reads the registry again
	// tries it first whichever order it uses.
	if addrs[1] > addrs[0] {
		addrs[0], addrs[1] = addrs[1], addrs[0]
		checked, late = second, first
	}
	late.readiness = loopbackReport(nil)
	registry := &changingRegistry{members: []serviceregistry.HostInfo{hostInfo(t, addrs[0])}}
	lateInfo := hostInfo(t, addrs[1])
	checked.onReadiness = func() { registry.add(lateInfo) }
	cmd.SetInviteRegistryForTest(t, registry)

	_, _, err := runWorkerInvite(t, checkHome(t), "--address", testInviteAddress)

	require.NoError(t, err)
	assert.Len(t, checked.createRequests(), 1)
	assert.Empty(t, late.createRequests())
}

// changingRegistry is a service registry whose coordinators a test adds to,
// each ahead of the ones it lists.
type changingRegistry struct {
	serviceregistry.ServiceRegistry
	mu      sync.Mutex
	members []serviceregistry.HostInfo
}

func (r *changingRegistry) add(member serviceregistry.HostInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members = append([]serviceregistry.HostInfo{member}, r.members...)
}

func (r *changingRegistry) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.members), nil
}

func hostInfo(t *testing.T, addr string) serviceregistry.HostInfo {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return serviceregistry.HostInfo{ID: "coordinator-" + portText, Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}
}

// A coordinator that refuses a call for TLS keeps its own remedy, and only
// the coordinator's peer token refusals get the peer.token one.
func TestWorkerInviteUnauthenticated(t *testing.T) {
	t.Parallel()

	const tokenRemedy = "set the same peer.token on this host"
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{
			// With peer.insecure: true, the client keeps this remedy in
			// whatever words and case it uses for the refusal.
			name:    "RequireTLS",
			message: "the coordinator requires TLS: upgrade this process, or remove peer.insecure: true",
			want:    "remove peer.insecure: true",
		},
		{
			name:    "TokenMissing",
			message: "the coordinator requires a peer token: set peer.token, or let this process read the coordinator's data directory",
			want:    "the coordinator refused this host's peer token, or its lack of one: " + tokenRemedy,
		},
		{
			// The wording of the next release.
			name:    "TokenMissingShort",
			message: "the coordinator requires a peer token: set peer.token",
			want:    "the coordinator refused this host's peer token, or its lack of one: " + tokenRemedy,
		},
		{
			name:    "TokenMismatch",
			message: "the peer token does not match the coordinator's peer token",
			want:    "the coordinator refused this host's peer token, or its lack of one: " + tokenRemedy,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readinessErr: status.Error(codes.Unauthenticated, tc.message)}
			home := configHome(t, "peer:\n  insecure: true\n")
			registerCoordinator(t, home, serveInviteCoordinator(t, fake))
			_, errOut, err := runWorkerInvite(t, home, "--address", testInviteAddress)

			require.Error(t, err)
			assert.Contains(t, errOut, " did not report whether it accepts workers: ")
			assert.Contains(t, strings.ToLower(errOut), strings.ToLower(tc.want))
			if tc.name == "RequireTLS" {
				assert.NotContains(t, errOut, tokenRemedy)
			}
		})
	}
}

// addressVectorsPath is the file of Add worker inputs and results that the
// dialog and the MCP action check too, from this package's directory.
var addressVectorsPath = filepath.Join("..", "..", "ui", "src", "features", "workers", "add-worker", "__fixtures__", "commands.json")

// The command takes the addresses the Add worker dialog and the MCP action
// take, and refuses the others before any call.
func TestWorkerInviteAddressVectors(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(addressVectorsPath)
	require.NoError(t, err)
	var vectors struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				Address string `json:"address"`
			} `json:"input"`
			Result struct {
				Accepted bool   `json:"accepted"`
				Field    string `json:"field"`
			} `json:"result"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &vectors))

	seen := map[string]bool{}
	for _, tc := range vectors.Cases {
		// Cases refused for another field say nothing about the address.
		if (!tc.Result.Accepted && tc.Result.Field != "address") || seen[tc.Input.Address] {
			continue
		}
		seen[tc.Input.Address] = true
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport()}
			_, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", tc.Input.Address)

			if tc.Result.Accepted {
				require.NoError(t, err)
				assert.Len(t, fake.createRequests(), 1)
				return
			}
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), "--address "), err.Error())
			assert.Empty(t, fake.createRequests())
		})
	}
}

// An invite whose output fails is deleted, since no one may have its
// secret, and the command fails naming it by its ID only. A delete the
// coordinator fails points to its log.
func TestWorkerInviteOutputFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		deleteErr error
		want      string
	}{
		{name: "Deleted", want: "failed to print invite " + testInviteID + ", so it was deleted: run the command again"},
		{
			name:      "DeleteFailed",
			deleteErr: status.Error(codes.Internal, "failed to delete the invite"),
			want: "failed to print invite " + testInviteID + ", and failed to delete it: " +
				"the coordinator failed to delete the invite: see its log for the cause; delete it before it is used",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport(), deleteErr: tc.deleteErr}
			home := startInviteCoordinators(t, fake)
			command := daguRoot()
			command.SetOut(failingWriter{})
			command.SetErr(io.Discard)
			command.SetArgs([]string{"worker", "invite", "--dagu-home", home, "--config", filepath.Join(home, "config.yaml"), "--address", testInviteAddress})

			err := command.ExecuteContext(t.Context())

			require.EqualError(t, err, tc.want)
			assert.Equal(t, []string{testInviteID}, fake.deletedIDs())
		})
	}
}

// Surrounding spaces leave the address, as the Add worker dialog drops
// them, so the printed command and the invite's address have none.
func TestWorkerInviteTrimsAddress(t *testing.T) {
	t.Parallel()

	fake := &inviteCoordinator{readiness: readyReport()}
	out, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", "\t"+testInviteAddress+" \n")

	require.NoError(t, err)
	assert.Contains(t, out, "--worker.coordinators="+testInviteAddress+" ")
	requests := fake.createRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, testInviteAddress, requests[0].Address)
}

// A host that does not share the coordinator's data directory finds it
// through worker.coordinators, as a worker there does.
func TestWorkerInviteConfiguredCoordinators(t *testing.T) {
	t.Parallel()

	fake := &inviteCoordinator{readiness: readyReport()}
	addr := serveInviteCoordinator(t, fake)
	home := configHome(t, "worker:\n  coordinators: "+addr+"\n")

	out, _, err := runWorkerInvite(t, home, "--address", testInviteAddress)

	require.NoError(t, err)
	assert.Contains(t, out, testInvite)
	assert.Len(t, fake.createRequests(), 1)
}

// A worker.coordinators that holds no usable address fails, and the
// coordinator in the service registry gets no create in its place.
func TestWorkerInviteInvalidCoordinators(t *testing.T) {
	t.Parallel()

	local := &inviteCoordinator{readiness: readyReport()}
	home := configHome(t, "worker:\n  coordinators: mistyped.example:not-a-port\n")
	registerCoordinator(t, home, serveInviteCoordinator(t, local))

	_, _, err := runWorkerInvite(t, home, "--address", testInviteAddress)

	require.EqualError(t, err, "worker.coordinators holds no valid host:port: fix it, or unset it to use the coordinators in the service registry")
	assert.Empty(t, local.createRequests())
}

// A failure raised on this host is reported without the error itself,
// which can name a credential file.
func TestWorkerInviteHidesLocalErrors(t *testing.T) {
	t.Parallel()

	const caFile = "/no/such/dir/peer-ca-secret.pem"
	addr := serveInviteCoordinator(t, &inviteCoordinator{readiness: readyReport()})
	home := configHome(t, "peer:\n  insecure: false\n  client_ca_file: "+caFile+"\nworker:\n  coordinators: "+addr+"\n")

	out, errOut, err := runWorkerInvite(t, home, "--address", testInviteAddress)

	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "the call failed on this host: run dagu worker --check here to see why")
	assert.NotContains(t, errOut+err.Error(), "peer-ca-secret")
}

// One coordinator that is not ready keeps the others from creating an
// invite, since the worker may reach any of them.
func TestWorkerInviteOneOfTwoNotReady(t *testing.T) {
	t.Parallel()

	ready := &inviteCoordinator{readiness: readyReport()}
	loopback := &inviteCoordinator{readiness: loopbackReport(nil)}
	_, errOut, err := runWorkerInvite(t, startInviteCoordinators(t, ready, loopback), "--address", testInviteAddress)

	require.EqualError(t, err, "no invite created: 1 of 2 coordinators cannot accept workers from other hosts")
	assert.Contains(t, errOut, "It listens on 127.0.0.1")
	assert.Empty(t, ready.createRequests())
	assert.Empty(t, loopback.createRequests())
}

// A refused create reaches the user with the coordinator's message. A
// coordinator that fails to store the invite points to its log. Any other
// failure, whose origin the CLI cannot tell, gets only its kind and the
// check to run.
func TestWorkerInviteRefused(t *testing.T) {
	t.Parallel()

	const ownCertificates = "invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "FailedPrecondition", err: status.Error(codes.FailedPrecondition, ownCertificates), want: ownCertificates},
		{name: "InvalidArgument", err: status.Error(codes.InvalidArgument, "address must be host:port"), want: "address must be host:port"},
		{
			name: "Unavailable",
			err:  status.Error(codes.Unavailable, "connection reset"),
			want: "the coordinator could not be reached or did not answer: run dagu worker --check on this host to see why",
		},
		{
			name: "WriteFailed",
			err:  status.Error(codes.Internal, "failed to store the invite"),
			want: "the coordinator failed to store the invite: see its log for the cause",
		},
		{
			// gRPC raises Internal on the calling host too, with its own
			// message.
			name: "UnknownInternal",
			err:  status.Error(codes.Internal, "grpc: error while marshaling"),
			want: "the call failed with code Internal: run dagu worker --check on this host to see why",
		},
		{
			name: "OtherCode",
			err:  status.Error(codes.DataLoss, "details"),
			want: "the call failed with code DataLoss: run dagu worker --check on this host to see why",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport(), createErr: tc.err}
			out, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), "--address", testInviteAddress)

			require.EqualError(t, err, "failed to create the invite: "+tc.want)
			assert.Empty(t, out)
		})
	}
}

func TestWorkerInviteFlags(t *testing.T) {
	t.Parallel()

	const (
		badAddress = "--address must be host:port, such as 192.168.1.20:50055"
		badExpiry  = "--expires must be a positive duration, such as 24h"
		oneAddress = "--address must be one host:port, without commas"
	)
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "NoAddress", wantErr: `required flag(s) "address" not set`},
		{name: "NoPort", args: []string{"--address", "192.168.1.20"}, wantErr: badAddress},
		{name: "BadPort", args: []string{"--address", "192.168.1.20:70000"}, wantErr: "--address must have a port from 1 to 65535"},
		{name: "MalformedIPv6", args: []string{"--address", "[not:ipv6]:50055"}, wantErr: "--address must name one host: an IP address without a zone, or a DNS name"},
		{name: "UnbracketedIPv6", args: []string{"--address", "2001:db8::1:50055"}, wantErr: badAddress},
		// The worker would split the printed value on the comma.
		{name: "Comma", args: []string{"--address", "missing,coordinator:50055"}, wantErr: oneAddress},
		// The request could not carry it, so it fails before any call.
		{name: "InvalidUTF8", args: []string{"--address", "host\xff:50055"}, wantErr: "--address must be valid UTF-8 text"},
		{name: "BadExpiry", args: []string{"--address", testInviteAddress, "--expires", "1day"}, wantErr: badExpiry},
		{name: "NegativeExpiry", args: []string{"--address", testInviteAddress, "--expires", "-1h"}, wantErr: badExpiry},
		// An empty value would otherwise give a reusable invite no expiry.
		{name: "EmptyExpiry", args: []string{"--address", testInviteAddress, "--reusable", "--expires="}, wantErr: badExpiry},
		{name: "TooLongExpiry", args: []string{"--address", testInviteAddress, "--expires", "2562047h47m16.5s"}, wantErr: "--expires must be at most 2562047h47m16s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &inviteCoordinator{readiness: readyReport()}
			_, _, err := runWorkerInvite(t, startInviteCoordinators(t, fake), tc.args...)

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
	logged := captureLog(t)

	out, errOut, err := runWorkerInvite(t, home, "--address", testInviteAddress)
	require.NoError(t, err)

	assert.Contains(t, out, testInvite)
	assert.NotContains(t, logged()+errOut, testInvite)
}

// On a host where Dagu never ran, the command sets up nothing a server
// needs: it writes no example DAGs and logs no server warning. It replaces
// os.Stderr like TestWorkerInviteNotLogged, so it does not run in parallel.
func TestWorkerInviteFreshHome(t *testing.T) {
	addr := serveInviteCoordinator(t, &inviteCoordinator{readiness: readyReport()})
	home := t.TempDir()
	t.Setenv("DAGU_WORKER_COORDINATORS", addr)
	logged := captureLog(t)

	command := daguRoot()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"worker", "invite", "--dagu-home", home, "--address", testInviteAddress})
	require.NoError(t, command.ExecuteContext(t.Context()))

	assert.Empty(t, logged())
	assert.NoDirExists(t, filepath.Join(home, "dags"))
}

// The client starts on built-in TLS from the published CA certificate and
// reaches the coordinator over h2c, a change it logs at the info level for
// other commands. The invite command logs it at no level. It replaces
// os.Stderr like TestWorkerInviteNotLogged, so it does not run in parallel.
func TestWorkerInviteNoTransportChangeLine(t *testing.T) {
	home := startInviteCoordinators(t, &inviteCoordinator{readiness: readyReport()})
	paths, err := coordinator.WritePeerInit(t.TempDir())
	require.NoError(t, err)
	caPEM, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	published := filepath.Join(home, "data", "coordinator", "peer-ca.crt")
	require.NoError(t, os.MkdirAll(filepath.Dir(published), 0o700))
	require.NoError(t, os.WriteFile(published, caPEM, 0o600))
	logged := captureLog(t)

	_, _, err = runWorkerInvite(t, home, "--address", testInviteAddress)

	require.NoError(t, err)
	assert.NotContains(t, logged(), "Peer transport")
}

// captureLog sends what the process writes to os.Stderr, where the logger
// writes, to a file until the test ends, and returns a function that reads
// it.
func captureLog(t *testing.T) func() string {
	t.Helper()
	logFile, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	require.NoError(t, err)
	stderr := os.Stderr
	os.Stderr = logFile
	t.Cleanup(func() {
		os.Stderr = stderr
		_ = logFile.Close()
	})
	return func() string {
		logged, err := os.ReadFile(logFile.Name())
		require.NoError(t, err)
		return string(logged)
	}
}

// runWorkerInvite runs dagu worker invite with home as DAGU_HOME and returns
// its standard output and standard error.
func runWorkerInvite(t *testing.T, home string, args ...string) (string, string, error) {
	t.Helper()
	command := daguRoot()
	var out, errOut bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&errOut)
	command.SetArgs(append([]string{"worker", "invite", "--dagu-home", home, "--config", filepath.Join(home, "config.yaml")}, args...))
	err := command.ExecuteContext(t.Context())
	return out.String(), errOut.String(), err
}

// daguRoot returns the dagu command with dagu worker below it, so the
// invite command has the path it has in the binary.
func daguRoot() *cobra.Command {
	root := &cobra.Command{Use: "dagu"}
	root.AddCommand(cmd.CmdWorker())
	return root
}

// configHome returns a DAGU_HOME whose config file holds config.
func configHome(t *testing.T, config string) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600))
	return home
}

// startInviteCoordinators serves each coordinator on h2c, lists them in the
// service registry of a new DAGU_HOME, and returns that home.
func startInviteCoordinators(t *testing.T, coordinators ...*inviteCoordinator) string {
	t.Helper()
	home := checkHome(t)
	for _, c := range coordinators {
		registerCoordinator(t, home, serveInviteCoordinator(t, c))
	}
	return home
}

// serveInviteCoordinator serves c on h2c with opts until the test ends and
// returns its address.
func serveInviteCoordinator(t *testing.T, c *inviteCoordinator, opts ...grpc.ServerOption) string {
	t.Helper()
	server := grpc.NewServer(opts...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, c)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}
