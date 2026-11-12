// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test/mtls"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const relayRealKey = "sk-ant-api03-THE-REAL-KEY-0123456789"

// relayFixture is a coordinator with mutual TLS, a run with one step that names
// the endpoint cle1, and a fake provider.
type relayFixture struct {
	t        *testing.T
	ca       *mtls.Authority
	addr     string
	service  *aiendpoint.Service
	leases   *store.DAGRunLeaseStore
	attemptK string
	attemptI string
	audit    *auditSink
	upstream *httptest.Server
	// mcp is the MCP server entry service and mcpUpstream the fake MCP server. They
	// exist when the option withMCP is set.
	mcp         *aiendpoint.MCPService
	mcpUpstream *httptest.Server
	mcpMu       sync.Mutex
	mcpRequests []mcpSeen

	mu      sync.Mutex
	headers []http.Header

	// models is the body that the fake models route /v1/models answers, and
	// modelsCalls counts its calls. They exist when the option withModels is set.
	modelsMu    sync.Mutex
	models      string
	modelsCalls int
}

type auditSink struct {
	mu      sync.Mutex
	records []aigateway.AuditRecord
}

func (a *auditSink) add(_ context.Context, r aigateway.AuditRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, r)
}

func (a *auditSink) all() []aigateway.AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]aigateway.AuditRecord(nil), a.records...)
}

type relayOptions struct {
	handler      http.HandlerFunc
	limits       func(*aigateway.Limits)
	allowDAGs    []string
	stepStatus   ir.NodeStatus
	runStatus    ir.Status
	noGateway    bool
	recheck      time.Duration
	leasePool    string
	withPools    bool
	endpointStep string
	// withMCP adds the MCP server entry docs, with the token relayMCPToken, and gives
	// the step code the named MCP entries docs (allow search) and ghost (no entry).
	withMCP bool
	// mcpAllowDAGs is the use rule of the entry docs. The default is fix-bug.
	mcpAllowDAGs []string
	mcpHandler   http.HandlerFunc
	// chatStep makes the step code a chat.completion step. Its models name the
	// endpoint endpointStep, or cle1.
	chatStep bool
	// withModels gives the endpoint cle1 a models URL on the fake provider and gives
	// the service a models store. The body of the list is modelsBody.
	withModels bool
	modelsBody string
	// foreachBody adds a foreach step whose body has the steps inner (endpoint cle1)
	// and code (no endpoint).
	foreachBody bool
	// leaseStore wraps the lease store that the handler reads.
	leaseStore func(dispatch.DAGRunLeaseStore) dispatch.DAGRunLeaseStore
}

// flakyLeases is a lease store whose next reads fail as a shared file system can.
type flakyLeases struct {
	dispatch.DAGRunLeaseStore
	failures atomic.Int32
}

func (s *flakyLeases) wrap(inner dispatch.DAGRunLeaseStore) dispatch.DAGRunLeaseStore {
	s.DAGRunLeaseStore = inner
	return s
}

func (s *flakyLeases) Get(ctx context.Context, attemptKey string) (*dispatch.DAGRunLease, error) {
	if s.failures.Add(-1) >= 0 {
		return nil, errors.New("stale file handle")
	}
	s.failures.Store(0)
	return s.DAGRunLeaseStore.Get(ctx, attemptKey)
}

