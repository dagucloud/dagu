// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// testWorkerInvite is the invite the stub coordinator creates.
const testWorkerInvite = "dagu1.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

// workerAccessStub answers the join readiness and invite calls of the
// coordinators, and records the invite it was asked to create.
type workerAccessStub struct {
	coordinator.Client
	readiness []coordinator.JoinReadiness
	invites   []*coordinatorv1.InviteRecord
	created   *coordinatorv1.CreateInviteRequest
}

func (s *workerAccessStub) GetJoinReadiness(context.Context) ([]coordinator.JoinReadiness, error) {
	return s.readiness, nil
}

// testInviteExpiry is when a single-use invite of the stub expires.
const testInviteExpiry = "2024-03-10T16:00:00Z"

// CreateInvite answers as a coordinator does: a single-use invite expires
// by default, and a reusable one does not.
func (s *workerAccessStub) CreateInvite(_ context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	s.created = req
	record := &coordinatorv1.InviteRecord{Id: "invite-1", Kind: coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE, ExpiresAt: 1710086400, Address: req.Address}
	if req.Reusable {
		record.Kind, record.ExpiresAt = coordinatorv1.InviteKind_INVITE_KIND_REUSABLE, 0
	}
	return &coordinatorv1.CreateInviteResponse{Invite: testWorkerInvite, Record: record}, nil
}

func (s *workerAccessStub) ListInvites(context.Context) ([]*coordinatorv1.InviteRecord, error) {
	return s.invites, nil
}

func (s *workerAccessStub) DeleteInvite(context.Context, string) error { return nil }

// memoryAuditStore keeps the audit entries in memory.
type memoryAuditStore struct{ entries []*audit.Entry }

func (s *memoryAuditStore) Append(_ context.Context, entry *audit.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func (s *memoryAuditStore) Query(context.Context, audit.QueryFilter) (*audit.QueryResult, error) {
	return &audit.QueryResult{Entries: s.entries, Total: len(s.entries)}, nil
}

// newAddWorkerService serves the API with client as its coordinator client.
func newAddWorkerService(client coordinator.Client) (*Service, *memoryAuditStore) {
	store := &memoryAuditStore{}
	api := frontendapi.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, client, nil, prometheus.NewRegistry(), nil,
		frontendapi.WithAuditService(audit.New(store)))
	return &Service{api: api}, store
}

// cloudUser is a caller Dagu Cloud vouches for, so roles apply to it.
func cloudUser(role auth.Role) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "cloud:user-1", Username: "pat", Role: role, AuthProvider: auth.AuthProviderCloud})
}

func readyReport() *coordinatorv1.GetJoinReadinessResponse {
	return &coordinatorv1.GetJoinReadinessResponse{
		ListenHost:  "0.0.0.0",
		Port:        50055,
		Addresses:   []string{"192.168.1.20"},
		Environment: coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST,
		BuiltinTls:  true,
		Ready:       true,
	}
}

// addWorker calls dagu_execute action=add_worker with args, and returns
// the result and its JSON.
func addWorker(t *testing.T, ctx context.Context, svc *Service, args map[string]any) (*mcpsdk.CallToolResult, string) {
	t.Helper()
	args["action"] = executeActionAddWorker
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := svc.executeTool(ctx, &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Name: toolExecute, Arguments: raw}})
	require.NoError(t, err)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	return result, string(data)
}

// A coordinator on loopback, or one of an earlier release beside a ready
// one, gets what to change, and no invite is created even with an address.
func TestAddWorkerNotReady(t *testing.T) {
	t.Parallel()

	loopback := &coordinatorv1.GetJoinReadinessResponse{
		ListenHost:   "127.0.0.1",
		Port:         50055,
		ConfigFile:   "/etc/dagu/config.yaml",
		LoopbackOnly: true,
		HostSource:   &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT},
		BuiltinTls:   true,
	}
	tests := map[string]struct {
		readiness []coordinator.JoinReadiness
		key, want string
	}{
		"loopback": {
			readiness: []coordinator.JoinReadiness{{Report: loopback}},
			key:       "changes",
			want:      "It listens on 127.0.0.1, so only its own machine can connect. Add this to /etc/dagu/config.yaml:\n  coordinator:\n    host: 0.0.0.0",
		},
		"older coordinator": {
			readiness: []coordinator.JoinReadiness{{Report: readyReport()}, {Err: status.Error(codes.Unimplemented, "unknown method")}},
			key:       "error",
			want:      "the coordinator runs an earlier Dagu release without worker invites: upgrade it",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stub := &workerAccessStub{readiness: tc.readiness}
			svc, _ := newAddWorkerService(stub)

			result, data := addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{"address": "192.168.1.20:50055"})

			require.False(t, result.IsError, data)
			output := structuredMap(t, result)
			require.Equal(t, false, output["ready"])
			steps := output["coordinators"].([]map[string]any)
			last := steps[len(steps)-1]
			require.Equal(t, false, last["ready"])
			if tc.key == "changes" {
				require.Equal(t, []string{tc.want}, last["changes"])
			} else {
				require.Equal(t, tc.want, last["error"])
			}
			require.Nil(t, stub.created)
			require.NotContains(t, data, testWorkerInvite)
		})
	}
}

