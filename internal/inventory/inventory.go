// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package inventory holds node inventories: named lists of SSH hosts with
// shared defaults, whose nodes steps select by label (RFC 012).
package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Inventory is a named list of SSH nodes and the defaults they share.
type Inventory struct {
	// ID is set by Dagu when the inventory is created and never changes.
	ID string `json:"id,omitempty"`
	// Name is the display name, unique in a project without regard to case.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Defaults fill the connection fields a node leaves unset, and add labels.
	Defaults Defaults `json:"defaults,omitzero"`
	Nodes    []Node   `json:"nodes"`
}

// Connection holds the SSH fields that a node and the defaults share.
type Connection struct {
	Port int    `json:"port,omitempty"`
	User string `json:"user,omitempty"`
	// Key is a path, {"file": <path>}, or {"secret": <ref>}.
	Key any `json:"key,omitempty"`
	// Password and Passphrase take only {"secret": <ref>}.
	Password   any `json:"password,omitempty"`
	Passphrase any `json:"passphrase,omitempty"`
	// Bastion names another node of the same inventory to connect through.
	Bastion string `json:"bastion,omitempty"`
	// HostKey pins the public key the host must present.
	HostKey       string `json:"host_key,omitempty"`
	StrictHostKey *bool  `json:"strict_host_key,omitempty"`
	Shell         string `json:"shell,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
}

// Defaults apply to every node of an inventory.
type Defaults struct {
	Connection
	// Labels are added to each node's labels. A node label with the same key
	// replaces a default label.
	Labels []string `json:"labels,omitempty"`
}

// Node is one SSH host of an inventory.
type Node struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Connection
	Labels      []string `json:"labels,omitempty"`
	Description string   `json:"description,omitempty"`
	// Disabled keeps runs off the node. It is still listed and matched.
	Disabled bool `json:"disabled,omitempty"`
}

// ResolvedNode is a node with its inventory's defaults applied.
type ResolvedNode struct {
	Name          string
	Host          string
	InventoryID   string
	InventoryName string
	Port          int
	User          string
	Auth          ir.SSHAuth
	Bastion       string
	HostKey       string
	StrictHostKey bool
	Shell         string
	Timeout       time.Duration
	Labels        ir.Labels
	Description   string
	Disabled      bool
}

// Decode reads one inventory from JSON. An unknown field is an error.
func Decode(data []byte) (Inventory, error) {
	var inv Inventory
	if err := decodeStrict(data, &inv); err != nil {
		return Inventory{}, err
	}
	return inv, nil
}

// DecodeList reads a JSON array of inventories, as an export holds. An unknown
// field is an error.
func DecodeList(data []byte) ([]Inventory, error) {
	var list []Inventory
	if err := decodeStrict(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid inventory JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid inventory JSON: unexpected data after the value")
	}
	return nil
}
