// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	// eventWriteTimeout bounds the write of one event to a slow client.
	eventWriteTimeout = 30 * time.Second
	// eventHeartbeat is how often an idle stream sends a comment, so proxies
	// keep it open while slow nodes are tested.
	eventHeartbeat = 15 * time.Second
	// fingerprintPrefix starts every fingerprint a test reports.
	fingerprintPrefix = "SHA256:"
	// maxRequestNodes bounds the nodes or keys one request names, as the
	// OpenAPI schema does.
	maxRequestNodes = 1000
)

// Events of a node test or trust stream.
const (
	nodeEventResult = "result"
	nodeEventDone   = "done"
)

// TestInventoryNodeConnections tests the named nodes, or the nodes a
// selector matches, from the server, and streams one event per node.
func (a *API) TestInventoryNodeConnections(ctx context.Context, request api.TestInventoryNodeConnectionsRequestObject) (api.TestInventoryNodeConnectionsResponseObject, error) {
	service, err := a.nodeConnections()
	if err != nil {
		return nil, err
	}
	if err := a.requireOperatorOrAbove(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nodeTestRefused("the request names no nodes to test"), nil
	}
	if request.Body.Node != nil {
		return a.testUnsavedNode(ctx, service, request.Body)
	}
	if len(valueOf(request.Body.Accept)) > 0 {
		return nodeTestRefused("accept is allowed only with a node that is not stored"), nil
	}
	names := valueOf(request.Body.Nodes)
	selector := strings.TrimSpace(valueOf(request.Body.Labels))
	if (len(names) == 0) == (selector == "") {
		return nodeTestRefused("the request must give either nodes or labels"), nil
	}
	if len(names) > maxRequestNodes {
		return nodeTestRefused(fmt.Sprintf("the request names %d nodes; a test takes at most %d", len(names), maxRequestNodes)), nil
	}

	nodes, err := a.inventoryStore.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
	}
	var targets []inventory.ResolvedNode
	details := map[string]any{}
	if selector != "" {
		filters, err := inventory.ParseSelector(selector)
		if err != nil {
			return nodeTestRefused(err.Error()), nil
		}
		if targets = inventory.Select(nodes, filters); len(targets) == 0 {
			return nodeTestRefused("selector matched no nodes"), nil
		}
		details["labels"], details["matched"] = selector, len(targets)
	} else {
		var missing []string
		for _, name := range names {
			node, ok := findInventoryNode(nodes, name)
			switch {
			case !ok:
				missing = append(missing, name)
			case !slices.ContainsFunc(targets, func(n inventory.ResolvedNode) bool { return n.Name == node.Name }):
				targets = append(targets, node)
			}
		}
		if len(missing) > 0 {
			return nodeTestRefused(fmt.Sprintf("no node is named %s", strings.Join(missing, ", "))), nil
		}
		details["nodes"] = nodeNames(targets)
	}
	a.logAudit(ctx, audit.CategorySystem, "node_connection_test", details)

	return testStream(ctx, func(ctx context.Context, emit func(nodeconn.TestResult)) {
		service.Test(ctx, nodes, targets, emit)
	}), nil
}

// testUnsavedNode tests a node that is not stored yet, as it would be in the
// inventory the request names, with the host keys the user accepted pinned
// in memory. Nothing is stored or trusted. The record decides which host
// receives the inventory's credentials, so this takes the role that may
// store it.
func (a *API) testUnsavedNode(ctx context.Context, service *nodeconn.Service, body *api.InventoryNodeConnectionTestRequest) (api.TestInventoryNodeConnectionsResponseObject, error) {
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if len(valueOf(body.Nodes)) > 0 || strings.TrimSpace(valueOf(body.Labels)) != "" {
		return nodeTestRefused("the request must give only one of nodes, labels, and node"), nil
	}
	ref := strings.TrimSpace(valueOf(body.Inventory))
	if ref == "" {
		return nodeTestRefused("a node that is not stored needs the inventory it would join"), nil
	}
	raw, err := json.Marshal(*body.Node)
	if err != nil {
		return nil, fmt.Errorf("failed to read the node: %w", err)
	}
	node, err := inventory.DecodeNode(raw)
	if err != nil {
		return nodeTestRefused(err.Error()), nil
	}
	accepted := make([]nodeconn.Accepted, 0, len(valueOf(body.Accept)))
	for _, key := range valueOf(body.Accept) {
		if !strings.HasPrefix(key.Fingerprint, fingerprintPrefix) {
			return nodeTestRefused(fmt.Sprintf("the accepted fingerprint for node %q is not a SHA256 fingerprint, such as %s…", key.Node, fingerprintPrefix)), nil
		}
		accepted = append(accepted, nodeconn.Accepted{Node: key.Node, Fingerprint: key.Fingerprint})
	}

	all, err := a.inventoryStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list inventories: %w", err)
	}
	target, ok := findInventoryIn(all, ref)
	if !ok {
		return nodeTestRefused(fmt.Sprintf("inventory %q not found", ref)), nil
	}
	invs, err := inventory.WithNode(all, target.ID, node)
	if err != nil {
		return nodeTestRefused(err.Error()), nil
	}
	nodes, err := inventory.Resolve(invs)
	if err != nil {
		return nodeTestRefused(err.Error()), nil
	}
	resolved, _ := findInventoryNode(nodes, node.Name)
	a.logAudit(ctx, audit.CategorySystem, "node_connection_test", map[string]any{
		"node":      resolved.Name,
		"host":      resolved.Host,
		"inventory": target.Name,
		"stored":    false,
	})

	return testStream(ctx, func(ctx context.Context, emit func(nodeconn.TestResult)) {
		staged, err := service.Stage(ctx, nodes, accepted)
		if err != nil {
			emit(nodeconn.TestResult{Node: resolved.Name, Err: err})
			return
		}
		node, _ := findInventoryNode(staged, resolved.Name)
		service.Test(ctx, staged, []inventory.ResolvedNode{node}, emit)
	}), nil
}

