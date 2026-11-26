// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	nodeStepDAG = `
name: node-dag
steps:
  - name: deploy
    run: "true"
    nodes: role=web
`
	plainDAG = `
name: plain-dag
steps:
  - name: noop
    run: "true"
`
)

// nodeSecrets are the values of the refs the test inventory uses.
var nodeSecrets = map[string]string{
	"ssh/jump-key":       "jump-key-value",
	"ssh/web-key":        "web-key-value",
	"ssh/web-passphrase": "web-passphrase-value",
	"ssh/web-password":   "web-password-value",
	"ssh/db-key":         "db-key-value",
}

// nodeStores holds an inventory whose web nodes connect through jump, the
// values of its secret refs, and keys trusted for web-1 and jump.
type nodeStores struct {
	inventory inventory.Store
	hostKeys  inventory.HostKeyStore
	secrets   secretpkg.Store
}

func newNodeStores(t *testing.T) nodeStores {
	t.Helper()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	hostKeys, err := store.NewHostKeyStore(backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	inventories, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories), hostKeys)
	require.NoError(t, err)
	secret := func(ref string) map[string]any { return map[string]any{"secret": ref} }
	_, err = inventories.Create(ctx, inventory.Inventory{
		Name:     "prod",
		Defaults: inventory.Defaults{Connection: inventory.Connection{User: "deploy"}},
		Nodes: []inventory.Node{
			{Name: "jump", Host: "jump.example.com", Connection: inventory.Connection{Key: secret("ssh/jump-key")}},
			{
				Name: "web-1", Host: "10.0.0.1", Labels: []string{"role=web"},
				Connection: inventory.Connection{Key: secret("ssh/web-key"), Passphrase: secret("ssh/web-passphrase"), Bastion: "jump"},
			},
			{
				Name: "web-2", Host: "10.0.0.2", Labels: []string{"role=web"}, Disabled: true,
				Connection: inventory.Connection{Password: secret("ssh/web-password"), Bastion: "jump"},
			},
			{Name: "db-1", Host: "10.0.0.3", Labels: []string{"role=db"}, Connection: inventory.Connection{Key: secret("ssh/db-key")}},
		},
	})
	require.NoError(t, err)
	for _, node := range []string{"web-1", "jump"} {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		key, err := ssh.NewPublicKey(pub)
		require.NoError(t, err)
		_, err = hostKeys.Trust(ctx, node, key, "admin", "")
		require.NoError(t, err)
	}

	enc, err := crypto.NewEncryptor("test-key-for-secrets")
	require.NoError(t, err)
	secrets, err := store.NewSecretStore(backend.Collection(persis.CollectionSecrets), enc)
	require.NoError(t, err)
	now := time.Now().UTC()
	for ref, value := range nodeSecrets {
		sec, err := secretpkg.New(secretpkg.CreateInput{Ref: ref, ProviderType: secretpkg.ProviderDaguManaged, CreatedBy: "test"}, now)
		require.NoError(t, err)
		require.NoError(t, secrets.Create(ctx, sec, &secretpkg.WriteValueInput{Value: value, CreatedBy: "test", CreatedAt: now}))
	}
	return nodeStores{inventory: inventories, hostKeys: hostKeys, secrets: secrets}
}

// nodeRun is an attempt of a DAG that worker-1 runs under a lease last
// renewed at heartbeat.
type nodeRun struct {
	handler    *coordinator.Handler
	attemptKey string
	attemptID  string
}

// newNodeRun starts the run. children are the DAG files the run's DAG can
// run, by name.
func newNodeRun(t *testing.T, stores nodeStores, dagYAML string, heartbeat time.Time, children ...string) nodeRun {
	t.Helper()
	ctx := context.Background()
	dagRepository := runtestutil.NewFileDAGRepository(filepath.Join(t.TempDir(), "dags"))
	for _, child := range children {
		require.NoError(t, dagRepository.Create(ctx, child, []byte("name: "+child+"\nnode: required\nsteps:\n  - run: \"true\"\n")))
	}
	dag, err := spec.LoadYAML(ctx, []byte(dagYAML))
	require.NoError(t, err)
	now := time.Now().UTC()
	repo := runtestutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	attempt, err := repo.CreateAttempt(ctx, dag, now, "run-1", persis.DAGRunCreateAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	attemptKey := ir.GenerateAttemptKey(dag.Name, "run-1", dag.Name, "run-1", attempt.ID())
	require.NoError(t, attempt.Open(ctx))
	t.Cleanup(func() { require.NoError(t, attempt.Close(context.Background())) })
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: attempt.ID(), AttemptKey: attemptKey, Status: ir.Running,
	}))
	leases := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection(persis.CollectionDAGRunLeases))
	require.NoError(t, leases.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:      attemptKey,
		DAGRun:          ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		Root:            ir.DAGRunRef{Name: dag.Name, ID: "run-1"},
		AttemptID:       attempt.ID(),
		WorkerID:        "worker-1",
		ClaimedAt:       heartbeat.UnixMilli(),
		LastHeartbeatAt: heartbeat.UnixMilli(),
	}))
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		SecretStore:         stores.secrets,
		NodeInventory:       stores.inventory,
		TrustedHostKeys:     stores.hostKeys,
		DAGRunRepository:    repo,
		DAGRepository:       dagRepository,
		DAGRunLeaseStore:    leases,
		StaleLeaseThreshold: time.Minute,
	})
	return nodeRun{handler: handler, attemptKey: attemptKey, attemptID: attempt.ID()}
}

func (r nodeRun) resolveNodes(selector string, names ...string) (*coordinatorv1.ResolveNodesResponse, error) {
	return r.resolveNodesFor("", selector, names...)
}

// resolveNodesFor asks as a run of dagName within the attempt.
func (r nodeRun) resolveNodesFor(dagName, selector string, names ...string) (*coordinatorv1.ResolveNodesResponse, error) {
	return r.handler.ResolveNodes(context.Background(), &coordinatorv1.ResolveNodesRequest{
		WorkerId: "worker-1", AttemptKey: r.attemptKey, AttemptId: r.attemptID, DagName: dagName,
		Selector: selector, Names: names,
	})
}

func (r nodeRun) resolveNodeSecret(node, ref string) (string, error) {
	resp, err := r.handler.ResolveSecretReference(context.Background(), &coordinatorv1.ResolveSecretReferenceRequest{
		Node: node, Ref: ref,
		WorkerId: "worker-1", AttemptKey: r.attemptKey, AttemptId: r.attemptID,
	})
	return resp.GetValue(), err
}

func protoNodeNames(nodes []*coordinatorv1.ResolvedNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.GetName()
	}
	return names
}

// A selector answers its nodes with the bastion they connect through and the
// keys trusted for both, holding each credential as a ref and no value.
func TestResolveNodesBySelector(t *testing.T) {
	t.Parallel()
	run := newNodeRun(t, newNodeStores(t), nodeStepDAG, time.Now())

	resp, err := run.resolveNodes("role=web")
	require.NoError(t, err)
	assert.Equal(t, []string{"web-1", "web-2"}, protoNodeNames(resp.GetNodes()))
	assert.Equal(t, []string{"jump"}, protoNodeNames(resp.GetBastions()))

	web1 := resp.GetNodes()[0]
	assert.Equal(t, coordinatorv1.NodeKind_NODE_KIND_SSH, web1.GetKind())
	assert.Equal(t, "prod", web1.GetInventoryName())
	assert.Equal(t, "10.0.0.1", web1.GetHost())
	assert.Equal(t, int32(22), web1.GetPort())
	assert.Equal(t, "deploy", web1.GetUser())
	assert.Equal(t, "ssh/web-key", web1.GetKeySecret())
	assert.Equal(t, "ssh/web-passphrase", web1.GetPassphraseSecret())
	assert.Equal(t, "jump", web1.GetBastion())
	assert.True(t, web1.GetStrictHostKey())
	assert.Equal(t, 30*time.Second, time.Duration(web1.GetTimeoutNanos()))
	assert.Equal(t, []string{"role=web"}, web1.GetLabels())
	assert.True(t, resp.GetNodes()[1].GetDisabled())
	assert.Equal(t, "ssh/web-password", resp.GetNodes()[1].GetPasswordSecret())

	var trusted []string
	for _, key := range resp.GetTrustedHostKeys() {
		trusted = append(trusted, key.GetNode())
		assert.Contains(t, key.GetKey(), "ssh-ed25519 ")
		assert.Contains(t, key.GetFingerprint(), "SHA256:")
	}
	assert.ElementsMatch(t, []string{"web-1", "jump"}, trusted)

	text := prototext.Format(resp)
	for _, value := range nodeSecrets {
		assert.NotContains(t, text, value)
	}
}

// Names are compared without case, and one that names no node is left out,
// so a caller learns which nodes are gone.
func TestResolveNodesByName(t *testing.T) {
	t.Parallel()
	run := newNodeRun(t, newNodeStores(t), nodeStepDAG, time.Now())

	resp, err := run.resolveNodes("", "WEB-1", "gone")
	require.NoError(t, err)
	assert.Equal(t, []string{"web-1"}, protoNodeNames(resp.GetNodes()))
	assert.Equal(t, []string{"jump"}, protoNodeNames(resp.GetBastions()))

	resp, err = run.resolveNodes("", "db-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"db-1"}, protoNodeNames(resp.GetNodes()))
	assert.Empty(t, resp.GetBastions())
	assert.Empty(t, resp.GetTrustedHostKeys())
}

func TestResolveNodesInvalidRequest(t *testing.T) {
	t.Parallel()
	run := newNodeRun(t, newNodeStores(t), nodeStepDAG, time.Now())

	for name, req := range map[string]struct {
		selector string
		names    []string
	}{
		"both":             {selector: "role=web", names: []string{"web-1"}},
		"neither":          {},
		"invalid selector": {selector: ","},
	} {
		_, err := run.resolveNodes(req.selector, req.names...)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
}

// Only an attempt with a fresh lease whose DAG targets nodes reads them.
func TestResolveNodesDenied(t *testing.T) {
	t.Parallel()
	stores := newNodeStores(t)

	_, err := newNodeRun(t, stores, plainDAG, time.Now()).resolveNodes("role=web")
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a DAG that targets no nodes")

	_, err = newNodeRun(t, stores, nodeStepDAG, time.Now().Add(-2*time.Minute)).resolveNodes("role=web")
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a stale lease")

	run := newNodeRun(t, stores, nodeStepDAG, time.Now())
	_, err = run.handler.ResolveNodes(context.Background(), &coordinatorv1.ResolveNodesRequest{
		WorkerId: "worker-2", AttemptKey: run.attemptKey, AttemptId: run.attemptID, Selector: "role=web",
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "another worker")

	_, err = coordinator.NewHandler(coordinator.HandlerConfig{}).ResolveNodes(context.Background(), &coordinatorv1.ResolveNodesRequest{Selector: "role=web"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "no inventory")
}

// A bound child reads nodes as a run of its own DAG, whether its parent runs
// it from a step, a foreach body, or a handler. A DAG the parent does not run
// reads none.
func TestResolveNodesForChildDAG(t *testing.T) {
	t.Parallel()
	run := newNodeRun(t, newNodeStores(t), `
