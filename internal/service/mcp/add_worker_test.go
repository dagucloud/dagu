// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
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
	// readinessCalls counts the join readiness calls.
	readinessCalls int
}

func (s *workerAccessStub) GetJoinReadiness(context.Context) ([]coordinator.JoinReadiness, error) {
	s.readinessCalls++
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

// inputError returns the refusal of raw as dagu_execute input, by the parser
// or, for add_worker, by its input check, or nil.
func inputError(raw string) *executeToolError {
	input, err := parseExecuteToolInput(json.RawMessage(raw))
	if err != nil || input.Action != executeActionAddWorker {
		return err
	}
	return checkAddWorkerInput(input)
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
		"CA unavailable": {
			readiness: []coordinator.JoinReadiness{{Report: &coordinatorv1.GetJoinReadinessResponse{
				ListenHost:       "0.0.0.0",
				Port:             50055,
				BuiltinTlsReason: coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE,
			}}},
			key:  "changes",
			want: "Built-in TLS is unavailable. Check the coordinator startup log.",
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
				require.Equal(t, daguapi.WorkerAccessReasonOlderCoordinator, last["errorReason"])
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

	ready := func() coordinator.Client {
		return &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
	}
	refusing := func(err error) coordinator.Client {
		return &workerAccessStub{readiness: []coordinator.JoinReadiness{{Err: err}}}
	}
	tests := map[string]struct {
		client  coordinator.Client
		role    auth.Role
		code    string
		reason  string
		message string
	}{
		"non-admin":      {client: ready(), role: auth.RoleDeveloper, code: executeErrorUnauthorized},
		"no coordinator": {role: auth.RoleAdmin, code: executeErrorResourceUnavailable, reason: "no_coordinator"},
		"older coordinator": {
			client: refusing(status.Error(codes.Unimplemented, "unknown method")), role: auth.RoleAdmin,
			code: executeErrorConflict, reason: "older_coordinator",
		},
		"coordinator refuses the server": {
			client: refusing(status.Error(codes.Unauthenticated, "no peer token")), role: auth.RoleAdmin,
			code: executeErrorResourceUnavailable, reason: "coordinator_refused", message: "the coordinator refused the server: no peer token",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, store := newAddWorkerService(tc.client)

			result, data := addWorker(t, cloudUser(tc.role), svc, map[string]any{"address": "192.168.1.20:50055", "expiresIn": "24h"})

			require.True(t, result.IsError, data)
			output := structuredMap(t, result)
			require.Equal(t, tc.code, output["code"], data)
			if tc.reason == "" {
				require.NotContains(t, output, "reason")
			} else {
				require.Equal(t, tc.reason, output["reason"], data)
			}
			if tc.message != "" {
				require.Equal(t, tc.message, output["message"], data)
			}
			if stub, ok := tc.client.(*workerAccessStub); ok {
				require.Nil(t, stub.created)
			}
			require.NotEmpty(t, store.entries)
			for _, entry := range store.entries {
				require.Equal(t, "cloud:user-1", entry.UserID, entry.Action)
			}
		})
	}
}

// A non-admin is refused before its input is checked, and an admin's
// refused input is in the audit log.
func TestAddWorkerChecksAccessFirst(t *testing.T) {
	t.Parallel()

	stub := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
	svc, store := newAddWorkerService(stub)

	result, data := addWorker(t, cloudUser(auth.RoleDeveloper), svc, map[string]any{"address": "0.0.0.0:50055"})
	require.True(t, result.IsError, data)
	require.Equal(t, executeErrorUnauthorized, structuredMap(t, result)["code"], data)

	store.entries = nil
	result, data = addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{"address": "0.0.0.0:50055"})
	require.True(t, result.IsError, data)
	require.Equal(t, executeErrorInvalidToolInput, structuredMap(t, result)["code"], data)
	actions := make([]string, 0, len(store.entries))
	for _, entry := range store.entries {
		actions = append(actions, entry.Action)
	}
	require.Contains(t, actions, "mcp.tool_call.failed")
	require.Zero(t, stub.readinessCalls)
}

