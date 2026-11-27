// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"math"
	"slices"
)

// valueTrie finds the longest of a set of values that a text starts with.
// Its nodes are stored flat, and the children of each node are contiguous
// and sorted by label, so a large set stays compact.
type valueTrie struct {
	// root maps a byte to the index in nodes of the root's child with that
	// label, or to 0 when no value starts with it.
	root  [256]int32
	nodes []trieNode
}

// trieNode is the last byte of a prefix shared by one or more values.
type trieNode struct {
	// first and count locate the node's children in valueTrie.nodes.
	first, count int32
	label        byte
	// end reports that a value ends at this node.
	end bool
}

// newValueTrie builds a trie of values, which must be distinct and not
// empty. It returns nil when there are no values.
func newValueTrie(values []string) *valueTrie {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	size := 1
	for _, value := range values {
		size += len(value)
	}
	t := &valueTrie{nodes: make([]trieNode, 1, size)}
	// pending holds the nodes whose children are not added yet, with the
	// sorted values sharing the node's prefix of depth bytes.
	type pending struct {
		node   int32
		values []string
		depth  int
	}
	stack := []pending{{node: 0, values: sorted}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		values := p.values
		// The value equal to the shared prefix sorts first.
		if len(values[0]) == p.depth {
			t.nodes[p.node].end = true
			values = values[1:]
		}
		first := nodeIndex(len(t.nodes))
		for i := 0; i < len(values); {
			next := i + 1
			for next < len(values) && values[next][p.depth] == values[i][p.depth] {
				next++
			}
			stack = append(stack, pending{node: nodeIndex(len(t.nodes)), values: values[i:next], depth: p.depth + 1})
			t.nodes = append(t.nodes, trieNode{label: values[i][p.depth]})
			i = next
		}
		t.nodes[p.node].first, t.nodes[p.node].count = first, nodeIndex(len(t.nodes))-first
	}
	root := t.nodes[0]
	for i := root.first; i < root.first+root.count; i++ {
		t.root[t.nodes[i].label] = i
	}
	return t
}

// nodeIndex converts a node count or index to the type nodes are indexed
// by. A trie needs more than 2 GiB of values to exceed it.
func nodeIndex(n int) int32 {
	if n < 0 || n > math.MaxInt32 {
		panic("masking: too many trie nodes")
	}
	return int32(n)
}

// starts reports whether a value starts with b.
func (t *valueTrie) starts(b byte) bool {
	return t.root[b] != 0
}

// longestPrefix returns the length of the longest value that text starts
// with, or 0 when it starts with none. open reports that text is the start
// of a longer value, so text that follows could match more.
func (t *valueTrie) longestPrefix(text string) (n int, open bool) {
	if text == "" || !t.starts(text[0]) {
		return 0, false
	}
	node, longest := &t.nodes[t.root[text[0]]], 0
	for i := 1; ; i++ {
		if node.end {
			longest = i
		}
		if i == len(text) {
			return longest, node.count > 0
		}
		if node = t.child(node, text[i]); node == nil {
			return longest, false
		}
	}
}

// child returns the child of node labeled label, or nil when it has none.
func (t *valueTrie) child(node *trieNode, label byte) *trieNode {
	children := t.nodes[node.first : node.first+node.count]
	low, high := 0, len(children)
	for low < high {
		mid := int(uint(low+high) >> 1)
		if children[mid].label < label {
			low = mid + 1
		} else {
			high = mid
		}
	}
	if low < len(children) && children[low].label == label {
		return &children[low]
	}
	return nil
}
