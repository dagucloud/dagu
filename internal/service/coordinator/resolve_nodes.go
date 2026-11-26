// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/sshconn"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const nodeAccessDenied = "node access denied"

// ResolveNodes answers the nodes a run on a worker targets (RFC 012 §Reading
// the inventory from a run). Only an attempt with a fresh lease whose DAG
// targets nodes is answered. Credentials are answered as secret refs.
func (h *Handler) ResolveNodes(ctx context.Context, req *coordinatorv1.ResolveNodesRequest) (*coordinatorv1.ResolveNodesResponse, error) {
	if h.nodeInventory == nil {
		return nil, status.Error(codes.FailedPrecondition, "node inventory is not configured")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "resolve nodes request is required")
	}
	var filters []ir.LabelFilter
	switch selector := req.GetSelector(); {
	case selector != "" && len(req.GetNames()) > 0:
		return nil, status.Error(codes.InvalidArgument, "selector and names cannot both be set")
	case selector != "":
		parsed, err := inventory.ParseSelector(selector)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid selector %q: %v", selector, err)
		}
		filters = parsed
	case len(req.GetNames()) == 0:
		return nil, status.Error(codes.InvalidArgument, "selector or names is required")
	}

	dag, err := h.leaseDAG(ctx, req.GetWorkerId(), req.GetAttemptKey(), req.GetAttemptId(), req.GetDagName(), nodeAccessDenied)
	if err != nil {
		return nil, err
	}
	if !targetsNodes(dag) {
		return nil, status.Error(codes.PermissionDenied, nodeAccessDenied)
	}

	candidates, err := h.candidateNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	var matched []inventory.ResolvedNode
	if filters != nil {
		matched = inventory.Select(candidates, filters)
	} else {
		matched = namedNodes(candidates, req.GetNames())
	}
	bastions := bastionsOf(candidates, matched)

	resp := &coordinatorv1.ResolveNodesResponse{
		Nodes:    resolvedNodesToProto(matched),
		Bastions: resolvedNodesToProto(bastions),
	}
	resp.TrustedHostKeys, err = h.trustedHostKeysOf(ctx, slices.Concat(matched, bastions))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return resp, nil
}

// candidateNodes lists the nodes a run on a worker can target, in name
// order: the project's inventory nodes and worker nodes.
func (h *Handler) candidateNodes(ctx context.Context) ([]inventory.ResolvedNode, error) {
	nodes, err := h.nodeInventory.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	workers, err := h.workerNodes.list(ctx)
	if err != nil {
		return nil, err
	}
	return inventory.WithWorkers(nodes, workers), nil
}

// authorizeNodeSecret lets a run read ref for node when the run targets
// nodes, node is an enabled node, and its connection uses ref, through its
// bastion included.
func (h *Handler) authorizeNodeSecret(ctx context.Context, dag *ir.DAG, node, ref string) error {
	if h.nodeInventory == nil {
		return status.Error(codes.FailedPrecondition, "node inventory is not configured")
	}
	if !targetsNodes(dag) {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}
	// Worker nodes have no credentials.
	candidates, err := h.nodeInventory.Nodes(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	target := findNode(candidates, node)
	if target == nil || target.Disabled {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}
	var bastion *inventory.ResolvedNode
	if target.Bastion != "" {
		bastion = findNode(candidates, target.Bastion)
	}
	for _, used := range sshconn.NodeSecretRefs(*target, bastion) {
		if used.Ref == ref {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
}

// targetsNodes reports whether a run of dag reads nodes: a step of it, a
// foreach body or a handler included, sets nodes, or it runs bound to one.
func targetsNodes(dag *ir.DAG) bool {
	if dag.NodeRequired {
		return true
	}
	var setsNodes func(steps []ir.Step) bool
	setsNodes = func(steps []ir.Step) bool {
		for _, step := range steps {
			if step.Nodes != nil || (step.Foreach != nil && setsNodes(step.Foreach.Steps)) {
				return true
			}
		}
		return false
	}
	if setsNodes(dag.Steps) {
		return true
	}
	for _, handler := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Failure, dag.HandlerOn.Success, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if handler != nil && setsNodes([]ir.Step{*handler}) {
			return true
		}
	}
	return false
}

// namedNodes returns the nodes that names lists, each once, in name order.
// Names are compared without case, and one that names no node is left out.
func namedNodes(nodes []inventory.ResolvedNode, names []string) []inventory.ResolvedNode {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = struct{}{}
	}
	return nodesNamedIn(nodes, wanted)
}

// bastionsOf returns each node that a node of matched connects through,
// once, in name order.
func bastionsOf(nodes, matched []inventory.ResolvedNode) []inventory.ResolvedNode {
	wanted := make(map[string]struct{})
	for _, node := range matched {
		if node.Bastion != "" {
			wanted[strings.ToLower(node.Bastion)] = struct{}{}
		}
	}
	return nodesNamedIn(nodes, wanted)
}

// nodesNamedIn returns the nodes whose lowercased names are in names, in the
// order given. Node names are ASCII.
func nodesNamedIn(nodes []inventory.ResolvedNode, names map[string]struct{}) []inventory.ResolvedNode {
	var out []inventory.ResolvedNode
	for _, node := range nodes {
		if _, ok := names[strings.ToLower(node.Name)]; ok {
			out = append(out, node)
		}
	}
	return out
}

func findNode(nodes []inventory.ResolvedNode, name string) *inventory.ResolvedNode {
	i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, name) })
	if i < 0 {
		return nil
	}
	return &nodes[i]
}

