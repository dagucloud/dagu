// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !headless

package frontend

import "embed"

//go:embed templates/* assets/*
var assetsFS embed.FS

const webUIEmbedded = true
