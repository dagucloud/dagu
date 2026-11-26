// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
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
		Long: `Add a new SSH node to an inventory. A name the inventory already
holds is refused before anything is tested.

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
	adder, err := nodesAdder(ctx, node)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	var accepted []nodeconn.Accepted
	if noTest, _ := ctx.Command.Flags().GetBool("no-test"); !noTest {
		if accepted, err = testBeforeSave(ctx, adder); err != nil {
			return notAdded(node.Name, err)
		}
		_, _ = fmt.Fprintf(out, "connection test of %s passed\n", node.Name)
	}

	by, _ := localOSSubject(user.Current)
	stored, trusted, err := adder.add(ctx, accepted, by)
	if stored.ID == "" {
		if _, ok := errors.AsType[uncertainAdd](err); ok {
			return err
		}
		return notAdded(node.Name, err)
	}
	_, _ = fmt.Fprintf(out, "added %s in %s\n", node.Name, stored.Name)
	for _, key := range trusted {
		_, _ = fmt.Fprintf(out, "trusted host key %s of %s\n", key.Fingerprint, key.Node)
	}
	if err != nil {
		// A remote add reports the stored node in its own words.
		if partial, ok := errors.AsType[partialAdd](err); ok {
			return partial
		}
		return fmt.Errorf("node %q was added, but a host key was not trusted: %w", node.Name, err)
	}
	return nil
}

// notAdded reports why the node named name was not added, listing each rule
// it breaks on its own line.
func notAdded(name string, err error) error {
	if errors.Is(err, inventory.ErrInvalid) {
		return fmt.Errorf("node %q not added:\n%w", name, err)
	}
	return fmt.Errorf("node %q not added: %w", name, err)
}

// nodeAdder tests a node that is not stored yet, and adds it, in this
// process or on the server of a remote context.
type nodeAdder interface {
	// test tests the node with the accepted keys accepted for that test
	// only.
	test(ctx context.Context, accepted []nodeconn.Accepted) (nodeconn.TestResult, error)
	// add stores the node, then trusts the accepted keys. The inventory it
	// returns has no ID when the node was not stored.
	add(ctx context.Context, accepted []nodeconn.Accepted, by string) (inventory.Stored, []inventory.TrustedHostKey, error)
}

// nodesAdder returns the adder of node to the inventory --inventory names,
// or the only one. A name that inventory holds is refused before anything
// is tested.
func nodesAdder(ctx *Context, node inventory.Node) (nodeAdder, error) {
	if ctx.IsRemote() {
		all, err := ctx.Remote.remoteInventories(ctx)
		if err != nil {
			return nil, err
		}
		target, err := addTarget(ctx, all)
		if err != nil {
			return nil, err
		}
		if target, err = ctx.Remote.remoteInventory(ctx, target.ID); err != nil {
			return nil, err
		}
		if err := refuseHeldName(target, node.Name); err != nil {
			return nil, err
		}
		return remoteAdder{client: ctx.Remote, target: target, node: node}, nil
	}
	stores, err := ctx.nodeStores()
	if err != nil {
		return nil, err
	}
	all, err := stores.inventories.List(ctx)
	if err != nil {
		return nil, err
	}
	target, err := addTarget(ctx, all)
	if err != nil {
		return nil, err
	}
	if err := refuseHeldName(target, node.Name); err != nil {
		return nil, err
	}
	// Check the node against every rule before it is tested.
	nodes, err := resolveWithNode(all, target, node)
	if err != nil {
		return nil, err
	}
	return &localAdder{
		service: nodeconn.New(stores.hostKeys, ctx.secretResolver()),
		store:   stores.inventories,
		target:  target,
		node:    node,
		nodes:   nodes,
		staged:  nodes,
	}, nil
}

// refuseHeldName fails when target holds a node named name: add adds new
// nodes only.
func refuseHeldName(target inventory.Stored, name string) error {
	if i := slices.IndexFunc(target.Nodes, func(n inventory.Node) bool { return strings.EqualFold(n.Name, name) }); i >= 0 {
		return fmt.Errorf("node %q not added: inventory %q already has a node %q", name, target.Name, target.Nodes[i].Name)
	}
	return nil
}

// localAdder tests and adds a node in this process.
type localAdder struct {
	service *nodeconn.Service
	store   inventory.Store
	target  inventory.Stored
	node    inventory.Node
	// nodes are the project's nodes with node added; staged are those with
	// the keys of the last test pinned, which add trusts.
	nodes  []inventory.ResolvedNode
	staged []inventory.ResolvedNode
}

func (l *localAdder) test(ctx context.Context, accepted []nodeconn.Accepted) (nodeconn.TestResult, error) {
	staged, err := l.service.Stage(ctx, l.nodes, l.node.Name, accepted)
	if err != nil {
		return nodeconn.TestResult{}, err
	}
	l.staged = staged
	var result nodeconn.TestResult
	l.service.Test(ctx, staged, []inventory.ResolvedNode{staged[nodeIndex(staged, l.node.Name)]}, func(r nodeconn.TestResult) { result = r })
	return result, nil
}

func (l *localAdder) add(ctx context.Context, accepted []nodeconn.Accepted, by string) (inventory.Stored, []inventory.TrustedHostKey, error) {
	stored, err := inventory.AddNode(ctx, l.store, l.target.ID, l.node, l.target.Version)
	if err != nil {
		return inventory.Stored{}, nil, err
	}
	trusted, err := l.service.TrustAccepted(ctx, l.staged, accepted, by)
	return stored, trusted, err
}

// testBeforeSave tests the node until the test passes or fails for good. When a hop presents an unknown key, it asks the user, and on yes
// tests again with that key accepted for that test only, so the node is
// authenticated and runs true before anything is stored. It returns the
// keys the user accepted, to trust once the node is stored.
func testBeforeSave(ctx *Context, adder nodeAdder) ([]nodeconn.Accepted, error) {
	out := ctx.Command.OutOrStdout()
	var accepted []nodeconn.Accepted
	for {
		result, err := adder.test(ctx, accepted)
		if err != nil {
			return nil, err
		}
		if result.Err == nil {
			return accepted, nil
		}
		refused := result.Refused
		unknown := refused != nil && len(refused.Expected) == 0 && refused.Presented != ""
		if !unknown || slices.ContainsFunc(accepted, func(k nodeconn.Accepted) bool { return strings.EqualFold(k.Node, refused.Node) }) {
			return nil, fmt.Errorf("its connection test failed: %w", result.Err)
		}
		_, _ = fmt.Fprintf(out, "The host key of %s %q is unknown: %s\n", refused.Hop, refused.Node, refused.Presented)
		if !confirmAction("Trust this key and add the node?") {
			return nil, errors.New("its host key was not trusted")
		}
		accepted = append(accepted, nodeconn.Accepted{Node: refused.Node, Fingerprint: refused.Presented})
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
		if ctx.IsRemote() {
			return remoteNodesRemove(ctx, args[0])
		}
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
	secrets := c.settings.SecretStore(c.Context)
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
		path, err := nodeKeyPath(key, ctx.IsRemote())
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

// nodeKeyPath returns the --key path as the node stores it. A remote server
// reads the key on its own host, so its path is kept as given, and must be
// one that server can read; a local path is made absolute.
func nodeKeyPath(path string, remote bool) (string, error) {
	if !remote {
		return keyFilePath(path)
	}
	if !serverKeyPath(path) {
		return "", errors.New("the server reads the key on its own host, so give an absolute path, or one that starts with ~ or $")
	}
	return path, nil
}

// serverKeyPath reports whether a remote server can read the --key path as
// given: an absolute path on a POSIX or a Windows server, or one that starts
// with ~ or $ for the server to expand. The server's platform is not known
// here, so a path absolute on either is taken.
func serverKeyPath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/"), strings.HasPrefix(path, `\\`),
		strings.HasPrefix(path, "~"), strings.HasPrefix(path, "$"):
		return true
	default:
		return len(path) > 2 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
			(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z')
	}
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

// addTarget returns the inventory of all that --inventory names, or the
// only one.
func addTarget(ctx *Context, all []inventory.Stored) (inventory.Stored, error) {
	if ref, _ := ctx.Command.Flags().GetString("inventory"); strings.TrimSpace(ref) != "" {
		return findInventoryIn(all, strings.TrimSpace(ref))
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

// resolveWithNode resolves the inventories of all as they would be with node
// in target, with each inventory's defaults applied.
func resolveWithNode(all []inventory.Stored, target inventory.Stored, node inventory.Node) ([]inventory.ResolvedNode, error) {
	invs, err := inventory.WithNode(all, target.ID, node)
	if errors.Is(err, inventory.ErrNotFound) {
		return nil, fmt.Errorf("inventory %q was deleted", target.Name)
	}
	if err != nil {
		return nil, err
	}
	nodes, err := inventory.Resolve(invs)
	if err != nil {
		return nil, notAdded(node.Name, err)
	}
	return nodes, nil
}

// nodeIndex returns the index of the node named name, compared without case,
// or -1.
func nodeIndex(nodes []inventory.ResolvedNode, name string) int {
	return slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, name) })
}
