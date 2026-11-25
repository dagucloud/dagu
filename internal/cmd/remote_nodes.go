// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
	"github.com/dagucloud/dagu/v2/internal/sshconn"
)

const (
	// remoteNodesPerPage is the most nodes GET /nodes returns per page.
	remoteNodesPerPage = 200
	// remoteTestBatch is the most nodes one test request names, as the
	// server allows.
	remoteTestBatch = 1000
	// maxRemoteEventBytes bounds one line of an event stream.
	maxRemoteEventBytes = 1 << 20
)

// remoteNodesList prints the nodes --labels matches on the server of a
// remote context, limited to --inventory when it is given.
func remoteNodesList(ctx *Context, format string) error {
	var selector *string
	if ctx.Command.Flags().Changed("labels") {
		value, err := ctx.StringParam("labels")
		if err != nil {
			return err
		}
		// The selector is checked here as a local list checks it, so an
		// invalid one fails alike.
		if _, err := inventory.ParseSelector(value); err != nil {
			return err
		}
		selector = &value
	}
	ref, err := ctx.StringParam("inventory")
	if err != nil {
		return err
	}
	var inventoryID string
	if ref != "" {
		all, err := ctx.Remote.remoteInventories(ctx)
		if err != nil {
			return err
		}
		inv, err := findInventoryIn(all, ref)
		if err != nil {
			return err
		}
		inventoryID = inv.ID
	}
	listing, err := ctx.Remote.remoteNodes(ctx, selector, inventoryID)
	if err != nil {
		return err
	}
	return printNodes(ctx, format, listing)
}

// remoteNodesExport prints the inventories of the server of a remote
// context, or the one ref names, as a local export prints them.
func remoteNodesExport(ctx *Context, ref string) error {
	all, err := ctx.Remote.remoteInventories(ctx)
	if err != nil {
		return err
	}
	if ref != "" {
		inv, err := findInventoryIn(all, ref)
		if err != nil {
			return err
		}
		all = []inventory.Stored{inv}
	}
	stored := make([]inventory.Stored, 0, len(all))
	for _, summary := range all {
		inv, err := ctx.Remote.remoteInventory(ctx, summary.ID)
		if err != nil {
			return err
		}
		stored = append(stored, inv)
	}
	return writeExport(ctx, stored)
}

// remoteNodesRemove removes the node named name from its inventory on the
// server of a remote context.
func remoteNodesRemove(ctx *Context, name string) error {
	listing, err := ctx.Remote.remoteNode(ctx, name)
	if err != nil {
		return err
	}
	// A worker node is in no inventory, as a local remove finds.
	if len(listing.nodes) == 0 {
		return fmt.Errorf("node %q not found", name)
	}
	node := listing.nodes[0]
	holder, err := ctx.Remote.remoteInventory(ctx, node.InventoryID)
	if err != nil {
		return err
	}
	if err := ctx.Remote.remoteDeleteNode(ctx, holder.ID, node.Name, holder.Version); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(ctx.Command.OutOrStdout(), "removed %s from %s\n", node.Name, holder.Name)
	return nil
}

// remoteInventory is an inventory as GET /inventories/{id} returns it: the
// record, which an export prints, and the version a change names.
type remoteInventory struct {
	inventory.Inventory
	Version int64 `json:"version"`
}

