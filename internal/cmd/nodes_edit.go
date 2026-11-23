// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
)

var (
	nodesAddInventoryFlag = commandLineFlag{
		name:  "inventory",
		usage: "Inventory to add the node to, given by name or ID; may be left out when there is one inventory",
	}
	nodesHostFlag      = commandLineFlag{name: "host", required: true, usage: "Hostname or IP address of the node"}
	nodesPortFlag      = commandLineFlag{name: "port", usage: "SSH port (default: the inventory's, or 22)"}
	nodesUserFlag      = commandLineFlag{name: "user", usage: "SSH user (default: the inventory's)"}
	nodesKeyFlag       = commandLineFlag{name: "key", usage: "Path of the SSH private key"}
	nodesKeySecretFlag = commandLineFlag{
		name:  "key-secret",
		usage: "Secret ref of the SSH private key, in place of --key",
	}
	nodesPasswordSecretFlag   = commandLineFlag{name: "password-secret", usage: "Secret ref of the SSH password"}
	nodesPassphraseSecretFlag = commandLineFlag{name: "passphrase-secret", usage: "Secret ref of the key's passphrase"}
	nodesBastionFlag          = commandLineFlag{name: "bastion", usage: "Node of the same inventory to connect through"}
	nodesAddLabelsFlag        = commandLineFlag{name: "labels", usage: "Labels of the node, comma-separated: key or key=value (e.g., 'role=web,canary')"}
	nodesDescriptionFlag      = commandLineFlag{name: "description", usage: "Description of the node"}
	nodesNoTestFlag           = commandLineFlag{
		name:   "no-test",
		isBool: true,
		usage:  "Add the node without testing its connection",
	}
)

func nodesAddCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "add <name>",
		Short: "Add a node to an inventory",
		Long: `Add an SSH node to an inventory, or replace the node of that name.

Before it saves, add tests the connection: it checks the host key,
authenticates, and runs true. When the node's host key is unknown, add
shows its fingerprint and asks before it trusts the key. A node whose test
fails is not added, unless --no-test is given. Credentials are key paths or
secret refs, never values.`,
		Example: `  dagu nodes add tokyo-web01 --inventory "Tokyo production" \
    --host 10.0.1.11 --labels role=web
  dagu nodes add db01 --host db01.internal --port 2222 --key-secret ssh/db`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{
		nodesAddInventoryFlag, nodesHostFlag, nodesPortFlag, nodesUserFlag, nodesKeyFlag,
		nodesKeySecretFlag, nodesPasswordSecretFlag, nodesPassphraseSecretFlag,
		nodesBastionFlag, nodesAddLabelsFlag, nodesDescriptionFlag, nodesNoTestFlag,
	}, runNodesAdd)
}

func runNodesAdd(ctx *Context, args []string) error {
	node, err := nodeFromFlags(ctx, args[0])
	if err != nil {
		return err
	}
	stores, err := ctx.nodeStores()
	if err != nil {
		return err
	}
	editor, inventories := stores.editor(), stores.inventories
	target, err := addTarget(ctx, inventories)
	if err != nil {
		return err
	}
	// Check the node against every rule before it is tested.
	candidate, nodes, err := resolveWithNode(ctx, inventories, target, node)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	service := nodeconn.New(stores.hostKeys, ctx.secretResolver())
	var trust *nodeconn.TrustRequest
	if noTest, _ := ctx.Command.Flags().GetBool("no-test"); !noTest {
		result := testNode(ctx, service, nodes, candidate)
		switch {
		case result.Err == nil:
		case result.Refused != nil && len(result.Refused.Expected) == 0 && result.Refused.Presented != "":
			refused := result.Refused
			_, _ = fmt.Fprintf(out, "The host key of %s %q is unknown: %s\n", refused.Hop, refused.Node, refused.Presented)
			if !confirmAction("Trust this key and add the node?") {
				return fmt.Errorf("node %q not added", node.Name)
			}
			trust = &nodeconn.TrustRequest{Node: refused.Node, Fingerprint: refused.Presented}
		default:
			return fmt.Errorf("node %q not added: its connection test failed: %w", node.Name, result.Err)
		}
	}

	stored, added, err := editor.PutNode(ctx, target.ID, node, target.Version)
	if errors.Is(err, inventory.ErrInvalid) {
		return fmt.Errorf("node %q not added:\n%w", node.Name, err)
	}
	if err != nil {
		return err
	}
	verb := "added"
	if !added {
		verb = "replaced"
	}
	_, _ = fmt.Fprintf(out, "%s %s in %s\n", verb, node.Name, stored.Name)
	if trust == nil {
		return nil
	}

	if err := trustKey(ctx, service, nodes, *trust); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "trusted host key %s of %s\n", trust.Fingerprint, trust.Node)
	if result := testNode(ctx, service, nodes, candidate); result.Err != nil {
		return fmt.Errorf("node %q was added, but its connection test failed: %w", node.Name, result.Err)
	}
	_, _ = fmt.Fprintf(out, "connection test of %s passed\n", node.Name)
	return nil
}

func nodesRemoveCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a node from its inventory",
		Long: `Remove a node from the inventory that holds it. The host key trusted for
the node is forgotten, so a host added later under the same name is trusted
again.`,
		Example: `  dagu nodes remove tokyo-web01`,
		Args:    cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		stores, err := ctx.nodeStores()
		if err != nil {
			return err
		}
		inventories := stores.inventories
		nodes, err := inventories.Nodes(ctx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, args[0]) })
		if i < 0 {
			return fmt.Errorf("node %q not found", args[0])
		}
		holder, err := inventories.Get(ctx, nodes[i].InventoryID)
		if err != nil {
			return err
		}
		if _, err := stores.editor().DeleteNode(ctx, holder.ID, nodes[i].Name, holder.Version); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(ctx.Command.OutOrStdout(), "removed %s from %s\n", nodes[i].Name, holder.Name)
		return nil
	})
}