func startRelayFixture(t *testing.T, opts relayOptions) *relayFixture {
	t.Helper()
	ctx := context.Background()
	f := &relayFixture{t: t, audit: &auditSink{}}

	upstreamHandler := opts.handler
	if upstreamHandler == nil {
		upstreamHandler = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}
	f.models = opts.modelsBody
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			f.modelsMu.Lock()
			f.modelsCalls++
			body := f.models
			f.modelsMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		upstreamHandler(w, r)
	}))
	t.Cleanup(f.upstream.Close)

	// The endpoint and its key.
	enc, err := crypto.NewEncryptor("test-key-for-relay")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)
	endpointStore, err := store.NewAIEndpointStore(testutil.NewMemoryBackend().Collection("ai"))
	require.NoError(t, err)
	f.service, err = aiendpoint.NewService(endpointStore, secretStore)
	require.NoError(t, err)
	allow := opts.allowDAGs
	if allow == nil {
		allow = []string{"fix-bug"}
	}
	create := aiendpoint.CreateInput{
		Name: "cle1", Preset: "custom", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: f.upstream.URL,
		Auth: aiendpoint.AuthXAPIKey, Scope: aiendpoint.GlobalScope(),
		Allow: aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: allow}, Key: relayRealKey, Actor: "test",
	}
	if opts.withModels {
		modelsStore, err := store.NewAIEndpointModelsStore(testutil.NewMemoryBackend().Collection("models"))
		require.NoError(t, err)
		reader, err := aiendpoint.NewModelsReader(f.upstream.Client(), 5*time.Second)
		require.NoError(t, err)
		require.NoError(t, f.service.EnableModels(modelsStore, reader))
		create.ModelsURL = f.upstream.URL + "/v1/models"
	}
	_, err = f.service.Create(ctx, create)
	require.NoError(t, err)
	f.service.WaitModels()

	if opts.withMCP {
		f.startMCP(t, opts, secretStore)
	}

	// The run: one DAG, one step that names the endpoint, a fresh lease.
	now := time.Now().UTC()
	runs := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	stepEndpoint := opts.endpointStep
	if stepEndpoint == "" {
		stepEndpoint = "cle1"
	}
	dag := &ir.DAG{
		Name: "fix-bug",
		Steps: []ir.Step{
			{Name: "code", Agent: &ir.AgentRun{Provider: "claude", Endpoint: stepEndpoint}},
			{Name: "plain", Agent: &ir.AgentRun{Provider: "claude"}},
		},
	}
	if opts.withMCP {
		dag.Steps[0].Agent.CLI = &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
			{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{Allow: []string{"search"}}},
			{Kind: ir.ToolEntryMCP, Name: "ghost", MCP: &ir.MCPToolSource{Allow: []string{"x"}}},
			{Kind: ir.ToolEntryMCP, Name: "inline", MCP: &ir.MCPToolSource{URL: "https://inline.example.com/mcp", Allow: []string{"x"}}},
		}}
	}
	if opts.foreachBody {
		// A foreach body holds an agent step that names the endpoint, and a step with
		// the name of the top-level step plain, which names no endpoint.
		dag.Steps = append(dag.Steps, ir.Step{Name: "loop", Foreach: &ir.ForeachConfig{Steps: []ir.Step{
			{Name: "inner", Agent: &ir.AgentRun{Provider: "claude", Endpoint: "cle1"}},
			{Name: "code", Agent: &ir.AgentRun{Provider: "claude"}},
		}}})
	}
	if opts.chatStep {
		dag.Steps[0] = ir.Step{
			Name:           "code",
			ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeChat},
			LLM: &ir.LLMConfig{Models: []ir.ModelEntry{
				{Provider: "openai", Name: "old-method-model"},
				{Name: "m", Endpoint: stepEndpoint},
			}},
		}
	}
	attempt, err := runs.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	f.attemptI = attempt.ID()
	f.attemptK = ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() { _ = attempt.Close(context.Background()) })
	runStatus := opts.runStatus
	if runStatus == ir.NotStarted {
		runStatus = ir.Running
	}
	nodeStatus := opts.stepStatus
	if nodeStatus == ir.NodeNotStarted {
		nodeStatus = ir.NodeRunning
	}
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: attempt.ID(), AttemptKey: f.attemptK, Status: runStatus,
		Nodes: []*ir.Node{{Step: dag.Steps[0], Status: nodeStatus}, {Step: dag.Steps[1], Status: ir.NodeRunning}},
	}))
	f.leases = store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("leases"))
	require.NoError(t, f.leases.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey: f.attemptK, DAGRun: ir.DAGRunRef{Name: dag.Name, ID: "run-1"}, Root: ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		AttemptID: attempt.ID(), WorkerID: "worker-1", WorkerPool: opts.leasePool,
		ClaimedAt: now.UnixMilli(), LastHeartbeatAt: now.UnixMilli(),
	}))

	limits := aigateway.Limits{
		MaxRequestBytes: 4 << 20, MaxResponseBytes: 256 << 20, MaxHeaderBytes: 32768,
		MaxStreamsPerRun: 4, MaxStreams: 16, ConnectTimeout: 2 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, TotalTimeout: 60 * time.Second,
	}
	if opts.limits != nil {
		opts.limits(&limits)
	}
	var gateway *aigateway.Gateway
	if !opts.noGateway {
		cfg := aigateway.Config{Endpoints: f.service, Limits: limits, Audit: f.audit.add}
		if f.mcp != nil {
			cfg.MCPServers = f.mcp
		}
		gateway, err = aigateway.New(cfg)
		require.NoError(t, err)
	}

	// The server, with mutual TLS.
	dir := t.TempDir()
	f.ca = mtls.NewAuthority(t, dir, "dagu-ca")
	serverPair := f.ca.Server(t, "coordinator")
	serverCert, err := tls.LoadX509KeyPair(serverPair.CertFile, serverPair.KeyFile)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(f.ca.CAFile)
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(caPEM))
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: clientCAs, MinVersion: tls.VersionTLS12,
	})
	var pools *coordinator.WorkerPools
	serverOpts := []grpc.ServerOption{grpc.Creds(creds), grpc.MaxRecvMsgSize(16 << 20), grpc.MaxSendMsgSize(16 << 20)}
	if opts.withPools {
		pools, err = coordinator.NewWorkerPools([]config.WorkerPool{
			{Name: "agent", Identities: []string{"agent-1"}, Labels: map[string]string{"agent.provider.claude": "true"}},
			{Name: "general", Identities: []string{"general-1"}, Labels: map[string]string{"zone": "*"}},
		}, []string{"server"}, config.Peer{CertFile: serverPair.CertFile, KeyFile: serverPair.KeyFile, ClientCaFile: f.ca.CAFile})
		require.NoError(t, err)
		serverOpts = append(serverOpts, pools.ServerOptions()...)
	}
	var leaseStore dispatch.DAGRunLeaseStore = f.leases
	if opts.leaseStore != nil {
		leaseStore = opts.leaseStore(f.leases)
	}
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository: runs, DAGRunLeaseStore: leaseStore, StaleLeaseThreshold: time.Minute,
		WorkerPools: pools, AIGateway: gateway, RelayRecheckInterval: opts.recheck,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f.addr = listener.Addr().String()
	server := grpc.NewServer(serverOpts...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return f
}