// remoteNodes lists the nodes the selector matches, or every node when
// selector is nil, limited to the inventory inventoryID when it is set, and
// the workers that are not nodes.
func (c *remoteClient) remoteNodes(ctx context.Context, selector *string, inventoryID string) (nodeListing, error) {
	var listing nodeListing
	for page := 1; ; page++ {
		query := map[string]string{"page": strconv.Itoa(page), "perPage": strconv.Itoa(remoteNodesPerPage), "inventory": inventoryID}
		if selector != nil {
			query["labels"] = *selector
		}
		var out api.InventoryNodeListResponse
		if err := c.do(ctx, http.MethodGet, "/nodes", nil, &out, query); err != nil {
			return nodeListing{}, err
		}
		for _, node := range out.Nodes {
			if err := listing.add(node); err != nil {
				return nodeListing{}, err
			}
		}
		// Every page lists every conflict.
		if page == 1 {
			for _, conflict := range out.WorkerConflicts {
				listing.conflicts = append(listing.conflicts, inventory.WorkerConflict{
					Worker: inventory.WorkerNode{Name: conflict.Worker},
					Node:   inventory.ResolvedNode{InventoryID: conflict.Inventory.Id, InventoryName: conflict.Inventory.Name},
				})
			}
		}
		if !out.Pagination.HasNextPage || len(out.Nodes) == 0 {
			return listing, nil
		}
	}
}

// remoteNode returns a listing of the one node named name, or an error
// naming it when the server holds none.
func (c *remoteClient) remoteNode(ctx context.Context, name string) (nodeListing, error) {
	var out api.InventoryNode
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(name), nil, &out, nil)
	if remote, ok := errors.AsType[*remoteError](err); ok && remote.NotFound() {
		return nodeListing{}, fmt.Errorf("node %q not found", name)
	}
	if err != nil {
		return nodeListing{}, err
	}
	var listing nodeListing
	return listing, listing.add(out)
}

// remoteForgetWorkerNode forgets the offline worker node named name.
func (c *remoteClient) remoteForgetWorkerNode(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/nodes/"+url.PathEscape(name), nil, nil, nil)
	if remote, ok := errors.AsType[*remoteError](err); ok && remote.NotFound() {
		return fmt.Errorf("node %q not found", name)
	}
	return err
}

// remoteInventories lists the inventories, without their nodes.
func (c *remoteClient) remoteInventories(ctx context.Context) ([]inventory.Stored, error) {
	var out api.InventoryListResponse
	if err := c.do(ctx, http.MethodGet, "/inventories", nil, &out, nil); err != nil {
		return nil, err
	}
	all := make([]inventory.Stored, 0, len(out.Inventories))
	for _, summary := range out.Inventories {
		all = append(all, inventory.Stored{
			Inventory: inventory.Inventory{ID: summary.Id, Name: summary.Name, Description: derefString(summary.Description)},
			Version:   summary.Version,
		})
	}
	return all, nil
}

// remoteInventory returns the inventory id with its nodes.
func (c *remoteClient) remoteInventory(ctx context.Context, id string) (inventory.Stored, error) {
	var out remoteInventory
	if err := c.do(ctx, http.MethodGet, "/inventories/"+url.PathEscape(id), nil, &out, nil); err != nil {
		return inventory.Stored{}, err
	}
	return inventory.Stored{Inventory: out.Inventory, Version: out.Version}, nil
}

// remoteImport imports data, a JSON array of inventories, as it was read.
func (c *remoteClient) remoteImport(ctx context.Context, data []byte, opts inventory.ImportOptions) (inventory.ImportResult, error) {
	query := map[string]string{"replace": strconv.FormatBool(opts.Replace), "dryRun": strconv.FormatBool(opts.DryRun)}
	var out api.InventoryImportResult
	if err := c.do(ctx, http.MethodPost, "/inventory-imports", json.RawMessage(data), &out, query); err != nil {
		return inventory.ImportResult{}, ruleErrorsOf(err)
	}
	return inventory.ImportResult{Created: out.Created, Replaced: out.Replaced}, nil
}

// remoteAddNode adds node to the inventory id at version, and trusts the
// accepted keys once it is stored.
func (c *remoteClient) remoteAddNode(ctx context.Context, id string, version int64, node inventory.Node, accepted []nodeconn.Accepted) (inventory.Stored, error) {
	body := map[string]any{"node": node}
	if len(accepted) > 0 {
		body["trust"] = acceptedKeysOf(accepted)
	}
	var out remoteInventory
	query := map[string]string{"version": strconv.FormatInt(version, 10)}
	if err := c.do(ctx, http.MethodPost, "/inventories/"+url.PathEscape(id)+"/nodes", body, &out, query); err != nil {
		return inventory.Stored{}, ruleErrorsOf(err)
	}
	return inventory.Stored{Inventory: out.Inventory, Version: out.Version}, nil
}