// nodeStores holds the inventories and the host keys trusted for their
// nodes.
type nodeStores struct {
	inventories inventory.Store
	hostKeys    inventory.HostKeyStore
}

// editor changes the inventories, forgetting the trusted keys of the nodes
// a change adds or removes.
func (s nodeStores) editor() *inventory.Editor {
	return inventory.NewEditor(s.inventories, s.hostKeys)
}

func (c *Context) nodeStores() (nodeStores, error) {
	inventories, err := c.inventoryStore()
	if err != nil {
		return nodeStores{}, err
	}
	hostKeys, err := store.NewHostKeyStore(c.backend.Collection(persis.CollectionHostKeys))
	if err != nil {
		return nodeStores{}, err
	}
	return nodeStores{inventories: inventories, hostKeys: hostKeys}, nil
}

// secretResolver resolves node credentials from this installation's secrets.
func (c *Context) secretResolver() nodeconn.SecretResolver {
	secrets := file.NewSecretStore(c.Context, c.Config, c.backend.Collection(persis.CollectionSecrets))
	return secretpkg.NewReferenceResolver(secrets, "")
}

// nodeFromFlags builds the node record that add stores.
func nodeFromFlags(ctx *Context, name string) (inventory.Node, error) {
	flag := func(name string) string {
		value, _ := ctx.Command.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	node := inventory.Node{Name: name, Host: flag("host"), Description: flag("description")}
	node.User, node.Bastion = flag("user"), flag("bastion")
	if port := flag("port"); port != "" {
		var err error
		if node.Port, err = strconv.Atoi(port); err != nil {
			return inventory.Node{}, fmt.Errorf("invalid --port %q: %w", port, err)
		}
	}
	switch key, keySecret := flag("key"), flag("key-secret"); {
	case key != "" && keySecret != "":
		return inventory.Node{}, errors.New("--key and --key-secret cannot be combined")
	case key != "":
		node.Key = map[string]any{"file": key}
	case keySecret != "":
		node.Key = map[string]any{"secret": keySecret}
	}
	if ref := flag("password-secret"); ref != "" {
		node.Password = map[string]any{"secret": ref}
	}
	if ref := flag("passphrase-secret"); ref != "" {
		node.Passphrase = map[string]any{"secret": ref}
	}
	for label := range strings.SplitSeq(flag("labels"), ",") {
		if label = strings.TrimSpace(label); label != "" {
			node.Labels = append(node.Labels, label)
		}
	}
	return node, nil
}

// addTarget returns the inventory --inventory names, or the only one.
func addTarget(ctx *Context, inventories inventory.Store) (inventory.Stored, error) {
	if ref, _ := ctx.Command.Flags().GetString("inventory"); strings.TrimSpace(ref) != "" {
		return findInventory(ctx, inventories, strings.TrimSpace(ref))
	}
	all, err := inventories.List(ctx)
	if err != nil {
		return inventory.Stored{}, err
	}
	switch len(all) {
	case 0:
		return inventory.Stored{}, errors.New("no inventory exists; create one with 'dagu nodes import'")
	case 1:
		return all[0], nil
	default:
		return inventory.Stored{}, fmt.Errorf("%d inventories exist; name one with --inventory", len(all))
	}
}

// resolveWithNode resolves the inventories as they would be with node in
// target, and returns the node with its inventory's defaults applied.
func resolveWithNode(ctx *Context, inventories inventory.Store, target inventory.Stored, node inventory.Node) (inventory.ResolvedNode, []inventory.ResolvedNode, error) {
	all, err := inventories.List(ctx)
	if err != nil {
		return inventory.ResolvedNode{}, nil, err
	}
	invs := make([]inventory.Inventory, 0, len(all))
	for _, stored := range all {
		inv := stored.Inventory
		if inv.ID == target.ID {
			inv.Nodes = slices.DeleteFunc(slices.Clone(inv.Nodes), func(n inventory.Node) bool { return strings.EqualFold(n.Name, node.Name) })
			inv.Nodes = append(inv.Nodes, node)
		}
		invs = append(invs, inv)
	}
	nodes, err := inventory.Resolve(invs)
	if err != nil {
		return inventory.ResolvedNode{}, nil, fmt.Errorf("node %q not added:\n%w", node.Name, err)
	}
	i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, node.Name) })
	return nodes[i], nodes, nil
}

func testNode(ctx *Context, service *nodeconn.Service, nodes []inventory.ResolvedNode, node inventory.ResolvedNode) nodeconn.TestResult {
	var result nodeconn.TestResult
	service.Test(ctx, nodes, []inventory.ResolvedNode{node}, func(r nodeconn.TestResult) { result = r })
	return result
}

func trustKey(ctx *Context, service *nodeconn.Service, nodes []inventory.ResolvedNode, request nodeconn.TrustRequest) error {
	by, _ := localOSSubject(user.Current)
	var result nodeconn.TrustResult
	service.Trust(ctx, nodes, []nodeconn.TrustRequest{request}, by, func(r nodeconn.TrustResult) { result = r })
	if result.Err != nil {
		return fmt.Errorf("failed to trust the host key of %q: %w", request.Node, result.Err)
	}
	return nil
}