func (f *relayFixture) conn(identity string) coordinatorv1.CoordinatorServiceClient {
	f.t.Helper()
	pair := f.ca.Client(f.t, identity)
	cert, err := tls.LoadX509KeyPair(pair.CertFile, pair.KeyFile)
	require.NoError(f.t, err)
	caPEM, err := os.ReadFile(f.ca.CAFile)
	require.NoError(f.t, err)
	roots := x509.NewCertPool()
	require.True(f.t, roots.AppendCertsFromPEM(caPEM))
	conn, err := grpc.NewClient(f.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert}, RootCAs: roots, MinVersion: tls.VersionTLS12,
	})), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20), grpc.MaxCallSendMsgSize(16<<20)))
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = conn.Close() })
	return coordinatorv1.NewCoordinatorServiceClient(conn)
}

// rpcAdapter makes the calls of the relay over a plain gRPC client.
type rpcAdapter struct {
	client coordinatorv1.CoordinatorServiceClient
}

func (a rpcAdapter) CheckAIEndpointTo(ctx context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	return a.client.CheckAIEndpoint(ctx, req)
}

func (a rpcAdapter) RelayAIRequestTo(ctx context.Context, _ serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
	return a.client.RelayAIRequest(ctx)
}

func (f *relayFixture) relayClient(identity, worker, attemptKey, attemptID string) aigateway.Client {
	return coordinator.NewAIRelayClientOver(rpcAdapter{client: f.conn(identity)}, serviceregistry.HostInfo{},
		coordinator.AIRelayRun{WorkerID: worker, AttemptKey: attemptKey, AttemptID: attemptID})
}

func (f *relayFixture) good() aigateway.Client {
	return f.relayClient("worker-cert", "worker-1", f.attemptK, f.attemptI)
}

func (f *relayFixture) upstreamHeaders() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.headers...)
}

var relayStep = aigateway.StepRef{RunID: "run-1", DAGName: "fix-bug", StepName: "code"}

type memSink struct {
	mu     sync.Mutex
	status int
	header http.Header
	buf    bytes.Buffer
	chunks int
	first  chan struct{}
	once   sync.Once
	delay  time.Duration
}

func newMemSink() *memSink { return &memSink{first: make(chan struct{})} }

func (s *memSink) Head(_ context.Context, status int, header http.Header) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.header = status, header
	return nil
}

func (s *memSink) Write(ctx context.Context, p []byte) error {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.buf.Write(p)
	s.chunks++
	s.mu.Unlock()
	s.once.Do(func() { close(s.first) })
	return nil
}