// remoteDeleteNode removes the node named name from the inventory id at
// version.
func (c *remoteClient) remoteDeleteNode(ctx context.Context, id, name string, version int64) error {
	query := map[string]string{"version": strconv.FormatInt(version, 10)}
	path := "/inventories/" + url.PathEscape(id) + "/nodes/" + url.PathEscape(name)
	return ruleErrorsOf(c.do(ctx, http.MethodDelete, path, nil, nil, query))
}

// stream posts body to path and passes each event of the server-sent event
// stream that answers to handle, until the done event. A stream lasts as
// long as the nodes take, so it is bounded by ctx, not by the client's
// request timeout; the server keeps it alive while nodes are slow.
func (c *remoteClient) stream(ctx context.Context, path string, body any, handle func(event string, data []byte) error) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := (&http.Client{Transport: c.client.Transport}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ruleErrorsOf(decodeRemoteError(resp))
	}
	return readEvents(resp.Body, handle)
}

// readEvents reads a server-sent event stream and passes each event to
// handle, until the done event. A keepalive comment carries no event. A
// stream that ends before done is an error.
func readEvents(r io.Reader, handle func(event string, data []byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRemoteEventBytes)
	var event string
	var data []byte
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if event != "" {
				if err := handle(event, data); err != nil {
					return err
				}
				if event == "done" {
					return nil
				}
			}
			event, data = "", nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("the server closed the stream before every node finished")
}

// remoteTester tests nodes and trusts host keys on a remote server.
type remoteTester struct {
	client *remoteClient
}

