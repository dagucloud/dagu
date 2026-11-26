// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/spf13/cobra"
)

const (
	nodesFormatTable = "table"
	nodesFormatJSON  = "json"
)

var (
	nodesLabelsFlag = commandLineFlag{
		name:  "labels",
		usage: "Select nodes by labels, comma-separated with AND logic: key, key=value, globs with * and ?, and ! to negate (e.g., 'site=tokyo,web,!canary')",
	}
	nodesInventoryFlag = commandLineFlag{
		name:  "inventory",
		usage: "Limit the command to one inventory, given by name or ID",
	}
	nodesFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: nodesFormatTable,
		usage:        "Output format: table or json (default: table)",
	}
	nodesReplaceFlag = commandLineFlag{
		name:   "replace",
		isBool: true,
		usage:  "Replace each inventory whose name exists, keeping its ID",
	}
	nodesDryRunFlag = commandLineFlag{
		name:   "dry-run",
		isBool: true,
		usage:  "Check the import and store nothing",
	}
)

// Nodes returns the command group for node inventories (RFC 012).
func Nodes() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "nodes",
		Short: "List, show, add, remove, forget, test, import, and export nodes",
		Long: `Work with the nodes of this installation: the hosts of its node inventories,
named lists of SSH hosts with labels, and the workers that set worker.id.
Steps select nodes with the label filter that 'dagu history --labels' takes.

Inventories are stored as JSON records. 'dagu nodes export' prints them, and
'dagu nodes import' reads the same JSON. 'dagu nodes add' and 'dagu nodes
remove' change one node. 'dagu nodes test' tests connections and trusts
host keys. 'dagu nodes forget' forgets an offline worker.`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(nodesListCommand())
	cmd.AddCommand(nodesShowCommand())
	cmd.AddCommand(nodesAddCommand())
	cmd.AddCommand(nodesRemoveCommand())
	cmd.AddCommand(nodesForgetCommand())
	cmd.AddCommand(nodesTestCommand())
	cmd.AddCommand(nodesImportCommand())
	cmd.AddCommand(nodesExportCommand())
	return cmd
}

func nodesListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list",
		Short: "List the nodes a label selector matches, worker nodes included, in name order",
		Example: `  dagu nodes list
  dagu nodes list --labels 'site=tokyo,web,!canary'
  dagu nodes list --inventory "Tokyo production" --format json`,
		Args: cobra.NoArgs,
	}, []commandLineFlag{nodesLabelsFlag, nodesInventoryFlag, nodesFormatFlag}, func(ctx *Context, _ []string) error {
		format, err := nodesFormat(ctx)
		if err != nil {
			return err
		}
		if ctx.IsRemote() {
			return remoteNodesList(ctx, format)
		}
		filters, err := selectorFilters(ctx)
		if err != nil {
			return err
		}
		store, err := ctx.inventoryStore()
		if err != nil {
			return err
		}
		all, err := store.Nodes(ctx)
		if err != nil {
			return err
		}
		workers, conflicts, err := localWorkerNodes(ctx, all)
		if err != nil {
			return err
		}
		listing := nodeListing{
			nodes:     inventory.Select(all, filters),
			workers:   selectWorkers(workers, filters),
			conflicts: conflicts,
		}
		ref, err := ctx.StringParam("inventory")
		if err != nil {
			return err
		}
		if ref != "" {
			inv, err := findInventory(ctx, store, ref)
			if err != nil {
				return err
			}
			listing.nodes = slices.DeleteFunc(listing.nodes, func(n inventory.ResolvedNode) bool { return n.InventoryID != inv.ID })
			// A worker node belongs to no inventory.
			listing.workers = nil
		}

		return printNodes(ctx, format, listing)
	})
}

func nodesShowCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show one node with its inventory's defaults applied",
		Long: `Show one node: its inventory, labels, and connection settings with the
inventory's defaults applied, or a worker node's host, labels, and when its
worker was last seen. Credentials appear as secret refs or key paths, never
as secret values.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{nodesFormatFlag}, func(ctx *Context, args []string) error {
		format, err := nodesFormat(ctx)
		if err != nil {
			return err
		}
		if ctx.IsRemote() {
			node, err := ctx.Remote.remoteNode(ctx, args[0])
			if err != nil {
				return err
			}
			return printNode(ctx, format, node)
		}
		store, err := ctx.inventoryStore()
		if err != nil {
			return err
		}
		nodes, err := store.Nodes(ctx)
		if err != nil {
			return err
		}
		// Node names are unique without regard to case.
		if i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, args[0]) }); i >= 0 {
			return printNode(ctx, format, nodeListing{nodes: nodes[i : i+1]})
		}
		workers, _, err := localWorkerNodes(ctx, nodes)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(workers, func(w inventory.WorkerNode) bool { return strings.EqualFold(w.Name, args[0]) }); i >= 0 {
			return printNode(ctx, format, nodeListing{workers: workers[i : i+1]})
		}
		return fmt.Errorf("node %q not found", args[0])
	})
}

func nodesImportCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "import <file>",
		Short: "Import inventories from a JSON file",
		Long: `Import a JSON array of inventories, as 'dagu nodes export' prints it.

An inventory whose name is new is created. An inventory whose name exists is
refused unless --replace is given, which replaces it as a whole and keeps its
ID. The import is checked as a whole first: when any inventory breaks a rule,
nothing is stored and every broken rule is listed. --dry-run only checks.`,
		Example: `  dagu nodes import inventories.json
  dagu nodes import inventories.json --replace --dry-run`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{nodesReplaceFlag, nodesDryRunFlag}, func(ctx *Context, args []string) error {
		replace, err := ctx.Command.Flags().GetBool("replace")
		if err != nil {
			return err
		}
		dryRun, err := ctx.Command.Flags().GetBool("dry-run")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", args[0], err)
		}
		invs, err := inventory.DecodeList(data)
		if err != nil {
			return err
		}
		opts := inventory.ImportOptions{Replace: replace, DryRun: dryRun}
		var result inventory.ImportResult
		if ctx.IsRemote() {
			// The server decodes the file again, as it was read.
			result, err = ctx.Remote.remoteImport(ctx, data, opts)
		} else {
			var store inventory.Store
			if store, err = ctx.inventoryStore(); err != nil {
				return err
			}
			result, err = store.Import(ctx, invs, opts)
		}
		if errors.Is(err, inventory.ErrInvalid) {
			return fmt.Errorf("nothing imported:\n%w", err)
		}
		if err != nil {
			return err
		}
		return renderImportResult(ctx.Command.OutOrStdout(), result, dryRun)
	})
}

func nodesExportCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "export",
		Short: "Print inventories as JSON, without their IDs",
		Long: `Print inventories as a JSON array in name order, as 'dagu nodes import'
