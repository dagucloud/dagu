// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// tokyo is the example inventory of RFC 012.
const tokyo = `{
  "id": "inv_7f3k2a",
  "name": "Tokyo production (DC-1)",
  "description": "Racks A and B",
  "defaults": {
    "user": "deploy",
    "key": { "secret": "ssh/tokyo" },
    "labels": ["site=tokyo", "env=prod"]
  },
  "nodes": [
    { "name": "tokyo-web01", "host": "10.0.1.11", "labels": ["web"] },
    { "name": "tokyo-web02", "host": "10.0.1.12", "labels": ["web", "canary"] },
    {
      "name": "tokyo-db01",
      "host": "db01.internal",
      "port": 2222,
      "user": "postgres",
      "key": { "secret": "ssh/tokyo-db" },
      "bastion": "tokyo-jump",
      "labels": ["db"]
    },
    { "name": "tokyo-jump", "host": "jump.tokyo.example.com" }
  ]
}`

func TestResolveExample(t *testing.T) {
	t.Parallel()

	inv, err := inventory.Decode([]byte(tokyo))
	require.NoError(t, err)
	nodes, err := inventory.Resolve([]inventory.Inventory{inv})
	require.NoError(t, err)

	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	assert.Equal(t, []string{"tokyo-db01", "tokyo-jump", "tokyo-web01", "tokyo-web02"}, names)

	assert.Equal(t, inventory.ResolvedNode{
		Name:          "tokyo-db01",
		Host:          "db01.internal",
		InventoryID:   "inv_7f3k2a",
		InventoryName: "Tokyo production (DC-1)",
		Port:          2222,
		User:          "postgres",
		Auth:          ir.SSHAuth{KeySecret: "ssh/tokyo-db"},
		Bastion:       "tokyo-jump",
		StrictHostKey: true,
		Timeout:       30 * time.Second,
		Labels:        ir.NewLabels([]string{"db", "site=tokyo", "env=prod"}),
	}, nodes[0])

	web := nodes[2]
	assert.Equal(t, 22, web.Port)
	assert.Equal(t, "deploy", web.User)
	assert.Equal(t, ir.SSHAuth{KeySecret: "ssh/tokyo"}, web.Auth)
	assert.Equal(t, ir.NewLabels([]string{"web", "site=tokyo", "env=prod"}), web.Labels)
}

func TestDecodeRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	t.Parallel()

	_, err := inventory.Decode([]byte(`{"name": "a", "nodes": [{"name": "n", "host": "h", "tags": ["x"]}]}`))
	require.ErrorContains(t, err, `unknown field "tags"`)

	_, err = inventory.Decode([]byte(`{"name": "a"} {"name": "b"}`))
	require.ErrorContains(t, err, "unexpected data after the value")

	list, err := inventory.DecodeList([]byte(`[{"name": "a"}, {"name": "b"}]`))
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