// testStream answers a test with an event stream: a result event for each
// result test emits, then a done event with their counts.
func testStream(ctx context.Context, test func(ctx context.Context, emit func(nodeconn.TestResult))) nodeEventStream {
	return nodeEventStream{ctx: ctx, run: func(ctx context.Context, send func(string, any)) {
		var summary api.InventoryNodeConnectionTestSummary
		test(ctx, func(result nodeconn.TestResult) {
			if result.Err == nil {
				summary.Passed++
			} else {
				summary.Failed++
			}
			send(nodeEventResult, toNodeConnectionTest(result))
		})
		send(nodeEventDone, summary)
	}}
}

// TrustInventoryNodeHostKeys trusts the host key of each node named, when
// the key the node presents has the fingerprint the user saw, and streams
// one event per node.
func (a *API) TrustInventoryNodeHostKeys(ctx context.Context, request api.TrustInventoryNodeHostKeysRequestObject) (api.TrustInventoryNodeHostKeysResponseObject, error) {
	service, err := a.nodeConnections()
	if err != nil {
		return nil, err
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil || len(request.Body.Keys) == 0 {
		return hostKeyTrustRefused("the request names no host keys to trust"), nil
	}
	if len(request.Body.Keys) > maxRequestNodes {
		return hostKeyTrustRefused(fmt.Sprintf("the request names %d keys; a trust takes at most %d", len(request.Body.Keys), maxRequestNodes)), nil
	}
	requests := make([]nodeconn.TrustRequest, 0, len(request.Body.Keys))
	for _, entry := range request.Body.Keys {
		if strings.TrimSpace(entry.Node) == "" {
			return hostKeyTrustRefused("an entry names no node"), nil
		}
		if slices.ContainsFunc(requests, func(r nodeconn.TrustRequest) bool { return strings.EqualFold(r.Node, entry.Node) }) {
			return hostKeyTrustRefused(fmt.Sprintf("node %q has more than one entry", entry.Node)), nil
		}
		if !strings.HasPrefix(entry.Fingerprint, fingerprintPrefix) {
			return hostKeyTrustRefused(fmt.Sprintf("the fingerprint for node %q is not a SHA256 fingerprint, such as %s…", entry.Node, fingerprintPrefix)), nil
		}
		if replace := valueOf(entry.Replace); replace != "" && !strings.HasPrefix(replace, fingerprintPrefix) {
			return hostKeyTrustRefused(fmt.Sprintf("the replaced fingerprint for node %q is not a SHA256 fingerprint, such as %s…", entry.Node, fingerprintPrefix)), nil
		}
		requests = append(requests, nodeconn.TrustRequest{Node: entry.Node, Fingerprint: entry.Fingerprint, Replace: valueOf(entry.Replace)})
	}

	nodes, err := a.inventoryStore.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
	}
	var by string
	if user, ok := auth.UserFromContext(ctx); ok {
		by = user.Username
	}
	return nodeEventStream{ctx: ctx, run: func(ctx context.Context, send func(string, any)) {
		var summary api.InventoryHostKeyTrustSummary
		service.Trust(ctx, nodes, requests, by, func(result nodeconn.TrustResult) {
			if result.Err != nil {
				summary.Failed++
			} else {
				summary.Trusted++
			}
			if result.Outcome == nodeconn.TrustOutcomeTrusted || result.Outcome == nodeconn.TrustOutcomeReplaced {
				a.logAudit(ctx, audit.CategorySystem, "node_host_key_trust", map[string]any{
					"node":        result.Node,
					"fingerprint": result.Fingerprint,
					"previous":    result.Previous,
				})
			}
			send(nodeEventResult, toHostKeyTrust(result))
		})
		send(nodeEventDone, summary)
	}}, nil
}