// A readiness-only call holds no invite. The call with an address creates
// one, and only its result holds it; no audit entry does. The test binary
// is a development build, so the result has no container command and says
// why.
func TestAddWorkerCreatesInvite(t *testing.T) {
	t.Parallel()

	stub := &workerAccessStub{
		readiness: []coordinator.JoinReadiness{{Report: readyReport()}},
		invites:   []*coordinatorv1.InviteRecord{{Id: "invite-0", Address: "10.0.0.5:50055"}},
	}
	svc, store := newAddWorkerService(stub)
	ctx := cloudUser(auth.RoleAdmin)

	result, data := addWorker(t, ctx, svc, map[string]any{})
	require.False(t, result.IsError, data)
	output := structuredMap(t, result)
	require.Equal(t, true, output["ready"])
	require.Equal(t, map[string]any{"initial": "10.0.0.5:50055", "suggestions": []string{"192.168.1.20:50055"}}, output["addressOptions"])
	require.NotContains(t, output, "invite")
	require.Nil(t, stub.created)

	result, data = addWorker(t, ctx, svc, map[string]any{"address": "192.168.1.20:50055", "labels": []string{"gpu=true"}})
	require.False(t, result.IsError, data)
	output = structuredMap(t, result)
	require.Equal(t, "invite-1", output["inviteId"])
	require.Equal(t, testWorkerInvite, output["invite"])
	require.Equal(t, daguapi.WorkerInviteKindSingleUse, output["kind"])
	require.Equal(t, testInviteExpiry, output["expiresAt"])
	require.Equal(t, map[string]string{
		"linux": "dagu worker --worker.coordinators=192.168.1.20:50055 --worker.labels=gpu=true --worker.invite=" + testWorkerInvite,
	}, output["commands"])
	require.Contains(t, output["imageUnavailable"], "no published image")
	require.False(t, stub.created.GetReusable())
	require.Equal(t, "192.168.1.20:50055", stub.created.GetAddress())
	require.Equal(t, "pat", stub.created.GetCreatedBy())

	actions := map[string]string{}
	for _, entry := range store.entries {
		require.NotContains(t, entry.Details, testWorkerInvite, entry.Action)
		actions[entry.Action] = entry.Details
	}
	require.Contains(t, actions["worker_invite_create"], `"invite_id":"invite-1"`)
	require.Contains(t, actions["mcp.tool_call.succeeded"], `"invite_id":"invite-1"`)
}

// A reusable invite has no expiry by default. The command quotes the IPv6
// address, whose brackets a shell would expand.
func TestAddWorkerReusableInvite(t *testing.T) {
	t.Parallel()

	stub := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
	svc, _ := newAddWorkerService(stub)

	result, data := addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{
		"address":  "[2001:db8::1]:50055",
		"reusable": true,
		"labels":   []string{"team=data-eng", "pool=gpu/a100"},
	})

	require.False(t, result.IsError, data)
	output := structuredMap(t, result)
	require.Equal(t, daguapi.WorkerInviteKindReusable, output["kind"])
	require.NotContains(t, output, "expiresAt")
	require.Equal(t, `dagu worker '--worker.coordinators=[2001:db8::1]:50055' --worker.labels=team=data-eng,pool=gpu/a100 --worker.invite=`+testWorkerInvite,
		output["commands"].(map[string]string)["linux"])
	require.True(t, stub.created.GetReusable())
}

func TestAddWorkerRefused(t *testing.T) {
	t.Parallel()

	older := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Err: status.Error(codes.Unimplemented, "unknown method")}}}
	tests := map[string]struct {
		client coordinator.Client
		role   auth.Role
		code   string
	}{
		"non-admin":         {client: &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}, role: auth.RoleDeveloper, code: executeErrorUnauthorized},
		"no coordinator":    {role: auth.RoleAdmin, code: executeErrorResourceUnavailable},
		"older coordinator": {client: older, role: auth.RoleAdmin, code: executeErrorConflict},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, store := newAddWorkerService(tc.client)

			// An invite pasted into expiresIn reaches no audit entry.
			result, data := addWorker(t, cloudUser(tc.role), svc, map[string]any{"address": "192.168.1.20:50055", "expiresIn": testWorkerInvite})

			require.True(t, result.IsError, data)
			require.Equal(t, tc.code, structuredMap(t, result)["code"])
			if stub, ok := tc.client.(*workerAccessStub); ok {
				require.Nil(t, stub.created)
			}
			require.NotEmpty(t, store.entries)
			for _, entry := range store.entries {
				require.Equal(t, "cloud:user-1", entry.UserID, entry.Action)
				require.NotContains(t, entry.Details, testWorkerInvite, entry.Action)
			}
		})
	}
}

