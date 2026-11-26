// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"encoding/json"
	"time"
)

// Catalog is the tools one MCP service of a connection lists. It holds no
// secret.
type Catalog struct {
	Service  string
	ListedAt time.Time
	Tools    []Tool
}

// Tool is one tool of a catalog, as the MCP server describes it.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Annotations json.RawMessage
	// Hash covers the description, input schema, and annotations, so a
	// changed tool is detected. The store sets it when the catalog is saved.
	Hash string
}
