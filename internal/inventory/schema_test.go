// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"

	cmnschema "github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// The inventory schema, which the Nodes page's JSON editor validates with,
// accepts a record exactly when it decodes and resolves on its own. Rules
// that span records, such as a bastion that names no node, are left to the
// store, so these cases break only rules of one field. One case is left
// out: the decoder reads "port": 0 as no port, and the schema refuses it,
// as Spec 088 allows 1 to 65535.
func TestSchemaMatchesDecoder(t *testing.T) {
	t.Parallel()

	var raw jsonschema.Schema
	require.NoError(t, json.Unmarshal(cmnschema.InventorySchemaJSON, &raw))
	schema, err := raw.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)

	cases := map[string]string{
		"minimal":             `{"name": "fleet"}`,
		"blank name":          `{"name": " "}`,
		"unknown field":       `{"name": "fleet", "lables": []}`,
		"id":                  `{"id": "inv_0123456789", "name": "fleet"}`,
		"key path":            `{"name": "fleet", "defaults": {"key": "~/.ssh/id_ed25519"}}`,
		"key file":            `{"name": "fleet", "defaults": {"key": {"file": "/etc/dagu/id"}}}`,
		"key secret":          `{"name": "fleet", "defaults": {"key": {"secret": "ssh/fleet"}}}`,
		"key both forms":      `{"name": "fleet", "defaults": {"key": {"file": "/k", "secret": "ssh/k"}}}`,
		"literal password":    `{"name": "fleet", "defaults": {"password": "hunter2"}}`,
		"literal passphrase":  `{"name": "fleet", "nodes": [{"name": "web01", "host": "h", "passphrase": "x"}]}`,
		"node without host":   `{"name": "fleet", "nodes": [{"name": "web01"}]}`,
		"node name":           `{"name": "fleet", "nodes": [{"name": "-web01", "host": "h"}]}`,
		"port -1":             `{"name": "fleet", "nodes": [{"name": "web01", "host": "h", "port": -1}]}`,
		"port 65536":          `{"name": "fleet", "defaults": {"port": 65536}}`,
		"user":                `{"name": "fleet", "defaults": {"user": "-deploy"}}`,
		"label key=value":     `{"name": "fleet", "defaults": {"labels": ["site=tokyo", "web", "env="]}}`,
		"label":               `{"name": "fleet", "defaults": {"labels": ["site tokyo"]}}`,
		"timeout":             `{"name": "fleet", "defaults": {"timeout": "1m30s"}}`,
		"timeout not a time":  `{"name": "fleet", "defaults": {"timeout": "soon"}}`,
		"disabled node":       `{"name": "fleet", "nodes": [{"name": "web01", "host": "10.0.1.11", "disabled": true}]}`,
		"unknown node field":  `{"name": "fleet", "nodes": [{"name": "web01", "host": "h", "hostname": "h"}]}`,
		"strict host key off": `{"name": "fleet", "defaults": {"strict_host_key": false}}`,
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "conformance", "spec088_node_inventory", "testdata", "inventories.json"))
	require.NoError(t, err)
	var examples []json.RawMessage
	require.NoError(t, json.Unmarshal(data, &examples))
	for i, example := range examples {
		cases["Spec 088 example "+string(rune('A'+i))] = string(example)
	}

	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decodeErr := decodeAndResolve(record)
			var doc any
			require.NoError(t, json.Unmarshal([]byte(record), &doc))
			schemaErr := schema.Validate(doc)
			require.Equal(t, decodeErr == nil, schemaErr == nil, "decoder: %v\nschema: %v", decodeErr, schemaErr)
		})
	}
}

func decodeAndResolve(record string) error {
	inv, err := inventory.Decode([]byte(record))
	if err != nil {
		return err
	}
	if inv.ID != "" {
		return errInventoryID
	}
	_, err = inventory.Resolve([]inventory.Inventory{inv})
	return err
}

// errInventoryID stands for the store's refusal of a record that sets id,
// which Decode accepts since stored records carry their ID.
var errInventoryID = os.ErrInvalid