// nodeConnections returns the service that tests and trusts nodes.
func (a *API) nodeConnections() (*nodeconn.Service, error) {
	if a.inventoryStore == nil || a.hostKeyStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	var secrets nodeconn.SecretResolver
	if a.secretStore != nil {
		secrets = secretpkg.NewReferenceResolver(a.secretStore, "")
	}
	return nodeconn.New(a.hostKeyStore, secrets), nil
}

func nodeTestRefused(message string) api.TestInventoryNodeConnections400JSONResponse {
	return api.TestInventoryNodeConnections400JSONResponse{Code: api.ErrorCodeBadRequest, Message: message}
}

func hostKeyTrustRefused(message string) api.TrustInventoryNodeHostKeys400JSONResponse {
	return api.TrustInventoryNodeHostKeys400JSONResponse{Code: api.ErrorCodeBadRequest, Message: message}
}

func findInventoryNode(nodes []inventory.ResolvedNode, name string) (inventory.ResolvedNode, bool) {
	for _, node := range nodes {
		if strings.EqualFold(node.Name, name) {
			return node, true
		}
	}
	return inventory.ResolvedNode{}, false
}

func nodeNames(nodes []inventory.ResolvedNode) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	return names
}

func toNodeConnectionTest(result nodeconn.TestResult) api.InventoryNodeConnectionTest {
	test := api.InventoryNodeConnectionTest{
		Node:               result.Node,
		Ok:                 result.Err == nil,
		Fingerprint:        ptrOf(result.Fingerprint),
		BastionFingerprint: ptrOf(result.BastionFingerprint),
		DurationMs:         result.Duration.Milliseconds(),
	}
	if result.Err != nil {
		test.Error = ptrOf(result.Err.Error())
	}
	if refused := result.Refused; refused != nil {
		status := api.InventoryHostKeyRefusalStatusUnknown
		if len(refused.Expected) > 0 {
			status = api.InventoryHostKeyRefusalStatusChanged
		}
		test.HostKey = &api.InventoryHostKeyRefusal{
			Hop:       api.InventoryHostKeyRefusalHop(refused.Hop),
			Node:      refused.Node,
			Status:    status,
			Presented: ptrOf(refused.Presented),
			Expected:  append([]string{}, refused.Expected...),
			Trusted:   ptrOf(refused.Trusted),
		}
	}
	return test
}

func toHostKeyTrust(result nodeconn.TrustResult) api.InventoryHostKeyTrust {
	trust := api.InventoryHostKeyTrust{
		Node:        result.Node,
		Fingerprint: result.Fingerprint,
		Previous:    ptrOf(result.Previous),
	}
	if result.Outcome != "" {
		trust.Outcome = new(api.InventoryHostKeyTrustOutcome(result.Outcome))
	}
	if result.Err != nil {
		trust.Error = ptrOf(result.Err.Error())
	}
	return trust
}

// nodeEventStream answers a node test or trust with a server-sent event
// stream. run sends each event, and the stream ends when run returns. The
// context run gets ends when the client stops reading.
type nodeEventStream struct {
	ctx context.Context
	run func(ctx context.Context, send func(event string, data any))
}

func (s nodeEventStream) VisitTestInventoryNodeConnectionsResponse(w http.ResponseWriter) error {
	return s.write(w)
}

func (s nodeEventStream) VisitTrustInventoryNodeHostKeysResponse(w http.ResponseWriter) error {
	return s.write(w)
}

type nodeEvent struct {
	name string
	data any
}

func (s nodeEventStream) write(w http.ResponseWriter) error {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	events := make(chan nodeEvent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx, func(name string, data any) {
			select {
			case events <- nodeEvent{name: name, data: data}:
			case <-ctx.Done():
			}
		})
	}()

	rc := http.NewResponseController(w)
	heartbeat := time.NewTicker(eventHeartbeat)
	defer heartbeat.Stop()
	for {
		var frame string
		select {
		case <-done:
			return nil
		case event := <-events:
			data, err := json.Marshal(event.data)
			if err != nil {
				return fmt.Errorf("failed to encode %s event: %w", event.name, err)
			}
			frame = fmt.Sprintf("event: %s\ndata: %s\n\n", event.name, data)
		case <-heartbeat.C:
			frame = ": keepalive\n\n"
		}
		if err := rc.SetWriteDeadline(time.Now().Add(eventWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := w.Write([]byte(frame)); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
	}
}

// isNodeEventStream matches the node test and trust requests, which answer
// with an event stream.
func isNodeEventStream(r *http.Request, apiBasePath string) bool {
	if r.Method != http.MethodPost {
		return false
	}
	base := strings.TrimRight(apiBasePath, "/")
	return r.URL.Path == base+"/node-connection-tests" || r.URL.Path == base+"/node-host-keys"
}