// An invite pasted into any field is refused, and no audit entry or log
// line holds it.
func TestAddWorkerKeepsPastedInvitesOut(t *testing.T) {
	t.Parallel()

	for field, value := range map[string]any{
		executeFieldAddress:   testWorkerInvite + ":50055",
		executeFieldLabels:    []string{"note=" + testWorkerInvite},
		executeFieldExpiresIn: testWorkerInvite,
		executeFieldName:      testWorkerInvite,
	} {
		stub := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
		svc, store := newAddWorkerService(stub)
		var logs bytes.Buffer
		ctx := logger.WithLogger(cloudUser(auth.RoleAdmin), logger.NewLogger(logger.WithDebug(), logger.WithWriter(&logs)))

		args := map[string]any{executeFieldAddress: "192.168.1.20:50055", field: value}
		result, data := addWorker(t, ctx, svc, args)

		require.True(t, result.IsError, field)
		output := structuredMap(t, result)
		require.Equal(t, executeErrorInvalidToolInput, output["code"], data)
		require.NotContains(t, data, testWorkerInvite, field)
		require.Nil(t, stub.created, field)
		for _, entry := range store.entries {
			require.NotContains(t, entry.Details, testWorkerInvite, field)
		}
		require.NotContains(t, logs.String(), testWorkerInvite, field)
	}
}

// The address to start from and the suggestions pass the address rule, so
// the next call does not refuse them.
func TestAddWorkerAddressOptions(t *testing.T) {
	t.Parallel()

	report := readyReport()
	report.Advertise = "10.0.0.5"
	report.Addresses = []string{"192.168.1.20", "169.254.10.20"}
	stub := &workerAccessStub{
		readiness: []coordinator.JoinReadiness{{Report: report}},
		// An invite made through the REST API for a tunnel.
		invites: []*coordinatorv1.InviteRecord{{Id: "invite-0", Address: "127.0.0.1:50055"}},
	}
	svc, _ := newAddWorkerService(stub)

	result, data := addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{})

	require.False(t, result.IsError, data)
	require.Equal(t, map[string]any{"initial": "10.0.0.5:50055", "suggestions": []string{"192.168.1.20:50055"}}, structuredMap(t, result)["addressOptions"])
}

// An address a worker on another host cannot dial is refused before any
// coordinator is asked.
func TestAddWorkerRefusesAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"0.0.0.0:50055", "[::]:50055", "127.0.0.1:50055", "[::1]:50055", "localhost:50055",
		"192.168.1.20", "192.168.1.20:0", "3f4e5a6b7c8d:50055",
		"[::1%lo0]:50055", "[fe80::1%eth0]:50055", "[fe80::1]:50055", "169.254.10.20:50055",
		"host1,host2:50055", "$(DAGU_WORKER_INVITE):50055",
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
		"name on add_worker":    {input: `{"action":"add_worker","name":"etl"}`, field: executeFieldName},
		"address on start":      {input: `{"action":"start","name":"etl","address":"192.168.1.20:50055"}`, field: executeFieldAddress},
		"reusable on backfill":  {input: `{"action":"backfill","name":"etl","reusable":true}`, field: executeFieldReusable},
		"empty label":           {input: `{"action":"add_worker","labels":[" "]}`, field: executeFieldLabels},
		"same key twice":        {input: `{"action":"add_worker","labels":["gpu=a","gpu=b"]}`, field: executeFieldLabels},
		"same key in two cases": {input: `{"action":"add_worker","labels":["GPU=true","gpu=false"]}`, field: executeFieldLabels},
		"built-in key":          {input: `{"action":"add_worker","labels":["os=linux"]}`, field: executeFieldLabels},
		"comma in a label":      {input: `{"action":"add_worker","labels":["zone=a,b"]}`, field: executeFieldLabels},
		"key twice in a label":  {input: `{"action":"add_worker","labels":["gpu=first,gpu=second"]}`, field: executeFieldLabels},
		"trailing comma":        {input: `{"action":"add_worker","labels":["gpu=a,"]}`, field: executeFieldLabels},
		"space in a label":      {input: `{"action":"add_worker","labels":["team=data eng"]}`, field: executeFieldLabels},
		"quote in a label":      {input: `{"action":"add_worker","labels":["note=it's"]}`, field: executeFieldLabels},
		"dollar in a label":     {input: `{"action":"add_worker","labels":["leak=$(DAGU_WORKER_INVITE)"]}`, field: executeFieldLabels},
		"bad label key":         {input: `{"action":"add_worker","labels":["gpu/type=a100"]}`, field: executeFieldLabels},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := inputError(tc.input)
			require.NotNil(t, err)
			require.Equal(t, executeErrorInvalidToolInput, err.Code)
			require.Equal(t, tc.field, err.Field)
		})
	}
}