// trustedHostKeysOf returns the host key trusted for each SSH node of nodes
// that has one, once per node, under the node's name as the inventory spells
// it.
func (h *Handler) trustedHostKeysOf(ctx context.Context, nodes []inventory.ResolvedNode) ([]*coordinatorv1.TrustedHostKey, error) {
	if h.trustedHostKeys == nil {
		return nil, nil
	}
	var keys []*coordinatorv1.TrustedHostKey
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.Kind == inventory.NodeKindWorker {
			continue
		}
		folded := strings.ToLower(node.Name)
		if _, dup := seen[folded]; dup {
			continue
		}
		seen[folded] = struct{}{}
		key, err := h.trustedHostKeys.Get(ctx, node.Name)
		if errors.Is(err, inventory.ErrNoTrustedHostKey) {
			continue
		}
		if err != nil {
			return nil, err
		}
		keys = append(keys, &coordinatorv1.TrustedHostKey{
			Node:        node.Name,
			Key:         key.Key,
			Fingerprint: key.Fingerprint,
		})
	}
	return keys, nil
}

func resolvedNodesToProto(nodes []inventory.ResolvedNode) []*coordinatorv1.ResolvedNode {
	out := make([]*coordinatorv1.ResolvedNode, len(nodes))
	for i, node := range nodes {
		// Literal passwords and passphrases are refused in inventories, so
		// the credentials are a key path and secret refs only.
		out[i] = &coordinatorv1.ResolvedNode{
			Name:             node.Name,
			Kind:             nodeKindToProto(node.Kind),
			InventoryId:      node.InventoryID,
			InventoryName:    node.InventoryName,
			Host:             node.Host,
			Port:             int32(node.Port), //nolint:gosec // inventory ports are 1 to 65535
			User:             node.User,
			KeyPath:          node.Auth.Key,
			KeySecret:        node.Auth.KeySecret,
			PasswordSecret:   node.Auth.PasswordSecret,
			PassphraseSecret: node.Auth.PassphraseSecret,
			Bastion:          node.Bastion,
			HostKey:          node.HostKey,
			StrictHostKey:    node.StrictHostKey,
			Shell:            node.Shell,
			TimeoutNanos:     int64(node.Timeout),
			Labels:           node.Labels.Strings(),
			Description:      node.Description,
			Disabled:         node.Disabled,
		}
	}
	return out
}

func nodeKindToProto(kind inventory.NodeKind) coordinatorv1.NodeKind {
	if kind == inventory.NodeKindWorker {
		return coordinatorv1.NodeKind_NODE_KIND_WORKER
	}
	return coordinatorv1.NodeKind_NODE_KIND_SSH
}