// An address a worker on another host cannot dial is refused before any
// coordinator is asked.
func TestAddWorkerRefusesAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"0.0.0.0:50055", "[::]:50055", "127.0.0.1:50055", "[::1]:50055", "localhost:50055",
		"192.168.1.20", "192.168.1.20:0", "3f4e5a6b7c8d:50055",
	} {
		stub := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
		svc, _ := newAddWorkerService(stub)

		result, data := addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{"address": address})

		require.True(t, result.IsError, address)
		output := structuredMap(t, result)
		require.Equal(t, executeErrorInvalidToolInput, output["code"], data)
		require.Equal(t, executeFieldAddress, output["field"], data)
		require.Nil(t, stub.created, address)
	}
}

func TestAddWorkerInputValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		field string
	}{
		"name on add_worker":   {input: `{"action":"add_worker","name":"etl"}`, field: executeFieldName},
		"address on start":     {input: `{"action":"start","name":"etl","address":"192.168.1.20:50055"}`, field: executeFieldAddress},
		"reusable on backfill": {input: `{"action":"backfill","name":"etl","reusable":true}`, field: executeFieldReusable},
		"label without value":  {input: `{"action":"add_worker","labels":["gpu"]}`, field: executeFieldLabels},
		"comma in a label":     {input: `{"action":"add_worker","labels":["zone=a,b"]}`, field: executeFieldLabels},
		"space in a label":     {input: `{"action":"add_worker","labels":["team=data eng"]}`, field: executeFieldLabels},
		"quote in a label":     {input: `{"action":"add_worker","labels":["note=it's"]}`, field: executeFieldLabels},
		"dollar in a label":    {input: `{"action":"add_worker","labels":["leak=$(DAGU_WORKER_INVITE)"]}`, field: executeFieldLabels},
		"bad label key":        {input: `{"action":"add_worker","labels":["gpu/type=a100"]}`, field: executeFieldLabels},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := parseExecuteToolInput(json.RawMessage(tc.input))
			require.NotNil(t, err)
			require.Equal(t, executeErrorInvalidToolInput, err.Code)
			require.Equal(t, tc.field, err.Field)
		})
	}
}

// A release or prerelease build gets its own image tag. A build that was
// never published gets none.
func TestWorkerImage(t *testing.T) {
	t.Parallel()

	for version, want := range map[string]string{
		"2.28.0":          "ghcr.io/dagucloud/dagu:2.28.0",
		"v2.28.0":         "ghcr.io/dagucloud/dagu:2.28.0",
		"v2.28.0-rc.1":    "ghcr.io/dagucloud/dagu:2.28.0-rc.1",
		"dev":             "",
		"0.0.0":           "",
		"v2.27.0-12-gabc": "",
		"2.27.1-next":     "",
	} {
		image, ok := workerImage(version)
		require.Equal(t, want, image, version)
		require.Equal(t, want != "", ok, version)
	}
}

// The container commands keep the address as given. The manifest takes
// the invite from the Secret only, and a $ in the address, which a host
// name may hold, does not expand a variable such as the invite's.
func TestWorkerContainerCommands(t *testing.T) {
	t.Parallel()

	commands := workerCommands("[2001:db8::1]:50055", []string{"team=data-eng"}, testWorkerInvite, "ghcr.io/dagucloud/dagu:2.28.0", true)
	require.Equal(t, "docker run -d --restart unless-stopped -v dagu-worker:/var/lib/dagu -e DAGU_WORKER_INVITE="+testWorkerInvite+
		` ghcr.io/dagucloud/dagu:2.28.0 dagu worker '--worker.coordinators=[2001:db8::1]:50055' --worker.labels=team=data-eng`,
		commands["docker"])

	commands = workerCommands("$(DAGU_WORKER_INVITE):50055", nil, testWorkerInvite, "ghcr.io/dagucloud/dagu:2.28.0", true)
	manifest := commands["kubernetes"]
	require.Contains(t, manifest, `args: ["dagu", "worker", "--worker.coordinators=$$(DAGU_WORKER_INVITE):50055"]`)
	require.Contains(t, manifest, "image: ghcr.io/dagucloud/dagu:2.28.0")
	require.Equal(t, 1, strings.Count(manifest, testWorkerInvite), "only the Secret holds the invite")
}
