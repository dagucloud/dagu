// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package schema provides embedded JSON schemas for use across the codebase.
package schema

import _ "embed"

//go:embed dag.schema.json
var DAGSchemaJSON []byte

//go:embed config.schema.json
var ConfigSchemaJSON []byte

//go:embed calendar.schema.json
var CalendarSchemaJSON []byte
