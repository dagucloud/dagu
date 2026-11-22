// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package nodeconn_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const (
	nodeUser     = "tester"
	nodePassword = "node-password-7c2m"
	passwordRef  = "ssh/fleet-password"
)

// countingSecrets resolves refs from a map and counts each resolution.
type countingSecrets struct {
	mu     sync.Mutex
	values map[string]string
	calls  map[string]int
}

func newSecrets() *countingSecrets {
	return &countingSecrets{values: map[string]string{passwordRef: nodePassword}, calls: map[string]int{}}
}

func (s *countingSecrets) ResolveReference(_ context.Context, ref secretref.Ref) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[ref.Ref]++
	value, ok := s.values[ref.Ref]
	if !ok {
		return "", errors.New("secret not found")
	}
	return value, nil
}

func (s *countingSecrets) callCount(ref string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[ref]
}

func newHostKeys(t *testing.T) inventory.HostKeyStore {
	t.Helper()
	store, err := persiststore.NewHostKeyStore(testutil.NewMemoryBackend().Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	return store
}

func fingerprint(server *harness.SSHServer) string {
	return ssh.FingerprintSHA256(server.HostKey)
}

// startNode starts an SSH server and returns the node that reaches it with
// a password from passwordRef, with the server's key pinned when pin is set.
func startNode(t *testing.T, name string, pin bool) (inventory.ResolvedNode, *harness.SSHServer) {
	t.Helper()
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: nodeUser, Password: nodePassword})
	port, err := strconv.Atoi(server.Port)
	require.NoError(t, err)
	node := inventory.ResolvedNode{
		Name:          name,
		Host:          "127.0.0.1",
		Port:          port,
		User:          nodeUser,
		Auth:          ir.SSHAuth{PasswordSecret: passwordRef},
		StrictHostKey: true,
	}
	if pin {
		node.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(server.HostKey)))
	}
	return node, server
}

func testNodes(t *testing.T, service *nodeconn.Service, nodes, targets []inventory.ResolvedNode) map[string]nodeconn.TestResult {
	t.Helper()
	results := map[string]nodeconn.TestResult{}
	service.Test(context.Background(), nodes, targets, func(r nodeconn.TestResult) {
		_, seen := results[r.Node]
		assert.False(t, seen, "node %q reported twice", r.Node)
		results[r.Node] = r
	})
	require.Len(t, results, len(targets))
	return results
}

func trust(t *testing.T, service *nodeconn.Service, nodes []inventory.ResolvedNode, requests ...nodeconn.TrustRequest) []nodeconn.TrustResult {
	t.Helper()
	var results []nodeconn.TrustResult
	service.Trust(context.Background(), nodes, requests, "alice", func(r nodeconn.TrustResult) {
		results = append(results, r)
	})
	require.Len(t, results, len(requests))
	return results
}

// A test reaches each node through its bastion, refuses an unknown key before
// any credential is sent, reads each shared secret once, and trusts nothing.
func TestServiceTest(t *testing.T) {
	t.Parallel()

	web01, web01Server := startNode(t, "web01", true)
	web02, web02Server := startNode(t, "web02", false)
	jump, jumpServer := startNode(t, "jump", true)
	db01, db01Server := startNode(t, "db01", true)
	db01.Bastion = "jump"
	nodes := []inventory.ResolvedNode{web01, web02, jump, db01}
	hostKeys := newHostKeys(t)
	secrets := newSecrets()

	results := testNodes(t, nodeconn.New(hostKeys, secrets), nodes, []inventory.ResolvedNode{web01, web02, db01})

	require.NoError(t, results["web01"].Err)
	assert.Equal(t, fingerprint(web01Server), results["web01"].Fingerprint)
	assert.Len(t, web01Server.Commands(), 1)

	require.NoError(t, results["db01"].Err)
	assert.Equal(t, fingerprint(db01Server), results["db01"].Fingerprint)
	assert.Equal(t, fingerprint(jumpServer), results["db01"].BastionFingerprint)

	require.Error(t, results["web02"].Err)
	require.NotNil(t, results["web02"].Refused)
	assert.Equal(t, "node", results["web02"].Refused.Hop)
	assert.Equal(t, fingerprint(web02Server), results["web02"].Refused.Presented)
	assert.Zero(t, web02Server.AuthAttempts(), "credentials sent to a node with an unknown key")
	_, err := hostKeys.Get(context.Background(), "web02")
	require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey, "a test trusted a key")

	assert.Equal(t, 1, secrets.callCount(passwordRef))
	for _, r := range results {
		assert.NotContains(t, errorText(r.Err), nodePassword)
	}
}

