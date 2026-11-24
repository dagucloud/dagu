// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// Every nodes subcommand runs against a remote context.
func TestNodesCommandsAreContextAware(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"list", "show", "add", "remove", "test", "import", "export"} {
		assert.Equal(t, commandScopeContextAware, scopeForCommand("nodes "+name), name)
	}
}

func TestReadEvents(t *testing.T) {
	t.Parallel()

	t.Run("StopsAtDone", func(t *testing.T) {
		t.Parallel()
		var events []string
		err := readEvents(strings.NewReader(": keepalive\n\nevent: result\ndata: {\"node\":\"web01\"}\n\nevent: done\ndata: {}\n\nevent: result\ndata: late\n\n"),
			func(event string, data []byte) error {
				events = append(events, event+" "+string(data))
				return nil
			})
		require.NoError(t, err)
		assert.Equal(t, []string{`result {"node":"web01"}`, "done {}"}, events)
	})
	t.Run("EndsBeforeDone", func(t *testing.T) {
		t.Parallel()
		err := readEvents(strings.NewReader("event: result\ndata: {}\n\n"), func(string, []byte) error { return nil })
		require.ErrorContains(t, err, "closed the stream before every node finished")
	})
	t.Run("StopsAtHandlerError", func(t *testing.T) {
		t.Parallel()
		stop := errors.New("stop")
		err := readEvents(strings.NewReader("event: result\ndata: {}\n\n"), func(string, []byte) error { return stop })
		require.ErrorIs(t, err, stop)
	})
}

// A node of the API maps to the resolved node the local commands print.
func TestResolvedNodeOf(t *testing.T) {
	t.Parallel()
	keyPath, secret := "/etc/dagu/id", "ssh/db"
	node, err := resolvedNodeOf(api.InventoryNode{
		Name:      "db01",
		Inventory: api.InventoryRef{Id: "inv_tokyo", Name: "Tokyo"},
		Host:      "db01.internal",
		Port:      2222,
		Auth:      api.InventoryNodeAuth{KeyPath: &keyPath, PassphraseSecret: &secret},
		Timeout:   "1m30s",
		Labels:    []string{"role=db", "site=tokyo"},
	})
	require.NoError(t, err)
	assert.Equal(t, "inv_tokyo", node.InventoryID)
	assert.Equal(t, 90*time.Second, node.Timeout)
	assert.Equal(t, keyPath, node.Auth.Key)
	assert.Equal(t, secret, node.Auth.PassphraseSecret)
	assert.Equal(t, []string{"role=db", "site=tokyo"}, node.Labels.Strings())
}

// The rules a refused change lists print as the local commands print them.
func TestRuleErrorsOf(t *testing.T) {
	t.Parallel()
	inv, node, field := "Nagoya", "nagoya-web01", "host"
	err := ruleErrorsOf(&remoteError{StatusCode: 400, Message: "nothing imported", Rules: []api.InventoryRuleError{
		{Inventory: &inv, Node: &node, Field: &field, Message: "is not a host name"},
		{Message: "an import holds no inventory"},
	}})

	require.ErrorIs(t, err, inventory.ErrInvalid)
	assert.Equal(t, "inventory \"Nagoya\": node \"nagoya-web01\": host: is not a host name\nan import holds no inventory", err.Error())

	plain := &remoteError{StatusCode: 409, Message: "run it again"}
	assert.Same(t, plain, ruleErrorsOf(plain))
}
