// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

type editorFixture struct {
	editor   *inventory.Editor
	hostKeys inventory.HostKeyStore
	stored   inventory.Stored
}

// newEditorFixture stores inventory "tokyo" with nodes web01 and web02, and
// trusts a key for each.
func newEditorFixture(t *testing.T) editorFixture {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewInventoryStore(backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories))
	require.NoError(t, err)
	hostKeys, err := persiststore.NewHostKeyStore(backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	editor := inventory.NewEditor(store, hostKeys)

	stored, err := editor.Create(context.Background(), inventory.Inventory{
		Name:  "tokyo",
		Nodes: []inventory.Node{{Name: "web01", Host: "10.0.1.11"}, {Name: "web02", Host: "10.0.1.12"}},
	})
	require.NoError(t, err)
	f := editorFixture{editor: editor, hostKeys: hostKeys, stored: stored}
	f.trust(t, "web01")
	f.trust(t, "web02")
	return f
}

func (f editorFixture) trust(t *testing.T, node string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	_, err = f.hostKeys.Trust(context.Background(), node, key, "alice", "")
	require.NoError(t, err)
}

// trusted reports whether a key is trusted for each node, in order.
func (f editorFixture) trusted(t *testing.T, nodes ...string) []bool {
	t.Helper()
	got := make([]bool, len(nodes))
	for i, node := range nodes {
		_, err := f.hostKeys.Get(context.Background(), node)
		if err != nil {
			require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)
		}
		got[i] = err == nil
	}
	return got
}

func TestEditorForgetsRemovedNodes(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)

	_, err := f.editor.DeleteNode(context.Background(), f.stored.ID, "WEB01", f.stored.Version)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true}, f.trusted(t, "web01", "web02"))
}

// A renamed node is a removed node and an added one, so neither name keeps
// a key, while a node whose host changes keeps its key.
func TestEditorForgetsRenamedNodes(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)
	f.trust(t, "web03") // left over from a node removed before

	inv := f.stored.Inventory
	inv.Nodes = []inventory.Node{{Name: "web03", Host: "10.0.1.11"}, {Name: "web02", Host: "10.0.9.12"}}
	_, err := f.editor.Replace(context.Background(), inv, f.stored.Version)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true, false}, f.trusted(t, "web01", "web02", "web03"))
}

func TestEditorForgetsNodesOfDeletedInventory(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)

	require.NoError(t, f.editor.Delete(context.Background(), f.stored.ID, f.stored.Version))

	assert.Equal(t, []bool{false, false}, f.trusted(t, "web01", "web02"))
}

// An import that replaces an inventory forgets the nodes it drops. A dry
// run forgets nothing.
func TestEditorImportForgetsDroppedNodes(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)
	next := []inventory.Inventory{{Name: "tokyo", Nodes: []inventory.Node{{Name: "web02", Host: "10.0.1.12"}}}}

	_, err := f.editor.Import(context.Background(), next, inventory.ImportOptions{Replace: true, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []bool{true, true}, f.trusted(t, "web01", "web02"))

	_, err = f.editor.Import(context.Background(), next, inventory.ImportOptions{Replace: true})
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true}, f.trusted(t, "web01", "web02"))
}

// PutNode adds a node, which starts with no trusted key, or replaces the
// node of that name.
func TestEditorPutNode(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)
	f.trust(t, "db01")

	stored, added, err := f.editor.PutNode(context.Background(), f.stored.ID, inventory.Node{Name: "db01", Host: "10.0.2.11"}, f.stored.Version)
	require.NoError(t, err)
	assert.True(t, added)
	assert.Len(t, stored.Nodes, 3)

	stored, added, err = f.editor.PutNode(context.Background(), f.stored.ID, inventory.Node{Name: "DB01", Host: "10.0.2.12"}, stored.Version)
	require.NoError(t, err)
	assert.False(t, added)
	require.Len(t, stored.Nodes, 3)
	assert.Equal(t, "10.0.2.12", stored.Nodes[2].Host)
	assert.Equal(t, []bool{true, true, false}, f.trusted(t, "web01", "web02", "db01"))
}

func TestEditorRefusesStaleAndMissing(t *testing.T) {
	t.Parallel()
	f := newEditorFixture(t)
	ctx := context.Background()

	_, _, err := f.editor.PutNode(ctx, f.stored.ID, inventory.Node{Name: "db01", Host: "10.0.2.11"}, f.stored.Version+1)
	require.ErrorIs(t, err, inventory.ErrStale)
	_, err = f.editor.DeleteNode(ctx, f.stored.ID, "web09", f.stored.Version)
	require.ErrorIs(t, err, inventory.ErrNodeNotFound)
	_, err = f.editor.DeleteNode(ctx, "no-such-id", "web01", 1)
	require.ErrorIs(t, err, inventory.ErrNotFound)
	assert.Equal(t, []bool{true, true}, f.trusted(t, "web01", "web02"))
}