name: fleet
steps:
  - name: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: role=web
  - name: each
    foreach:
      items: [a]
      steps:
        - name: deploy-each
          action: dag.run
          with:
            dag: deploy-each
          nodes: role=web
handler_on:
  failure:
    action: dag.run
    with:
      dag: rollback-one
    nodes: role=web
`, time.Now(), "deploy-one", "deploy-each", "rollback-one", "unrelated")

	for _, child := range []string{"deploy-one", "deploy-each", "rollback-one"} {
		resp, err := run.resolveNodesFor(child, "", "web-1")
		require.NoError(t, err, child)
		assert.Equal(t, []string{"web-1"}, protoNodeNames(resp.GetNodes()), child)
	}
	_, err := run.resolveNodesFor("unrelated", "", "web-1")
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a DAG the run does not run")
}

// A run that targets nodes reads the refs the connection to a node uses,
// its bastion's included, and no others.
func TestResolveSecretReferenceForNode(t *testing.T) {
	t.Parallel()
	stores := newNodeStores(t)
	run := newNodeRun(t, stores, nodeStepDAG, time.Now())

	value, err := run.resolveNodeSecret("web-1", "ssh/web-key")
	require.NoError(t, err)
	assert.Equal(t, "web-key-value", value)

	value, err = run.resolveNodeSecret("WEB-1", "ssh/jump-key")
	require.NoError(t, err)
	assert.Equal(t, "jump-key-value", value, "the bastion's key")

	for _, denied := range []struct{ node, ref, why string }{
		{"web-1", "ssh/db-key", "a ref of another node"},
		{"web-2", "ssh/web-password", "a disabled node"},
		{"gone", "ssh/web-key", "no such node"},
	} {
		_, err := run.resolveNodeSecret(denied.node, denied.ref)
		assert.Equal(t, codes.PermissionDenied, status.Code(err), denied.why)
	}

	_, err = newNodeRun(t, stores, plainDAG, time.Now()).resolveNodeSecret("db-1", "ssh/db-key")
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a DAG that targets no nodes")

	_, err = newNodeRun(t, stores, nodeStepDAG, time.Now().Add(-2*time.Minute)).resolveNodeSecret("web-1", "ssh/web-key")
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a stale lease")

	_, err = run.handler.ResolveSecretReference(context.Background(), &coordinatorv1.ResolveSecretReferenceRequest{
		Name: "WEB_KEY", Node: "web-1", Ref: "ssh/web-key",
		WorkerId: "worker-1", AttemptKey: run.attemptKey, AttemptId: run.attemptID,
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "a name and a node")
}

// A worker's client reads the nodes ResolveNodes answers as the inventory
// holds them, and reads a node's credential by the node.
func TestClientResolveNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stores := newNodeStores(t)
	run := newNodeRun(t, stores, nodeStepDAG, time.Now())
	server, addr := startMockServer(t, run.handler)
	t.Cleanup(server.Stop)
	host, port := parseHostPort(addr)
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{{
		ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive,
	}}}, coordinator.DefaultConfig())
	identity := coordinator.SecretReferenceRun{WorkerID: "worker-1", AttemptKey: run.attemptKey, AttemptID: run.attemptID}

	nodeClient, ok := client.(coordinator.NodeClient)
	require.True(t, ok)
	resolved, err := nodeClient.ResolveNodes(ctx, serviceregistry.HostInfo{}, "", []string{"WEB-1"}, identity)
	require.NoError(t, err)
	stored, err := stores.inventory.Named(ctx, []string{"web-1"})
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.Equal(t, []inventory.ResolvedNode{stored[1]}, resolved.Nodes)
	assert.Equal(t, []inventory.ResolvedNode{stored[0]}, resolved.Bastions)
	require.Len(t, resolved.HostKeys, 2)
	for _, key := range resolved.HostKeys {
		trusted, err := stores.hostKeys.Get(ctx, key.Node)
		require.NoError(t, err)
		assert.Equal(t, trusted.Key, key.Key)
		assert.Equal(t, trusted.Fingerprint, key.Fingerprint)
	}

	secrets, ok := client.(coordinator.SecretReferenceClient)
	require.True(t, ok)
	value, err := secrets.ResolveNodeSecretReference(ctx, serviceregistry.HostInfo{}, "web-1",
		secretref.Ref{Name: "nodes.web-1.bastion.key", Ref: "ssh/jump-key"}, "", identity)
	require.NoError(t, err)
	assert.Equal(t, "jump-key-value", value)
	_, err = secrets.ResolveNodeSecretReference(ctx, serviceregistry.HostInfo{}, "web-1",
		secretref.Ref{Name: "nodes.web-1.key", Ref: "ssh/db-key"}, "", identity)
	assert.ErrorContains(t, err, "secret reference access denied", "a ref of another node")
}