func (s *memSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func postMessages(body string) aigateway.HTTPRequest {
	return aigateway.HTTPRequest{
		Method: http.MethodPost, Path: "/v1/messages",
		Header:        http.Header{"Content-Type": {"application/json"}, "X-Api-Key": {"placeholder"}},
		ContentLength: int64(len(body)),
	}
}

func asFailure(t *testing.T, err error) *aigateway.Failure {
	t.Helper()
	require.Error(t, err)
	failure, ok := err.(*aigateway.Failure)
	require.True(t, ok, "expected a failure of the gateway, got %T: %v", err, err)
	return failure
}

func TestAIRelay_CheckAndRelayWithTheRealKey(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	client := f.good()

	result, err := client.Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	require.NoError(t, err)
	assert.Equal(t, aiendpoint.ProtocolAnthropic, result.Protocol)
	assert.Equal(t, "anthropic", result.ChatType)

	body := `{"model":"m","messages":[]}`
	sink := newMemSink()
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages(body), strings.NewReader(body), sink))
	assert.Equal(t, http.StatusOK, sink.status)
	assert.Equal(t, `{"ok":true}`, sink.text())

	headers := f.upstreamHeaders()
	require.Len(t, headers, 1)
	assert.Equal(t, relayRealKey, headers[0].Get("X-Api-Key"), "the provider gets the real key")
	assert.NotContains(t, strings.Join(headers[0].Values("X-Api-Key"), ","), "placeholder")

	records := f.audit.all()
	require.Len(t, records, 1)
	assert.Equal(t, "messages", records[0].Route)
	assert.Equal(t, "worker-1", records[0].WorkerID)
	assert.Equal(t, "run-1", records[0].RunID)
	assert.Equal(t, "code", records[0].StepName)
	assert.NotEmpty(t, records[0].Fingerprint)
	assert.NotContains(t, strings.Join([]string{records[0].Fingerprint, records[0].Name, records[0].Route}, " "), relayRealKey)
}

