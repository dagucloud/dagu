// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migrations

import "embed"

// FS contains PostgreSQL migrations for the control-plane store.
//
//go:embed *.sql
var FS embed.FS
