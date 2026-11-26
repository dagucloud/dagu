// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/user"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
)

func nodesTestCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "test [<name>...]",
		Short: "Test connections to nodes, and trust their host keys",
		Long: `Test the connection to each named node, to the nodes --labels selects, or
to every node: check the host key of each hop, authenticate, and run true.
Each result is printed as it ends.

When a hop presents a host key that is unknown, or that differs from the
key trusted for it, test shows the fingerprints and asks before it trusts
the key. It then tests again the nodes that reach that hop. The command
fails when any node fails its last test.`,
		Example: `  dagu nodes test --labels web
  dagu nodes test tokyo-web01 tokyo-web02`,
	}, []commandLineFlag{nodesLabelsFlag}, runNodesTest)
}

func runNodesTest(ctx *Context, args []string) error {
	nodes, tester, err := nodesTester(ctx)
	if err != nil {
		return err
	}
	targets, err := testTargets(ctx, nodes, args)
	if err != nil {
		return err
	}
	width := 0
	for _, node := range targets {
		width = max(width, len(node.Name))
	}
	session := connectionTest{
		ctx:    ctx,
		tester: tester,
		nodes:  nodes,
		out:    ctx.Command.OutOrStdout(),
		width:  width,
	}
	return session.run(targets)
}

// nodeTester tests nodes and trusts their host keys, in this process or on
// the server of a remote context.
type nodeTester interface {
	test(ctx context.Context, targets []inventory.ResolvedNode, emit func(nodeconn.TestResult)) error
	trust(ctx context.Context, requests []nodeconn.TrustRequest, by string, emit func(nodeconn.TrustResult)) error
}

// nodesTester returns every node, and the tester that reaches them.
func nodesTester(ctx *Context) ([]inventory.ResolvedNode, nodeTester, error) {
	if ctx.IsRemote() {
		listing, err := ctx.Remote.remoteNodes(ctx, nil, "")
		return listing.nodes, remoteTester{client: ctx.Remote}, err
	}
	stores, err := ctx.nodeStores()
	if err != nil {
		return nil, nil, err
	}
	nodes, err := stores.inventories.Nodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nodes, localTester{service: nodeconn.New(stores.hostKeys, ctx.secretResolver()), nodes: nodes}, nil
}

// localTester tests nodes and trusts host keys in this process.
type localTester struct {
	service *nodeconn.Service
	nodes   []inventory.ResolvedNode
}

func (l localTester) test(ctx context.Context, targets []inventory.ResolvedNode, emit func(nodeconn.TestResult)) error {
	l.service.Test(ctx, l.nodes, targets, emit)
	return nil
}

func (l localTester) trust(ctx context.Context, requests []nodeconn.TrustRequest, by string, emit func(nodeconn.TrustResult)) error {
	l.service.Trust(ctx, l.nodes, requests, by, emit)
	return nil
}

// testTargets returns the nodes named in args, or the nodes --labels
// selects, or every node.
func testTargets(ctx *Context, nodes []inventory.ResolvedNode, args []string) ([]inventory.ResolvedNode, error) {
	if ctx.Command.Flags().Changed("labels") {
		if len(args) > 0 {
			return nil, errors.New("give node names or --labels, not both")
		}
		selector, err := ctx.StringParam("labels")
		if err != nil {
			return nil, err
		}
		filters, err := inventory.ParseSelector(selector)
		if err != nil {
			return nil, err
		}
		matched := inventory.Select(nodes, filters)
		if len(matched) == 0 {
			return nil, errors.New("selector matched no nodes")
		}
		return matched, nil
	}
	if len(args) == 0 {
		if len(nodes) == 0 {
			return nil, errors.New("no node exists; add one with 'dagu nodes add' or 'dagu nodes import'")
		}
		return nodes, nil
	}
	var targets []inventory.ResolvedNode
	for _, name := range args {
		i := nodeIndex(nodes, name)
		if i < 0 {
			return nil, fmt.Errorf("node %q not found", name)
		}
		if nodeIndex(targets, name) < 0 {
			targets = append(targets, nodes[i])
		}
	}
	return targets, nil
}

// connectionTest tests nodes, and trusts the host keys the user accepts.
type connectionTest struct {
	ctx    *Context
	tester nodeTester
	nodes  []inventory.ResolvedNode
	out    io.Writer
	// width is the width of the name column of the results.
	width int
}