func TestAIRelay_ChecksRefuseBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	ctx := context.Background()

	t.Run("a worker that does not hold the lease", func(t *testing.T) {
		other := f.relayClient("worker-cert", "worker-2", f.attemptK, f.attemptI)
		_, err := other.Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
		failure := asFailure(t, other.Relay(ctx, relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
		assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	})
	t.Run("another attempt id", func(t *testing.T) {
		other := f.relayClient("worker-cert", "worker-1", f.attemptK, "attempt-other")
		_, err := other.Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	})
	t.Run("an unknown attempt key", func(t *testing.T) {
		other := f.relayClient("worker-cert", "worker-1", "no-such-key", f.attemptI)
		_, err := other.Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	})
	t.Run("a step that does not name the endpoint", func(t *testing.T) {
		_, err := f.good().Check(ctx, aigateway.StepRef{DAGName: "fix-bug", StepName: "plain"}, aigateway.EndpointTarget("cle1"), "claude")
		failure := asFailure(t, err)
		assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
		assert.Contains(t, failure.Message, "does not name")
	})
	t.Run("a step that does not exist", func(t *testing.T) {
		_, err := f.good().Check(ctx, aigateway.StepRef{DAGName: "fix-bug", StepName: "ghost"}, aigateway.EndpointTarget("cle1"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	})
	t.Run("another endpoint name", func(t *testing.T) {
		_, err := f.good().Check(ctx, relayStep, aigateway.EndpointTarget("cle2"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason, "the request cannot name another target")
		failure := asFailure(t, f.good().Relay(ctx, relayStep, aigateway.EndpointTarget("cle2"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
		assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	})
	t.Run("a DAG that is not reachable", func(t *testing.T) {
		_, err := f.good().Check(ctx, aigateway.StepRef{DAGName: "unrelated", StepName: "code"}, aigateway.EndpointTarget("cle1"), "claude")
		assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	})
	t.Run("a provider that needs another protocol", func(t *testing.T) {
		_, err := f.good().Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "codex")
		assert.Equal(t, aigateway.ReasonEndpointProtocolMismatch, asFailure(t, err).Reason)
	})
	assert.Empty(t, f.upstreamHeaders(), "no refused call reaches the provider")
}

func TestAIRelay_NeedsAVerifiedCertificate(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	handler := coordinator.NewHandler(coordinator.HandlerConfig{})
	_, err := handler.CheckAIEndpoint(context.Background(), &coordinatorv1.CheckAIEndpointRequest{
		WorkerId: "worker-1", AttemptKey: f.attemptK, AttemptId: f.attemptI, DagName: "fix-bug", StepName: "code",
		Target: &coordinatorv1.RelayTarget{Kind: "endpoint", Name: "cle1"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Contains(t, err.Error(), "mutual TLS")

	// A client with no certificate does not get past the handshake.
	caPEM, err := os.ReadFile(f.ca.CAFile)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caPEM))
	conn, err := grpc.NewClient(f.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = coordinatorv1.NewCoordinatorServiceClient(conn).CheckAIEndpoint(ctx, &coordinatorv1.CheckAIEndpointRequest{})
	require.Error(t, err)
}

func TestAIRelay_ForbiddenDAGAndDeletedEndpoint(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{allowDAGs: []string{"other-dag"}})
	_, err := f.good().Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	failure := asFailure(t, err)
	assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	assert.Contains(t, failure.Message, "allow list")
	assert.NotContains(t, failure.Error(), relayRealKey)

	g := startRelayFixture(t, relayOptions{})
	client := g.good()
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	require.NoError(t, g.service.Delete(context.Background(), aiendpoint.GlobalScope(), "cle1"))
	failure = asFailure(t, client.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	assert.Equal(t, aigateway.ReasonEndpointUnknown, failure.Reason, "the next request of a deleted endpoint fails")

	// The refused request has an audit line, as a request that went to the provider has.
	records := g.audit.all()
	require.NotEmpty(t, records)
	last := records[len(records)-1]
	assert.Equal(t, aigateway.ReasonEndpointUnknown, last.Reason)
	assert.Equal(t, http.StatusNotFound, last.Status)
	assert.Equal(t, "cle1", last.Name)
	assert.Equal(t, "fix-bug", last.DAGName)
	assert.Equal(t, http.MethodPost, last.Method)
}

func TestAIRelay_RotationAppliesToTheNextRequest(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	client := f.good()
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	_, err := f.service.RotateKey(context.Background(), aiendpoint.GlobalScope(), "cle1", "sk-ant-the-new-key-987654321", "test")
	require.NoError(t, err)
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	headers := f.upstreamHeaders()
	require.Len(t, headers, 2)
	assert.Equal(t, relayRealKey, headers[0].Get("X-Api-Key"))
	assert.Equal(t, "sk-ant-the-new-key-987654321", headers[1].Get("X-Api-Key"))
}

func TestAIRelay_FinalStatusRefusesTheCall(t *testing.T) {
	t.Parallel()
	for name, opts := range map[string]relayOptions{
		"step":    {stepStatus: ir.NodeSucceeded},
		"attempt": {runStatus: ir.Succeeded},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := startRelayFixture(t, opts)
			_, err := f.good().Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
			failure := asFailure(t, err)
			assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
			assert.Contains(t, failure.Message, "final status")
		})
	}
}

func TestAIRelay_WorkerPools(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{withPools: true, leasePool: "agent"})
	_, err := f.relayClient("agent-1", "worker-1", f.attemptK, f.attemptI).Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	require.NoError(t, err)
	// A certificate of another pool cannot use the lease of the agent pool.
	_, err = f.relayClient("general-1", "worker-1", f.attemptK, f.attemptI).Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	// An identity in no pool is refused.
	_, err = f.relayClient("stranger", "worker-1", f.attemptK, f.attemptI).Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	failure := asFailure(t, f.relayClient("general-1", "worker-1", f.attemptK, f.attemptI).
		Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	assert.Empty(t, f.upstreamHeaders())
}

func TestAIRelay_NoGatewayGivesEndpointUnavailable(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{noGateway: true})
	_, err := f.good().Check(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), "claude")
	assert.Equal(t, aigateway.ReasonEndpointUnavailable, asFailure(t, err).Reason)
	failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	assert.Equal(t, aigateway.ReasonEndpointUnavailable, failure.Reason)
}

func TestAIRelay_UpstreamStatusAndHeadersPass(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "17")
		w.Header().Set("Set-Cookie", "a=b")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error"}`))
	}})
	sink := newMemSink()
	require.NoError(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink))
	assert.Equal(t, http.StatusTooManyRequests, sink.status)
	assert.Equal(t, "17", sink.header.Get("Retry-After"))
	assert.Empty(t, sink.header.Get("Set-Cookie"))
}