// A label the worker accepts at start passes, key-only ones included. A
// refused label is named by its position, never by its text, which may be
// a pasted invite.
func TestAddWorkerLabels(t *testing.T) {
	t.Parallel()

	require.Nil(t, inputError(`{"action":"add_worker","labels":["gpu","Zone=Tokyo-1","pool=gpu/a100"]}`))

	err := inputError(`{"action":"add_worker","labels":["gpu=true","` + testWorkerInvite + `"]}`)
	require.NotNil(t, err)
	require.Equal(t, executeFieldLabels, err.Field)
	require.Contains(t, err.Message, "label 2")
	require.NotContains(t, err.Message, "AAAAAAAAAA")

	// A repeated key is named by the label that repeats it.
	err = inputError(`{"action":"add_worker","labels":["zone=a","gpu=1","GPU=2"]}`)
	require.NotNil(t, err)
	require.Contains(t, err.Message, "label 3")
}

// An expiresIn the invite API would refuse is refused before any
// coordinator is asked, so an invite pasted into it reaches no audit entry.
func TestAddWorkerChecksExpiresInFirst(t *testing.T) {
	t.Parallel()

	for _, expiresIn := range []string{"soon", "-1h", "0s", "2562047h47m17s", testWorkerInvite} {
		stub := &workerAccessStub{readiness: []coordinator.JoinReadiness{{Report: readyReport()}}}
		svc, store := newAddWorkerService(stub)

		result, data := addWorker(t, cloudUser(auth.RoleAdmin), svc, map[string]any{"address": "192.168.1.20:50055", "expiresIn": expiresIn})

		require.True(t, result.IsError, expiresIn)
		output := structuredMap(t, result)
		require.Equal(t, executeErrorInvalidToolInput, output["code"], data)
		require.Equal(t, executeFieldExpiresIn, output["field"], data)
		require.Zero(t, stub.readinessCalls, expiresIn)
		for _, entry := range store.entries {
			require.NotContains(t, entry.Details, testWorkerInvite, entry.Action)
		}
	}
	// The longest expiry the invite API accepts passes.
	require.Nil(t, inputError(`{"action":"add_worker","expiresIn":"2562047h47m16s"}`))
}

// The image rule is the one the image workflow applies before it publishes
// a tag, so a version gets an image exactly when the workflow publishes it.
func TestWorkerImageFollowsImageWorkflow(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "docker.yaml"))
	require.NoError(t, err)
	var workflow struct {
		Env map[string]string `yaml:"env"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Equal(t, publishedVersionPattern, workflow.Env["PUBLISHED_VERSION"])
	require.Equal(t, unreleasedSuffixPattern, workflow.Env["UNRELEASED_SUFFIX"])
	require.Equal(t, strconv.Itoa(maxImageTag), workflow.Env["MAX_TAG_LENGTH"])

	for _, tag := range []string{"v2.28.0-next", "v2.28.0-1-gabc1234"} {
		_, ok := workerImage(tag)
		require.False(t, ok, "the workflow refuses %s, so no image exists", tag)
	}
}

// A release or prerelease build gets its own image tag. A build that was
// never published gets none.
func TestWorkerImage(t *testing.T) {
	t.Parallel()

	for version, want := range map[string]string{
		"2.28.0":                   "ghcr.io/dagucloud/dagu:2.28.0",
		"v2.28.0":                  "ghcr.io/dagucloud/dagu:2.28.0",
		"v2.28.0-rc.1":             "ghcr.io/dagucloud/dagu:2.28.0-rc.1",
		"dev":                      "",
		"0.0.0":                    "",
		"v2.27.0-12-gabc":          "",
		"2.28.0-preview-test.1":    "ghcr.io/dagucloud/dagu:2.28.0-preview-test.1",
		"v2.28.0--preview.1":       "ghcr.io/dagucloud/dagu:2.28.0--preview.1",
		"2.28.0-rc_1":              "ghcr.io/dagucloud/dagu:2.28.0-rc_1",
		"2.28.0+build.1":           "",
		"v2.28.0-rc.1-3-g1a2b3c4d": "",
		"2.27.1-next":              "",
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

	commands = workerCommands("192.168.1.20:50055", nil, testWorkerInvite, "ghcr.io/dagucloud/dagu:2.28.0", false)
	require.Equal(t, map[string]string{
		"linux":  "dagu worker --worker.coordinators=192.168.1.20:50055 --worker.invite=" + testWorkerInvite,
		"docker": "docker run -d --restart unless-stopped -v dagu-worker:/var/lib/dagu -e DAGU_WORKER_INVITE=" + testWorkerInvite + " ghcr.io/dagucloud/dagu:2.28.0 dagu worker --worker.coordinators=192.168.1.20:50055",
	}, commands, "a single-use invite gets no manifest")
}