// run tests targets. After each round it offers to trust the keys that
// stopped a test, each at most once, and tests again the targets that a
// trusted key stopped. It fails when any target fails its last test, or has
// no result because the command was cancelled.
func (t connectionTest) run(targets []inventory.ResolvedNode) error {
	all := targets
	last := map[string]nodeconn.TestResult{}
	asked := map[string]bool{}
	for len(targets) > 0 {
		var round []nodeconn.TestResult
		err := t.tester.test(t.ctx, targets, func(result nodeconn.TestResult) {
			t.printResult(result)
			last[strings.ToLower(result.Node)] = result
			round = append(round, result)
		})
		if err != nil {
			return err
		}
		offers := keyOffers(round, asked)
		if len(offers) == 0 {
			break
		}
		trusted, err := t.trust(t.confirmOffers(offers))
		if err != nil {
			return err
		}
		targets = nil
		for _, result := range round {
			if result.Refused != nil && trusted[strings.ToLower(result.Refused.Node)] {
				targets = append(targets, t.nodes[nodeIndex(t.nodes, result.Node)])
			}
		}
	}

	var untested []string
	for _, node := range all {
		if _, ok := last[strings.ToLower(node.Name)]; !ok {
			untested = append(untested, node.Name)
		}
	}
	if len(untested) > 0 {
		stopped := fmt.Errorf("the test stopped before %s finished", strings.Join(untested, ", "))
		if cause := context.Cause(t.ctx); cause != nil {
			return fmt.Errorf("%w: %w", stopped, cause)
		}
		return stopped
	}

	failed := 0
	for _, result := range last {
		if result.Err != nil {
			failed++
		}
	}
	_, _ = fmt.Fprintf(t.out, "%d passed, %d failed\n", len(last)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d nodes failed the connection test", failed, len(last))
	}
	return nil
}

func (t connectionTest) printResult(result nodeconn.TestResult) {
	if result.Err != nil {
		_, _ = fmt.Fprintf(t.out, "%-*s  failed  %v\n", t.width, result.Node, result.Err)
		return
	}
	_, _ = fmt.Fprintf(t.out, "%-*s  passed  %s  %s\n", t.width, result.Node, result.Fingerprint, result.Duration.Round(time.Millisecond))
}

// keyOffer is a host key that stopped a test, which the user may trust.
type keyOffer struct {
	node      string
	presented string
	// trusted is the fingerprint of the key trusted for the node, which the
	// presented key replaces, or empty when the key is unknown.
	trusted string
}

// keyOffers returns the keys that stopped the tests of round and that a
// trust can store, one per node not asked about before, and marks those
// nodes asked. A changed key is offered only in place of a trusted key: a
// key that differs from the node's host_key alone is fixed in the inventory.
func keyOffers(round []nodeconn.TestResult, asked map[string]bool) []keyOffer {
	var offers []keyOffer
	for _, result := range round {
		refused := result.Refused
		if refused == nil || refused.Presented == "" || asked[strings.ToLower(refused.Node)] {
			continue
		}
		if len(refused.Expected) > 0 && refused.Trusted == "" {
			continue
		}
		asked[strings.ToLower(refused.Node)] = true
		offers = append(offers, keyOffer{node: refused.Node, presented: refused.Presented, trusted: refused.Trusted})
	}
	return offers
}

// confirmOffers asks once for the unknown keys and once for the changed
// ones, and returns the offers the user accepted.
func (t connectionTest) confirmOffers(offers []keyOffer) []keyOffer {
	var unknown, changed []keyOffer
	for _, offer := range offers {
		if offer.trusted == "" {
			unknown = append(unknown, offer)
		} else {
			changed = append(changed, offer)
		}
	}
	var accepted []keyOffer
	if len(unknown) > 0 {
		_, _ = fmt.Fprintln(t.out, "Unknown host keys:")
		for _, offer := range unknown {
			_, _ = fmt.Fprintf(t.out, "  %s  %s\n", offer.node, offer.presented)
		}
		if confirmAction("Trust these host keys?") {
			accepted = append(accepted, unknown...)
		}
	}
	if len(changed) > 0 {
		_, _ = fmt.Fprintln(t.out, "Changed host keys:")
		for _, offer := range changed {
			_, _ = fmt.Fprintf(t.out, "  %s  trusted %s, presents %s\n", offer.node, offer.trusted, offer.presented)
		}
		_, _ = fmt.Fprintln(t.out, "A changed key may mean the host was reinstalled, or that the connection is intercepted.")
		if confirmAction("Replace the trusted host keys?") {
			accepted = append(accepted, changed...)
		}
	}
	return accepted
}

// trust trusts the accepted keys and returns the nodes, lowercased, whose
// key is now trusted.
func (t connectionTest) trust(accepted []keyOffer) (map[string]bool, error) {
	if len(accepted) == 0 {
		return nil, nil
	}
	requests := make([]nodeconn.TrustRequest, 0, len(accepted))
	for _, offer := range accepted {
		requests = append(requests, nodeconn.TrustRequest{Node: offer.node, Fingerprint: offer.presented, Replace: offer.trusted})
	}
	by, _ := localOSSubject(user.Current)
	trusted := map[string]bool{}
	err := t.tester.trust(t.ctx, requests, by, func(result nodeconn.TrustResult) {
		switch result.Outcome {
		case nodeconn.TrustOutcomeTrusted:
			_, _ = fmt.Fprintf(t.out, "trusted host key %s of %s\n", result.Fingerprint, result.Node)
		case nodeconn.TrustOutcomeReplaced:
			_, _ = fmt.Fprintf(t.out, "replaced host key %s of %s with %s\n", result.Previous, result.Node, result.Fingerprint)
		case nodeconn.TrustOutcomeUnchanged:
			_, _ = fmt.Fprintf(t.out, "host key %s of %s is already trusted\n", result.Fingerprint, result.Node)
		default:
			_, _ = fmt.Fprintf(t.out, "host key of %s not trusted: %v\n", result.Node, result.Err)
			return
		}
		trusted[strings.ToLower(result.Node)] = true
	})
	return trusted, err
}