// A node whose secret ref does not resolve fails, naming the field and the
// ref, and the node receives nothing.
func TestServiceTestUnresolvedSecret(t *testing.T) {
	t.Parallel()

	web01, server := startNode(t, "web01", true)
	web01.Auth = ir.SSHAuth{PasswordSecret: "ssh/missing"}

	results := testNodes(t, nodeconn.New(newHostKeys(t), newSecrets()), []inventory.ResolvedNode{web01}, []inventory.ResolvedNode{web01})

	require.ErrorContains(t, results["web01"].Err, `failed to resolve nodes.web01.password from secret ref "ssh/missing"`)
	assert.Zero(t, server.AuthAttempts())
}

// Trusting a key stores it only when the node presents that key, and later
// tests accept it. Replacing another trusted key must be asked for.
func TestServiceTrust(t *testing.T) {
	t.Parallel()

	web01, server := startNode(t, "web01", false)
	nodes := []inventory.ResolvedNode{web01}
	hostKeys := newHostKeys(t)
	service := nodeconn.New(hostKeys, newSecrets())
	want := fingerprint(server)

	wrong := trust(t, service, nodes, nodeconn.TrustRequest{Node: "WEB01", Fingerprint: "SHA256:not-the-key"})
	require.ErrorContains(t, wrong[0].Err, want, "the error names the key the node presents")
	_, err := hostKeys.Get(context.Background(), "web01")
	require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)

	trusted := trust(t, service, nodes, nodeconn.TrustRequest{Node: "WEB01", Fingerprint: want})
	require.NoError(t, trusted[0].Err)
	assert.Equal(t, nodeconn.TrustResult{Node: "web01", Fingerprint: want, Outcome: nodeconn.TrustOutcomeTrusted}, trusted[0])
	stored, err := hostKeys.Get(context.Background(), "web01")
	require.NoError(t, err)
	assert.Equal(t, want, stored.Fingerprint)
	assert.Equal(t, "alice", stored.TrustedBy)
	assert.Zero(t, server.AuthAttempts(), "trusting sends the node no credential")

	again := trust(t, service, nodes, nodeconn.TrustRequest{Node: "web01", Fingerprint: want})
	assert.Equal(t, nodeconn.TrustOutcomeUnchanged, again[0].Outcome)

	results := testNodes(t, service, nodes, nodes)
	require.NoError(t, results["web01"].Err)

	// The server's key changes, as on a rebuilt host.
	_, err = hostKeys.Trust(context.Background(), "web01", newKey(t), "bob", want)
	require.NoError(t, err)
	previous, err := hostKeys.Get(context.Background(), "web01")
	require.NoError(t, err)

	refused := trust(t, service, nodes, nodeconn.TrustRequest{Node: "web01", Fingerprint: want})
	require.ErrorContains(t, refused[0].Err, "must be asked for")
	assert.Equal(t, previous.Fingerprint, refused[0].Previous)

	replaced := trust(t, service, nodes, nodeconn.TrustRequest{Node: "web01", Fingerprint: want, Replace: true})
	require.NoError(t, replaced[0].Err)
	assert.Equal(t, nodeconn.TrustOutcomeReplaced, replaced[0].Outcome)
	assert.Equal(t, previous.Fingerprint, replaced[0].Previous)
}

// A node behind a bastion is trusted through the bastion, whose own key must
// be accepted first.
func TestServiceTrustThroughBastion(t *testing.T) {
	t.Parallel()

	jump, jumpServer := startNode(t, "jump", false)
	db01, db01Server := startNode(t, "db01", false)
	db01.Bastion = "jump"
	nodes := []inventory.ResolvedNode{jump, db01}
	service := nodeconn.New(newHostKeys(t), newSecrets())
	request := nodeconn.TrustRequest{Node: "db01", Fingerprint: fingerprint(db01Server)}

	refused := trust(t, service, nodes, request)
	require.ErrorContains(t, refused[0].Err, `host key of bastion "jump" is unknown`)
	assert.Zero(t, jumpServer.AuthAttempts(), "credentials sent to an unknown bastion")

	trust(t, service, nodes, nodeconn.TrustRequest{Node: "jump", Fingerprint: fingerprint(jumpServer)})
	trusted := trust(t, service, nodes, request)
	require.NoError(t, trusted[0].Err)
	assert.Equal(t, nodeconn.TrustOutcomeTrusted, trusted[0].Outcome)
	assert.Zero(t, db01Server.AuthAttempts(), "trusting sends the node no credential")
}

func TestServiceTrustUnknownNode(t *testing.T) {
	t.Parallel()

	results := trust(t, nodeconn.New(newHostKeys(t), newSecrets()), nil, nodeconn.TrustRequest{Node: "web09", Fingerprint: "SHA256:x"})
	require.ErrorContains(t, results[0].Err, `node "web09" is not in the project's inventories`)
}

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, key := harness.NewSSHKey(t)
	return key
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