func (r remoteTester) test(ctx context.Context, targets []inventory.ResolvedNode, emit func(nodeconn.TestResult)) error {
	for start := 0; start < len(targets); start += remoteTestBatch {
		names := nodeNamesOf(targets[start:min(start+remoteTestBatch, len(targets))])
		err := r.client.stream(ctx, "/node-connection-tests", map[string]any{"nodes": names}, func(event string, data []byte) error {
			return emitTestResult(event, data, emit)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r remoteTester) trust(ctx context.Context, requests []nodeconn.TrustRequest, _ string, emit func(nodeconn.TrustResult)) error {
	for start := 0; start < len(requests); start += remoteTestBatch {
		batch := requests[start:min(start+remoteTestBatch, len(requests))]
		keys := make([]api.InventoryHostKeyTrustEntry, 0, len(batch))
		for _, request := range batch {
			entry := api.InventoryHostKeyTrustEntry{Node: request.Node, Fingerprint: request.Fingerprint}
			if request.Replace != "" {
				entry.Replace = &request.Replace
			}
			keys = append(keys, entry)
		}
		err := r.client.stream(ctx, "/node-host-keys", map[string]any{"keys": keys}, func(event string, data []byte) error {
			if event != "result" {
				return nil
			}
			var result api.InventoryHostKeyTrust
			if err := json.Unmarshal(data, &result); err != nil {
				return fmt.Errorf("invalid trust result: %w", err)
			}
			emit(trustResultOf(result))
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// remoteAdder tests a node that is not stored yet on a remote server, and
// adds it there.
type remoteAdder struct {
	client *remoteClient
	target inventory.Stored
	node   inventory.Node
}

func (r remoteAdder) test(ctx context.Context, accepted []nodeconn.Accepted) (nodeconn.TestResult, error) {
	body := map[string]any{"node": r.node}
	if len(accepted) > 0 {
		body["accept"] = acceptedKeysOf(accepted)
	}
	var result nodeconn.TestResult
	err := r.client.stream(ctx, "/inventories/"+url.PathEscape(r.target.ID)+"/node-tests", body, func(event string, data []byte) error {
		return emitTestResult(event, data, func(r nodeconn.TestResult) { result = r })
	})
	return result, err
}

func (r remoteAdder) add(ctx context.Context, accepted []nodeconn.Accepted, _ string) (inventory.Stored, []inventory.TrustedHostKey, error) {
	stored, err := r.client.remoteAddNode(ctx, r.target.ID, r.target.Version, r.node, accepted)
	if remote, ok := errors.AsType[*remoteError](err); ok && remote.StatusCode >= http.StatusInternalServerError {
		// The server stores the node before it trusts the keys, so a
		// failure may come after the node is stored.
		current, readErr := r.client.remoteInventory(ctx, r.target.ID)
		if readErr != nil {
			return inventory.Stored{}, nil, uncertainAdd{node: r.node.Name, err: err, readErr: readErr}
		}
		if holdsNode(current, r.node.Name) {
			return current, nil, partialAdd{node: r.node.Name, err: err}
		}
	}
	if err != nil {
		return inventory.Stored{}, nil, err
	}
	// The server trusts the accepted keys before it answers.
	trusted := make([]inventory.TrustedHostKey, 0, len(accepted))
	for _, key := range accepted {
		trusted = append(trusted, inventory.TrustedHostKey{Node: key.Node, Fingerprint: key.Fingerprint})
	}
	return stored, trusted, nil
}

// partialAdd is the error of an add whose node the server stored before
// trusting its keys failed.
type partialAdd struct {
	node string
	err  error
}

func (e partialAdd) Error() string {
	return fmt.Sprintf("node %q was added, but its host keys were not trusted: %v; trust them with 'dagu nodes test %s'", e.node, e.err, e.node)
}

func (e partialAdd) Unwrap() error { return e.err }

// uncertainAdd is the error of an add that failed on the server when the
// inventory could not be read again to learn whether the node was stored.
type uncertainAdd struct {
	node    string
	err     error
	readErr error
}

func (e uncertainAdd) Error() string {
	return fmt.Sprintf("node %q may have been added: the server failed (%v), and reading its inventory again failed (%v); check with 'dagu nodes show %s' before adding it again", e.node, e.err, e.readErr, e.node)
}

func (e uncertainAdd) Unwrap() error { return e.err }

// holdsNode reports whether inv holds a node named name, compared without
// case.
func holdsNode(inv inventory.Stored, name string) bool {
	for _, node := range inv.Nodes {
		if strings.EqualFold(node.Name, name) {
			return true
		}
	}
	return false
}

// emitTestResult passes a result event of a test stream to emit.
func emitTestResult(event string, data []byte, emit func(nodeconn.TestResult)) error {
	if event != "result" {
		return nil
	}
	var result api.InventoryNodeConnectionTest
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("invalid test result: %w", err)
	}
	emit(testResultOf(result))
	return nil
}

// testResultOf maps a test result of the API to the one the local test
// reports, so both print and prompt alike.
func testResultOf(result api.InventoryNodeConnectionTest) nodeconn.TestResult {
	out := nodeconn.TestResult{
		Node:               result.Node,
		Fingerprint:        derefString(result.Fingerprint),
		BastionFingerprint: derefString(result.BastionFingerprint),
		Duration:           time.Duration(result.DurationMs) * time.Millisecond,
	}
	if !result.Ok {
		out.Err = errors.New(derefString(result.Error))
	}
	if refused := result.HostKey; refused != nil {
		out.Refused = &sshconn.HostKeyRefusal{
			Hop:       string(refused.Hop),
			Node:      refused.Node,
			Presented: derefString(refused.Presented),
			Expected:  refused.Expected,
			Trusted:   derefString(refused.Trusted),
		}
	}
	return out
}

func trustResultOf(result api.InventoryHostKeyTrust) nodeconn.TrustResult {
	out := nodeconn.TrustResult{
		Node:        result.Node,
		Fingerprint: result.Fingerprint,
		Previous:    derefString(result.Previous),
	}
	if result.Outcome != nil {
		out.Outcome = nodeconn.TrustOutcome(*result.Outcome)
	}
	if result.Error != nil {
		out.Err = errors.New(*result.Error)
	}
	return out
}

// add appends a node of the API to the listing as the local commands hold
// it, so both print alike.
func (l *nodeListing) add(node api.InventoryNode) error {
	if node.Kind == api.InventoryNodeKindWorker {
		l.workers = append(l.workers, inventory.WorkerNode{
			Name:       node.Name,
			Host:       node.Host,
			Labels:     ir.NewLabels(node.Labels),
			Online:     derefBool(node.Online),
			LastSeenAt: valueOrZero(node.LastSeenAt),
		})
		return nil
	}
	resolved, err := resolvedNodeOf(node)
	if err != nil {
		return err
	}
	l.nodes = append(l.nodes, resolved)
	return nil
}

// resolvedNodeOf maps an SSH node of the API to the resolved node the local
// commands print, so both print alike.
func resolvedNodeOf(node api.InventoryNode) (inventory.ResolvedNode, error) {
	timeout, err := time.ParseDuration(derefString(node.Timeout))
	if err != nil {
		return inventory.ResolvedNode{}, fmt.Errorf("node %q: invalid timeout %q: %w", node.Name, derefString(node.Timeout), err)
	}
	ref, auth := valueOrZero(node.Inventory), valueOrZero(node.Auth)
	return inventory.ResolvedNode{
		Name:          node.Name,
		Host:          node.Host,
		InventoryID:   ref.Id,
		InventoryName: ref.Name,
		Port:          valueOrZero(node.Port),
		User:          derefString(node.User),
		Auth: ir.SSHAuth{
			Key:              derefString(auth.KeyPath),
			KeySecret:        derefString(auth.KeySecret),
			PasswordSecret:   derefString(auth.PasswordSecret),
			PassphraseSecret: derefString(auth.PassphraseSecret),
		},
		Bastion:       derefString(node.Bastion),
		HostKey:       derefString(node.HostKey),
		StrictHostKey: derefBool(node.StrictHostKey),
		Shell:         derefString(node.Shell),
		Timeout:       timeout,
		Labels:        ir.NewLabels(node.Labels),
		Description:   derefString(node.Description),
		Disabled:      node.Disabled,
	}, nil
}

// valueOrZero returns the value v points to, or the zero value when v is
// nil, as the API leaves out the fields a node of the other kind has.
func valueOrZero[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}
	return *v
}

// ruleErrorsOf returns the broken rules a refused change lists as the
// errors the local commands report, so both print alike. Any other error is
// returned as it is.
func ruleErrorsOf(err error) error {
	remote, ok := errors.AsType[*remoteError](err)
	if !ok || len(remote.Rules) == 0 {
		return err
	}
	errs := make([]error, 0, len(remote.Rules))
	for _, rule := range remote.Rules {
		// The server leaves out an empty inventory name, so a rule is told
		// from a plain error by its field too.
		if rule.Inventory == nil && rule.Field == nil {
			errs = append(errs, errors.New(rule.Message))
			continue
		}
		errs = append(errs, &inventory.Error{
			Inventory: derefString(rule.Inventory),
			Node:      derefString(rule.Node),
			Field:     derefString(rule.Field),
			Err:       errors.New(rule.Message),
		})
	}
	return errors.Join(errs...)
}

func acceptedKeysOf(accepted []nodeconn.Accepted) []api.InventoryAcceptedHostKey {
	keys := make([]api.InventoryAcceptedHostKey, 0, len(accepted))
	for _, key := range accepted {
		keys = append(keys, api.InventoryAcceptedHostKey{Node: key.Node, Fingerprint: key.Fingerprint})
	}
	return keys
}

func nodeNamesOf(nodes []inventory.ResolvedNode) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	return names
}
