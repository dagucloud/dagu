// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
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
	target, err := addTarget(ctx, stores.inventories)
	if err != nil {
		return err
	}
	// Check the node against every rule before it is tested.
	nodes, err := resolveWithNode(ctx, stores.inventories, target, node)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	var accepted []acceptedKey
	if noTest, _ := ctx.Command.Flags().GetBool("no-test"); !noTest {
		service := nodeconn.New(stores.hostKeys, ctx.secretResolver())
		if accepted, err = testBeforeSave(ctx, service, nodes, node.Name); err != nil {
			return fmt.Errorf("node %q not added: %w", node.Name, err)
		}
		_, _ = fmt.Fprintf(out, "connection test of %s passed\n", node.Name)
	}

	stored, added, err := inventory.PutNode(ctx, stores.inventories, target.ID, node, target.Version)
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

	// The keys are trusted once the node is stored, since storing a node
	// forgets any key trusted before for its name.
	by, _ := localOSSubject(user.Current)
	for _, key := range accepted {
		trusted, err := stores.hostKeys.Trust(ctx, key.node, key.key, by, "")
		if err != nil {
			return fmt.Errorf("node %q was added, but the host key of %q was not trusted: %w", node.Name, key.node, err)
		}
		_, _ = fmt.Fprintf(out, "trusted host key %s of %s\n", trusted.Fingerprint, key.node)
	}
	return nil
}

// acceptedKey is a host key the user accepted for a node during a test.
type acceptedKey struct {
	node string
	key  ssh.PublicKey
}

// testBeforeSave tests the node named name among nodes until the test passes
// or fails for good. When a hop presents an unknown key, it asks the user,
// and on yes tests again with that key accepted for that hop in memory only,
// so the node is authenticated and runs true before anything is stored. It
// returns the keys the user accepted, to trust once the node is stored.
func testBeforeSave(ctx *Context, service *nodeconn.Service, nodes []inventory.ResolvedNode, name string) ([]acceptedKey, error) {
	out := ctx.Command.OutOrStdout()
	staged := slices.Clone(nodes)
	var accepted []acceptedKey
	for {
		var result nodeconn.TestResult
		service.Test(ctx, staged, []inventory.ResolvedNode{staged[nodeIndex(staged, name)]}, func(r nodeconn.TestResult) { result = r })
		if result.Err == nil {
			return accepted, nil
		}
		refused := result.Refused
		unknown := refused != nil && len(refused.Expected) == 0 && refused.Presented != ""
		if !unknown || slices.ContainsFunc(accepted, func(k acceptedKey) bool { return strings.EqualFold(k.node, refused.Node) }) {
			return nil, fmt.Errorf("its connection test failed: %w", result.Err)
		}
		_, _ = fmt.Fprintf(out, "The host key of %s %q is unknown: %s\n", refused.Hop, refused.Node, refused.Presented)
		if !confirmAction("Trust this key and add the node?") {
			return nil, errors.New("its host key was not trusted")
		}
		key, err := service.ReadHostKey(ctx, staged, refused.Node, refused.Presented)
		if err != nil {
			return nil, err
		}
		hop := nodeIndex(staged, refused.Node)
		staged[hop].HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		accepted = append(accepted, acceptedKey{node: staged[hop].Name, key: key})
	}
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
		if _, err := inventory.DeleteNode(ctx, inventories, holder.ID, nodes[i].Name, holder.Version); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(ctx.Command.OutOrStdout(), "removed %s from %s\n", nodes[i].Name, holder.Name)
		return nil
	})
}

// nodeStores holds the inventories and the host keys trusted for their
// nodes. A change to the inventories forgets the keys of the node names it
// adds or removes.
type nodeStores struct {
	inventories inventory.Store
	hostKeys    inventory.HostKeyStore
}

func (c *Context) nodeStores() (nodeStores, error) {
	hostKeys, err := c.hostKeyStore()
	if err != nil {
		return nodeStores{}, err
	}
	inventories, err := c.inventoryStore()
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
		path, err := keyFilePath(key)
		if err != nil {
			return inventory.Node{}, fmt.Errorf("invalid --key %q: %w", key, err)
		}
		node.Key = map[string]any{"file": path}
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

// keyFilePath returns the --key path as the node stores it. The node is
// later connected from processes with other working directories, so a
// relative path is made absolute here. A path that starts with ~ or $ is
// kept for each connecting process to expand.
func keyFilePath(path string) (string, error) {
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~") || strings.HasPrefix(path, "$") {
		return path, nil
	}
	return filepath.Abs(path)
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
// target, with each inventory's defaults applied.
func resolveWithNode(ctx *Context, inventories inventory.Store, target inventory.Stored, node inventory.Node) ([]inventory.ResolvedNode, error) {
	all, err := inventories.List(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(all, func(s inventory.Stored) bool { return s.ID == target.ID }) {
		return nil, fmt.Errorf("inventory %q was deleted", target.Name)
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
		return nil, fmt.Errorf("node %q not added:\n%w", node.Name, err)
	}
	return nodes, nil
}

// nodeIndex returns the index of the node named name, compared without case,
// or -1.
func nodeIndex(nodes []inventory.ResolvedNode, name string) int {
	return slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, name) })
}