// Each case breaks one rule of RFC 012 §Nodes, and the error names the
// inventory, the node, and the field.
func TestResolveRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"InventoryNameRequired", `[{"name": " "}]`, `inventory " ": name: is required`},
		{"InventoryNameTooLong", `[{"name": "` + strings.Repeat("x", 201) + `"}]`, "name: is longer than 200 characters"},
		{"InventoryNameClashIgnoresCase", `[{"name": "Tokyo"}, {"name": "TOKYO"}]`, `inventory "TOKYO": name: is also the name of inventory "Tokyo"`},
		{"NodeName", `[{"name": "a", "nodes": [{"name": "-web", "host": "h"}]}]`, `inventory "a": node "-web": name: "-web" must match`},
		{"NodeNameClashAcrossInventories", `[{"name": "a", "nodes": [{"name": "web", "host": "h"}]}, {"name": "b", "nodes": [{"name": "WEB", "host": "h"}]}]`, `inventory "b": node "WEB": name: is also a node of inventory "a"`},
		{"HostRequired", `[{"name": "a", "nodes": [{"name": "web"}]}]`, `node "web": host: is required`},
		{"HostNotADNSName", `[{"name": "a", "nodes": [{"name": "web", "host": "web;rm -rf /"}]}]`, `host: "web;rm -rf /" is not a DNS name`},
		{"HostWithIPv6Zone", `[{"name": "a", "nodes": [{"name": "web", "host": "fe80::1%eth0"}]}]`, `host: "fe80::1%eth0" is not a DNS name`},
		{"User", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "user": "a b"}]}]`, `user: "a b" must match`},
		{"Port", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "port": 70000}]}]`, "port: 70000 is not between 1 and 65535"},
		{"Timeout", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "timeout": "soon"}]}]`, `timeout: "soon" is not a positive duration`},
		{"HostKey", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "host_key": "not a key"}]}]`, "host_key: must be one public key"},
		{"LiteralPassword", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "password": "hunter2"}]}]`, `password: takes only {"secret": <ref>}`},
		{"LiteralPassphraseInDefaults", `[{"name": "a", "defaults": {"passphrase": "x"}}]`, `defaults.passphrase: takes only {"secret": <ref>}`},
		{"SecretRefGrammar", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "key": {"secret": "SSH/Key"}}]}]`, `node "web": key.secret: invalid secret ref`},
		{"Label", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "labels": ["env=prod!"]}]}]`, `node "web": labels: label value "prod!" contains invalid characters`},
		{"BastionMissing", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "bastion": "jump"}]}]`, `bastion: "jump" is not a node of this inventory`},
		{"BastionInOtherInventory", `[{"name": "a", "nodes": [{"name": "jump", "host": "h"}]}, {"name": "b", "nodes": [{"name": "web", "host": "h", "bastion": "jump"}]}]`, `inventory "b": node "web": bastion: "jump" is not a node of this inventory`},
		{"BastionItself", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "bastion": "web"}]}]`, "bastion: names the node itself"},
		{"BastionTwoHops", `[{"name": "a", "nodes": [{"name": "web", "host": "h", "bastion": "jump"}, {"name": "jump", "host": "h", "bastion": "outer"}, {"name": "outer", "host": "h"}]}]`, `node "web": bastion: "jump" connects through a bastion itself`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			list, err := inventory.DecodeList([]byte(tt.json))
			require.NoError(t, err)
			_, err = inventory.Resolve(list)
			require.ErrorIs(t, err, inventory.ErrInvalid)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolveAcceptsAddressesAndNames(t *testing.T) {
	t.Parallel()

	list, err := inventory.DecodeList([]byte(`[{"name": "a", "nodes": [
		{"name": "v4", "host": "192.0.2.10"},
		{"name": "v6", "host": "2001:db8::1"},
		{"name": "dns", "host": "Web-01.Example.COM"}
	]}]`))
	require.NoError(t, err)
	nodes, err := inventory.Resolve(list)
	require.NoError(t, err)
	assert.Len(t, nodes, 3)
}

func TestResolveReportsEveryError(t *testing.T) {
	t.Parallel()

	list, err := inventory.DecodeList([]byte(`[{"name": "a", "nodes": [
		{"name": "web", "host": "", "user": "a b"},
		{"name": "db", "host": "h", "password": "x"}
	]}]`))
	require.NoError(t, err)
	_, err = inventory.Resolve(list)
	require.Error(t, err)
	assert.Len(t, err.(interface{ Unwrap() []error }).Unwrap(), 3)
}

func TestResolveDefaults(t *testing.T) {
	t.Parallel()

	list, err := inventory.DecodeList([]byte(`[{
		"name": "a",
		"defaults": {
			"port": 2200,
			"key": {"secret": "ssh/shared"},
			"passphrase": {"secret": "ssh/shared-passphrase"},
			"bastion": "jump",
			"strict_host_key": false,
			"labels": ["env=prod", "site=tokyo"]
		},
		"nodes": [
			{"name": "jump", "host": "jump.example.com"},
			{"name": "web", "host": "h", "port": 22, "labels": ["env=staging"]},
			{"name": "own-key", "host": "h", "key": "/keys/plain"},
			{"name": "own-passphrase", "host": "h", "passphrase": {"secret": "ssh/other"}},
			{"name": "strict", "host": "h", "strict_host_key": true}
		]
	}]`))
	require.NoError(t, err)
	nodes, err := inventory.Resolve(list)
	require.NoError(t, err)
	byName := make(map[string]inventory.ResolvedNode)
	for _, n := range nodes {
		byName[n.Name] = n
	}

	// The default bastion applies to every node but itself.
	assert.Empty(t, byName["jump"].Bastion)
	assert.Equal(t, "jump", byName["web"].Bastion)

	// A node's own field wins, and a node label replaces a default label with
	// the same key.
	assert.Equal(t, 22, byName["web"].Port)
	assert.Equal(t, 2200, byName["jump"].Port)
	assert.Equal(t, ir.NewLabels([]string{"env=staging", "site=tokyo"}), byName["web"].Labels)
	assert.False(t, byName["web"].StrictHostKey)
	assert.True(t, byName["strict"].StrictHostKey)

	// A node's own key takes no passphrase from the defaults; a node's own
	// passphrase keeps the default key.
	assert.Equal(t, ir.SSHAuth{Key: "/keys/plain"}, byName["own-key"].Auth)
	assert.Equal(t, ir.SSHAuth{KeySecret: "ssh/shared", PassphraseSecret: "ssh/other"}, byName["own-passphrase"].Auth)
	assert.Equal(t, ir.SSHAuth{KeySecret: "ssh/shared", PassphraseSecret: "ssh/shared-passphrase"}, byName["web"].Auth)
}

func TestSelect(t *testing.T) {
	t.Parallel()

	inv, err := inventory.Decode([]byte(tokyo))
	require.NoError(t, err)
	nodes, err := inventory.Resolve([]inventory.Inventory{inv})
	require.NoError(t, err)

	match := func(selector string) []string {
		t.Helper()
		filters, err := inventory.ParseSelector(selector)
		require.NoError(t, err)
		var names []string
		for _, n := range inventory.Select(nodes, filters) {
			names = append(names, n.Name)
		}
		return names
	}
	assert.Equal(t, []string{"tokyo-web01", "tokyo-web02"}, match("web"))
	assert.Equal(t, []string{"tokyo-web01"}, match("site=tokyo,web,!canary"))
	assert.Equal(t, []string{"tokyo-db01", "tokyo-jump", "tokyo-web01", "tokyo-web02"}, match("ENV=PROD"))
	assert.Empty(t, match("site=osaka"))

	_, err = inventory.ParseSelector(" , ")
	require.ErrorIs(t, err, inventory.ErrEmptySelector)
	_, err = inventory.ParseSelector("env==prod")
	require.ErrorIs(t, err, ir.ErrInvalidLabelFilter)
}