func TestAIRelay_StreamsChunkByChunk(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	f := startRelayFixture(t, relayOptions{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	}})
	sink := newMemSink()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink)
	}()
	select {
	case <-sink.first:
	case <-time.After(5 * time.Second):
		t.Fatal("the first event did not arrive before the provider finished")
	}
	t.Logf("first chunk after %s", time.Since(start))
	select {
	case err := <-done:
		t.Fatalf("the call ended before the provider finished: %v", err)
	default:
	}
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, "event: message_start\ndata: {}\n\nevent: message_stop\ndata: {}\n\n", sink.text())
}

// A response of 96 MiB in many chunks arrives whole and in order, with the
// backpressure of the stream: a slow sink slows the provider.
func TestAIRelay_LargeResponseKeepsEveryByte(t *testing.T) {
	t.Parallel()
	const total = 96 << 20
	want := sha256.New()
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	for sent := 0; sent < total; sent += len(chunk) {
		want.Write(chunk)
	}
	f := startRelayFixture(t, relayOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		for sent := 0; sent < total; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}})
	hash := sha256.New()
	var n int64
	sink := &hashSink{hash: hash, n: &n}
	start := time.Now()
	require.NoError(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink))
	t.Logf("%d bytes in %s", n, time.Since(start))
	assert.Equal(t, int64(total), n)
	assert.Equal(t, want.Sum(nil), hash.Sum(nil))
}

type hashSink struct {
	hash interface{ Write([]byte) (int, error) }
	n    *int64
}

func (s *hashSink) Head(context.Context, int, http.Header) error { return nil }
func (s *hashSink) Write(_ context.Context, p []byte) error {
	*s.n += int64(len(p))
	_, err := s.hash.Write(p)
	return err
}

func TestAIRelay_ManySmallChunks(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		for i := range 5000 {
			_, _ = io.WriteString(w, "data: "+strings.Repeat("x", i%50)+"\n\n")
			w.(http.Flusher).Flush()
		}
	}})
	sink := newMemSink()
	require.NoError(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink))
	var want strings.Builder
	for i := range 5000 {
		want.WriteString("data: " + strings.Repeat("x", i%50) + "\n\n")
	}
	assert.Equal(t, want.String(), sink.text())
	// The count of the chunks depends on how the network joins small writes, so the test
	// checks only that the relay passes the stream on in many pieces, not as one buffer.
	assert.Greater(t, sink.chunks, 10, "the events arrive in many chunks")
}

func TestAIRelay_LargeRequestBodyInChunks(t *testing.T) {
	t.Parallel()
	var got atomic.Int64
	f := startRelayFixture(t, relayOptions{handler: func(_ http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		got.Store(n)
	}})
	body := strings.Repeat("a", 3<<20+17)
	require.NoError(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages(body), strings.NewReader(body), newMemSink()))
	assert.Equal(t, int64(len(body)), got.Load())
}

func TestAIRelay_RequestAndResponseLimits(t *testing.T) {
	t.Parallel()
	t.Run("request too large by content length", func(t *testing.T) {
		t.Parallel()
		f := startRelayFixture(t, relayOptions{limits: func(l *aigateway.Limits) { l.MaxRequestBytes = 1024 }})
		body := strings.Repeat("a", 2048)
		failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages(body), strings.NewReader(body), newMemSink()))
		assert.Equal(t, aigateway.ReasonRequestTooLarge, failure.Reason)
		assert.Empty(t, f.upstreamHeaders())
	})
	t.Run("request too large with an unknown length", func(t *testing.T) {
		t.Parallel()
		f := startRelayFixture(t, relayOptions{limits: func(l *aigateway.Limits) { l.MaxRequestBytes = 1024 }})
		req := postMessages("")
		req.ContentLength = -1
		failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), req, strings.NewReader(strings.Repeat("a", 4096)), newMemSink()))
		assert.Equal(t, aigateway.ReasonRequestTooLarge, failure.Reason)
		assert.Empty(t, f.upstreamHeaders())
	})
	t.Run("response too large", func(t *testing.T) {
		t.Parallel()
		f := startRelayFixture(t, relayOptions{
			limits: func(l *aigateway.Limits) { l.MaxResponseBytes = 4096 },
			handler: func(w http.ResponseWriter, _ *http.Request) {
				for range 100 {
					_, _ = w.Write(bytes.Repeat([]byte("z"), 1024))
				}
			},
		})
		sink := newMemSink()
		failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink))
		assert.Equal(t, aigateway.ReasonResponseTooLarge, failure.Reason)
		assert.LessOrEqual(t, len(sink.text()), 4096)
	})
	t.Run("too many streams", func(t *testing.T) {
		t.Parallel()
		hold := make(chan struct{})
		f := startRelayFixture(t, relayOptions{
			limits: func(l *aigateway.Limits) { l.MaxStreamsPerRun = 1 },
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "x")
				w.(http.Flusher).Flush()
				select {
				case <-hold:
				case <-r.Context().Done():
				}
			},
		})
		sink := newMemSink()
		done := make(chan error, 1)
		go func() {
			done <- f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink)
		}()
		<-sink.first
		failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
		assert.Equal(t, aigateway.ReasonTooManyStreams, failure.Reason)
		close(hold)
		require.NoError(t, <-done)
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		f := startRelayFixture(t, relayOptions{
			limits: func(l *aigateway.Limits) { l.ResponseHeaderTimeout = 200 * time.Millisecond },
			handler: func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			},
		})
		failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
		assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
	})
}

