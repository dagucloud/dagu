// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNode is a tree a test builds by hand.
type fakeNode struct {
	el     Element
	hidden bool
	kids   []*fakeNode
}

func (n *fakeNode) element() Element    { return n.el }
func (n *fakeNode) visible() bool       { return !n.hidden }
func (n *fakeNode) placed(p []PathStep) { n.el.Path = p }
func (n *fakeNode) children() []treeNode {
	out := make([]treeNode, len(n.kids))
	for i, kid := range n.kids {
		out[i] = kid
	}
	return out
}

func node(role, name string, kids ...*fakeNode) *fakeNode {
	return &fakeNode{el: Element{Role: role, Name: name}, kids: kids}
}

// The outline lists descendants in preorder, with each path counting the
// element's position among same-role siblings.
func TestOutlineTreeOrder(t *testing.T) {
	t.Parallel()

	root := node(RoleWindow, "Form",
		node(RoleButton, "OK"),
		node(RoleText, "Amount"),
		node(RoleGroup, "Payment",
			node(RoleButton, "Save"),
		),
		node(RoleButton, "Cancel"),
	)
	out := outlineTree(root, "Form", 0)

	names := make([]string, 0, len(out))
	for _, e := range out {
		names = append(names, e.Name)
		assert.Equal(t, "Form", e.Window)
	}
	assert.Equal(t, []string{"OK", "Amount", "Payment", "Save", "Cancel"}, names)
	assert.Equal(t, []PathStep{{Role: RoleButton, Name: "OK", Index: 0}}, out[0].Path)
	assert.Equal(t, []PathStep{{Role: RoleGroup, Name: "Payment", Index: 0}, {Role: RoleButton, Name: "Save", Index: 0}}, out[3].Path)
	assert.Equal(t, []PathStep{{Role: RoleButton, Name: "Cancel", Index: 1}}, out[4].Path, "the text between the buttons does not count")
}

// A hidden element is left out but still counts toward its siblings'
// indexes, and its children are still listed.
func TestOutlineTreeSkipsHidden(t *testing.T) {
	t.Parallel()

	hidden := node(RoleButton, "Hidden", node(RoleText, "Inside"))
	hidden.hidden = true
	root := node(RoleWindow, "Form", node(RoleButton, "First"), hidden, node(RoleButton, "Third"))
	out := outlineTree(root, "Form", 0)

	require.Len(t, out, 3)
	assert.Equal(t, "Inside", out[1].Name)
	assert.Equal(t, []PathStep{{Role: RoleButton, Name: "Hidden", Index: 1}, {Role: RoleText, Name: "Inside", Index: 0}}, out[1].Path)
	assert.Equal(t, 2, out[2].Path[0].Index)
}

func TestOutlineTreeLimit(t *testing.T) {
	t.Parallel()

	root := node(RoleWindow, "Form", node(RoleButton, "A"), node(RoleButton, "B"), node(RoleButton, "C"))
	assert.Len(t, outlineTree(root, "Form", 2), 2)
	assert.Len(t, outlineTree(root, "Form", 0), 3, "0 means the built-in cap")
}

func TestElementJSON(t *testing.T) {
	t.Parallel()

	window := Element{Role: RoleWindow, Name: "Form", App: "powershell", Bounds: image.Rect(100, 100, 700, 500), Window: "Form"}
	data, err := json.Marshal(window)
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"window","name":"Form","app":"powershell","bounds":{"x":100,"y":100,"width":600,"height":400},"window":"Form","path":[]}`, string(data))

	field := Element{Role: RoleTextField, ID: "amountBox", Value: "12", Label: "金額", Bounds: image.Rect(190, 130, 390, 154), Window: "Form", Path: []PathStep{{Role: RoleTextField, Index: 0}}}
	data, err = json.Marshal(field)
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"text_field","name":"","id":"amountBox","value":"12","label":"金額","bounds":{"x":190,"y":130,"width":200,"height":24},"window":"Form","path":[{"role":"text_field","name":"","index":0}]}`, string(data))

	var back Element
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, field, back)
}

// An error is a refusal when it came before the element acted, so it
// sent nothing to the application; any other may have come after.
func TestRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		err     error
		refused bool
	}{
		{"not there", fmt.Errorf("%w: the element went away", ErrNotFound), true},
		{"disabled", ErrDisabled, true},
		{"no such pattern", ErrNoPattern, true},
		{"a read-only field", refusal("the field is read-only"), true},
		{"nothing to expand", refusal("the element has nothing to expand"), true},
		{"no answer in time", fmt.Errorf("AXPress: no answer within 2s: %w", context.DeadlineExceeded), false},
		{"a choice that did not stick", errors.New("the menu stayed open after its option was chosen"), false},
	} {
		assert.Equal(t, tc.refused, Refused(tc.err), tc.name)
	}
}