reads it. IDs are left out, so the output imports into any installation.`,
		Example: `  dagu nodes export > inventories.json
  dagu nodes export --inventory "Tokyo production"`,
		Args: cobra.NoArgs,
	}, []commandLineFlag{nodesInventoryFlag}, func(ctx *Context, _ []string) error {
		ref, err := ctx.StringParam("inventory")
		if err != nil {
			return err
		}
		if ctx.IsRemote() {
			return remoteNodesExport(ctx, ref)
		}
		store, err := ctx.inventoryStore()
		if err != nil {
			return err
		}
		var stored []inventory.Stored
		if ref == "" {
			stored, err = store.List(ctx)
		} else {
			var inv inventory.Stored
			inv, err = findInventory(ctx, store, ref)
			stored = []inventory.Stored{inv}
		}
		if err != nil {
			return err
		}
		return writeExport(ctx, stored)
	})
}

// writeExport prints stored as a JSON array without IDs, as import reads it.
func writeExport(ctx *Context, stored []inventory.Stored) error {
	invs := make([]inventory.Inventory, 0, len(stored))
	for _, s := range stored {
		inv := s.Inventory
		inv.ID = ""
		invs = append(invs, inv)
	}
	return writeNodesJSON(ctx.Command.OutOrStdout(), invs)
}

// printNodes prints the nodes of a listing in name order as a table, or as
// JSON, and reports on stderr each worker that is not a node.
func printNodes(ctx *Context, format string, listing nodeListing) error {
	for _, c := range listing.conflicts {
		_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "worker %q is not a node: inventory %q has a node of that name\n",
			c.Worker.Name, c.Node.InventoryName)
	}
	out := ctx.Command.OutOrStdout()
	if format == nodesFormatJSON {
		views := make([]any, 0, len(listing.nodes)+len(listing.workers))
		_ = listing.each(func(n inventory.ResolvedNode) error {
			views = append(views, nodeViewOf(n))
			return nil
		}, func(w inventory.WorkerNode) error {
			views = append(views, workerNodeViewOf(w))
			return nil
		})
		return writeNodesJSON(out, views)
	}
	return renderNodesTable(out, listing)
}

// printNode prints the one node of a listing as a table, or as JSON.
func printNode(ctx *Context, format string, listing nodeListing) error {
	out := ctx.Command.OutOrStdout()
	return listing.each(func(n inventory.ResolvedNode) error {
		if format == nodesFormatJSON {
			return writeNodesJSON(out, nodeViewOf(n))
		}
		return renderNode(out, n)
	}, func(w inventory.WorkerNode) error {
		if format == nodesFormatJSON {
			return writeNodesJSON(out, workerNodeViewOf(w))
		}
		return renderWorkerNode(out, w)
	})
}

// nodeView is a node as the CLI prints it in JSON: its fields with the
// inventory's defaults applied, and credentials as secret refs or key paths.
type nodeView struct {
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Inventory     inventoryRef    `json:"inventory"`
	Host          string          `json:"host"`
	Port          int             `json:"port"`
	User          string          `json:"user,omitempty"`
	Key           *credentialView `json:"key,omitempty"`
	Password      *credentialView `json:"password,omitempty"`
	Passphrase    *credentialView `json:"passphrase,omitempty"`
	Bastion       string          `json:"bastion,omitempty"`
	HostKey       string          `json:"host_key,omitempty"`
	StrictHostKey bool            `json:"strict_host_key"`
	Shell         string          `json:"shell,omitempty"`
	Timeout       string          `json:"timeout"`
	Labels        []string        `json:"labels"`
	Description   string          `json:"description,omitempty"`
	Disabled      bool            `json:"disabled"`
}

type inventoryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// credentialView takes the forms an inventory record gives a credential.
type credentialView struct {
	Secret string `json:"secret,omitempty"`
	File   string `json:"file,omitempty"`
}

func nodeViewOf(n inventory.ResolvedNode) nodeView {
	return nodeView{
		Name:          n.Name,
		Kind:          nodeKindSSH,
		Inventory:     inventoryRef{ID: n.InventoryID, Name: n.InventoryName},
		Host:          n.Host,
		Port:          n.Port,
		User:          n.User,
		Key:           credentialOf(n.Auth.KeySecret, n.Auth.Key),
		Password:      credentialOf(n.Auth.PasswordSecret, ""),
		Passphrase:    credentialOf(n.Auth.PassphraseSecret, ""),
		Bastion:       n.Bastion,
		HostKey:       n.HostKey,
		StrictHostKey: n.StrictHostKey,
		Shell:         n.Shell,
		Timeout:       n.Timeout.String(),
		Labels:        sortedLabels(n.Labels),
		Description:   n.Description,
		Disabled:      n.Disabled,
	}
}

// credentialOf describes a credential by its secret ref or its key path. The
// store refuses literal passwords and passphrases, so those take only a ref.
func credentialOf(secretRef, file string) *credentialView {
	switch {
	case secretRef != "":
		return &credentialView{Secret: secretRef}
	case file != "":
		return &credentialView{File: file}
	default:
		return nil
	}
}

func (c *credentialView) String() string {
	if c.Secret != "" {
		return "secret " + c.Secret
	}
	return "file " + c.File
}

func renderNodesTable(out io.Writer, listing nodeListing) error {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tINVENTORY\tKIND\tHOST\tLABELS\tSTATUS"); err != nil {
		return err
	}
	row := func(name, inventoryName, kind, host string, labels ir.Labels, status string) error {
		_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			name, inventoryName, kind, host, strings.Join(sortedLabels(labels), ","), status)
		return err
	}
	err := listing.each(func(n inventory.ResolvedNode) error {
		return row(n.Name, n.InventoryName, nodeKindSSH, n.Host, n.Labels, nodeStatus(n))
	}, func(n inventory.WorkerNode) error {
		// A worker node belongs to no inventory.
		return row(n.Name, "-", nodeKindWorker, n.Host, n.Labels, workerNodeStatus(n))
	})
	if err != nil {
		return err
	}
	return w.Flush()
}

func renderNode(out io.Writer, n inventory.ResolvedNode) error {
	v := nodeViewOf(n)
	rows := [][2]string{
		{"Name", v.Name},
		{"Kind", v.Kind},
		{"Inventory", v.Inventory.Name},
		{"Host", v.Host},
		{"Port", fmt.Sprint(v.Port)},
		{"User", v.User},
	}
	for _, c := range []struct {
		field string
		cred  *credentialView
	}{{"Key", v.Key}, {"Password", v.Password}, {"Passphrase", v.Passphrase}} {
		if c.cred != nil {
			rows = append(rows, [2]string{c.field, c.cred.String()})
		}
	}
	rows = append(rows,
		[2]string{"Bastion", v.Bastion},
		[2]string{"Host key", v.HostKey},
		[2]string{"Strict host key", fmt.Sprint(v.StrictHostKey)},
		[2]string{"Shell", v.Shell},
		[2]string{"Timeout", v.Timeout},
		[2]string{"Labels", strings.Join(v.Labels, ",")},
		[2]string{"Description", v.Description},
		[2]string{"Status", nodeStatus(n)},
	)

	return renderNodeRows(out, rows)
}

// renderNodeRows prints rows as a two-column table, leaving out the rows
// without a value.
func renderNodeRows(out io.Writer, rows [][2]string) error {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	return w.Flush()
}

func renderImportResult(out io.Writer, result inventory.ImportResult, dryRun bool) error {
	created, replaced := "created", "replaced"
	if dryRun {
		created, replaced = "would create", "would replace"
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, row := range []struct {
		verb  string
		names []string
	}{{created, result.Created}, {replaced, result.Replaced}} {
		for _, name := range row.names {
			if _, err := fmt.Fprintf(w, "%s\t%s\n", row.verb, name); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

func writeNodesJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func nodesFormat(ctx *Context) (string, error) {
	format, err := ctx.StringParam("format")
	if err != nil {
		return "", err
	}
	switch format {
	case nodesFormatTable, nodesFormatJSON:
		return format, nil
	default:
		return "", fmt.Errorf("invalid --format %q: use table or json", format)
	}
}

// selectorFilters returns the filters of the --labels selector, or none when
// the flag is not given, so every node matches. A given selector without
// terms is an error.
func selectorFilters(ctx *Context) ([]ir.LabelFilter, error) {
	if !ctx.Command.Flags().Changed("labels") {
		return nil, nil
	}
	selector, err := ctx.StringParam("labels")
	if err != nil {
		return nil, err
	}
	return inventory.ParseSelector(selector)
}

// findInventory returns the inventory with the given ID, or else the one with
// the given name, compared without case as names are unique that way.
func findInventory(ctx *Context, store inventory.Store, ref string) (inventory.Stored, error) {
	all, err := store.List(ctx)
	if err != nil {
		return inventory.Stored{}, err
	}
	return findInventoryIn(all, ref)
}

// findInventoryIn returns the inventory of all with the given ID, or else the
// one with the given name, compared without case.
func findInventoryIn(all []inventory.Stored, ref string) (inventory.Stored, error) {
	if i := slices.IndexFunc(all, func(s inventory.Stored) bool { return s.ID == ref }); i >= 0 {
		return all[i], nil
	}
	if i := slices.IndexFunc(all, func(s inventory.Stored) bool { return strings.EqualFold(s.Name, ref) }); i >= 0 {
		return all[i], nil
	}
	return inventory.Stored{}, fmt.Errorf("inventory %q not found", ref)
}

func nodeStatus(n inventory.ResolvedNode) string {
	if n.Disabled {
		return "disabled"
	}
	return "enabled"
}

func sortedLabels(labels ir.Labels) []string {
	strs := labels.Strings()
	slices.Sort(strs)
	return strs
}