func TestAIRelay_ClientCancelEndsTheUpstreamCall(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	upstreamDone := make(chan struct{})
	f := startRelayFixture(t, relayOptions{handler: func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- f.good().Relay(ctx, relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink())
	}()
	<-started
	cancel()
	failure := asFailure(t, <-done)
	assert.Equal(t, aigateway.ReasonCanceled, failure.Reason)
	select {
	case <-upstreamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the call to the provider kept running after the cancel")
	}
}

func TestAIRelay_LostLeaseCancelsTheOpenStream(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	upstreamDone := make(chan struct{})
	f := startRelayFixture(t, relayOptions{recheck: 100 * time.Millisecond, handler: func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}})
	done := make(chan error, 1)
	go func() {
		done <- f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink())
	}()
	<-started
	require.NoError(t, f.leases.Delete(context.Background(), f.attemptK))
	select {
	case err := <-done:
		failure := asFailure(t, err)
		assert.Equal(t, aigateway.ReasonRelayUnavailable, failure.Reason)
		assert.Contains(t, failure.Message, "lease")
	case <-time.After(5 * time.Second):
		t.Fatal("the stream stayed open after the lease ended")
	}
	select {
	case <-upstreamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the call to the provider kept running after the lease ended")
	}
}

// A slow read of a shared file system must not cut a response that streams for
// a long time. Errors of the storage in a row past the limit end the stream.
func TestAIRelay_LeaseCheckToleratesStorageErrors(t *testing.T) {
	t.Parallel()
	streamWith := func(t *testing.T, failures int32) (string, error) {
		t.Helper()
		release := make(chan struct{})
		flaky := &flakyLeases{}
		f := startRelayFixture(t, relayOptions{recheck: 50 * time.Millisecond, leaseStore: flaky.wrap, handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "data: 1\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
				_, _ = io.WriteString(w, "data: 2\n\n")
			case <-r.Context().Done():
			}
		}})
		sink := newMemSink()
		done := make(chan error, 1)
		go func() {
			done <- f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink)
		}()
		<-sink.first
		flaky.failures.Store(failures)
		select {
		case err := <-done:
			return sink.text(), err
		case <-time.After(time.Second):
		}
		close(release)
		select {
		case err := <-done:
			return sink.text(), err
		case <-time.After(5 * time.Second):
			t.Fatal("the stream did not end")
			return "", nil
		}
	}

	t.Run("a few errors keep the stream", func(t *testing.T) {
		t.Parallel()
		text, err := streamWith(t, 2)
		require.NoError(t, err)
		assert.Equal(t, "data: 1\n\ndata: 2\n\n", text)
	})
	t.Run("errors in a row end the stream", func(t *testing.T) {
		t.Parallel()
		_, err := streamWith(t, 1000)
		failure := asFailure(t, err)
		assert.Equal(t, aigateway.ReasonRelayUnavailable, failure.Reason)
		assert.Contains(t, failure.Message, "lease")
	})
}

// stuckStream is a relay stream whose body sends wait on the flow control of a
// coordinator that reads nothing. Its calls end when the stream context ends.
type stuckStream struct {
	grpc.ClientStream
	ctx      context.Context
	accepted atomic.Bool
}

func (s *stuckStream) Context() context.Context { return s.ctx }

