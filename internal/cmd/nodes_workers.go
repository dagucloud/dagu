// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// The kinds of node, as the API names them.
const (
	nodeKindSSH    = "ssh"
	nodeKindWorker = "worker"
)

// nodeListing is what the node commands print: nodes of both kinds, each
// kind in name order, and the workers that are not nodes because an
// inventory node has their name.
type nodeListing struct {
	nodes     []inventory.ResolvedNode
	workers   []inventory.WorkerNode
	conflicts []inventory.WorkerConflict
}

// each passes the nodes of both kinds to their function, merged in name
// order, until one fails.
func (l nodeListing) each(ssh func(inventory.ResolvedNode) error, worker func(inventory.WorkerNode) error) error {
	i, j := 0, 0
	for i < len(l.nodes) || j < len(l.workers) {
		if j == len(l.workers) || (i < len(l.nodes) && l.nodes[i].Name < l.workers[j].Name) {
			if err := ssh(l.nodes[i]); err != nil {
				return err
			}
			i++
			continue
		}
		if err := worker(l.workers[j]); err != nil {
			return err
		}
		j++
	}
	return nil
}

// workerNodeView is a worker node as the CLI prints it in JSON.
type workerNodeView struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Host       string    `json:"host"`
	Labels     []string  `json:"labels"`
	Online     bool      `json:"online"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

func workerNodeViewOf(w inventory.WorkerNode) workerNodeView {
	return workerNodeView{
		Name:       w.Name,
		Kind:       nodeKindWorker,
		Host:       w.Host,
		Labels:     sortedLabels(w.Labels),
		Online:     w.Online,
		LastSeenAt: w.LastSeenAt.UTC(),
	}
}

func renderWorkerNode(out io.Writer, w inventory.WorkerNode) error {
	return renderNodeRows(out, [][2]string{
		{"Name", w.Name},
		{"Kind", nodeKindWorker},
		{"Host", w.Host},
		{"Labels", strings.Join(sortedLabels(w.Labels), ",")},
		{"Status", workerNodeStatus(w)},
		{"Last seen", w.LastSeenAt.UTC().Format(time.RFC3339)},
	})
}

func workerNodeStatus(w inventory.WorkerNode) string {
	if w.Online {
		return "online"
	}
	return "offline"
}

// localWorkerNodes returns the worker nodes of this installation in name
// order, and the workers that are not nodes because a node of nodes has
// their name.
func localWorkerNodes(ctx *Context, nodes []inventory.ResolvedNode) ([]inventory.WorkerNode, []inventory.WorkerConflict, error) {
	store, err := ctx.workerNodeStore()
	if err != nil {
		return nil, nil, err
	}
	workers, err := store.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	kept, conflicts := inventory.SplitWorkers(workers, nodes)
	return kept, conflicts, nil
}

// selectWorkers returns the worker nodes that match every filter, in the
// order given.
func selectWorkers(workers []inventory.WorkerNode, filters []ir.LabelFilter) []inventory.WorkerNode {
	var matched []inventory.WorkerNode
	for _, w := range workers {
		if w.Labels.MatchesFilters(filters) {
			matched = append(matched, w)
		}
	}
	return matched
}

func nodesForgetCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "forget <name>",
		Short: "Forget an offline worker node",
		Long: `Forget a worker node whose worker is offline. The worker is listed again
when it connects. A node of an inventory is removed with 'dagu nodes remove'
instead. On a remote server, forgetting a worker needs the admin role.`,
		Example: `  dagu nodes forget tokyo-gw`,
		Args:    cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		name := args[0]
		var err error
		if ctx.IsRemote() {
			err = ctx.Remote.remoteForgetWorkerNode(ctx, name)
		} else {
			err = forgetWorkerNode(ctx, name)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(ctx.Command.OutOrStdout(), "forgot %s\n", name)
		return nil
	})
}

// forgetWorkerNode forgets the offline worker node named name, with the
// errors the API gives.
func forgetWorkerNode(ctx *Context, name string) error {
	workers, err := ctx.workerNodeStore()
	if err != nil {
		return err
	}
	err = workers.Forget(ctx, name)
	switch {
	case errors.Is(err, inventory.ErrWorkerNodeOnline):
		return fmt.Errorf("worker %q is online; a worker is forgotten once it is offline", name)
	case errors.Is(err, inventory.ErrWorkerNodeNotFound):
		store, err := ctx.inventoryStore()
		if err != nil {
			return err
		}
		nodes, err := store.Nodes(ctx)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if strings.EqualFold(n.Name, name) {
				return fmt.Errorf("node %q is a node of inventory %q; remove it from its inventory", n.Name, n.InventoryName)
			}
		}
		return fmt.Errorf("node %q not found", name)
	}
	return err
}