func (s *stuckStream) Send(msg *coordinatorv1.RelayClientMessage) error {
	if msg.GetBody() == nil {
		return nil
	}
	<-s.ctx.Done()
	return s.ctx.Err()
}

func (s *stuckStream) Recv() (*coordinatorv1.RelayServerMessage, error) {
	if s.accepted.CompareAndSwap(false, true) {
		return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Accepted{Accepted: &coordinatorv1.RelayAccepted{}}}, nil
	}
	<-s.ctx.Done()
	return nil, status.Error(codes.Canceled, "canceled")
}

type stuckRelay struct{}

func (stuckRelay) CheckAIEndpointTo(context.Context, serviceregistry.HostInfo, *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	return nil, errors.New("not used")
}

func (stuckRelay) RelayAIRequestTo(ctx context.Context, _ serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
	return &stuckStream{ctx: ctx}, nil
}

// A client that goes away while the upload waits on the flow control ends the
// request: the cancel message cannot hold the stream open.
func TestAIRelayClient_CancelEndsABlockedUpload(t *testing.T) {
	t.Parallel()
	client := coordinator.NewAIRelayClientOver(stuckRelay{}, serviceregistry.HostInfo{}, coordinator.AIRelayRun{WorkerID: "w", AttemptKey: "k", AttemptID: "a"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Relay(ctx, relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink())
	}()
	time.AfterFunc(100*time.Millisecond, cancel)
	select {
	case err := <-done:
		assert.Equal(t, aigateway.ReasonCanceled, asFailure(t, err).Reason)
	case <-time.After(5 * time.Second):
		t.Fatal("the request stayed open after the client went away")
	}
}

// An agent step of a foreach body uses its endpoint through the relay. A body
// step with the name of a top-level step does not widen what that name may use:
// each step of the name must name the target.
func TestAIRelay_StepsOfAForeachBody(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{foreachBody: true})
	ctx := context.Background()
	inner := aigateway.StepRef{RunID: "run-1", DAGName: "fix-bug", StepName: "inner"}
	_, err := f.good().Check(ctx, inner, aigateway.EndpointTarget("cle1"), "claude")
	require.NoError(t, err)
	require.NoError(t, f.good().Relay(ctx, inner, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))

	// The name code is the top-level step that names cle1, and a body step that names
	// no endpoint.
	_, err = f.good().Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "claude")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
}

func TestAIRelay_IdentityRefusalHasAnAuditLine(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	other := f.relayClient("worker-cert", "worker-2", f.attemptK, f.attemptI)
	failure := asFailure(t, other.Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), newMemSink()))
	require.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	records := f.audit.all()
	require.Len(t, records, 1)
	assert.Empty(t, records[0].RunID, "a refusal of the identity has no run ID")
	assert.Equal(t, "worker-2", records[0].WorkerID)
	assert.Equal(t, aigateway.ReasonEndpointForbidden, records[0].Reason)
	assert.Equal(t, http.StatusForbidden, records[0].Status)
}

func TestAIRelay_SlowReaderSlowsTheProvider(t *testing.T) {
	t.Parallel()
	var written atomic.Int64
	chunk := bytes.Repeat([]byte("q"), 64*1024)
	f := startRelayFixture(t, relayOptions{
		limits: func(l *aigateway.Limits) { l.IdleTimeout = 1 * time.Second },
		handler: func(w http.ResponseWriter, _ *http.Request) {
			for {
				n, err := w.Write(chunk)
				written.Add(int64(n))
				if err != nil {
					return
				}
			}
		},
	})
	// The sink holds the first chunk for 3 seconds, then fails: the client is gone.
	sink := &gateSink{release: make(chan struct{})}
	time.AfterFunc(3*time.Second, func() { close(sink.release) })
	failure := asFailure(t, f.good().Relay(context.Background(), relayStep, aigateway.EndpointTarget("cle1"), postMessages("{}"), strings.NewReader("{}"), sink))
	assert.Contains(t, []aigateway.Reason{aigateway.ReasonUpstreamTimeout, aigateway.ReasonCanceled, aigateway.ReasonRelayUnavailable}, failure.Reason)
	assert.Less(t, written.Load(), int64(200<<20), "the coordinator does not buffer without bound")
}

type gateSink struct{ release chan struct{} }

func (s *gateSink) Head(context.Context, int, http.Header) error { return nil }
func (s *gateSink) Write(context.Context, []byte) error {
	<-s.release
	return io.ErrClosedPipe
}
